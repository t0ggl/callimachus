package main

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

func (s *Server) newCatalogMonitor() *catalog.Monitor {
	return &catalog.Monitor{DB: s.db, Discover: func(ctx context.Context, kind, name string, known []metadata.BookMetadata) ([]metadata.BookMetadata, error) {
		cfg, err := config.Load(ctx, s.db, s.baseOverrides)
		if err != nil {
			return nil, err
		}
		client := s.prowlarrHTTPClient
		if client == nil {
			client = &http.Client{Timeout: 30 * time.Second}
		}
		if cfg.Settings.HardcoverAPIKey != "" {
			p := metadata.NewHardcoverProvider(cfg.Settings.HardcoverAPIKey, metadata.WithHardcoverClient(client))
			if kind == "series" {
				if len(known) == 0 {
					return nil, nil
				}
				return p.SeriesBooks(ctx, name, known...)
			}
			return p.AuthorBooks(ctx, name)
		}
		if kind == "series" {
			return nil, fmt.Errorf("configure Hardcover metadata to discover series releases")
		}
		if !cfg.Settings.OpenLibraryEnabled {
			return nil, fmt.Errorf("enable OpenLibrary or configure Hardcover metadata to discover author releases")
		}
		// OpenLibrary remains available without an API key. Persist only exact memberships.
		return metadata.NewOpenLibraryProvider(metadata.WithOpenLibraryClient(client)).Search(ctx, name)
	}, Search: s.searchMonitoredBook}
}

func (s *Server) searchMonitoredBook(ctx context.Context, id string) error {
	// This non-login identity owns automated requests and has no password or OIDC subject.
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO users(id,username,email,role,auth_source) VALUES('catalog-monitor','catalog-monitor','catalog-monitor@invalid','guest','system')`); err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx, `INSERT INTO book_requests(user_id,book_id,title,author,format,status,year)
		SELECT 'catalog-monitor',b.id,b.title,a.name,f.format,'searching',COALESCE(CAST(substr(b.release_date,1,4) AS INTEGER),0)
		FROM books b JOIN authors a ON a.id=b.author_id
		JOIN missing_monitored_formats f ON f.book_id=b.id
		WHERE b.id=?
		AND NOT EXISTS(SELECT 1 FROM hidden_library_review review WHERE review.book_id=b.id AND review.kept=0)
		AND NOT EXISTS(SELECT 1 FROM book_requests r WHERE r.book_id=b.id
			AND (r.format=f.format OR r.format='both')
			AND r.status IN ('searching','downloading','available','cancelled'))`, id); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id FROM book_requests r
		WHERE r.book_id=? AND r.status='searching' AND r.dismissed=0
		  AND EXISTS(SELECT 1 FROM missing_monitored_formats f WHERE f.book_id=r.book_id AND (r.format=f.format OR r.format='both'))
		  AND NOT EXISTS(SELECT 1 FROM hidden_library_review review WHERE review.book_id=r.book_id AND review.kept=0)`, id)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var requestID int64
		if err := rows.Scan(&requestID); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, requestID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var refreshErr error
	for _, requestID := range ids {
		refreshErr = errors.Join(refreshErr, s.acquisition.Refresh(ctx, requestID))
	}
	return refreshErr
}

func (s *Server) runCatalogMonitor(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.catalogMonitor.Tick(ctx, time.Now()); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "catalog monitoring check failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-s.monitorWake:
		case <-ticker.C:
		}
	}
}

func requestedSeriesIdentity(r *http.Request) (id, author string, hardcoverID int) {
	query := r.URL.Query()
	hardcoverID, _ = strconv.Atoi(query.Get("hardcover_id"))
	return query.Get("id"), strings.TrimSpace(query.Get("author")), max(0, hardcoverID)
}

func (s *Server) catalogEntity(r *http.Request, kind, name string) (string, error) {
	ctx := r.Context()
	c := catalog.New(s.db)
	if kind == "authors" {
		a, err := c.GetOrCreateAuthor(ctx, name)
		if err != nil {
			return "", err
		}
		return a.ID, nil
	}
	if kind == "series" {
		id, author, hardcoverID := requestedSeriesIdentity(r)
		v, err := c.FindSeries(ctx, id, name, author, hardcoverID)
		if id == "" && errors.Is(err, sql.ErrNoRows) {
			var authorID string
			if author != "" {
				a, err := c.GetOrCreateAuthor(ctx, author)
				if err != nil {
					return "", err
				}
				authorID = a.ID
			}
			v, err = c.GetOrCreateSeries(ctx, name, authorID, hardcoverID)
		}
		if err != nil {
			return "", err
		}
		if v != nil {
			return v.ID, nil
		}
	}
	return "", fmt.Errorf("invalid catalog entity")
}

