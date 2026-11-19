package main

import (
	"net/http"
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
			status=CASE WHEN status IN ('completed','failed','error') THEN status ELSE 'cancelled' END
			WHERE id=? AND request_id IS NULL AND status!='importing'
			AND NOT EXISTS(SELECT 1 FROM conversion_queue WHERE download_id=download_queue.id AND status='processing' AND current_stage='moving')`
	case "conversions":
		query = `UPDATE conversion_queue SET dismissed=1, updated_at=CURRENT_TIMESTAMP,
			status=CASE WHEN status IN ('completed','failed') THEN status ELSE 'cancelled' END
			WHERE id=? AND download_id IS NULL AND NOT(status='processing' AND current_stage='moving')`
	default:
		http.NotFound(w, r)
		return
	}
	result, err := tx.ExecContext(ctx, query, id)
	if err != nil {
		http.Error(w, "Unable to remove activity", http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		http.Error(w, "Activity is unavailable or its import is finishing", http.StatusConflict)
		return
	}
	if r.PathValue("kind") == "downloads" {
		if _, err := tx.ExecContext(ctx, `UPDATE conversion_queue SET status='cancelled',updated_at=CURRENT_TIMESTAMP
			WHERE download_id=? AND status IN ('pending','processing')`, id); err != nil {
			http.Error(w, "Unable to cancel conversion", http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Unable to remove activity", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
