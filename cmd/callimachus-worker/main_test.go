package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestWorkerCLI_HelpFlag(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := run(ctx, []string{"-help"})
	if err == nil {
		t.Errorf("expected error or ErrHelp for -help, got nil")
	}
}

func TestWorkerCLI_RunAndGracefulShutdown(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cli_test.db")
	testutil.PrepareTestDBFile(t, dbPath)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context after 100 milliseconds to simulate SIGINT/SIGTERM
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := run(ctx, []string{
		"-db", dbPath,
		"-poll", "50ms",
		"-workdir", filepath.Join(tempDir, "work"),
	})

	if err != nil {
		t.Fatalf("expected graceful shutdown without error, got: %v", err)
	}
}
