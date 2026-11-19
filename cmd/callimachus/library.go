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
	rows, err := s.db.QueryContext(ctx, "SELECT id, file_path FROM media_assets")
	if err != nil {
		return 0, err
	}
	type asset struct{ id, path, hiddenPath string }
	var stale []asset
	for rows.Next() {
		var a asset
		if err := rows.Scan(&a.id, &a.path); err != nil {
			rows.Close()
			return 0, err
		}
		rel, relErr := filepath.Rel(report.LibraryRoot, a.path)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if hasHiddenPathSegment(rel) {
			a.hiddenPath = rel
			stale = append(stale, a)
			continue
		}
		if _, err := os.Stat(a.path); os.IsNotExist(err) {
			stale = append(stale, a)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(stale) == 0 {
		return len(results), nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	type hiddenAsset struct{ edition, path string }
	var hidden []hiddenAsset
	for _, a := range stale {
		var edition string
		err := tx.QueryRowContext(ctx, "DELETE FROM media_assets WHERE id=? AND file_path=? RETURNING edition_id", a.id, a.path).Scan(&edition)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if a.hiddenPath != "" {
			hidden = append(hidden, hiddenAsset{edition, a.hiddenPath})
		}
	}

	// Legacy scans and catalog discovery could reuse the same records. Save
	// evidence for review only when the title has no remaining media, after all
	// stale inventory is gone. A prior Keep decision survives another purge.
	for _, a := range hidden {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO hidden_library_review(book_id, hidden_path)
			SELECT e.book_id, ? FROM book_editions e
			WHERE e.id=? AND NOT EXISTS (
				SELECT 1 FROM book_editions other JOIN media_assets m ON m.edition_id=other.id
				WHERE other.book_id=e.book_id
			)
			ON CONFLICT(book_id) DO NOTHING`, a.path, a.edition); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(results), nil
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
