package main

import (
	"database/sql"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/t0ggl/callimachus/pkg/config"
)

func bookAssetFilename(serverPath string) string {
	// Also strip Windows separators from libraries imported on another OS.
	serverPath = strings.ReplaceAll(serverPath, `\`, "/")
	if serverPath == "" || strings.HasSuffix(serverPath, "/") {
		return ""
	}
	name := filepath.Base(serverPath)
	if name == "." || name == ".." {
		return ""
	}
	return name
}

func (s *Server) handleBookFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var path string
	err := s.db.QueryRowContext(ctx, `
		SELECT m.file_path FROM media_assets m
		JOIN book_editions e ON e.id = m.edition_id
		WHERE e.book_id = ? AND m.id = ?
	`, r.PathValue("id"), r.PathValue("assetId")).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Unable to load book file", http.StatusInternalServerError)
		return
	}
	cfg, err := config.Load(ctx, s.db, s.baseOverrides)
	if err != nil {
		http.Error(w, "Unable to load book file", http.StatusInternalServerError)
		return
	}
	file, info, err := openLibraryFile(cfg.Settings.LibraryRoot, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	filename := bookAssetFilename(path)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func openLibraryFile(libraryRoot, path string) (*os.File, os.FileInfo, error) {
	// Resolve both paths before comparing them, including absolute symlink targets.
	rootPath, err := filepath.EvalSymlinks(libraryRoot)
	if err != nil {
		return nil, nil, err
	}
	rootPath, err = filepath.Abs(rootPath)
	if err != nil {
		return nil, nil, err
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, nil, err
	}
	resolvedPath, err = filepath.Abs(resolvedPath)
	if err != nil {
		return nil, nil, err
	}
	rel, err := filepath.Rel(rootPath, resolvedPath)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, nil, os.ErrInvalid
	}
	// Keep the open confined even if a symlink changes after resolution.
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	if info, err := root.Stat(rel); err != nil || !info.Mode().IsRegular() {
		return nil, nil, os.ErrInvalid
	}
	file, err := root.Open(rel)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, os.ErrInvalid
	}
	return file, info, nil
}
