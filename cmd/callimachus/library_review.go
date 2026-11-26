package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// These links can change after a review is loaded. Use the same checks for
// display and within the removal transaction.
const libraryReviewBlock = `CASE
	WHEN EXISTS (SELECT 1 FROM book_editions e JOIN media_assets m ON m.edition_id=e.id WHERE e.book_id=b.id)
		THEN 'This title has indexed media.'
	WHEN EXISTS (SELECT 1 FROM book_requests r WHERE r.book_id=b.id)
		THEN 'This title has linked requests.'
	WHEN EXISTS (SELECT 1 FROM book_editions e JOIN download_queue q ON q.edition_id=e.id WHERE e.book_id=b.id)
	  OR EXISTS (SELECT 1 FROM book_editions e JOIN conversion_queue q ON q.edition_id=e.id WHERE e.book_id=b.id)
		THEN 'This title has linked downloads or conversions.'
	ELSE '' END`

type libraryReviewTitle struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	Series       string `json:"series"`
	HiddenPath   string `json:"hiddenPath"`
	RemovalBlock string `json:"removalBlock"`
}

func (s *Server) handleLibraryReview(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT b.id, b.title, a.name, COALESCE(s.title, ''), review.hidden_path, `+libraryReviewBlock+`
		FROM hidden_library_review review
		JOIN books b ON b.id=review.book_id
		JOIN authors a ON a.id=b.author_id
		LEFT JOIN series s ON s.id=b.series_id
		WHERE review.kept=0
		ORDER BY b.title COLLATE NOCASE, a.name COLLATE NOCASE, b.id`)
	if err != nil {
		libraryReviewError(w, err)
		return
	}
	defer rows.Close()

	titles := []libraryReviewTitle{}
	for rows.Next() {
		var title libraryReviewTitle
		if err := rows.Scan(&title.ID, &title.Title, &title.Author, &title.Series, &title.HiddenPath, &title.RemovalBlock); err != nil {
			libraryReviewError(w, err)
			return
		}
		titles = append(titles, title)
	}
	if err := rows.Err(); err != nil {
		libraryReviewError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(titles)
}

func (s *Server) handleKeepLibraryReview(w http.ResponseWriter, r *http.Request) {
	result, err := s.db.ExecContext(r.Context(), "UPDATE hidden_library_review SET kept=1 WHERE book_id=? AND kept=0", r.PathValue("id"))
	if err != nil {
		libraryReviewError(w, err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		libraryReviewError(w, err)
		return
	}
	if count == 0 {
		http.Error(w, "This title is no longer awaiting review.", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoveLibraryReview(w http.ResponseWriter, r *http.Request) {
	libraryScanMu.Lock()
	defer libraryScanMu.Unlock()

	ctx := r.Context()
	id := r.PathValue("id")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		libraryReviewError(w, err)
		return
	}
	defer tx.Rollback()

	var authorID, blocked string
	var seriesID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT b.author_id, b.series_id, `+libraryReviewBlock+`
		FROM books b JOIN hidden_library_review review ON review.book_id=b.id
		WHERE b.id=? AND review.kept=0`, id).Scan(&authorID, &seriesID, &blocked)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "This title is no longer awaiting review.", http.StatusNotFound)
		return
	}
	if err != nil {
		libraryReviewError(w, err)
		return
	}
	if blocked != "" {
		http.Error(w, blocked, http.StatusConflict)
		return
	}

	// Only the selected book and its owned rows can cascade. Clean up its two
	// parents only if they have no other catalog or monitoring links.
	if _, err := tx.ExecContext(ctx, "DELETE FROM books WHERE id=?", id); err != nil {
		libraryReviewError(w, err)
		return
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM authors WHERE id=? AND monitored=0 AND discovery_checked_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM books WHERE author_id=authors.id)
		AND NOT EXISTS (SELECT 1 FROM catalog_actions WHERE kind='authors' AND entity_id=authors.id)`, authorID); err != nil {
		libraryReviewError(w, err)
		return
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM series WHERE id=? AND monitored=0 AND audiobook_monitored=0 AND discovery_checked_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM books WHERE series_id=series.id)
		AND NOT EXISTS (SELECT 1 FROM catalog_actions WHERE kind='series' AND entity_id=series.id)`, seriesID); err != nil {
		libraryReviewError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		libraryReviewError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func libraryReviewError(w http.ResponseWriter, err error) {
	slog.Error("hidden library review failed", "error", err)
	http.Error(w, "Unable to process hidden library review.", http.StatusInternalServerError)
}
