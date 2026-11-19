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
		// OpenLibrary remains available without an API key. Persist only exact memberships.
		return metadata.NewOpenLibraryProvider(metadata.WithOpenLibraryClient(client)).Search(ctx, name)
	}, Search: s.searchMonitoredBook}
}

func (s *Server) searchMonitoredBook(ctx context.Context, id string) error {
	// This non-login identity owns automated requests and has no password or OIDC subject.
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO users(id,username,email,role,auth_source) VALUES('catalog-monitor','catalog-monitor','catalog-monitor@invalid','guest','system')`); err != nil {
		return err
	}
	var requestID int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM book_requests WHERE book_id=? AND status IN ('searching','downloading','available') ORDER BY id DESC LIMIT 1`, id).Scan(&requestID)
	if errors.Is(err, sql.ErrNoRows) {
		result, e := s.db.ExecContext(ctx, `INSERT INTO book_requests(user_id,book_id,title,author,format,status,year) SELECT 'catalog-monitor',b.id,b.title,a.name,'both','searching',COALESCE(CAST(substr(b.release_date,1,4) AS INTEGER),0) FROM books b JOIN authors a ON a.id=b.author_id WHERE b.id=?`, id)
		if e != nil {
			return e
		}
		requestID, err = result.LastInsertId()
	}
	if err != nil {
		return err
	}
	var status string
	if err = s.db.QueryRowContext(ctx, `SELECT status FROM book_requests WHERE id=?`, requestID).Scan(&status); err != nil {
		return err
	}
	if status != "searching" {
		return nil
	}
	return s.acquisition.Refresh(ctx, requestID)
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
	column, bookColumn := "name", "author_id"
	if kind == "series" {
		column, bookColumn = "title", "series_id"
	}
	var id string
	var monitored bool
	err := s.db.QueryRowContext(r.Context(), "SELECT id,monitored FROM "+kind+" WHERE "+column+"=? COLLATE NOCASE", name).Scan(&id, &monitored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Unable to load entity", 500)
		return
	}
	type entry struct {
		ID    string   `json:"id"`
		Title string   `json:"title"`
		Date  string   `json:"releaseDate"`
		Index *float64 `json:"seriesIndex"`
		State string   `json:"state"`
	}
	books := []entry{}
	rows, err := s.db.QueryContext(r.Context(), `SELECT b.id,b.title,COALESCE(b.release_date,''),b.series_index,CASE WHEN EXISTS(SELECT 1 FROM book_editions e JOIN media_assets m ON m.edition_id=e.id WHERE e.book_id=b.id) THEN 'In library' WHEN b.release_date>date('now') THEN 'Scheduled' WHEN b.monitored=1 THEN 'Monitored' ELSE 'Not monitored' END FROM books b WHERE b.`+bookColumn+`=? ORDER BY b.series_index,b.release_date,b.title`, id)
	if err != nil {
		http.Error(w, "Unable to load books", 500)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var b entry
		if err = rows.Scan(&b.ID, &b.Title, &b.Date, &b.Index, &b.State); err != nil {
			http.Error(w, "Unable to load books", 500)
			return
		}
		books = append(books, b)
	}
	if rows.Err() != nil {
		http.Error(w, "Unable to load books", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"monitored": monitored, "books": books})
}
