package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/converter"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestServer_EmbeddedConversionWorker(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "callimachus.db")
	testutil.PrepareTestDBFile(t, dbPath)

	sourceDir := filepath.Join(dir, "staged-audio")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "01.mp3"), []byte("mp3-1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "02.mp3"), []byte("mp3-2"), 0644); err != nil {
		t.Fatal(err)
	}

	runner := converter.NewMockRunner()
	runner.SetDefaultHandler(func(context.Context, converter.RunOptions) error {
		t.Fatal("unexpected subprocess")
		return nil
	})
	runner.RegisterHandler("ffprobe", func(_ context.Context, opt converter.RunOptions) error {
		_, err := io.WriteString(opt.Stdout, `{"streams":[{"codec_type":"audio","codec_name":"mp3","duration":"5"}],"format":{"duration":"5"}}`)
		return err
	})
	runner.RegisterHandler("ffmpeg", func(_ context.Context, opt converter.RunOptions) error {
		return os.WriteFile(opt.Args[len(opt.Args)-1], []byte("converted-m4b"), 0644)
	})

	srv, err := NewServer(Config{
		ConfigDir:          dir,
		DBPath:             dbPath,
		LibraryPath:        filepath.Join(dir, "library"),
		Host:               "127.0.0.1",
		Port:               0,
		ConverterRunner:    runner,
		WorkerPollInterval: 20 * time.Millisecond,
		WorkerWorkDir:      filepath.Join(dir, "work"),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if srv.converterWorker == nil {
		t.Fatal("expected embedded conversion worker to be enabled by default")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if _, err := srv.db.ExecContext(ctx, `
		INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Andy Weir', 'Weir, Andy');
		INSERT INTO books (id, author_id, title) VALUES ('b1', 'a1', 'The Martian');
		INSERT INTO book_editions (id, book_id, format) VALUES ('ed1', 'b1', 'm4b');
	`); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(dir, "library", "Andy Weir", "The Martian", "The Martian - Andy Weir.m4b")
	job, err := converter.NewQueue(srv.db).Enqueue(ctx, "ed1", sourceDir, targetPath, 0)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, err := converter.NewQueue(srv.db).GetJob(ctx, job.ID)
		if err == nil && got.Status == converter.StatusCompleted {
			if _, statErr := os.Stat(targetPath); statErr != nil {
				t.Fatalf("expected converted file at %s: %v", targetPath, statErr)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	got, _ := converter.NewQueue(srv.db).GetJob(ctx, job.ID)
	t.Fatalf("embedded conversion worker did not complete job in time: %+v", got)
}

func TestServer_EmbeddedConversionWorkerDisabledByEnv(t *testing.T) {
	t.Setenv("CALLIMACHUS_EMBEDDED_WORKER", "false")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "callimachus.db")
	testutil.PrepareTestDBFile(t, dbPath)

	srv, err := NewServer(Config{
		ConfigDir:   dir,
		DBPath:      dbPath,
		LibraryPath: filepath.Join(dir, "library"),
		Host:        "127.0.0.1",
		Port:        0,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	if srv.converterWorker != nil {
		t.Fatal("expected embedded conversion worker to be disabled when CALLIMACHUS_EMBEDDED_WORKER=false")
	}
}
