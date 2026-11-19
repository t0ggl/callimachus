package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

func (s *Server) newCatalogMonitor() *catalog.Monitor {
	return &catalog.Monitor{DB: s.db, Discover: func(ctx context.Context, kind, name string) ([]metadata.BookMetadata, error) {
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
				return p.SeriesBooks(ctx, name)
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
		CROSS JOIN (SELECT 'epub' AS format UNION ALL SELECT 'm4b') f
		WHERE b.id=? AND NOT EXISTS(SELECT 1 FROM book_requests r WHERE r.book_id=b.id
			AND r.status IN ('searching','downloading','available','cancelled'))`, id); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM book_requests WHERE book_id=? AND status='searching' AND dismissed=0`, id)
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

func (s *Server) catalogEntity(ctx context.Context, kind, name string) (string, error) {
	c := catalog.New(s.db)
	if kind == "authors" {
		a, err := c.GetOrCreateAuthor(ctx, name)
		if err != nil {
			return "", err
		}
		return a.ID, nil
	}
	if kind == "series" {
		v, err := c.GetOrCreateSeries(ctx, name)
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
		Monitored *bool `json:"monitored"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil || input.Monitored == nil {
		http.Error(w, "monitored is required", 400)
		return
	}
	id, err := s.catalogEntity(r.Context(), kind, name)
	if err == nil {
		err = s.catalogMonitor.SetMonitored(r.Context(), kind, id, *input.Monitored)
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
	id, err := s.catalogEntity(r.Context(), kind, name)
	if err == nil {
		err = s.catalogMonitor.Queue(r.Context(), kind, id, action)
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
	var monitored bool
	err := s.db.QueryRowContext(r.Context(), "SELECT id,monitored,"+photo+" FROM "+kind+" WHERE "+column+"=? COLLATE NOCASE", name).Scan(&id, &monitored, &photoURL)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Unable to load entity", 500)
		return
	}
	type entry struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		Subtitle    string   `json:"subtitle,omitempty"`
		AuthorName  string   `json:"authorName"`
		SeriesTitle string   `json:"seriesTitle,omitempty"`
		Date        string   `json:"releaseDate"`
		Index       *float64 `json:"seriesIndex"`
		State       string   `json:"state"`
		Formats     []string `json:"formats"`
		CoverURL    string   `json:"coverUrl,omitempty"`
	}
	books := []entry{}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT b.id, b.title, COALESCE(b.subtitle,''), a.name, COALESCE(s.title,''),
		       COALESCE(b.release_date,''), b.series_index, `+bookAvailabilitySQL+`,
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
		if err := rows.Scan(&b.ID, &b.Title, &b.Subtitle, &b.AuthorName, &b.SeriesTitle, &b.Date, &b.Index, &b.State, &formats); err != nil {
			http.Error(w, "Unable to load books", 500)
			return
		}
		if err := json.Unmarshal([]byte(formats), &b.Formats); err != nil {
			http.Error(w, "Unable to load books", 500)
			return
		}
		b.State = bookAvailability(b.State, b.Date, today)
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
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"monitored": monitored, "photoUrl": photoURL, "books": books})
}
