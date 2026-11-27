package converter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/internal/uuid"
	"github.com/t0ggl/callimachus/pkg/database"
)

// Pipeline stages for audio converting.
const (
	StageQueued     = "queued"
	StagePreparing  = "preparing"
	StageConverting = "converting"
	StageMuxing     = "muxing"
	StageTagging    = "tagging"
	StageMoving     = "moving"
	StageCompleted  = "completed"
)

// Job statuses for conversion_queue.
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
)

// DefaultMaxAttempts is the standard number of retries before marking a job permanently failed.
const DefaultMaxAttempts = 3

// ErrJobNotFound indicates the requested conversion job does not exist.
var ErrJobNotFound = errors.New("conversion job not found")

// Queue provides SQLite-backed FIFO queue management for audio converting jobs.
type Queue struct {
	db *database.DB
}

// NewQueue creates a new Queue instance backed by db.
func NewQueue(db *database.DB) *Queue {
	return &Queue{db: db}
}

// Enqueue creates and inserts a new conversion job into the queue, returning the created item.
func (q *Queue) Enqueue(ctx context.Context, editionID, sourcePath, targetPath string, priority int, downloadID ...string) (*database.ConversionQueueItem, error) {
	return q.enqueue(ctx, editionID, sourcePath, targetPath, priority, "", downloadID)
}

func (q *Queue) enqueue(ctx context.Context, editionID, sourcePath, targetPath string, priority int, ebookContext string, downloadIDs []string) (*database.ConversionQueueItem, error) {
	return insertJob(ctx, q.db, editionID, sourcePath, targetPath, priority, ebookContext, "", downloadIDs)
}

func insertJob(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, editionID, sourcePath, targetPath string, priority int, ebookContext, intakeContext string, downloadIDs []string) (*database.ConversionQueueItem, error) {
	if strings.TrimSpace(editionID) == "" {
		return nil, errors.New("editionID cannot be empty")
	}
	if strings.TrimSpace(sourcePath) == "" {
		return nil, errors.New("sourcePath cannot be empty")
	}
	if strings.TrimSpace(targetPath) == "" {
		return nil, errors.New("targetPath cannot be empty")
	}

	var downloadID *string
	if len(downloadIDs) > 0 {
		downloadID = &downloadIDs[0]
	}
	id := uuid.NewString()
	now := time.Now().UTC()

	query := `
		INSERT INTO conversion_queue (
			id, edition_id, source_path, target_path, status,
			progress_percent, current_stage, priority, attempts, max_attempts,
			created_at, updated_at, ebook_context, intake_context, download_id
		) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE ? IS NULL OR EXISTS(SELECT 1 FROM download_queue WHERE id=? AND status='importing')
		RETURNING id, edition_id, source_path, target_path, status, progress_percent,
		          current_stage, error_message, worker_id, attempts, max_attempts,
		          priority, created_at, updated_at, started_at, completed_at;
	`

	row := db.QueryRowContext(ctx, query,
		id, editionID, sourcePath, targetPath, StatusPending,
		0.0, StageQueued, priority, 0, DefaultMaxAttempts,
		now, now, ebookContext, intakeContext, downloadID, downloadID, downloadID,
	)

	item, err := scanQueueItem(row)
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue conversion job: %w", err)
	}
	slog.InfoContext(ctx, "conversion job queued", "job_id", id)
	return item, nil
}

// LeaseJob atomically leases strictly one pending job for the specified worker instance.
// Returns (nil, nil) if no pending job is available.
func (q *Queue) LeaseJob(ctx context.Context, workerID string) (*database.ConversionQueueItem, error) {
	if strings.TrimSpace(workerID) == "" {
		return nil, errors.New("workerID cannot be empty")
	}

	now := time.Now().UTC()
	query := `
		UPDATE conversion_queue
		SET status = ?,
		    worker_id = ?,
		    started_at = ?,
		    updated_at = ?
		WHERE id = (
			SELECT id FROM conversion_queue
			WHERE status = ?
			ORDER BY priority DESC, created_at ASC
			LIMIT 1
		)
		RETURNING id, edition_id, source_path, target_path, status, progress_percent,
		          current_stage, error_message, worker_id, attempts, max_attempts,
		          priority, created_at, updated_at, started_at, completed_at;
	`

	row := q.db.QueryRowContext(ctx, query,
		StatusProcessing, workerID, now, now, StatusPending,
	)

	item, err := scanQueueItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lease conversion job failed: %w", err)
	}
	return item, nil
}

