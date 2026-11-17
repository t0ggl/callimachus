package converter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func setupTestQueueDB(t *testing.T) (*Queue, *database.DB) {
	t.Helper()
	db := testutil.NewTestDB(t)

	ctx := context.Background()
	// Insert synthetic dummy author, book, book_edition for foreign keys
	_, err := db.ExecContext(ctx, `
		INSERT INTO authors (id, name, sort_name) VALUES ('synth-author-1', 'Synthetic Author One', 'Author One, Synthetic');
		INSERT INTO books (id, author_id, title) VALUES ('synth-book-1', 'synth-author-1', 'Synthetic Book Alpha');
		INSERT INTO book_editions (id, book_id, format) VALUES ('synth-ed-1', 'synth-book-1', 'm4b');
		INSERT INTO book_editions (id, book_id, format) VALUES ('synth-ed-2', 'synth-book-1', 'm4b');
	`)
	if err != nil {
		t.Fatalf("setup foreign keys failed: %v", err)
	}

	return NewQueue(db), db
}

func TestQueue_EnqueueAndGet(t *testing.T) {
	t.Parallel()
	q, _ := setupTestQueueDB(t)
	ctx := context.Background()

	item, err := q.Enqueue(ctx, "synth-ed-1", "/staging/synthetic_book", "/audiobooks/synthetic_book.m4b", 5)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	if item.ID == "" {
		t.Errorf("expected non-empty ID")
	}
	if item.EditionID != "synth-ed-1" {
		t.Errorf("expected edition_id 'synth-ed-1', got '%s'", item.EditionID)
	}
	if item.Status != StatusPending {
		t.Errorf("expected status 'pending', got '%s'", item.Status)
	}
	if item.CurrentStage != StageQueued {
		t.Errorf("expected stage 'queued', got '%s'", item.CurrentStage)
	}
	if item.Priority != 5 {
		t.Errorf("expected priority 5, got %d", item.Priority)
	}
	if item.Attempts != 0 {
		t.Errorf("expected attempts 0, got %d", item.Attempts)
	}
	if item.MaxAttempts != 3 {
		t.Errorf("expected max_attempts 3, got %d", item.MaxAttempts)
	}

	// GetJob
	fetched, err := q.GetJob(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if fetched.ID != item.ID {
		t.Errorf("expected ID '%s', got '%s'", item.ID, fetched.ID)
	}
}

func TestQueue_LeaseJob_PriorityFIFO(t *testing.T) {
	t.Parallel()
	q, _ := setupTestQueueDB(t)
	ctx := context.Background()

	// Enqueue low priority job first, then high priority job
	jobLow, err := q.Enqueue(ctx, "synth-ed-1", "/src/low", "/dst/low.m4b", 0)
	if err != nil {
		t.Fatalf("enqueue low failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond) // ensure distinct timestamps

	jobHigh, err := q.Enqueue(ctx, "synth-ed-1", "/src/high", "/dst/high.m4b", 10)
	if err != nil {
		t.Fatalf("enqueue high failed: %v", err)
	}

	// Lease 1: Must lease the higher priority job first
	leased1, err := q.LeaseJob(ctx, "worker-alpha")
	if err != nil {
		t.Fatalf("LeaseJob 1 failed: %v", err)
	}
	if leased1 == nil {
		t.Fatalf("expected leased job, got nil")
	}
	if leased1.ID != jobHigh.ID {
		t.Errorf("expected high priority job %s to be leased first, got %s", jobHigh.ID, leased1.ID)
	}
	if leased1.Status != StatusProcessing {
		t.Errorf("expected status 'processing', got '%s'", leased1.Status)
	}
	if leased1.WorkerID == nil || *leased1.WorkerID != "worker-alpha" {
		t.Errorf("expected worker_id 'worker-alpha', got %v", leased1.WorkerID)
	}
	if leased1.StartedAt == nil {
		t.Errorf("expected started_at to be populated")
	}

	// Lease 2: Now lease the remaining low priority job
	leased2, err := q.LeaseJob(ctx, "worker-beta")
	if err != nil {
		t.Fatalf("LeaseJob 2 failed: %v", err)
	}
	if leased2 == nil {
		t.Fatalf("expected leased job, got nil")
	}
	if leased2.ID != jobLow.ID {
		t.Errorf("expected low priority job %s to be leased next, got %s", jobLow.ID, leased2.ID)
	}

	// Lease 3: No more pending jobs
	leased3, err := q.LeaseJob(ctx, "worker-gamma")
	if err != nil {
		t.Fatalf("LeaseJob 3 failed: %v", err)
	}
	if leased3 != nil {
		t.Errorf("expected nil job when queue is empty, got %v", leased3)
	}
}

func TestQueue_AtomicConcurrency(t *testing.T) {
	t.Parallel()
	q, _ := setupTestQueueDB(t)
	ctx := context.Background()

	const jobCount = 30
	jobIDs := make([]string, jobCount)
	for i := 0; i < jobCount; i++ {
		item, err := q.Enqueue(ctx, "synth-ed-1", fmt.Sprintf("/src/%d", i), fmt.Sprintf("/dst/%d.m4b", i), 0)
		if err != nil {
			t.Fatalf("enqueue %d failed: %v", i, err)
		}
		jobIDs[i] = item.ID
	}

	const workerCount = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	leasedJobs := make(map[string]string) // jobID -> workerID
	var duplicateCount int64

	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		workerID := fmt.Sprintf("worker-%d", w)
		go func(wID string) {
			defer wg.Done()
			for {
				item, err := q.LeaseJob(ctx, wID)
				if err != nil {
					t.Errorf("worker %s lease error: %v", wID, err)
					return
				}
				if item == nil {
					// Queue empty for this worker
					return
				}

				mu.Lock()
				if existingWorker, exists := leasedJobs[item.ID]; exists {
					atomic.AddInt64(&duplicateCount, 1)
					t.Errorf("CONFLICT: Job %s leased by both %s and %s", item.ID, existingWorker, wID)
				} else {
					leasedJobs[item.ID] = wID
				}
				mu.Unlock()
			}
		}(workerID)
	}

	wg.Wait()

	if duplicateCount > 0 {
		t.Fatalf("Detected %d duplicate leases! Atomic single-job lease contract violated.", duplicateCount)
	}

	mu.Lock()
	totalLeased := len(leasedJobs)
	mu.Unlock()

	if totalLeased != jobCount {
		t.Fatalf("expected all %d jobs to be leased exactly once, got %d", jobCount, totalLeased)
	}
}

func TestQueue_UpdateProgressAndComplete(t *testing.T) {
	t.Parallel()
	q, _ := setupTestQueueDB(t)
	ctx := context.Background()

	item, err := q.Enqueue(ctx, "synth-ed-1", "/src/test", "/dst/test.m4b", 0)
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	leased, err := q.LeaseJob(ctx, "worker-1")
	if err != nil || leased == nil {
		t.Fatalf("lease failed: %v", err)
	}

	// Update stage to encoding
	err = q.UpdateProgress(ctx, item.ID, StageConverting, 45.0, nil)
	if err != nil {
		t.Fatalf("update progress failed: %v", err)
	}

	updated, err := q.GetJob(ctx, item.ID)
	if err != nil {
		t.Fatalf("get job failed: %v", err)
	}
	if updated.CurrentStage != StageConverting {
		t.Errorf("expected stage %s, got %s", StageConverting, updated.CurrentStage)
	}
	if updated.ProgressPercent != 45.0 {
		t.Errorf("expected progress 45.0, got %f", updated.ProgressPercent)
	}

	// Complete job
	err = q.CompleteJob(ctx, item.ID)
	if err != nil {
		t.Fatalf("complete job failed: %v", err)
	}

	completed, err := q.GetJob(ctx, item.ID)
	if err != nil {
		t.Fatalf("get job failed: %v", err)
	}
	if completed.Status != StatusCompleted {
		t.Errorf("expected status 'completed', got '%s'", completed.Status)
	}
	if completed.ProgressPercent != 100.0 {
		t.Errorf("expected progress 100.0, got %f", completed.ProgressPercent)
	}
	if completed.CompletedAt == nil {
		t.Errorf("expected completed_at timestamp to be set")
	}
}

func TestQueue_FailJob_RetryAndMaxAttempts(t *testing.T) {
	t.Parallel()
	q, _ := setupTestQueueDB(t)
	ctx := context.Background()

	item, err := q.Enqueue(ctx, "synth-ed-1", "/src/fail", "/dst/fail.m4b", 0)
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	// Attempt 1: Lease and fail
	l1, err := q.LeaseJob(ctx, "worker-1")
	if err != nil || l1 == nil {
		t.Fatalf("lease 1 failed: %v", err)
	}

	err1 := errors.New("simulated error 1")
	if err := q.FailJob(ctx, item.ID, err1); err != nil {
		t.Fatalf("fail job 1 failed: %v", err)
	}

	j1, _ := q.GetJob(ctx, item.ID)
	if j1.Status != StatusPending {
		t.Errorf("attempt 1: expected status 'pending', got '%s'", j1.Status)
	}
	if j1.Attempts != 1 {
		t.Errorf("attempt 1: expected attempts 1, got %d", j1.Attempts)
	}
	if j1.WorkerID != nil {
		t.Errorf("attempt 1: expected worker_id to be cleared, got %v", *j1.WorkerID)
	}
	if j1.StartedAt != nil {
		t.Errorf("attempt 1: expected started_at to be cleared, got %v", *j1.StartedAt)
	}
	if j1.ErrorMessage == nil || *j1.ErrorMessage != "simulated error 1" {
		t.Errorf("attempt 1: expected error message 'simulated error 1', got %v", j1.ErrorMessage)
	}

	// Attempt 2: Lease and fail
	l2, err := q.LeaseJob(ctx, "worker-2")
	if err != nil || l2 == nil {
		t.Fatalf("lease 2 failed: %v", err)
	}

	err2 := errors.New("simulated error 2")
	if err := q.FailJob(ctx, item.ID, err2); err != nil {
		t.Fatalf("fail job 2 failed: %v", err)
	}

	j2, _ := q.GetJob(ctx, item.ID)
	if j2.Status != StatusPending {
		t.Errorf("attempt 2: expected status 'pending', got '%s'", j2.Status)
	}
	if j2.Attempts != 2 {
		t.Errorf("attempt 2: expected attempts 2, got %d", j2.Attempts)
	}

	// Attempt 3: Lease and fail -> Reaches max_attempts (3) -> Permanently failed
	l3, err := q.LeaseJob(ctx, "worker-3")
	if err != nil || l3 == nil {
		t.Fatalf("lease 3 failed: %v", err)
	}

	err3 := errors.New("fatal simulated error 3")
	if err := q.FailJob(ctx, item.ID, err3); err != nil {
		t.Fatalf("fail job 3 failed: %v", err)
	}

	j3, _ := q.GetJob(ctx, item.ID)
	if j3.Status != StatusFailed {
		t.Errorf("attempt 3: expected status 'failed', got '%s'", j3.Status)
	}
	if j3.Attempts != 3 {
		t.Errorf("attempt 3: expected attempts 3, got %d", j3.Attempts)
	}
	if j3.CompletedAt == nil {
		t.Errorf("attempt 3: expected completed_at timestamp to be set on permanent failure")
	}
	if j3.ErrorMessage == nil || *j3.ErrorMessage != "fatal simulated error 3" {
		t.Errorf("attempt 3: expected error message 'fatal simulated error 3', got %v", j3.ErrorMessage)
	}

	// Verify it can no longer be leased
	l4, err := q.LeaseJob(ctx, "worker-4")
	if err != nil {
		t.Fatalf("lease 4 query failed: %v", err)
	}
	if l4 != nil {
		t.Errorf("expected no job to be leased after permanent failure, got %v", l4)
	}
}
