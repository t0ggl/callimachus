package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

type seriesBook struct {
	metadata.BookMetadata
	SeriesIndexDisplay string   `json:"series_index_display,omitempty"`
	Formats            []string `json:"formats"`
	LibraryBookID      string   `json:"library_book_id,omitempty"`
	Monitored          bool     `json:"monitored"`
}

type seriesBooksResponse struct {
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Status      string       `json:"status,omitempty"`
	Monitored   bool         `json:"monitored"`
	Authors     []string     `json:"authors"`
	TotalBooks  int          `json:"total_books"`
	OwnedCount  int          `json:"owned_count"`
	Books       []seriesBook `json:"books"`
	Complete    bool         `json:"complete"`
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

	seriesTitle := title
	var seriesDescription, seriesStatus string
	var seriesTotalBooks int
	var seriesMonitored bool
	books := make([]seriesBook, 0)

	if s.db != nil {
		err = s.db.QueryRowContext(r.Context(),
			`SELECT title, COALESCE(description, ''), COALESCE(status, ''), COALESCE(total_books, 0), monitored FROM series WHERE title=? COLLATE NOCASE LIMIT 1`,
			title,
		).Scan(&seriesTitle, &seriesDescription, &seriesStatus, &seriesTotalBooks, &seriesMonitored)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Unable to load series", 500)
			return
		}

		formatsByBook := make(map[string][]string)
		fmtRows, err := s.db.QueryContext(r.Context(), `
			SELECT DISTINCT e.book_id, UPPER(e.format)
			FROM book_editions e
			JOIN media_assets m ON m.edition_id=e.id
			JOIN books b ON b.id=e.book_id
			JOIN series s ON s.id=b.series_id
			WHERE s.title=? COLLATE NOCASE
			ORDER BY UPPER(e.format) ASC`, title)
		if err != nil {
			http.Error(w, "Unable to load series", 500)
			return
		}
		for fmtRows.Next() {
			var bookID, format string
			if err := fmtRows.Scan(&bookID, &format); err != nil {
				fmtRows.Close()
				http.Error(w, "Unable to load series", 500)
				return
			}
			if format != "" {
				formatsByBook[bookID] = append(formatsByBook[bookID], format)
			}
		}
		if err := fmtRows.Err(); err != nil {
			fmtRows.Close()
			http.Error(w, "Unable to load series", 500)
			return
		}
		fmtRows.Close()

		rows, err := s.db.QueryContext(r.Context(), `
			SELECT b.id, b.title, COALESCE(b.subtitle, ''), a.name, b.series_index,
			       COALESCE(b.series_index_display, ''), COALESCE(b.release_date, ''),
			       COALESCE(b.synopsis, ''), b.monitored,
			       EXISTS(SELECT 1 FROM book_editions e JOIN media_assets m ON m.edition_id=e.id WHERE e.book_id=b.id)
			FROM books b
			JOIN authors a ON a.id=b.author_id
			JOIN series s ON s.id=b.series_id
			WHERE s.title=? COLLATE NOCASE`, title)
		if err != nil {
			http.Error(w, "Unable to load series", 500)
			return
		}
		for rows.Next() {
			var b seriesBook
			var id, author string
			var held bool
			if err := rows.Scan(
				&id, &b.Title, &b.Subtitle, &author, &b.SeriesIndex,
				&b.SeriesIndexDisplay, &b.PublishDate, &b.Description, &b.Monitored, &held,
			); err != nil {
				rows.Close()
				http.Error(w, "Unable to load series", 500)
				return
			}
			if author != "" {
				b.Authors = []string{author}
			} else {
				b.Authors = []string{}
			}
			b.Series = seriesTitle
			b.Formats = formatsByBook[id]
			if b.Formats == nil {
				b.Formats = []string{}
			}
			if held {
				b.LibraryBookID = id
			}
			if s.bookCover(r, id) != "" {
				b.CoverURL = "/api/books/" + id + "/cover"
			}
			books = append(books, b)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			http.Error(w, "Unable to load series", 500)
			return
		}
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
				if remote.SeriesTotal > seriesTotalBooks {
					seriesTotalBooks = remote.SeriesTotal
				}
				matched := false
				for i := range books {
					local := &books[i]
					if len(local.Authors) == 0 {
						continue
					}
					for _, author := range remote.Authors {
						if discoveryKey(remote.Title, author) == discoveryKey(local.Title, local.Authors[0]) {
							mergeSeriesBookMetadata(local, remote)
							matched = true
							break
						}
					}
					if matched {
						break
					}
				}
				if !matched {
					if remote.Authors == nil {
						remote.Authors = []string{}
					}
					books = append(books, seriesBook{
						BookMetadata: remote,
						Formats:      []string{},
					})
				}
			}
		}
	}

	sort.SliceStable(books, func(i, j int) bool {
		ai, bi := books[i].SeriesIndex, books[j].SeriesIndex
		if ai != nil && bi != nil {
			if *ai != *bi {
				return *ai < *bi
			}
		} else if ai != nil || bi != nil {
			return ai != nil
		}
		di, dj := books[i].PublishDate, books[j].PublishDate
		if di != dj {
			if di == "" {
				return false
			}
			if dj == "" {
				return true
			}
			return di < dj
		}
		return strings.ToLower(books[i].Title) < strings.ToLower(books[j].Title)
	})

	authors := make([]string, 0)
	seenAuthor := make(map[string]bool)
	ownedCount := 0
	for _, b := range books {
		if b.LibraryBookID != "" {
			ownedCount++
		}
		for _, a := range b.Authors {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			key := strings.ToLower(a)
			if !seenAuthor[key] {
				seenAuthor[key] = true
				authors = append(authors, a)
			}
		}
	}
	totalBooks := seriesTotalBooks
	if len(books) > totalBooks {
		totalBooks = len(books)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(seriesBooksResponse{
		Title:       seriesTitle,
		Description: seriesDescription,
		Status:      seriesStatus,
		Monitored:   seriesMonitored,
		Authors:     authors,
		TotalBooks:  totalBooks,
		OwnedCount:  ownedCount,
		Books:       books,
		Complete:    complete,
	})
}

