package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/internal/logging"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/database"
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

func TestWorkerCLI_LogLevelPrecedence(t *testing.T) {
	previous := logging.Level.Level()
	t.Cleanup(func() { logging.Level.Set(previous) })

	dbPath := filepath.Join(t.TempDir(), "worker.db")
	testutil.PrepareTestDBFile(t, dbPath)
	db, err := database.Open(database.Config{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO settings(key, value) VALUES (?, 'debug')", config.KeyLogLevel)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Startup still configures the logger. No jobs need to run for this test.
	for _, tc := range []struct {
		env  string
		want slog.Level
	}{
		{"", slog.LevelDebug},
		{"warn", slog.LevelWarn},
	} {
		t.Setenv(config.EnvVarName(config.KeyLogLevel), tc.env)
		if err := run(ctx, []string{"-db", dbPath}); err != nil {
			t.Fatal(err)
		}
		if got := logging.Level.Level(); got != tc.want {
			t.Errorf("worker level for environment %q = %s, want %s", tc.env, got, tc.want)
		}
	}
}
