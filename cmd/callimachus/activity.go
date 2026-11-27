package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/t0ggl/callimachus/pkg/database"
)

// Standalone jobs have no requester. Curators manage them through the same Activity view.
func (s *Server) handleDismissActivity(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), r.PathValue("id")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "Unable to remove activity", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	var query string
	switch r.PathValue("kind") {
	case "downloads":
		query = `UPDATE download_queue SET dismissed=1, updated_at=CURRENT_TIMESTAMP,
			status=CASE WHEN status IN ('failed','error') THEN status ELSE 'cancelled' END
			WHERE id=? AND request_id IS NULL AND status NOT IN ('importing','completed')
			AND NOT EXISTS(SELECT 1 FROM conversion_queue WHERE download_id=download_queue.id AND status='processing' AND current_stage='moving')
			RETURNING edition_id`
	case "conversions":
		query = `UPDATE conversion_queue SET dismissed=1, updated_at=CURRENT_TIMESTAMP,
			status=CASE WHEN status='failed' THEN status ELSE 'cancelled' END
			WHERE id=? AND download_id IS NULL AND status!='completed' AND NOT(status='processing' AND current_stage='moving')
			RETURNING CASE WHEN intake_context='' THEN edition_id END`
	default:
		http.NotFound(w, r)
		return
	}
	var editionID sql.NullString
	err = tx.QueryRowContext(ctx, query, id).Scan(&editionID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Activity is unavailable, already completed, or its import is finishing", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "Unable to remove activity", http.StatusInternalServerError)
		return
	}
	if r.PathValue("kind") == "downloads" {
		if _, err := tx.ExecContext(ctx, `UPDATE conversion_queue SET status='cancelled',updated_at=CURRENT_TIMESTAMP
			WHERE download_id=? AND status IN ('pending','processing')`, id); err != nil {
			http.Error(w, "Unable to cancel conversion", http.StatusInternalServerError)
			return
		}
	}
	if editionID.Valid && editionID.String != "" {
		var bookID, format string
		err := tx.QueryRowContext(ctx, `SELECT book_id, format FROM book_editions WHERE id=?`, editionID.String).Scan(&bookID, &format)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Unable to update book monitoring", http.StatusInternalServerError)
			return
		}
		if err == nil && bookID != "" {
			disabled := false
			var ebook, audiobook *bool
			lower := strings.ToLower(format)
			if lower == "m4b" || lower == "mp3" {
				audiobook = &disabled
			} else {
				ebook = &disabled
			}
			if _, _, err := database.SetBookMonitoring(ctx, tx, bookID, ebook, audiobook); err != nil && !errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Unable to update book monitoring", http.StatusInternalServerError)
				return
			}
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Unable to remove activity", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
