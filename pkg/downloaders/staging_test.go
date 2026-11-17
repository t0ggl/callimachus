package downloaders_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/testutil"
	"github.com/t0ggl/callimachus/pkg/transcoder"
)

func setupTestDB(t *testing.T) *database.DB { t.Helper(); return testutil.NewTestDB(t) }

func TestStagingMonitor_EBookIngestionLifecycle(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	ctx := context.Background()
	libraryRoot := t.TempDir()
	completedRoot := t.TempDir()
	stagingDir := filepath.Join(completedRoot, "release")
	if err := os.Mkdir(stagingDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create dummy synthetic ebook file in staging
	ebookPayload := filepath.Join(stagingDir, "Synthetic Author - Synthetic Test Book (2024).epub")
	if err := os.WriteFile(ebookPayload, []byte("dummy epub payload content"), 0644); err != nil {
		t.Fatalf("failed to write dummy epub: %v", err)
	}

	// Insert queued item
	_, err := db.ExecContext(ctx, `
		INSERT INTO download_queue (id, client_id, download_client_task_id, title, protocol, status, progress_percent, staging_path)
		VALUES ('dq-ebook-1', 'mock-dc-1', 'task-eb-1', 'Synthetic Author - Synthetic Test Book (2024)', 'torrent', 'queued', 0.0, ?);
	`, stagingDir)
	if err != nil {
		t.Fatalf("insert download queue item failed: %v", err)
	}

	cat := catalog.New(db)
	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:            db,
		Catalog:       cat,
		LibraryRoot:   libraryRoot,
		CompletedRoot: completedRoot,
	})

	var status string
	var progress float64
	if err := sm.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce completed failed: %v", err)
	}

	err = db.QueryRowContext(ctx, `SELECT status, progress_percent FROM download_queue WHERE id = 'dq-ebook-1'`).Scan(&status, &progress)
	if err != nil {
		t.Fatalf("query completed status failed: %v", err)
	}
	if status != "completed" {
		t.Errorf("expected status 'completed', got '%s'", status)
	}
	if progress != 100.0 {
		t.Errorf("expected progress 100.0, got %f", progress)
	}

	// Verify the book was organized and ingested into the library root
	authorDir := filepath.Join(libraryRoot, "Synthetic Author")
	if _, err := os.Stat(authorDir); os.IsNotExist(err) {
		t.Errorf("expected author directory %s to be created", authorDir)
	}

	// Verify database media asset record was created
	var assetCount int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets`).Scan(&assetCount)
	if err != nil {
		t.Fatalf("query media assets count failed: %v", err)
	}
	if assetCount != 1 {
		t.Errorf("expected 1 media asset, got %d", assetCount)
	}
}

func TestStagingMonitor_MultiPartMP3_DispatchesToTranscoder(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	ctx := context.Background()
	libraryRoot := t.TempDir()
	completedRoot := t.TempDir()
	stagingDir := filepath.Join(completedRoot, "release")
	if err := os.Mkdir(stagingDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create multi-part MP3 files
	part1 := filepath.Join(stagingDir, "01_Synthetic_Part1.mp3")
	part2 := filepath.Join(stagingDir, "02_Synthetic_Part2.mp3")
	if err := os.WriteFile(part1, []byte("fake mp3 data 1"), 0644); err != nil {
		t.Fatalf("write part1 failed: %v", err)
	}
	if err := os.WriteFile(part2, []byte("fake mp3 data 2"), 0644); err != nil {
		t.Fatalf("write part2 failed: %v", err)
	}

	// Insert download_queue item
	_, err := db.ExecContext(ctx, `
		INSERT INTO download_queue (id, client_id, download_client_task_id, title, protocol, status, progress_percent, staging_path)
		VALUES ('dq-audio-mp3', 'mock-dc-1', 'task-mp3-1', 'Synthetic Voice - Synthetic Audiobook Series #1', 'torrent', 'downloading', 99.0, ?);
	`, stagingDir)
	if err != nil {
		t.Fatalf("insert download queue item failed: %v", err)
	}

	cat := catalog.New(db)
	tq := transcoder.NewQueue(db)

	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:            db,
		Catalog:       cat,
		Transcoder:    tq,
		LibraryRoot:   libraryRoot,
		CompletedRoot: completedRoot,
	})

	if err := sm.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce multi-part mp3 failed: %v", err)
	}

	// Verify download_queue is completed
	var status string
	err = db.QueryRowContext(ctx, `SELECT status FROM download_queue WHERE id = 'dq-audio-mp3'`).Scan(&status)
	if err != nil {
		t.Fatalf("query queue status failed: %v", err)
	}
	if status != "completed" {
		t.Errorf("expected status 'completed', got '%s'", status)
	}

	// Verify task was enqueued into transcode_queue
	var tqCount int
	var tqSource, tqTarget, tqStatus string
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MAX(source_path), ''), COALESCE(MAX(target_path), ''), COALESCE(MAX(status), '')
		FROM transcode_queue;
	`).Scan(&tqCount, &tqSource, &tqTarget, &tqStatus)
	if err != nil {
		t.Fatalf("query transcode queue failed: %v", err)
	}

	if tqCount != 1 {
		t.Fatalf("expected 1 item in transcode_queue, got %d", tqCount)
	}
	if tqSource != stagingDir {
		t.Errorf("expected transcode source %s, got %s", stagingDir, tqSource)
	}
	if filepath.Ext(tqTarget) != ".m4b" {
		t.Errorf("expected transcode target extension .m4b, got %s", tqTarget)
	}
	if tqStatus != transcoder.StatusPending {
		t.Errorf("expected transcode status '%s', got '%s'", transcoder.StatusPending, tqStatus)
	}
}