// UpdateProgress updates the progress percentage, stage, and optional error message of a job.
func (q *Queue) UpdateProgress(ctx context.Context, jobID string, stage string, percent float64, errorMsg *string) error {
	if strings.TrimSpace(jobID) == "" {
		return errors.New("jobID cannot be empty")
	}

	now := time.Now().UTC()
	query := `
		UPDATE conversion_queue
		SET current_stage = ?,
		    progress_percent = ?,
		    error_message = ?,
		    updated_at = ?
		WHERE id = ? AND status != 'cancelled';
	`

	res, err := q.db.ExecContext(ctx, query, stage, percent, errorMsg, now, jobID)
	if err != nil {
		return fmt.Errorf("update progress failed: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrJobNotFound
	}
	return nil
}

// CompleteJob marks the conversion job as completed with 100% progress.
func (q *Queue) CompleteJob(ctx context.Context, jobID string) error {
	if strings.TrimSpace(jobID) == "" {
		return errors.New("jobID cannot be empty")
	}

	now := time.Now().UTC()
	query := `
		UPDATE conversion_queue
		SET status = ?,
		    progress_percent = 100.0,
		    current_stage = ?,
		    completed_at = ?,
		    updated_at = ?
		WHERE id = ? AND status != 'cancelled';
	`

	res, err := q.db.ExecContext(ctx, query, StatusCompleted, StageCompleted, now, now, jobID)
	if err != nil {
		return fmt.Errorf("complete job failed: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrJobNotFound
	}
	return nil
}

// FailJob handles job execution failure. It increments attempts, marks as 'failed' if
// attempts >= max_attempts, or resets back to 'pending' with cleared worker lease if retryable.
func (q *Queue) FailJob(ctx context.Context, jobID string, jobErr error) error {
	if strings.TrimSpace(jobID) == "" {
		return errors.New("jobID cannot be empty")
	}

	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fail job begin tx failed: %w", err)
	}
	defer tx.Rollback()

	var attempts, maxAttempts int
	err = tx.QueryRowContext(ctx, "SELECT attempts, max_attempts FROM conversion_queue WHERE id = ? AND status != 'cancelled'", jobID).Scan(&attempts, &maxAttempts)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrJobNotFound
		}
		return fmt.Errorf("fail job query attempts failed: %w", err)
	}

	newAttempts := attempts + 1
	var errMsg *string
	if jobErr != nil {
		msg := jobErr.Error()
		errMsg = &msg
	}

	now := time.Now().UTC()
	if newAttempts >= maxAttempts {
		// Permanently failed
		_, err = tx.ExecContext(ctx, `
			UPDATE conversion_queue
			SET status = ?,
			    attempts = ?,
			    error_message = ?,
			    completed_at = ?,
			    updated_at = ?
			WHERE id = ? AND status != 'cancelled';
		`, StatusFailed, newAttempts, errMsg, now, now, jobID)
	} else {
		// Retryable: return to pending and clear worker lease
		_, err = tx.ExecContext(ctx, `
			UPDATE conversion_queue
			SET status = ?,
			    worker_id = NULL,
			    started_at = NULL,
			    attempts = ?,
			    error_message = ?,
			    updated_at = ?
			WHERE id = ? AND status != 'cancelled';
		`, StatusPending, newAttempts, errMsg, now, jobID)
	}

	if err != nil {
		return fmt.Errorf("fail job update status failed: %w", err)
	}

	return tx.Commit()
}

// GetJob retrieves a conversion queue item by ID.
func (q *Queue) GetJob(ctx context.Context, jobID string) (*database.ConversionQueueItem, error) {
	if strings.TrimSpace(jobID) == "" {
		return nil, errors.New("jobID cannot be empty")
	}

	query := `
		SELECT id, edition_id, source_path, target_path, status, progress_percent,
		       current_stage, error_message, worker_id, attempts, max_attempts,
		       priority, created_at, updated_at, started_at, completed_at
		FROM conversion_queue
		WHERE id = ?;
	`
	row := q.db.QueryRowContext(ctx, query, jobID)
	item, err := scanQueueItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrJobNotFound
		}
		return nil, fmt.Errorf("get job failed: %w", err)
	}
	return item, nil
}

func scanQueueItem(scanner interface{ Scan(dest ...any) error }) (*database.ConversionQueueItem, error) {
	var item database.ConversionQueueItem
	var errMsg, workerID sql.NullString
	var createdAtVal, updatedAtVal, startedAtVal, completedAtVal any

	err := scanner.Scan(
		&item.ID,
		&item.EditionID,
		&item.SourcePath,
		&item.TargetPath,
		&item.Status,
		&item.ProgressPercent,
		&item.CurrentStage,
		&errMsg,
		&workerID,
		&item.Attempts,
		&item.MaxAttempts,
		&item.Priority,
		&createdAtVal,
		&updatedAtVal,
		&startedAtVal,
		&completedAtVal,
	)
	if err != nil {
		return nil, err
	}

	if errMsg.Valid {
		item.ErrorMessage = &errMsg.String
	}
	if workerID.Valid {
		item.WorkerID = &workerID.String
	}

	if t, ok := parseTimeValue(createdAtVal); ok {
		item.CreatedAt = t
	}
	if t, ok := parseTimeValue(updatedAtVal); ok {
		item.UpdatedAt = t
	}
	if t, ok := parseTimeValue(startedAtVal); ok {
		item.StartedAt = &t
	}
	if t, ok := parseTimeValue(completedAtVal); ok {
		item.CompletedAt = &t
	}

	return &item, nil
}

func parseTimeValue(val any) (time.Time, bool) {
	if val == nil {
		return time.Time{}, false
	}
	switch v := val.(type) {
	case time.Time:
		return v, true
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return time.Time{}, false
		}
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05",
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, v); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	case []byte:
		return parseTimeValue(string(v))
	default:
		return time.Time{}, false
	}
}
