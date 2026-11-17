package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

type seriesBook struct {
	metadata.BookMetadata
	LibraryBookID string `json:"library_book_id,omitempty"`
	Monitored     bool   `json:"monitored"`
}

func (s *Server) handleSeriesBooks(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	if title == "" || len(title) > 500 {
		http.Error(w, "Enter a series title", 400)
		return
	}
	cfg, err := config.Load(r.Context(), s.db, s.baseOverrides)
	if err != nil {
		http.Error(w, "Unable to load metadata settings", 500)
		return
	}
	books := []seriesBook{}
	rows, err := s.db.QueryContext(r.Context(), `SELECT b.id,b.title,a.name,b.series_index,COALESCE(b.release_date,''),b.monitored,
 EXISTS(SELECT 1 FROM book_editions e JOIN media_assets m ON m.edition_id=e.id WHERE e.book_id=b.id)
 FROM books b JOIN authors a ON a.id=b.author_id JOIN series s ON s.id=b.series_id WHERE s.title=? COLLATE NOCASE`, title)
	if err != nil {
		http.Error(w, "Unable to load series", 500)
		return
	}
	for rows.Next() {
		var b seriesBook
		var id, author string
		var held bool
		if err := rows.Scan(&id, &b.Title, &author, &b.SeriesIndex, &b.PublishDate, &b.Monitored, &held); err != nil {
			rows.Close()
			http.Error(w, "Unable to load series", 500)
			return
		}
		b.Authors = []string{author}
		b.Series = title
		if held {
			b.LibraryBookID = id
		}
		books = append(books, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "Unable to load series", 500)
		return
	}
	complete := false
	if cfg.Settings.HardcoverAPIKey != "" {
		client := s.prowlarrHTTPClient
		if client == nil {
			client = &http.Client{Timeout: 20 * time.Second}
		}
		found, lookupErr := metadata.NewHardcoverProvider(cfg.Settings.HardcoverAPIKey, metadata.WithHardcoverClient(client)).SeriesBooks(r.Context(), title)
		if lookupErr == nil {
			complete = true
			for _, remote := range found {
				matched := false
				for i, local := range books {
					for _, author := range remote.Authors {
						if discoveryKey(remote.Title, author) == discoveryKey(local.Title, local.Authors[0]) {
							books[i].BookMetadata = remote
							matched = true
							break
						}
					}
					if matched {
						break
					}
				}
				if !matched {
					books = append(books, seriesBook{BookMetadata: remote})
				}
			}
		}
	}
	sort.SliceStable(books, func(i, j int) bool {
		a, b := books[i].SeriesIndex, books[j].SeriesIndex
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		return *a < *b
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Books    []seriesBook `json:"books"`
		Complete bool         `json:"complete"`
	}{books, complete})
}
