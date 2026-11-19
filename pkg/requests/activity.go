package requests

import (
	"context"
	"errors"
	"time"
)

var ErrImportFinishing = errors.New("import is finishing; try again when it completes")

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
	result, err := tx.ExecContext(ctx, `UPDATE book_requests SET dismissed=1,
		status=CASE WHEN status IN ('searching','downloading') THEN 'cancelled' ELSE status END,
		updated_at=CURRENT_TIMESTAMP
		WHERE id=? AND dismissed=0
		AND NOT EXISTS(SELECT 1 FROM download_queue q WHERE q.request_id=book_requests.id AND q.status='importing')
		AND NOT EXISTS(SELECT 1 FROM conversion_queue c JOIN download_queue q ON q.id=c.download_id
			WHERE q.request_id=book_requests.id AND c.status='processing' AND c.current_stage='moving')`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		var dismissed bool
		if err := tx.QueryRowContext(ctx, "SELECT dismissed FROM book_requests WHERE id=?", id).Scan(&dismissed); err != nil {
			return err
		}
		if dismissed {
			return nil
		}
		return ErrImportFinishing
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
