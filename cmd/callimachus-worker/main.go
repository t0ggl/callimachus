package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/transcoder"
)

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("callimachus-worker", flag.ContinueOnError)
	dbFlag := fs.String("db", "", "Path to Callimachus SQLite database (default: $CALLIMACHUS_DATABASE_PATH or ./callimachus.db)")
	pollFlag := fs.Duration("poll", 100*time.Millisecond, "Queue polling interval (override: $CALLIMACHUS_WORKER_POLL_INTERVAL)")
	workerIDFlag := fs.String("worker-id", "", "Worker identifier (override: $CALLIMACHUS_WORKER_ID)")
	workDirFlag := fs.String("workdir", "", "Working directory for temporary transcode files (override: $CALLIMACHUS_WORKER_WORKDIR)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	boot := config.ResolveBootstrap("", *dbFlag, "", 0, false)

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
		workDir = filepath.Join(os.TempDir(), "callimachus-transcoder")
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

	queue := transcoder.NewQueue(db)
	runner := transcoder.NewRealRunner()
	worker := transcoder.NewWorker(queue, runner, transcoder.WorkerConfig{
		WorkerID:     workerID,
		PollInterval: pollInterval,
		WorkDir:      workDir,
	})

	log.Printf("[worker] Worker %s running. Listening for jobs...", workerID)
	err = worker.Run(ctx)
	if err != nil && err != context.Canceled {
		return err
	}
	return nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:]); err != nil {
		log.Fatalf("[worker] fatal error: %v", err)
	}
}
