package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/t0ggl/callimachus/pkg/config"
)

type deleteBookRequest struct {
	DeleteFiles bool `json:"deleteFiles"`
}

type deleteBookResponse struct {
	Deleted      bool `json:"deleted"`
	DeletedFiles int  `json:"deletedFiles"`
}

func parseDeleteFilesOption(w http.ResponseWriter, r *http.Request) (bool, bool) {
	if raw := strings.TrimSpace(r.URL.Query().Get("deleteFiles")); raw != "" {
		val, err := strconv.ParseBool(raw)
		if err != nil {
			http.Error(w, "deleteFiles must be a boolean", http.StatusBadRequest)
			return false, false
		}
		return val, true
	}
	if r.Body == nil {
		return false, true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return false, false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return false, true
	}
	var payload deleteBookRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return false, false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return false, false
	}
	return payload.DeleteFiles, true
}

func (s *Server) handleDeleteBook(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "Database unavailable", http.StatusInternalServerError)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	deleteFiles, ok := parseDeleteFilesOption(w, r)
	if !ok {
		return
	}

	deletedFiles, status, errMsg := s.deleteBook(r.Context(), id, deleteFiles)
	if errMsg != "" {
		http.Error(w, errMsg, status)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(deleteBookResponse{
		Deleted:      true,
		DeletedFiles: deletedFiles,
	})
}