func mergeSeriesBookMetadata(local *seriesBook, remote metadata.BookMetadata) {
	if remote.Title != "" && local.Title == "" {
		local.Title = remote.Title
	}
	if local.Subtitle == "" && remote.Subtitle != "" {
		local.Subtitle = remote.Subtitle
	}
	if local.Description == "" && remote.Description != "" {
		local.Description = remote.Description
	}
	if local.CoverURL == "" && remote.CoverURL != "" {
		local.CoverURL = remote.CoverURL
	}
	if remote.PublishDate != "" {
		local.PublishDate = remote.PublishDate
	}
	if remote.Year > 0 {
		local.Year = remote.Year
	}
	if remote.SeriesIndex != nil {
		local.SeriesIndex = remote.SeriesIndex
	}
	if remote.Series != "" {
		local.Series = remote.Series
	}
	if remote.SeriesTotal > 0 {
		local.SeriesTotal = remote.SeriesTotal
	}
	if remote.HardcoverID != "" {
		local.HardcoverID = remote.HardcoverID
	}
	if len(remote.Authors) > 0 {
		seen := make(map[string]bool, len(local.Authors))
		for _, a := range local.Authors {
			seen[strings.ToLower(strings.TrimSpace(a))] = true
		}
		for _, a := range remote.Authors {
			trimmed := strings.TrimSpace(a)
			if trimmed != "" && !seen[strings.ToLower(trimmed)] {
				seen[strings.ToLower(trimmed)] = true
				local.Authors = append(local.Authors, trimmed)
			}
		}
	}
}