func entityParams(r *http.Request) (string, string, bool) {
	kind, name := r.URL.Query().Get("kind"), strings.TrimSpace(r.URL.Query().Get("name"))
	return kind, name, (kind == "authors" || kind == "series") && name != "" && len(name) <= 500
}

func (s *Server) handleCatalogMonitor(w http.ResponseWriter, r *http.Request) {
	kind, name, ok := entityParams(r)
	if !ok {
		http.Error(w, "Invalid entity", 400)
		return
	}
	var input struct {
		Monitored          *bool `json:"monitored"`
		AudiobookMonitored *bool `json:"audiobookMonitored"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil || (input.Monitored == nil && input.AudiobookMonitored == nil) ||
		(kind != "series" && input.AudiobookMonitored != nil) {
		http.Error(w, "A valid monitoring setting is required", 400)
		return
	}
	id, err := s.catalogEntity(r, kind, name)
	if err == nil {
		err = s.catalogMonitor.SetMonitoring(r.Context(), kind, id, input.Monitored, input.AudiobookMonitored)
	}
	if errors.Is(err, catalog.ErrAmbiguousSeries) {
		http.Error(w, "Choose a series by catalog ID", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "Unable to save monitoring", 500)
		return
	}
	s.wakeMonitor()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) wakeMonitor() {
	select {
	case s.monitorWake <- struct{}{}:
	default:
	}
}
func (s *Server) handleCatalogAction(w http.ResponseWriter, r *http.Request) {
	kind, name, ok := entityParams(r)
	action := r.URL.Query().Get("action")
	if !ok || (action != "discover" && action != "downloads") {
		http.Error(w, "Invalid action", 400)
		return
	}
	id, err := s.catalogEntity(r, kind, name)
	if err == nil {
		err = s.catalogMonitor.Queue(r.Context(), kind, id, action)
	}
	if errors.Is(err, catalog.ErrAmbiguousSeries) {
		http.Error(w, "Choose a series by catalog ID", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "Unable to queue check", 500)
		return
	}
	s.wakeMonitor()
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleCatalogEntity(w http.ResponseWriter, r *http.Request) {
	kind, name, ok := entityParams(r)
	if !ok {
		http.Error(w, "Invalid entity", 400)
		return
	}
	column, bookColumn, photo := "name", "author_id", "COALESCE(photo_url,'')"
	if kind == "series" {
		column, bookColumn, photo = "title", "series_id", "''"
	}
	var id, photoURL string
	var monitored, audiobookMonitored bool
	var err error
	var known []metadata.BookMetadata
	lookup := true
	seriesTitle := name
	var seriesAuthor string
	var hardcoverID int
	if kind == "series" {
		var series *database.Series
		var seriesID string
		seriesID, seriesAuthor, hardcoverID = requestedSeriesIdentity(r)
		c := catalog.New(s.db)
		series, err = c.FindSeries(r.Context(), seriesID, name, seriesAuthor, hardcoverID)
		if errors.Is(err, catalog.ErrAmbiguousSeries) {
			http.Error(w, "Choose a series by catalog ID", http.StatusConflict)
			return
		}
		if err == nil {
			id = series.ID
			seriesTitle = series.Title
			err = s.db.QueryRowContext(r.Context(), "SELECT monitored, audiobook_monitored FROM series WHERE id=?", id).Scan(&monitored, &audiobookMonitored)
			if err == nil {
				known, err = c.SeriesEvidence(r.Context(), id)
				if errors.Is(err, catalog.ErrAmbiguousSeries) {
					lookup = false
					err = nil
				} else if err == nil {
					lookup = len(known) > 0
				}
			}
		} else if errors.Is(err, sql.ErrNoRows) && seriesID != "" {
			lookup = false
		}
	} else {
		err = s.db.QueryRowContext(r.Context(), "SELECT id,monitored,"+photo+" FROM "+kind+" WHERE "+column+"=? COLLATE NOCASE", name).Scan(&id, &monitored, &photoURL)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Unable to load entity", 500)
		return
	}
	type entry struct {
		ID                 string   `json:"id"`
		Title              string   `json:"title"`
		Subtitle           string   `json:"subtitle,omitempty"`
		AuthorName         string   `json:"authorName"`
		SeriesID           string   `json:"seriesId,omitempty"`
		SeriesTitle        string   `json:"seriesTitle,omitempty"`
		Date               string   `json:"releaseDate"`
		Index              *float64 `json:"seriesIndex"`
		Synopsis           string   `json:"synopsis,omitempty"`
		Monitored          bool     `json:"monitored"`
		AudiobookMonitored bool     `json:"audiobookMonitored"`
		State              string   `json:"state"`
		Formats            []string `json:"formats"`
		CoverURL           string   `json:"coverUrl,omitempty"`
	}
	books := []entry{}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT b.id, b.title, COALESCE(b.subtitle,''), a.name, COALESCE(s.title,''), COALESCE(s.id,''),
		       COALESCE(b.release_date,''), b.series_index, COALESCE(b.synopsis,''), b.monitored, COALESCE(b.audiobook_monitored, b.monitored), `+bookAvailabilitySQL+`,
		       (SELECT json_group_array(format) FROM (
		           SELECT DISTINCT UPPER(e.format) AS format
		           FROM book_editions e JOIN media_assets m ON m.edition_id=e.id
		           WHERE e.book_id=b.id AND e.format<>'' ORDER BY format
		       ))
		FROM books b JOIN authors a ON a.id=b.author_id LEFT JOIN series s ON s.id=b.series_id
		WHERE b.`+bookColumn+`=?
		ORDER BY b.series_index IS NULL, b.series_index, b.release_date, b.title, b.id`, id)
	if err != nil {
		http.Error(w, "Unable to load books", 500)
		return
	}
	defer rows.Close()
	today := time.Now().UTC().Format(time.DateOnly)
	for rows.Next() {
		var b entry
		var formats string
		if err := rows.Scan(&b.ID, &b.Title, &b.Subtitle, &b.AuthorName, &b.SeriesTitle, &b.SeriesID, &b.Date, &b.Index, &b.Synopsis, &b.Monitored, &b.AudiobookMonitored, &b.State, &formats); err != nil {
			http.Error(w, "Unable to load books", 500)
			return
		}
		if b.State == "" && metadata.IsNonSingleBookTitle(b.Title, b.Subtitle) {
			continue
		}
		if err := json.Unmarshal([]byte(formats), &b.Formats); err != nil {
			http.Error(w, "Unable to load books", 500)
			return
		}
		b.Date = cmp.Or(metadata.NormalizeReleaseDate(b.Date, 0), b.Date)
		b.State = bookAvailability(b.State, b.Date, 0, today)
		books = append(books, b)
	}
	if rows.Err() != nil {
		http.Error(w, "Unable to load books", 500)
		return
	}
	for i := range books {
		if len(books[i].Formats) > 0 && s.bookCover(r, books[i].ID) != "" {
			books[i].CoverURL = "/api/books/" + books[i].ID + "/cover"
		}
		if kind == "series" && books[i].Index == nil {
			if ser, idx := metadata.ExtractSeriesFromTitle(books[i].Title); idx != nil && (ser == "" || seriesTitleKey(ser) == seriesTitleKey(seriesTitle)) {
				books[i].Index = idx
			}
		}
	}

	if kind == "series" {
		toSeriesBook := func(b entry) seriesBook {
			var authors []string
			if b.AuthorName != "" {
				authors = []string{b.AuthorName}
			}
			return seriesBook{
				BookMetadata: metadata.BookMetadata{
					Title:       b.Title,
					Subtitle:    b.Subtitle,
					Authors:     authors,
					Description: b.Synopsis,
					CoverURL:    b.CoverURL,
					PublishDate: b.Date,
					Series:      b.SeriesTitle,
					SeriesIndex: b.Index,
				},
				BookID: b.ID,
				LibraryBookID: func() string {
					if b.State != "Not in library" && b.State != "Scheduled" {
						return b.ID
					}
					return ""
				}(),
				State:              b.State,
				Monitored:          b.Monitored,
				AudiobookMonitored: b.AudiobookMonitored,
				Formats:            b.Formats,
			}
		}
		mergeLocalEntry := func(dst *entry, src entry) {
			owned := dst.State == "In library"
			if dst.Subtitle == "" && src.Subtitle != "" {
				dst.Subtitle = src.Subtitle
			}
			if dst.Synopsis == "" && src.Synopsis != "" {
				dst.Synopsis = src.Synopsis
			}
			if dst.CoverURL == "" && src.CoverURL != "" {
				dst.CoverURL = src.CoverURL
			}
			if src.Date != "" && (dst.Date == "" || !owned) {
				dst.Date = metadata.MergeReleaseDate(dst.Date, src.Date)
				if dst.State == "Not in library" || dst.State == "Scheduled" {
					dst.State = bookAvailability("", dst.Date, 0, today)
				}
			}
			if src.Index != nil && (dst.Index == nil || !owned) {
				idx := *src.Index
				dst.Index = &idx
			}
			if dst.AuthorName == "" && src.AuthorName != "" {
				dst.AuthorName = src.AuthorName
			}
			if dst.SeriesID == "" && src.SeriesID != "" {
				dst.SeriesID = src.SeriesID
			}
			if !owned && src.State != "In library" {
				dst.Monitored = dst.Monitored || src.Monitored
				dst.AudiobookMonitored = dst.AudiobookMonitored || src.AudiobookMonitored
			}
			if src.State == "In library" && len(src.Formats) > 0 {
				seen := make(map[string]bool, len(dst.Formats))
				for _, f := range dst.Formats {
					seen[f] = true
				}
				for _, f := range src.Formats {
					if !seen[f] {
						seen[f] = true
						dst.Formats = append(dst.Formats, f)
					}
				}
				sort.Strings(dst.Formats)
			}
		}
		if len(books) > 1 {
			kept := make([]entry, 0, len(books))
			for _, b := range books {
				sb := toSeriesBook(b)
				bestIdx, bestScore := -1, 0
				for i := range kept {
					score := seriesBookMatch(toSeriesBook(kept[i]), sb.BookMetadata, seriesTitle)
					if score > bestScore {
						bestIdx, bestScore = i, score
					}
				}
				if bestIdx >= 0 {
					if preferLocalSeriesBook(sb, toSeriesBook(kept[bestIdx])) {
						prev := kept[bestIdx]
						kept[bestIdx] = b
						mergeLocalEntry(&kept[bestIdx], prev)
					} else {
						mergeLocalEntry(&kept[bestIdx], b)
					}
					continue
				}
				kept = append(kept, b)
			}
			books = kept
		}

		if len(known) == 0 && seriesAuthor != "" {
			known = append(known, metadata.BookMetadata{Authors: []string{seriesAuthor}})
		}
		for i := range known {
			clean := stripSeriesTitleAnnotation(known[i].Title)
			known[i].Title = strings.TrimSpace(metadata.StripDiscoveryAuthorPrefix(clean, known[i].Authors...))
		}
		if hardcoverID > 0 && id == "" {
			known = append(known, metadata.BookMetadata{HardcoverSeriesID: hardcoverID})
		}

		if lookup {
			if cfg, cfgErr := config.Load(r.Context(), s.db, s.baseOverrides); cfgErr == nil && cfg.Settings.HardcoverAPIKey != "" {
				client := s.prowlarrHTTPClient
				if client == nil {
					client = &http.Client{Timeout: 20 * time.Second}
				}
				if found, lookupErr := metadata.NewHardcoverProvider(cfg.Settings.HardcoverAPIKey, metadata.WithHardcoverClient(client)).SeriesBooks(r.Context(), seriesTitle, known...); lookupErr == nil {
					localBooks := make([]seriesBook, len(books))
					for i, b := range books {
						localBooks[i] = toSeriesBook(b)
					}
					localCount := len(localBooks)
					redundant := make(map[int]bool)
					for _, remote := range found {
						if remote.SeriesIndex == nil {
							if ser, idx := metadata.ExtractSeriesFromTitle(remote.Title); idx != nil && (ser == "" || seriesTitleKey(ser) == seriesTitleKey(seriesTitle)) {
								remote.SeriesIndex = idx
							}
						}
						best, bestScore := -1, 0
						for i := range localBooks {
							if i < localCount && redundant[i] {
								continue
							}
							score := seriesBookMatch(localBooks[i], remote, seriesTitle)
							if score > bestScore || (score > 0 && score == bestScore && localBooks[best].LibraryBookID == "" && localBooks[i].LibraryBookID != "") {
								best, bestScore = i, score
							}
						}
						if best >= 0 {
							if localBooks[best].State == "In library" {
								for i := range localBooks[:localCount] {
									if i != best && localBooks[i].LibraryBookID == "" && seriesBookMatch(localBooks[i], remote, seriesTitle) > 0 {
										redundant[i] = true
									}
								}
							}
							owned := books[best].State == "In library"
							if books[best].Subtitle == "" && remote.Subtitle != "" {
								books[best].Subtitle = remote.Subtitle
							}
							if books[best].Synopsis == "" && remote.Description != "" {
								books[best].Synopsis = remote.Description
							}
							if books[best].CoverURL == "" && remote.CoverURL != "" {
								books[best].CoverURL = remote.CoverURL
							}
							if remoteDate := metadata.NormalizeReleaseDate(remote.PublishDate, remote.Year); remoteDate != "" && (books[best].Date == "" || !owned) {
								books[best].Date = metadata.MergeReleaseDate(books[best].Date, remoteDate)
								if books[best].State == "Not in library" || books[best].State == "Scheduled" {
									books[best].State = bookAvailability("", books[best].Date, remote.Year, today)
								}
							}
							if remote.SeriesIndex != nil && (books[best].Index == nil || !owned) {
								idx := *remote.SeriesIndex
								books[best].Index = &idx
							}
							mergeSeriesBookMetadata(&localBooks[best], remote)
							continue
						}
						authorName := ""
						if len(remote.Authors) > 0 {
							authorName = remote.Authors[0]
						}
						entrySeriesTitle := remote.Series
						if entrySeriesTitle == "" {
							entrySeriesTitle = seriesTitle
						}
						remoteDate := cmp.Or(metadata.NormalizeReleaseDate(remote.PublishDate, remote.Year), remote.PublishDate)
						newEntry := entry{
							Title:       remote.Title,
							Subtitle:    remote.Subtitle,
							AuthorName:  authorName,
							SeriesID:    id,
							SeriesTitle: entrySeriesTitle,
							Date:        remoteDate,
							Index:       remote.SeriesIndex,
							Synopsis:    remote.Description,
							State:       bookAvailability("", remoteDate, remote.Year, today),
							Formats:     []string{},
							CoverURL:    remote.CoverURL,
						}
						books = append(books, newEntry)
						localBooks = append(localBooks, toSeriesBook(newEntry))
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
		}
		sort.SliceStable(books, func(i, j int) bool {
			ai, bi := books[i].Index, books[j].Index
			if ai != nil && bi != nil {
				if *ai != *bi {
					return *ai < *bi
				}
			} else if ai != nil || bi != nil {
				return ai != nil
			}
			if books[i].Date != books[j].Date {
				if books[i].Date == "" {
					return false
				}
				if books[j].Date == "" {
					return true
				}
				return books[i].Date < books[j].Date
			}
			if c := strings.Compare(strings.ToLower(books[i].Title), strings.ToLower(books[j].Title)); c != 0 {
				return c < 0
			}
			return books[i].ID < books[j].ID
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"monitored": monitored, "audiobookMonitored": audiobookMonitored, "photoUrl": photoURL, "books": books})
}

type resolveBookRequest struct {
	BookID            string   `json:"book_id,omitempty"`
	Title             string   `json:"title"`
	Subtitle          string   `json:"subtitle,omitempty"`
	Author            string   `json:"author"`
	Series            string   `json:"series,omitempty"`
	SeriesID          string   `json:"series_id,omitempty"`
	HardcoverSeriesID int      `json:"hardcover_series_id,omitempty"`
	SeriesIndex       *float64 `json:"series_index,omitempty"`
	ReleaseDate       string   `json:"release_date,omitempty"`
	Synopsis          string   `json:"synopsis,omitempty"`
	CoverURL          string   `json:"cover_url,omitempty"`
}

func validExternalCoverURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return ""
	}
	return u.String()
}

func (s *Server) handleResolveBook(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "Database unavailable", http.StatusInternalServerError)
		return
	}
	var req resolveBookRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, "Invalid book request", http.StatusBadRequest)
		return
	}
	c := catalog.New(s.db)
	bookID := strings.TrimSpace(req.BookID)
	if bookID != "" {
		existing, err := c.GetBookByID(r.Context(), bookID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Book not found", http.StatusNotFound)
				return
			}
			http.Error(w, "Unable to resolve book", http.StatusInternalServerError)
			return
		}
		bookID = existing.ID
	} else {
		title := strings.TrimSpace(req.Title)
		authorName := strings.TrimSpace(req.Author)
		if authorName == "" {
			authorName = "Unknown"
		}
		seriesTitle := strings.TrimSpace(req.Series)
		requestedSeriesID := strings.TrimSpace(req.SeriesID)
		if title == "" || len(title) > 500 || len(authorName) > 500 || len(seriesTitle) > 500 {
			http.Error(w, "Invalid book metadata", http.StatusBadRequest)
			return
		}
		author, err := c.GetOrCreateAuthor(r.Context(), authorName)
		if err != nil {
			http.Error(w, "Unable to resolve author", http.StatusInternalServerError)
			return
		}
		var seriesID *string
		if requestedSeriesID != "" {
			series, err := c.GetSeriesByID(r.Context(), requestedSeriesID)
			if err == nil && series != nil {
				seriesID = &series.ID
			} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Unable to resolve series", http.StatusInternalServerError)
				return
			}
		}
		if seriesID == nil && seriesTitle != "" {
			series, err := c.GetOrCreateSeries(r.Context(), seriesTitle, author.ID, req.HardcoverSeriesID)
			if err != nil {
				http.Error(w, "Unable to resolve series", http.StatusInternalServerError)
				return
			}
			if series != nil {
				seriesID = &series.ID
			}
		}
		var indexDisplay *string
		if req.SeriesIndex != nil {
			label := catalog.SeriesIndexLabel(req.SeriesIndex)
			indexDisplay = &label
		}
		var releaseDate *string
		if norm := metadata.NormalizeReleaseDate(req.ReleaseDate, 0); norm != "" {
			releaseDate = &norm
		}
		var subtitle *string
		if sub := strings.TrimSpace(req.Subtitle); sub != "" {
			subtitle = &sub
		}
		var synopsis *string
		if syn := strings.TrimSpace(req.Synopsis); syn != "" {
			synopsis = &syn
		}
		saved, err := c.GetOrCreateBook(r.Context(), &database.Book{
			AuthorID:           author.ID,
			SeriesID:           seriesID,
			SeriesIndex:        req.SeriesIndex,
			SeriesIndexDisplay: indexDisplay,
			Title:              title,
			Subtitle:           subtitle,
			ReleaseDate:        releaseDate,
			Synopsis:           synopsis,
			Monitored:          false,
		})
		if err != nil {
			http.Error(w, "Unable to save book", http.StatusInternalServerError)
			return
		}
		if err := c.SaveIdealMetadata(r.Context(), saved.ID, title, seriesTitle, req.SeriesIndex, catalog.IdealSourceMatch); err != nil {
			http.Error(w, "Unable to save ideal metadata", http.StatusInternalServerError)
			return
		}
		bookID = saved.ID
	}

	books, err := s.queryBooks(r.Context(), r, bookID)
	if err != nil || len(books) == 0 {
		http.Error(w, "Unable to load resolved book", http.StatusInternalServerError)
		return
	}
	book := books[0]
	if book.CoverURL == "" {
		book.CoverURL = validExternalCoverURL(req.CoverURL)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(book)
}

