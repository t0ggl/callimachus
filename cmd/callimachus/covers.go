package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/t0ggl/callimachus/pkg/config"
)

// Resolve covers only through catalogued assets inside the configured library.
// No request-supplied filesystem path is accepted.
func (s *Server) bookCover(r *http.Request, bookID string) string {
	cfg, err := config.Load(r.Context(), s.db, s.baseOverrides)
	if err != nil {
		return ""
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT m.file_path FROM media_assets m JOIN book_editions e ON e.id=m.edition_id WHERE e.book_id=? ORDER BY m.file_path`, bookID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	root, err := filepath.EvalSymlinks(cfg.Settings.LibraryRoot)
	if err != nil {
		return ""
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return ""
	}
	for rows.Next() {
		var media string
		if rows.Scan(&media) != nil {
			continue
		}
		candidates := []string{strings.TrimSuffix(media, filepath.Ext(media)) + ".jpg", strings.TrimSuffix(media, filepath.Ext(media)) + ".jpeg", strings.TrimSuffix(media, filepath.Ext(media)) + ".png", filepath.Join(filepath.Dir(media), "cover.jpg"), filepath.Join(filepath.Dir(media), "cover.png"), filepath.Join(filepath.Dir(media), "folder.jpg")}
		for _, candidate := range candidates {
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				continue
			}
			resolved, err = filepath.Abs(resolved)
			if err != nil {
				continue
			}
			rel, err := filepath.Rel(root, resolved)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			if info, err := os.Stat(resolved); err == nil && info.Mode().IsRegular() {
				return resolved
			}
		}
	}
	return ""
}

func (s *Server) handleBookCover(w http.ResponseWriter, r *http.Request) {
	path := s.bookCover(r, r.PathValue("id"))
	if path == "" {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(path); err == nil {
		w.Header().Set("ETag", fmt.Sprintf(`"%x-%x"`, info.Size(), info.ModTime().UnixNano()))
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, path)
}
