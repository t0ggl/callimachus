package database

import (
	"context"
	"fmt"
)

// PruneLogs removes expired terminal job history in bounded transactions. SQLite
// WAL readers can continue throughout; writers only wait for one small batch.
// Both timestamps must be old so a recently finished long-running job survives.
// Acquisition still needs queue state until its request becomes available.
func (db *DB) PruneLogs(ctx context.Context, retentionDays int) error {
	if retentionDays < 1 || retentionDays > 365000 {
		return fmt.Errorf("retention days must be between 1 and 365000")
	}
	var cutoff string
	if err := db.QueryRowContext(ctx, `SELECT datetime('now', ?)`, fmt.Sprintf("-%d days", retentionDays)).Scan(&cutoff); err != nil {
		return fmt.Errorf("retention cutoff: %w", err)
	}
	for _, table := range []string{"transcode_queue", "download_queue"} {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := db.pruneLogBatch(ctx, table, cutoff)
			if err != nil {
				return err
			}
			if n < 500 {
				break
			}
		}
	}
	return nil
}

func (db *DB) pruneLogBatch(ctx context.Context, table, cutoff string) (int64, error) {
	// Table names come exclusively from PruneLogs, never from user input.
	query := `SELECT id FROM ` + table + ` WHERE status IN ('completed', 'failed', 'cancelled') AND updated_at < ? AND created_at < ?`
	if table == "download_queue" {
		query += ` AND NOT EXISTS (SELECT 1 FROM acquisition_candidates c JOIN book_requests r ON r.id=c.request_id WHERE c.queue_id=download_queue.id AND r.status != 'available')`
	}
	query += ` LIMIT 500`
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin log pruning: %w", err)
	}
	defer tx.Rollback()
	if table == "download_queue" {
		// Available requests already hold their book ID. Remove their expired cached
		// candidates alongside history, avoiding dangling queue references.
		if _, err := tx.ExecContext(ctx, `DELETE FROM acquisition_candidates WHERE queue_id IN (`+query+`)`, cutoff, cutoff); err != nil {
			return 0, fmt.Errorf("prune candidates: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id IN (`+query+`)`, cutoff, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune %s: %w", table, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit log pruning: %w", err)
	}
	return n, nil
}
