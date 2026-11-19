package main

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

type seriesBook struct {
	metadata.BookMetadata
	SeriesIndexDisplay string   `json:"series_index_display,omitempty"`
	Formats            []string `json:"formats"`
	LibraryBookID      string   `json:"library_book_id,omitempty"`
	State              string   `json:"state"`
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
			       COALESCE(b.release_date, ''),
			       COALESCE(b.synopsis, ''), `+bookAvailabilitySQL+`
			FROM books b
			JOIN authors a ON a.id=b.author_id
			JOIN series s ON s.id=b.series_id
			WHERE s.title=? COLLATE NOCASE
			ORDER BY b.id`, title)
		if err != nil {
			http.Error(w, "Unable to load series", 500)
			return
		}
		for rows.Next() {
			var b seriesBook
			var id, author string
			if err := rows.Scan(
				&id, &b.Title, &b.Subtitle, &author, &b.SeriesIndex,
				&b.PublishDate, &b.Description, &b.State,
			); err != nil {
				rows.Close()
				http.Error(w, "Unable to load series", 500)
				return
			}
			// Older catalog imports may have left derivative placeholders behind.
			if b.State == "" && metadata.IsSeriesDerivativeTitle(b.Title, b.Subtitle) {
				continue
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
			if b.State != "" {
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
		known := make([]metadata.BookMetadata, 0, len(books))
		for _, b := range books {
			title := seriesTitleAnnotation.ReplaceAllString(b.Title, "")
			title = metadata.StripDiscoveryAuthorPrefix(title, b.Authors...)
			known = append(known, metadata.BookMetadata{Title: strings.TrimSpace(title), Authors: b.Authors})
		}
		found, lookupErr := metadata.NewHardcoverProvider(cfg.Settings.HardcoverAPIKey, metadata.WithHardcoverClient(client)).SeriesBooks(r.Context(), seriesTitle, known...)
		if lookupErr == nil && len(found) > 0 {
			complete = true
			if found[0].SeriesTotal > 0 {
				seriesTotalBooks = found[0].SeriesTotal
			}
			localCount := len(books)
			redundant := make(map[int]bool)
			for _, remote := range found {
				best, bestScore := -1, 0
				positionMatches, titleMatch := 0, -1
				for i := range books[:localCount] {
					if redundant[i] {
						continue
					}
					score := seriesBookMatch(books[i], remote)
					if score >= 3 {
						positionMatches++
					}
					if score == 2 || (score == 1 && titleMatch == -1) {
						titleMatch = i
					}
					if score > bestScore || (score > 0 && score == bestScore && books[best].LibraryBookID == "" && books[i].LibraryBookID != "") {
						best, bestScore = i, score
					}
				}
				if bestScore == 3 && positionMatches > 1 {
					best = titleMatch
				}
				if best >= 0 {
					if books[best].State == "In library" {
						for i := range books[:localCount] {
							if books[i].LibraryBookID == "" && seriesBookMatch(books[i], remote) > 0 {
								redundant[i] = true
							}
						}
					}
					mergeSeriesBookMetadata(&books[best], remote)
					continue
				}
				if remote.Authors == nil {
					remote.Authors = []string{}
				}
				books = append(books, seriesBook{BookMetadata: remote, Formats: []string{}})
			}
			visible := books[:0]
			for i, b := range books {
				if !redundant[i] {
					visible = append(visible, b)
				}
			}
			books = visible
		}
	}

	for i := range books {
		books[i].PublishDate = cmp.Or(metadata.NormalizeReleaseDate(books[i].PublishDate, books[i].Year), books[i].PublishDate)
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
	today := time.Now().UTC().Format(time.DateOnly)
	for i := range books {
		b := &books[i]
		b.SeriesIndexDisplay = catalog.SeriesIndexLabel(b.SeriesIndex)
		b.State = bookAvailability(b.State, b.PublishDate, b.Year, today)
		if b.State == "In library" {
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

var seriesTitleAnnotation = regexp.MustCompile(`\s+(?:\([^()]*\)|\[[^\[\]]*\])\s*$`)

// A matching owned volume takes precedence over an exact-title catalog placeholder.
func seriesBookMatch(local seriesBook, remote metadata.BookMetadata) int {
	if len(local.Authors) == 0 {
		return 0
	}
	for _, author := range remote.Authors {
		if !strings.EqualFold(metadata.NormalizeAuthor(local.Authors[0]), metadata.NormalizeAuthor(author)) {
			continue
		}
		if local.SeriesIndex != nil && remote.SeriesIndex != nil && *local.SeriesIndex != *remote.SeriesIndex {
			continue
		}
		localTitle, _ := discoveryTitleParts(seriesTitleAnnotation.ReplaceAllString(local.Title, ""), local.Authors)
		remoteTitle, _ := discoveryTitleParts(seriesTitleAnnotation.ReplaceAllString(remote.Title, ""), remote.Authors)
		exact := discoveryKey(local.Title, local.Authors[0]) == discoveryKey(remote.Title, author)
		sameTitle := discoveryTitleKey(localTitle) == discoveryTitleKey(remoteTitle)
		if local.State == "In library" {
			if exact {
				return 5
			}
			if sameTitle {
				return 4
			}
			if local.SeriesIndex != nil && remote.SeriesIndex != nil {
				return 3
			}
		}
		if exact {
			return 2
		}
		if sameTitle {
			return 1
		}
	}
	return 0
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
	if remoteDate := metadata.NormalizeReleaseDate(remote.PublishDate, remote.Year); remoteDate != "" {
		local.PublishDate = metadata.MergeReleaseDate(local.PublishDate, remoteDate)
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