func TestStagingMonitor_DirectM4B_Ingestion(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	ctx := context.Background()
	libraryRoot := t.TempDir()
	completedRoot := t.TempDir()
	stagingDir := filepath.Join(completedRoot, "release")
	if err := os.Mkdir(stagingDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create single .m4b file
	m4bFile := filepath.Join(stagingDir, "Synthetic Direct Audiobook (2022).m4b")
	if err := os.WriteFile(m4bFile, []byte("fake m4b binary content"), 0644); err != nil {
		t.Fatalf("write m4b failed: %v", err)
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO download_queue (id, client_id, download_client_task_id, title, protocol, status, progress_percent, staging_path)
		VALUES ('dq-direct-m4b', 'mock-dc-1', 'task-m4b-1', 'Synthetic Direct Author - Synthetic Direct Audiobook (2022)', 'usenet', 'downloading', 95.0, ?);
	`, stagingDir)
	if err != nil {
		t.Fatalf("insert download queue failed: %v", err)
	}

	cat := catalog.New(db)
	tq := transcoder.NewQueue(db)

	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:            db,
		Catalog:       cat,
		Transcoder:    tq,
		LibraryRoot:   libraryRoot,
		CompletedRoot: completedRoot,
	})

	if err := sm.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce direct m4b failed: %v", err)
	}

	// Verify organized into libraryRoot
	authorDir := filepath.Join(libraryRoot, "Synthetic Direct Author")
	if _, err := os.Stat(authorDir); os.IsNotExist(err) {
		t.Errorf("expected direct m4b to create author directory %s", authorDir)
	}

	// Verify transcode queue has 0 items (since it was already .m4b)
	var tqCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcode_queue`).Scan(&tqCount)
	if tqCount != 0 {
		t.Errorf("expected 0 transcode queue items for direct m4b, got %d", tqCount)
	}
}

func TestStagingMonitor_StartStop(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:           db,
		PollInterval: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := sm.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Starting again should error
	if err := sm.Start(ctx); err == nil {
		t.Errorf("expected error on duplicate Start, got nil")
	}

	sm.Stop()
}

func TestStagingMonitor_RetryAndIdempotency(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	root := t.TempDir()
	release := filepath.Join(root, "Author - Book")
	if err := os.Mkdir(release, 0755); err != nil {
		t.Fatal(err)
	}
	// An unfinished job outside the completed directory is never ingested.
	unfinished := filepath.Join(t.TempDir(), "unfinished.epub.part")
	if err := os.WriteFile(unfinished, []byte("partial"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "01.mp3"), []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "02.mp3"), []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{DB: db, CompletedRoot: root, LibraryRoot: t.TempDir()})
	if err := sm.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status string
	var message *string
	if err := db.QueryRow(`SELECT status, error_message FROM download_queue`).Scan(&status, &message); err != nil || status != "queued" || message == nil {
		t.Fatalf("failed import should remain retryable: %s %v %v", status, message, err)
	}
	// Replace the failed multipart job with a completed ebook, then retry.
	if err := os.RemoveAll(release); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(release, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "Book.pdf"), []byte("book"), 0644); err != nil {
		t.Fatal(err)
	}
	sm = downloaders.NewStagingMonitor(downloaders.StagingConfig{DB: db, Catalog: catalog.New(db), CompletedRoot: root, LibraryRoot: t.TempDir()})
	for i := 0; i < 2; i++ {
		if err := sm.PollOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow(`SELECT status, error_message FROM download_queue`).Scan(&status, &message); err != nil || status != "completed" || message != nil {
		t.Fatalf("retry: %s %v %v", status, message, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("idempotency: %d %v", count, err)
	}
}
