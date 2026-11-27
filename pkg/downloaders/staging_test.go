package downloaders_test

import (
	"archive/zip"
	"bytes"
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
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func setupTestDB(t *testing.T) *database.DB { t.Helper(); return testutil.NewTestDB(t) }

func writeStagingTestEPUB(t *testing.T, path string, meta epub.Metadata) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = mw.Write([]byte("application/epub+zip"))
	cw, _ := zw.Create("META-INF/container.xml")
	_, _ = cw.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`))
	var opf strings.Builder
	opf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="uid" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:identifier id="uid">urn:uuid:aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee</dc:identifier>
`)
	fmt.Fprintf(&opf, "    <dc:title>%s</dc:title>\n", meta.Title)
	for _, a := range meta.Authors {
		fmt.Fprintf(&opf, "    <dc:creator opf:role=\"aut\">%s</dc:creator>\n", a)
	}
	if meta.PublishDate != "" {
		fmt.Fprintf(&opf, "    <dc:date>%s</dc:date>\n", meta.PublishDate)
	}
	if meta.Publisher != "" {
		fmt.Fprintf(&opf, "    <dc:publisher>%s</dc:publisher>\n", meta.Publisher)
	}
	if meta.ISBN13 != "" {
		fmt.Fprintf(&opf, "    <dc:identifier opf:scheme=\"ISBN\">%s</dc:identifier>\n", meta.ISBN13)
	}
	if meta.Description != "" {
		fmt.Fprintf(&opf, "    <dc:description>%s</dc:description>\n", meta.Description)
	}
	if meta.Language != "" {
		fmt.Fprintf(&opf, "    <dc:language>%s</dc:language>\n", meta.Language)
	}
	if meta.Series != "" {
		fmt.Fprintf(&opf, "    <meta name=\"calibre:series\" content=\"%s\"/>\n", meta.Series)
	}
	if meta.SeriesIndex != nil {
		fmt.Fprintf(&opf, "    <meta name=\"calibre:series_index\" content=\"%g\"/>\n", *meta.SeriesIndex)
	}
	opf.WriteString(`  </metadata>
  <manifest><item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`)
	ow, _ := zw.Create("OEBPS/content.opf")
	_, _ = ow.Write([]byte(opf.String()))
	xw, _ := zw.Create("OEBPS/chapter1.xhtml")
	_, _ = xw.Write([]byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Chapter content</p></body></html>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

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

	// Create valid synthetic ebook file in staging
	ebookPayload := filepath.Join(stagingDir, "Synthetic Author - Synthetic Test Book (2024).epub")
	writeStagingTestEPUB(t, ebookPayload, epub.Metadata{
		Title:   "Synthetic Test Book",
		Authors: []string{"Synthetic Author"},
	})

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

	// Create multi-part MP3 files (no playlist needed; chapters come from parts)
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

	// Verify the download waits for conversion
	var status string
	err = db.QueryRowContext(ctx, `SELECT status FROM download_queue WHERE id = 'dq-audio-mp3'`).Scan(&status)
	if err != nil {
		t.Fatalf("query queue status failed: %v", err)
	}
	if status != "converting" {
		t.Errorf("expected status 'converting', got '%s'", status)
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
			if tc.source == "epub" {
				writeStagingTestEPUB(t, source, epub.Metadata{Title: "Book", Authors: []string{"Author"}})
			} else if err := os.WriteFile(source, []byte("source"), 0644); err != nil {
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
	release := filepath.Join(root, "Author - Book")
	if err := os.Mkdir(release, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(release, "Author - Book.mp3")
	if err := os.WriteFile(source, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "book.m3u"), []byte("Author - Book.mp3\n"), 0644); err != nil {
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

func TestStagingMonitor_SingleMP3WithoutPlaylistFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, completedRoot string) string
	}{
		{
			name: "directory with single mp3 and no playlist",
			setup: func(t *testing.T, completedRoot string) string {
				dir := filepath.Join(completedRoot, "Author - Missing Playlist")
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "book.mp3"), []byte("audio"), 0644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
		},
		{
			name: "single mp3 file without playlist",
			setup: func(t *testing.T, completedRoot string) string {
				file := filepath.Join(completedRoot, "Author - Single.mp3")
				if err := os.WriteFile(file, []byte("audio"), 0644); err != nil {
					t.Fatal(err)
				}
				return file
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupTestDB(t)
			ctx := context.Background()
			completedRoot, libraryRoot := t.TempDir(), t.TempDir()
			stagingPath := tc.setup(t, completedRoot)

			if _, err := db.ExecContext(ctx, `
				INSERT INTO users (id, username, email, role) VALUES ('u1', 'reader', 'reader@example.com', 'requester');
				INSERT INTO book_requests (id, user_id, title, author, format, status) VALUES (1, 'u1', 'The Martian', 'Andy Weir', 'm4b', 'downloading');
				INSERT INTO download_queue (id, request_id, download_client_task_id, title, protocol, status, staging_path)
				VALUES ('dq-mp3-fail', 1, 'task-mp3-fail', 'The Martian MP3', 'torrent', 'downloading', ?);
			`, stagingPath); err != nil {
				t.Fatal(err)
			}

			sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
				DB:            db,
				Catalog:       catalog.New(db),
				Converter:     converter.NewQueue(db),
				LibraryRoot:   libraryRoot,
				CompletedRoot: completedRoot,
			})
			if err := sm.PollOnce(ctx); err != nil {
				t.Fatalf("PollOnce failed: %v", err)
			}

			var status, errMsg string
			if err := db.QueryRowContext(ctx, `SELECT status, COALESCE(error_message, '') FROM download_queue WHERE id = 'dq-mp3-fail'`).Scan(&status, &errMsg); err != nil {
				t.Fatal(err)
			}
			if status != "failed" {
				t.Fatalf("download status = %q, want failed", status)
			}
			if !strings.Contains(errMsg, "playlist") {
				t.Fatalf("error_message = %q, want playlist error", errMsg)
			}

			var convCount int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversion_queue`).Scan(&convCount); err != nil || convCount != 0 {
				t.Fatalf("conversion_queue count = %d (err=%v), want 0", convCount, err)
			}

			var eventMsg string
			if err := db.QueryRowContext(ctx, `SELECT message FROM request_events WHERE request_id = 1 AND kind = 'failed' ORDER BY id DESC LIMIT 1`).Scan(&eventMsg); err != nil {
				t.Fatalf("missing failed request_event: %v", err)
			}
			if !strings.Contains(eventMsg, "playlist") {
				t.Fatalf("search log event = %q, want playlist error detail", eventMsg)
			}
		})
	}
}

func TestStagingIgnoresLateCancelledDownload(t *testing.T) {
	db := testutil.NewTestDB(t)
	_, err := db.Exec(`
		INSERT INTO download_queue(id,download_client_task_id,title,protocol,status)
			VALUES('old','client','Cancelled Release','torrent','queued');
		UPDATE download_queue SET status='cancelled' WHERE id='old';
		DELETE FROM download_queue WHERE id='old';`)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cancelled Release.epub"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{DB: db, CompletedRoot: root})
	if err := sm.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM download_queue").Scan(&count); err != nil || count != 0 {
		t.Fatalf("late cancelled release queued again: %d %v", count, err)
	}
}

func TestStagingMonitor_EnrichMetadataPersistsSynopsisOnNewImport(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	libraryRoot := t.TempDir()
	completedRoot := t.TempDir()
	stagingDir := filepath.Join(completedRoot, "Author Enriched - Fresh Import (2025)")
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeStagingTestEPUB(t, filepath.Join(stagingDir, "Fresh Import.epub"), epub.Metadata{
		Title:   "Raw Import",
		Authors: []string{"Raw Author"},
	})

	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:            db,
		Catalog:       catalog.New(db),
		LibraryRoot:   libraryRoot,
		CompletedRoot: completedRoot,
		EnrichMetadata: func(_ context.Context, details catalog.BookDetails) catalog.BookDetails {
			details.Description = "Enriched description from metadata provider."
			details.Subtitle = "An Enriched Subtitle"
			return details
		},
	})
	if err := sm.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}

	var synopsis, subtitle string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(synopsis, ''), COALESCE(subtitle, '') FROM books LIMIT 1`).Scan(&synopsis, &subtitle); err != nil {
		t.Fatal(err)
	}
	if synopsis != "Enriched description from metadata provider." || subtitle != "An Enriched Subtitle" {
		t.Fatalf("expected enriched synopsis/subtitle, got (%q, %q)", synopsis, subtitle)
	}
}

func TestStagingMonitor_AutoFixesSelectedBookEPUBMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)
	libraryRoot := t.TempDir()
	completedRoot := t.TempDir()

	wrongIdx := 9.0
	selectedIdx := 2.5

	// Seed a selected book + request + edition + ideal metadata in SQLite.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, email, role) VALUES ('u1', 'reader', 'reader@example.com', 'requester');
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Brandon Sanderson', 'Sanderson, Brandon');
		INSERT INTO series (id, title, author_id) VALUES ('s1', 'The Stormlight Archive', 'a1');
		INSERT INTO books (id, author_id, series_id, series_index, title, subtitle, release_date, synopsis)
		VALUES ('b1', 'a1', 's1', 2.5, 'Edgedancer', 'A Stormlight Novella', '2016-11-22', 'Lift travels to Yeddaw.');
		INSERT INTO book_editions (id, book_id, format, isbn13, publisher, publication_date, language)
		VALUES ('e1', 'b1', 'epub', '9781250166548', 'Tor Books', '2016-11-22', 'en');
		INSERT INTO book_requests (id, user_id, book_id, title, author, format, status, year)
		VALUES (1, 'u1', 'b1', 'Edgedancer', 'Brandon Sanderson', 'epub', 'downloading', 2016);
	`); err != nil {
		t.Fatalf("seed selected book: %v", err)
	}
	cat := catalog.New(db)
	if err := cat.SaveIdealMetadata(ctx, "b1", "Edgedancer", "The Stormlight Archive", &selectedIdx, catalog.IdealSourceMatch); err != nil {
		t.Fatalf("SaveIdealMetadata: %v", err)
	}

	releaseDir := filepath.Join(completedRoot, "Stormlight.2.5.Edgedancer.Retail.EPUB")
	downloadedEPUB := filepath.Join(releaseDir, "dirty_download.epub")
	writeStagingTestEPUB(t, downloadedEPUB, epub.Metadata{
		Title:       "02.5 - Stormlight Novella [Retail]",
		Authors:     []string{"Sanderson, B."},
		Series:      "Wrong Series",
		SeriesIndex: &wrongIdx,
		PublishDate: "1999",
		Description: "Raw messy scene blurb.",
		Language:    "en",
	})

	if _, err := db.ExecContext(ctx, `
		INSERT INTO download_queue (id, request_id, edition_id, download_client_task_id, title, protocol, status, staging_path)
		VALUES ('dq-selected', 1, 'e1', 'guid-1', 'Stormlight.2.5.Edgedancer.Retail.EPUB', 'torrent', 'downloading', ?);
	`, releaseDir); err != nil {
		t.Fatalf("seed download_queue: %v", err)
	}

	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:            db,
		Catalog:       cat,
		LibraryRoot:   libraryRoot,
		CompletedRoot: completedRoot,
	})
	if err := sm.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	var status, filePath string
	if err := db.QueryRowContext(ctx, `SELECT status FROM download_queue WHERE id = 'dq-selected'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Fatalf("download_queue status = %q, want completed", status)
	}
	if err := db.QueryRowContext(ctx, `SELECT file_path FROM media_assets WHERE edition_id = 'e1'`).Scan(&filePath); err != nil {
		t.Fatal(err)
	}

	if err := epub.Validate(filePath); err != nil {
		t.Fatalf("imported EPUB failed Validate: %v", err)
	}
	imported, err := epub.OpenNative(filePath)
	if err != nil {
		t.Fatalf("OpenNative imported EPUB: %v", err)
	}
	got := imported.Metadata()
	if got.Title != "Edgedancer" {
		t.Errorf("title = %q, want Edgedancer", got.Title)
	}
	if got.Subtitle != "A Stormlight Novella" {
		t.Errorf("subtitle = %q, want A Stormlight Novella", got.Subtitle)
	}
	if len(got.Authors) != 1 || got.Authors[0] != "Brandon Sanderson" {
		t.Errorf("authors = %v, want [Brandon Sanderson]", got.Authors)
	}
	if got.Series != "The Stormlight Archive" {
		t.Errorf("series = %q, want The Stormlight Archive", got.Series)
	}
	if got.SeriesIndex == nil || *got.SeriesIndex != 2.5 {
		t.Errorf("seriesIndex = %v, want 2.5", got.SeriesIndex)
	}
	if got.PublishDate != "2016-11-22" {
		t.Errorf("publishDate = %q, want 2016-11-22", got.PublishDate)
	}
	if got.Publisher != "Tor Books" {
		t.Errorf("publisher = %q, want Tor Books", got.Publisher)
	}
	if got.ISBN13 != "9781250166548" {
		t.Errorf("isbn13 = %q, want 9781250166548", got.ISBN13)
	}
	if got.Description != "Lift travels to Yeddaw." {
		t.Errorf("description = %q", got.Description)
	}

	// Verify a subsequent corrupt download for the same selected book fails safely,
	// preserving both the prior library EPUB and the staged source file.
	priorBytes, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	corruptDir := filepath.Join(completedRoot, "Stormlight.2.5.Edgedancer.Corrupt.EPUB")
	if err := os.MkdirAll(corruptDir, 0755); err != nil {
		t.Fatal(err)
	}
	corruptSrc := filepath.Join(corruptDir, "corrupt.epub")
	corruptPayload := []byte("not a valid epub archive")
	if err := os.WriteFile(corruptSrc, corruptPayload, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO download_queue (id, request_id, edition_id, download_client_task_id, title, protocol, status, staging_path)
		VALUES ('dq-corrupt', 1, 'e1', 'guid-2', 'Stormlight.2.5.Edgedancer.Corrupt.EPUB', 'torrent', 'downloading', ?);
	`, corruptDir); err != nil {
		t.Fatal(err)
	}
	if err := sm.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce corrupt: %v", err)
	}
	var corruptStatus string
	var corruptErr *string
	if err := db.QueryRowContext(ctx, `SELECT status, error_message FROM download_queue WHERE id = 'dq-corrupt'`).Scan(&corruptStatus, &corruptErr); err != nil {
		t.Fatal(err)
	}
	if corruptStatus != "queued" || corruptErr == nil || *corruptErr == "" {
		t.Fatalf("corrupt download queue = (%q, %v), want retryable queued with error", corruptStatus, corruptErr)
	}
	afterPriorBytes, err := os.ReadFile(filePath)
	if err != nil || !bytes.Equal(priorBytes, afterPriorBytes) {
		t.Fatalf("prior library EPUB altered by failed corrupt import (err=%v)", err)
	}
	afterCorruptSrc, err := os.ReadFile(corruptSrc)
	if err != nil || !bytes.Equal(corruptPayload, afterCorruptSrc) {
		t.Fatalf("staged corrupt source altered on failure (err=%v)", err)
	}
}

func TestStagingMonitor_ConvertedEbookAutoFixesSelectedEPUBMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)
	libraryRoot := t.TempDir()
	completedRoot := t.TempDir()

	selectedIdx := 1.0
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, email, role) VALUES ('u1', 'reader', 'reader@example.com', 'requester');
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Frank Herbert', 'Herbert, Frank');
		INSERT INTO series (id, title, author_id) VALUES ('s1', 'Dune Chronicles', 'a1');
		INSERT INTO books (id, author_id, series_id, series_index, title, release_date, synopsis)
		VALUES ('b1', 'a1', 's1', 1.0, 'Dune', '1965', 'Desert planet Arrakis.');
		INSERT INTO book_requests (id, user_id, book_id, title, author, format, status, year)
		VALUES (1, 'u1', 'b1', 'Dune', 'Frank Herbert', 'epub', 'downloading', 1965);
	`); err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(db)
	if err := cat.SaveIdealMetadata(ctx, "b1", "Dune", "Dune Chronicles", &selectedIdx, catalog.IdealSourceMatch); err != nil {
		t.Fatal(err)
	}

	mobiPath := filepath.Join(completedRoot, "Dune.MOBI.Release.mobi")
	if err := os.WriteFile(mobiPath, []byte("mock mobi payload"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO download_queue (id, request_id, download_client_task_id, title, protocol, status, staging_path)
		VALUES ('dq-conv', 1, 'guid-conv', 'Dune.MOBI.Release', 'torrent', 'downloading', ?);
	`, mobiPath); err != nil {
		t.Fatal(err)
	}

	q := converter.NewQueue(db)
	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:            db,
		Catalog:       cat,
		Converter:     q,
		LibraryRoot:   libraryRoot,
		CompletedRoot: completedRoot,
		QualityProfile: func(context.Context) (downloaders.QualityProfile, error) {
			return downloaders.QualityProfile{ConvertEbooks: true, EbookTargetFormat: "epub"}, nil
		},
	})
	if err := sm.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	// Mock ebook-convert emitting a valid EPUB with dirty embedded metadata.
	wrongIdx := 4.0
	runner := converter.NewMockRunner()
	runner.SetDefaultHandler(func(context.Context, converter.RunOptions) error { t.Fatal("unexpected subprocess"); return nil })
	runner.RegisterHandler("ebook-convert", func(_ context.Context, opt converter.RunOptions) error {
		writeStagingTestEPUB(t, opt.Args[1], epub.Metadata{
			Title:       "Dune [MOBI Convert]",
			Authors:     []string{"Herbert, F."},
			Series:      "Wrong Dune Series",
			SeriesIndex: &wrongIdx,
		})
		return nil
	})
	worker := converter.NewWorker(q, runner, converter.WorkerConfig{WorkerID: "ebook-meta-test", WorkDir: t.TempDir()})
	processed, err := worker.RunOnce(ctx)
	if !processed || err != nil {
		t.Fatalf("worker.RunOnce: processed=%v err=%v", processed, err)
	}

	var filePath string
	if err := db.QueryRowContext(ctx, `SELECT m.file_path FROM media_assets m JOIN book_editions e ON e.id=m.edition_id WHERE e.book_id='b1'`).Scan(&filePath); err != nil {
		t.Fatal(err)
	}
	if err := epub.Validate(filePath); err != nil {
		t.Fatalf("converted EPUB failed Validate: %v", err)
	}
	converted, err := epub.OpenNative(filePath)
	if err != nil {
		t.Fatal(err)
	}
	got := converted.Metadata()
	if got.Title != "Dune" || got.Series != "Dune Chronicles" || got.SeriesIndex == nil || *got.SeriesIndex != 1.0 {
		t.Fatalf("converted EPUB metadata = %+v, want Dune / Dune Chronicles #1", got)
	}
	if len(got.Authors) != 1 || got.Authors[0] != "Frank Herbert" {
		t.Fatalf("converted EPUB authors = %v, want [Frank Herbert]", got.Authors)
	}
}
