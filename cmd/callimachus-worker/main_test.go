package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	slog.SetDefault(slog.New(slog.NewTextHandler(cancelWhenListening{cancel}, nil)))

	err := run(ctx, []string{
		"-db", dbPath,
		"-poll", "50ms",
		"-workdir", filepath.Join(tempDir, "work"),
	})

	if err != nil {
		t.Fatalf("expected graceful shutdown without error, got: %v", err)
	}
}

type cancelWhenListening struct {
	cancel context.CancelFunc
}

func (w cancelWhenListening) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("conversion worker listening for jobs")) {
		w.cancel()
	}
	return len(p), nil
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

func TestWorkerCLI_ConfigFlagResolvesDatabase(t *testing.T) {
	cfgDir := t.TempDir()
	dbPath := filepath.Join(cfgDir, "callimachus.db")
	testutil.PrepareTestDBFile(t, dbPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	slog.SetDefault(slog.New(slog.NewTextHandler(cancelWhenListening{cancel}, nil)))

	if err := run(ctx, []string{"--config", cfgDir, "--poll", "20ms", "--workdir", filepath.Join(cfgDir, "work")}); err != nil {
		t.Fatalf("run with --config failed: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected database at %s: %v", dbPath, err)
	}
}
