package database

import (
	"context"
	"database/sql"
)

func (db *DB) SetBookMonitoring(ctx context.Context, id string, ebook, audiobook *bool) (bool, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	e, a, err := SetBookMonitoring(ctx, tx, id, ebook, audiobook)
	if err != nil {
		return false, false, err
	}
	return e, a, tx.Commit()
}

// SetBookMonitoring records an explicit choice for each supplied format and
// removes pending automatic requests for formats the user has opted out of.
func SetBookMonitoring(ctx context.Context, tx *sql.Tx, id string, ebook, audiobook *bool) (bool, bool, error) {
	var ebookMonitored, audiobookMonitored bool
	err := tx.QueryRowContext(ctx, `
		UPDATE books
		SET ebook_monitored = COALESCE(?1, ebook_monitored),
		    audiobook_monitored = COALESCE(?2, audiobook_monitored, ebook_monitored),
		    ebook_monitoring_excluded = COALESCE(NOT ?1, ebook_monitoring_excluded),
		    audiobook_monitoring_excluded = COALESCE(NOT ?2, audiobook_monitoring_excluded),
		    availability_checked_at = CASE WHEN ?1 OR ?2 THEN NULL ELSE availability_checked_at END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?3
		RETURNING ebook_monitored, audiobook_monitored
	`, ebook, audiobook, id).Scan(&ebookMonitored, &audiobookMonitored)
	if err != nil {
		return false, false, err
	}
	if (ebook != nil && !*ebook) || (audiobook != nil && !*audiobook) {
		// Retire legacy combined searches so the monitor can queue only the
		// remaining format.
		_, err = tx.ExecContext(ctx, `
			UPDATE book_requests
			SET status='cancelled', dismissed=1, updated_at=CURRENT_TIMESTAMP
			WHERE book_id=? AND user_id='catalog-monitor' AND dismissed=0
			  AND status IN ('searching','error')
			  AND (format='both' OR NOT EXISTS (
			      SELECT 1 FROM missing_monitored_formats f
			      WHERE f.book_id=book_requests.book_id AND f.format=book_requests.format
			  ))`, id)
	}
	return ebookMonitored, audiobookMonitored, err
}
