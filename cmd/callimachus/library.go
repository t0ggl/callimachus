package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
)

// Serialize manual and scheduled scans so reconciliation cannot race itself.
var libraryScanMu sync.Mutex

func hasHiddenPathSegment(rel string) bool {
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

func (s *Server) scanLibrary(ctx context.Context) (books int, scanErr error) {
	slog.InfoContext(ctx, "library scan started")
	defer func() {
		if scanErr == nil {
			slog.InfoContext(ctx, "library scan completed", "books", books)
		} else if ctx.Err() == nil {
			slog.ErrorContext(ctx, "library scan failed")
		}
	}()
	libraryScanMu.Lock()
	defer libraryScanMu.Unlock()
	cfg, err := config.Load(ctx, s.db, s.baseOverrides)
	if err != nil {
		return 0, err
	}
	report, err := catalog.ScanLibrary(cfg.Settings.LibraryRoot)
	if err != nil {
		return 0, err
	}
	results, err := catalog.New(s.db).ReconcileScanReport(ctx, report)
	if err != nil {
		return 0, err
	}
	// Reconcile missing files and hidden-path entries only after a successful complete walk.
	// Never delete books for normal missing files; only purge orphans left by hidden-path assets.
	rows, err := s.db.QueryContext(ctx, "SELECT id, edition_id, file_path FROM media_assets")
	if err != nil {
		return 0, err
	}
	type asset struct{ id, editionID, path string }
	var missing []asset
	var hidden []asset
	for rows.Next() {
		var a asset
		if err := rows.Scan(&a.id, &a.editionID, &a.path); err != nil {
			rows.Close()
			return 0, err
		}
		rel, relErr := filepath.Rel(report.LibraryRoot, a.path)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if hasHiddenPathSegment(rel) {
			hidden = append(hidden, a)
			continue
		}
		if _, err := os.Stat(a.path); os.IsNotExist(err) {
			missing = append(missing, a)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(missing) == 0 && len(hidden) == 0 {
		return len(results), nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	for _, a := range missing {
		if _, err := tx.ExecContext(ctx, "DELETE FROM media_assets WHERE id=?", a.id); err != nil {
			return 0, err
		}
	}
	if len(hidden) > 0 {
		seenEditions := make(map[string]bool, len(hidden))
		var editionIDs []string
		for _, a := range hidden {
			if _, err := tx.ExecContext(ctx, "DELETE FROM media_assets WHERE id=?", a.id); err != nil {
				return 0, err
			}
			if a.editionID != "" && !seenEditions[a.editionID] {
				seenEditions[a.editionID] = true
				editionIDs = append(editionIDs, a.editionID)
			}
		}
		if err := purgeHiddenCatalogOrphans(ctx, tx, editionIDs); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(results), nil
}

func purgeHiddenCatalogOrphans(ctx context.Context, tx *sql.Tx, editionIDs []string) error {
	seenBooks := make(map[string]bool)
	var bookIDs []string
	for _, editionID := range editionIDs {
		var bookID string
		err := tx.QueryRowContext(ctx, "SELECT book_id FROM book_editions WHERE id=?", editionID).Scan(&bookID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			DELETE FROM book_editions
			WHERE id = ?
			  AND NOT EXISTS (SELECT 1 FROM media_assets m WHERE m.edition_id = book_editions.id)
			  AND NOT EXISTS (SELECT 1 FROM download_queue q WHERE q.edition_id = book_editions.id)
			  AND NOT EXISTS (SELECT 1 FROM conversion_queue c WHERE c.edition_id = book_editions.id)
			  AND NOT EXISTS (
			      SELECT 1 FROM book_requests r
			      WHERE r.book_id = book_editions.book_id
			        AND (r.format = book_editions.format OR r.format = 'both')
			  )`, editionID)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected > 0 && bookID != "" && !seenBooks[bookID] {
			seenBooks[bookID] = true
			bookIDs = append(bookIDs, bookID)
		}
	}

	seenAuthors := make(map[string]bool)
	var authorIDs []string
	seenSeries := make(map[string]bool)
	var seriesIDs []string
	for _, bookID := range bookIDs {
		var authorID string
		var seriesID sql.NullString
		err := tx.QueryRowContext(ctx, "SELECT author_id, series_id FROM books WHERE id=?", bookID).Scan(&authorID, &seriesID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			DELETE FROM books
			WHERE id = ?
			  AND availability_checked_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM book_editions e WHERE e.book_id = books.id)
			  AND NOT EXISTS (SELECT 1 FROM book_requests r WHERE r.book_id = books.id)`, bookID)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			continue
		}
		if authorID != "" && !seenAuthors[authorID] {
			seenAuthors[authorID] = true
			authorIDs = append(authorIDs, authorID)
		}
		if seriesID.Valid && seriesID.String != "" && !seenSeries[seriesID.String] {
			seenSeries[seriesID.String] = true
			seriesIDs = append(seriesIDs, seriesID.String)
		}
	}

	for _, authorID := range authorIDs {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM authors
			WHERE id = ?
			  AND monitored = 0
			  AND discovery_checked_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM books b WHERE b.author_id = authors.id)
			  AND NOT EXISTS (SELECT 1 FROM catalog_actions ca WHERE ca.kind = 'authors' AND ca.entity_id = authors.id)`, authorID); err != nil {
			return err
		}
	}
	for _, seriesID := range seriesIDs {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM series
			WHERE id = ?
			  AND monitored = 0
			  AND discovery_checked_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM books b WHERE b.series_id = series.id)
			  AND NOT EXISTS (SELECT 1 FROM catalog_actions ca WHERE ca.kind = 'series' AND ca.entity_id = series.id)`, seriesID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handleScanLibrary(w http.ResponseWriter, r *http.Request) {
	count, err := s.scanLibrary(r.Context())
	if err != nil {
		http.Error(w, "Library scan failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"books": count})
}
