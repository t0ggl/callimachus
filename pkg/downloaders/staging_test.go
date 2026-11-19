package downloaders_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/converter"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func setupTestDB(t *testing.T) *database.DB { t.Helper(); return testutil.NewTestDB(t) }

func TestStagingMonitor_EBookIngestionLifecycle(t *testing.T) {
	logs := testutil.CaptureLogs(t)

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
		QualityProfile: func(context.Context) (downloaders.QualityProfile, error) {
			var current string
			if err := db.QueryRowContext(ctx, `SELECT status FROM download_queue WHERE id = 'dq-ebook-1'`).Scan(&current); err != nil {
				t.Fatal(err)
			}
			if current != "importing" {
				t.Errorf("expected importing during ingestion, got %q", current)
			}
			return downloaders.QualityProfile{}, nil
		},
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
	for _, event := range []string{"download staging polled", "staged download processing started", "library media organized", "staged download processing completed"} {
		if !strings.Contains(logs.String(), event) {
			t.Errorf("missing log event %q", event)
		}
	}
}

func TestStagingMonitor_MultiPartMP3_DispatchesToConverter(t *testing.T) {
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
	tq := converter.NewQueue(db)

	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:            db,
		Catalog:       cat,
		Converter:     tq,
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

	// Verify task was enqueued into conversion_queue
	var tqCount int
	var tqSource, tqTarget, tqStatus string
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MAX(source_path), ''), COALESCE(MAX(target_path), ''), COALESCE(MAX(status), '')
		FROM conversion_queue;
	`).Scan(&tqCount, &tqSource, &tqTarget, &tqStatus)
	if err != nil {
		t.Fatalf("query conversion queue failed: %v", err)
	}

	if tqCount != 1 {
		t.Fatalf("expected 1 item in conversion_queue, got %d", tqCount)
	}
	if tqSource != stagingDir {
		t.Errorf("expected conversion source %s, got %s", stagingDir, tqSource)
	}
	if filepath.Ext(tqTarget) != ".m4b" {
		t.Errorf("expected conversion target extension .m4b, got %s", tqTarget)
	}
	if tqStatus != converter.StatusPending {
		t.Errorf("expected conversion status '%s', got '%s'", converter.StatusPending, tqStatus)
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
	tq := converter.NewQueue(db)

	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:            db,
		Catalog:       cat,
		Converter:     tq,
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

	// Verify conversion queue has 0 items (since it was already .m4b)
	var tqCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversion_queue`).Scan(&tqCount)
	if tqCount != 0 {
		t.Errorf("expected 0 conversion queue items for direct m4b, got %d", tqCount)
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

// Every converter call is mocked, including AZW3 decoding for validation.
func TestStagingEbookConversion(t *testing.T) {
	for _, tc := range []struct {
		name, source, target   string
		enabled, fail, invalid bool
	}{
		{name: "mobi to epub", source: "mobi", target: "epub", enabled: true},
		{name: "fb2 to azw3", source: "fb2", target: "azw3", enabled: true},
		{name: "pdf conversion failure", source: "pdf", target: "epub", enabled: true, fail: true},
		{name: "invalid output", source: "azw", target: "epub", enabled: true, invalid: true},
		{name: "disabled", source: "mobi", target: "epub"},
		{name: "target passthrough", source: "epub", target: "epub", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := setupTestDB(t)
			root := t.TempDir()
			library := t.TempDir()
			source := filepath.Join(root, "Author - Book."+tc.source)
			if err := os.WriteFile(source, []byte("source"), 0644); err != nil {
				t.Fatal(err)
			}
			q := converter.NewQueue(db)
			sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{DB: db, Catalog: catalog.New(db), Converter: q, CompletedRoot: root, LibraryRoot: library,
				QualityProfile: func(context.Context) (downloaders.QualityProfile, error) {
					return downloaders.QualityProfile{ConvertEbooks: tc.enabled, EbookTargetFormat: tc.target}, nil
				},
			})
			if err := sm.PollOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if err := sm.PollOnce(ctx); err != nil {
				t.Fatal(err)
			} // Must not enqueue twice.
			var count int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversion_queue").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if !tc.enabled || tc.source == tc.target {
				if count != 0 {
					t.Fatal("unexpected conversion")
				}
				return
			}
			if count != 1 {
				t.Fatalf("jobs: %d", count)
			}
			var id string
			if err := db.QueryRowContext(ctx, "SELECT id FROM conversion_queue").Scan(&id); err != nil {
				t.Fatal(err)
			}
			job, err := q.GetJob(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(job.TargetPath); !os.IsNotExist(err) {
				t.Fatal("output organized before conversion")
			}
			fixture, err := os.ReadFile("../../testdata/epubs/epub_with_cover.epub")
			if err != nil {
				t.Fatal(err)
			}
			runner := converter.NewMockRunner()
			runner.SetDefaultHandler(func(context.Context, converter.RunOptions) error { t.Fatal("unexpected command"); return nil })
			runner.RegisterHandler("ebook-convert", func(_ context.Context, opt converter.RunOptions) error {
				if tc.fail {
					return fmt.Errorf("mock converter: DRM encrypted input")
				}
				data := fixture
				if tc.invalid {
					data = []byte("broken")
				}
				return os.WriteFile(opt.Args[1], data, 0644)
			})
			worker := converter.NewWorker(q, runner, converter.WorkerConfig{WorkerID: "ebook-test", WorkDir: t.TempDir()})
			processed, err := worker.RunOnce(ctx)
			if !processed {
				t.Fatal("not processed")
			}
			if tc.fail || tc.invalid {
				if err == nil {
					t.Fatal("expected conversion error")
				}
				job, _ = q.GetJob(ctx, id)
				if job.ErrorMessage == nil || *job.ErrorMessage == "" {
					t.Fatal("missing activity error")
				}
				if _, err := os.Stat(source); err != nil {
					t.Fatal("source lost")
				}
				if _, err := os.Stat(job.TargetPath); !os.IsNotExist(err) {
					t.Fatal("invalid output published")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			job, _ = q.GetJob(ctx, id)
			if job.Status != converter.StatusCompleted || job.ProgressPercent != 100 {
				t.Fatalf("incomplete: %+v", job)
			}
			for _, ext := range []string{"." + tc.target, ".opf", ".jpg"} {
				p := strings.TrimSuffix(job.TargetPath, filepath.Ext(job.TargetPath)) + ext
				if _, err := os.Stat(p); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE file_path = ?", job.TargetPath).Scan(&count); err != nil || count != 1 {
				t.Fatalf("asset count %d: %v", count, err)
			}
		})
	}
}

func TestStagingMonitor_SingleMP3RequiresConversion(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	root, library := t.TempDir(), t.TempDir()
	source := filepath.Join(root, "Author - Book.mp3")
	if err := os.WriteFile(source, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{DB: db, CompletedRoot: root, LibraryRoot: library})
	if err := sm.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status string
	var message *string
	if err := db.QueryRow(`SELECT status, error_message FROM download_queue`).Scan(&status, &message); err != nil || status != "queued" || message == nil {
		t.Fatalf("MP3 without conversion must remain retryable: %s %v %v", status, message, err)
	}
	entries, err := os.ReadDir(library)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unconverted MP3 imported: %v %v", entries, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("MP3 source lost: %v", err)
	}
}
