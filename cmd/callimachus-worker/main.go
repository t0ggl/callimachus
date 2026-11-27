package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/t0ggl/callimachus/internal/logging"
	"github.com/t0ggl/callimachus/internal/uuid"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/converter"
	"github.com/t0ggl/callimachus/pkg/database"
)

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("callimachus-worker", flag.ContinueOnError)
	configFlag := fs.String("config", "", "Directory containing configuration & database (override: $CALLIMACHUS_CONFIG_DIR)")
	dbFlag := fs.String("db", "", "Path to Callimachus SQLite database (default: $CALLIMACHUS_DATABASE_PATH or <config>/callimachus.db)")
	pollFlag := fs.Duration("poll", 100*time.Millisecond, "Queue polling interval (override: $CALLIMACHUS_WORKER_POLL_INTERVAL)")
	workerIDFlag := fs.String("worker-id", "", "Worker identifier (override: $CALLIMACHUS_WORKER_ID)")
	workDirFlag := fs.String("workdir", "", "Working directory for temporary conversion files (override: $CALLIMACHUS_WORKER_WORKDIR)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	boot := config.ResolveBootstrap(*configFlag, *dbFlag, "", 0, false)

	pollInterval := *pollFlag
	if envVal, _, ok := config.LookupEnvForSetting("worker_poll_interval"); ok {
		if d, err := time.ParseDuration(envVal); err == nil && d > 0 {
			pollInterval = d
		}
	}

	workerID := *workerIDFlag
	if envVal, _, ok := config.LookupEnvForSetting("worker_id"); ok {
		workerID = envVal
	}
	if workerID == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "worker"
		}
		workerID = fmt.Sprintf("%s-%d-%s", host, os.Getpid(), uuid.NewString()[:8])
	}

	workDir := *workDirFlag
	if envVal, _, ok := config.LookupEnvForSetting("worker_workdir"); ok {
		workDir = envVal
	}
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "callimachus-converter")
	}

	db, err := database.Open(database.Config{Path: boot.DBPath})
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	migrateCtx, migrateCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer migrateCancel()

	if err := db.Migrate(migrateCtx); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	resolved, err := config.Load(migrateCtx, db, config.Overrides{})
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logging.Level.Set(resolved.Settings.LogLevel)

	queue := converter.NewQueue(db)
	runner := converter.NewRealRunner()
	worker := converter.NewWorker(queue, runner, converter.WorkerConfig{
		WorkerID:     workerID,
		PollInterval: pollInterval,
		WorkDir:      workDir,
	})

	slog.Info("conversion worker listening for jobs")
	err = worker.Run(ctx)
	if err != nil && err != context.Canceled {
		return err
	}
	return nil
}

func main() {
	slog.SetDefault(logging.New(os.Stderr))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, config.ErrInvalidLogLevel) {
			slog.Error(config.ErrInvalidLogLevel.Error(), "setting", config.EnvVarName(config.KeyLogLevel))
		} else {
			slog.Error("conversion worker stopped with an error")
		}
		os.Exit(1)
	}
}