func (s *Server) deleteBook(ctx context.Context, id string, deleteFiles bool) (int, int, string) {
	if s.db == nil {
		return 0, http.StatusInternalServerError, "Database unavailable"
	}

	s.metadataRepairMu.Lock()
	defer s.metadataRepairMu.Unlock()
	libraryScanMu.Lock()
	defer libraryScanMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		slog.ErrorContext(ctx, "delete book failed", "book", id, "error", err)
		return 0, http.StatusInternalServerError, "Unable to delete book."
	}
	defer tx.Rollback()

	var authorID string
	var seriesID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT author_id, series_id FROM books WHERE id = ?`, id).Scan(&authorID, &seriesID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, http.StatusNotFound, "Book not found"
	}
	if err != nil {
		slog.ErrorContext(ctx, "delete book lookup failed", "book", id, "error", err)
		return 0, http.StatusInternalServerError, "Unable to delete book."
	}

	var activeImport bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM book_editions e JOIN download_queue q ON q.edition_id = e.id
			WHERE e.book_id = ? AND q.status = 'importing'
		) OR EXISTS(
			SELECT 1 FROM book_editions e JOIN conversion_queue c ON c.edition_id = e.id
			WHERE e.book_id = ? AND c.status = 'processing' AND c.current_stage = 'moving'
		)`, id, id).Scan(&activeImport)
	if err != nil {
		slog.ErrorContext(ctx, "delete book import check failed", "book", id, "error", err)
		return 0, http.StatusInternalServerError, "Unable to delete book."
	}
	if activeImport {
		return 0, http.StatusConflict, "Cannot delete book while an import or conversion is finishing."
	}

	var assetPaths []string
	if deleteFiles {
		rows, err := tx.QueryContext(ctx, `
			SELECT m.file_path
			FROM media_assets m
			JOIN book_editions e ON e.id = m.edition_id
			WHERE e.book_id = ?
			ORDER BY m.file_path, m.id`, id)
		if err != nil {
			slog.ErrorContext(ctx, "delete book asset query failed", "book", id, "error", err)
			return 0, http.StatusInternalServerError, "Unable to delete book."
		}
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				slog.ErrorContext(ctx, "delete book asset scan failed", "book", id, "error", err)
				return 0, http.StatusInternalServerError, "Unable to delete book."
			}
			assetPaths = append(assetPaths, p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			slog.ErrorContext(ctx, "delete book asset rows failed", "book", id, "error", err)
			return 0, http.StatusInternalServerError, "Unable to delete book."
		}
		rows.Close()
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, id); err != nil {
		slog.ErrorContext(ctx, "delete book query failed", "book", id, "error", err)
		return 0, http.StatusInternalServerError, "Unable to delete book."
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM authors WHERE id = ? AND monitored = 0 AND discovery_checked_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM books WHERE author_id = authors.id)
		AND NOT EXISTS (SELECT 1 FROM catalog_actions WHERE kind = 'authors' AND entity_id = authors.id)`, authorID); err != nil {
		slog.ErrorContext(ctx, "delete book author cleanup failed", "book", id, "error", err)
		return 0, http.StatusInternalServerError, "Unable to delete book."
	}
	if seriesID.Valid {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM series WHERE id = ? AND monitored = 0 AND discovery_checked_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM books WHERE series_id = series.id)
			AND NOT EXISTS (SELECT 1 FROM catalog_actions WHERE kind = 'series' AND entity_id = series.id)`, seriesID.String); err != nil {
			slog.ErrorContext(ctx, "delete book series cleanup failed", "book", id, "error", err)
			return 0, http.StatusInternalServerError, "Unable to delete book."
		}
	}

	sharedDirs := make(map[string]bool)
	sharedStems := make(map[string]bool)
	var rootPath string
	var root *os.Root
	if deleteFiles && len(assetPaths) > 0 {
		cfg, err := config.Load(ctx, s.db, s.baseOverrides)
		if err != nil {
			slog.ErrorContext(ctx, "delete book config load failed", "book", id, "error", err)
			return 0, http.StatusInternalServerError, "Unable to delete book."
		}
		if cfg.Settings.LibraryRoot != "" {
			if absRoot, err := filepath.Abs(cfg.Settings.LibraryRoot); err == nil {
				rootPath = absRoot
				if opened, err := os.OpenRoot(rootPath); err == nil {
					root = opened
					defer root.Close()
				} else if !errors.Is(err, os.ErrNotExist) {
					slog.ErrorContext(ctx, "delete book open root failed", "book", id, "error", err)
					return 0, http.StatusInternalServerError, "Unable to delete book files."
				}
			}
		}
		if root != nil {
			for _, assetPath := range assetPaths {
				rel, relErr := repairRelativePath(root, rootPath, assetPath)
				if relErr != nil {
					continue
				}
				dirAbs := filepath.Join(rootPath, filepath.Dir(rel)) + string(filepath.Separator)
				if _, ok := sharedDirs[dirAbs]; !ok {
					var shared bool
					if err := tx.QueryRowContext(ctx, `
						SELECT EXISTS(SELECT 1 FROM media_assets WHERE substr(file_path, 1, length(?)) = ?)`,
						dirAbs, dirAbs).Scan(&shared); err != nil {
						slog.ErrorContext(ctx, "delete book shared dir check failed", "book", id, "error", err)
						return 0, http.StatusInternalServerError, "Unable to delete book."
					}
					sharedDirs[dirAbs] = shared
				}
				stem := strings.TrimSuffix(rel, filepath.Ext(rel))
				stemPrefix := filepath.Join(rootPath, stem) + "."
				if _, ok := sharedStems[stemPrefix]; !ok {
					var shared bool
					if err := tx.QueryRowContext(ctx, `
						SELECT EXISTS(SELECT 1 FROM media_assets WHERE substr(file_path, 1, length(?)) = ?)`,
						stemPrefix, stemPrefix).Scan(&shared); err != nil {
						slog.ErrorContext(ctx, "delete book shared stem check failed", "book", id, "error", err)
						return 0, http.StatusInternalServerError, "Unable to delete book."
					}
					sharedStems[stemPrefix] = shared
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		slog.ErrorContext(ctx, "delete book commit failed", "book", id, "error", err)
		return 0, http.StatusInternalServerError, "Unable to delete book."
	}

	deletedFiles := 0
	if root != nil {
		var fileErr error
		deletedFiles, fileErr = deleteLibraryBookFiles(root, rootPath, assetPaths, sharedDirs, sharedStems)
		if fileErr != nil {
			slog.ErrorContext(ctx, "delete book files failed", "book", id, "error", fileErr)
			return deletedFiles, http.StatusInternalServerError, "Book removed from library, but some files could not be deleted."
		}
	}

	return deletedFiles, http.StatusOK, ""
}

func deleteLibraryBookFiles(root *os.Root, rootPath string, assetPaths []string, sharedDirs, sharedStems map[string]bool) (int, error) {
	deleted := 0
	dirsToPrune := make(map[string]bool)
	seen := make(map[string]bool)

	removeRegular := func(rel string) error {
		if seen[rel] {
			return nil
		}
		seen[rel] = true
		info, err := root.Lstat(rel)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := root.Remove(rel); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		deleted++
		dirsToPrune[filepath.Dir(rel)] = true
		return nil
	}

	for _, assetPath := range assetPaths {
		rel, err := repairRelativePath(root, rootPath, assetPath)
		if err != nil {
			continue
		}
		if err := removeRegular(rel); err != nil {
			return deleted, err
		}
		stem := strings.TrimSuffix(rel, filepath.Ext(rel))
		stemPrefix := filepath.Join(rootPath, stem) + "."
		if !sharedStems[stemPrefix] {
			for _, ext := range []string{".opf", ".jpg", ".jpeg", ".png"} {
				if err := removeRegular(stem + ext); err != nil {
					return deleted, err
				}
			}
		}
		dir := filepath.Dir(rel)
		dirAbs := filepath.Join(rootPath, dir) + string(filepath.Separator)
		if !sharedDirs[dirAbs] {
			for _, name := range []string{"metadata.opf", "cover.jpg", "cover.png", "folder.jpg"} {
				if err := removeRegular(filepath.Join(dir, name)); err != nil {
					return deleted, err
				}
			}
		}
	}

	// Prune empty parent directories up to (never including) libraryRoot.
	for dir := range dirsToPrune {
		for dir != "." && dir != "" && dir != string(filepath.Separator) {
			if err := root.Remove(dir); err != nil {
				break
			}
			dir = filepath.Dir(dir)
		}
	}

	return deleted, nil
}
