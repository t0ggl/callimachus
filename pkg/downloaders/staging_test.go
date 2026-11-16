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

type mockDownloadClient struct {
	clientType string
	statusMap  map[string]*downloaders.TaskStatus
}

func (m *mockDownloadClient) Type() string {
	return m.clientType
}

func (m *mockDownloadClient) AddTorrent(ctx context.Context, urlOrMagnet string, category string) (string, error) {
	return "mock-torrent-task-id", nil
}

func (m *mockDownloadClient) AddNZB(ctx context.Context, nzbURL string, category string) (string, error) {
	return "mock-nzb-task-id", nil
}

func (m *mockDownloadClient) GetTaskStatus(ctx context.Context, taskID string) (*downloaders.TaskStatus, error) {
	if st, ok := m.statusMap[taskID]; ok {
		return st, nil
	}
	return nil, downloaders.ErrTaskNotFound
}

func (m *mockDownloadClient) RemoveTask(ctx context.Context, taskID string, deleteFiles bool) error {
	delete(m.statusMap, taskID)
	return nil
}

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	db := testutil.NewTestDB(t)

	// Insert dummy download_client
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO download_clients (id, name, type, host, port, use_ssl, enabled)
		VALUES ('mock-dc-1', 'Mock Client', 'qbittorrent', 'localhost', 8080, 0, 1);
	`)
	if err != nil {
		t.Fatalf("insert mock download client failed: %v", err)
	}

	return db
}

func TestStagingMonitor_EBookIngestionLifecycle(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	ctx := context.Background()
	libraryRoot := t.TempDir()
	stagingDir := t.TempDir()

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

	mockClient := &mockDownloadClient{
		clientType: "qbittorrent",
		statusMap: map[string]*downloaders.TaskStatus{
			"task-eb-1": {
				TaskID:          "task-eb-1",
				Name:            "Synthetic Author - Synthetic Test Book (2024)",
				Progress:        35.0,
				DownloadRate:    512000,
				TotalBytes:      1048576,
				DownloadedBytes: 367001,
				State:           "downloading",
				SavePath:        stagingDir,
				ContentPath:     ebookPayload,
				IsCompleted:     false,
			},
		},
	}

	cat := catalog.New(db)
	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:          db,
		Catalog:     cat,
		LibraryRoot: libraryRoot,
		ClientResolver: func(ctx context.Context, clientID string) (downloaders.DownloadClient, error) {
			return mockClient, nil
		},
	})

	// Poll 1: Active downloading
	if err := sm.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce downloading failed: %v", err)
	}

	var status string
	var progress float64
	err = db.QueryRowContext(ctx, `SELECT status, progress_percent FROM download_queue WHERE id = 'dq-ebook-1'`).Scan(&status, &progress)
	if err != nil {
		t.Fatalf("query status failed: %v", err)
	}
	if status != "downloading" {
		t.Errorf("expected status 'downloading', got '%s'", status)
	}
	if progress != 35.0 {
		t.Errorf("expected progress 35.0, got %f", progress)
	}

	// Poll 2: Completed download
	mockClient.statusMap["task-eb-1"] = &downloaders.TaskStatus{
		TaskID:          "task-eb-1",
		Name:            "Synthetic Author - Synthetic Test Book (2024)",
		Progress:        100.0,
		DownloadRate:    0,
		TotalBytes:      1048576,
		DownloadedBytes: 1048576,
		State:           "uploading",
		SavePath:        stagingDir,
		ContentPath:     ebookPayload,
		IsCompleted:     true,
	}

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
	stagingDir := t.TempDir()

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

	mockClient := &mockDownloadClient{
		clientType: "transmission",
		statusMap: map[string]*downloaders.TaskStatus{
			"task-mp3-1": {
				TaskID:          "task-mp3-1",
				Name:            "Synthetic Voice - Synthetic Audiobook Series #1",
				Progress:        100.0,
				DownloadRate:    0,
				TotalBytes:      50000000,
				DownloadedBytes: 50000000,
				State:           "seeding",
				SavePath:        stagingDir,
				ContentPath:     stagingDir,
				IsCompleted:     true,
			},
		},
	}

	cat := catalog.New(db)
	tq := transcoder.NewQueue(db)

	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:          db,
		Catalog:     cat,
		Transcoder:  tq,
		LibraryRoot: libraryRoot,
		ClientResolver: func(ctx context.Context, clientID string) (downloaders.DownloadClient, error) {
			return mockClient, nil
		},
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
	stagingDir := t.TempDir()

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

	mockClient := &mockDownloadClient{
		clientType: "sabnzbd",
		statusMap: map[string]*downloaders.TaskStatus{
			"task-m4b-1": {
				TaskID:          "task-m4b-1",
				Name:            "Synthetic Direct Author - Synthetic Direct Audiobook (2022)",
				Progress:        100.0,
				DownloadRate:    0,
				TotalBytes:      60000000,
				DownloadedBytes: 60000000,
				State:           "Completed",
				SavePath:        stagingDir,
				ContentPath:     m4bFile,
				IsCompleted:     true,
			},
		},
	}

	cat := catalog.New(db)
	tq := transcoder.NewQueue(db)

	sm := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:          db,
		Catalog:     cat,
		Transcoder:  tq,
		LibraryRoot: libraryRoot,
		ClientResolver: func(ctx context.Context, clientID string) (downloaders.DownloadClient, error) {
			return mockClient, nil
		},
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
		ClientResolver: func(ctx context.Context, clientID string) (downloaders.DownloadClient, error) {
			return &mockDownloadClient{}, nil
		},
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

	time.Sleep(50 * time.Millisecond)
	sm.Stop()
}