func (s *Server) handleBookMonitor(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "Database unavailable", http.StatusInternalServerError)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		trimmed := strings.TrimPrefix(r.URL.Path, "/api/books/")
		if strings.HasSuffix(trimmed, "/monitor") {
			id = strings.TrimSpace(strings.TrimSuffix(trimmed, "/monitor"))
		}
	}
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	var input struct {
		Monitored          *bool `json:"monitored"`
		AudiobookMonitored *bool `json:"audiobookMonitored"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil || (input.Monitored == nil && input.AudiobookMonitored == nil) {
		http.Error(w, "A monitoring setting is required", http.StatusBadRequest)
		return
	}
	var monitored, audiobookMonitored bool
	err := s.db.QueryRowContext(r.Context(), `
		UPDATE books
		SET monitored = COALESCE(?, monitored),
		    audiobook_monitored = COALESCE(?, audiobook_monitored),
		    availability_checked_at = CASE WHEN ? OR ? THEN NULL ELSE availability_checked_at END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		RETURNING monitored, COALESCE(audiobook_monitored, monitored)`, input.Monitored, input.AudiobookMonitored,
		input.Monitored, input.AudiobookMonitored, id).Scan(&monitored, &audiobookMonitored)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Unable to update book monitoring", http.StatusInternalServerError)
		return
	}
	s.wakeMonitor()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":                 id,
		"monitored":          monitored,
		"audiobookMonitored": audiobookMonitored,
	})
}
