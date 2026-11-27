package requests

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/t0ggl/callimachus/pkg/database"
)

var (
	ErrImportFinishing   = errors.New("import is finishing; try again when it completes")
	ErrCompletedActivity = errors.New("completed activity cannot be removed")
)

type Event struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	Stage      string    `json:"stage,omitempty"`
	Message    string    `json:"message"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (s *RequestService) events(ctx context.Context, id int64) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,stage,message,occurred_at FROM request_events WHERE request_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Kind, &e.Stage, &e.Message, &e.OccurredAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *RequestService) dismiss(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Once publishing to the library starts, allow it to finish before dismissal.
	// Completed activities are kept as a permanent log and cannot be dismissed.
	var bookID *string
	var format string
	err = tx.QueryRowContext(ctx, `UPDATE book_requests SET dismissed=1,
		status=CASE WHEN status IN ('searching','downloading') THEN 'cancelled' ELSE status END,
		updated_at=CURRENT_TIMESTAMP
		WHERE id=? AND dismissed=0 AND status != 'available'
		AND NOT EXISTS(SELECT 1 FROM download_queue q WHERE q.request_id=book_requests.id AND q.status='importing')
		AND NOT EXISTS(SELECT 1 FROM conversion_queue c JOIN download_queue q ON q.id=c.download_id
			WHERE q.request_id=book_requests.id AND c.status='processing' AND c.current_stage='moving')
		RETURNING book_id, format`, id).Scan(&bookID, &format)
	if errors.Is(err, sql.ErrNoRows) {
		var dismissed bool
		var status string
		if err := tx.QueryRowContext(ctx, "SELECT dismissed, status FROM book_requests WHERE id=?", id).Scan(&dismissed, &status); err != nil {
			return err
		}
		if dismissed {
			return nil
		}
		if status == StatusAvailable {
			return ErrCompletedActivity
		}
		return ErrImportFinishing
	}
	if err != nil {
		return err
	}
	if bookID != nil {
		var ebook, audiobook *bool
		disabled := false
		if format == FormatEPUB || format == FormatBoth {
			ebook = &disabled
		}
		if format == FormatM4B || format == FormatBoth {
			audiobook = &disabled
		}
		if _, _, err := database.SetBookMonitoring(ctx, tx, *bookID, ebook, audiobook); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversion_queue SET status='cancelled',updated_at=CURRENT_TIMESTAMP
		WHERE status IN ('pending','processing') AND download_id IN (SELECT id FROM download_queue WHERE request_id=?)`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE download_queue SET status='cancelled',updated_at=CURRENT_TIMESTAMP
		WHERE request_id=? AND status NOT IN ('completed','failed','error','cancelled')`, id); err != nil {
		return err
	}
	return tx.Commit()
}
