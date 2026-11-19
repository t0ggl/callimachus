package main

import (
	"context"
	"encoding/json"
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
	// Reconcile missing files only after a successful complete walk. Never delete books.
	rows, err := s.db.QueryContext(ctx, "SELECT id, file_path FROM media_assets")
	if err != nil {
		return 0, err
	}
	type asset struct{ id, path string }
	var missing []asset
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
		if _, err := os.Stat(a.path); os.IsNotExist(err) {
			missing = append(missing, a)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, a := range missing {
		if _, err := s.db.ExecContext(ctx, "DELETE FROM media_assets WHERE id=?", a.id); err != nil {
			return 0, err
		}
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
