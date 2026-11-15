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
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/transcoder"
)

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("callimachus-worker", flag.ContinueOnError)
	dbFlag := fs.String("db", "", "Path to Callimachus SQLite database (default: $DATABASE_PATH or ./callimachus.db)")
	pollFlag := fs.Duration("poll", 100*time.Millisecond, "Queue polling interval")
	bitrateFlag := fs.String("bitrate", "64k", "Target audio bitrate (e.g. 64k, 128k)")
	codecFlag := fs.String("codec", "aac", "Target audio codec (e.g. aac, libfdk_aac)")
	workerIDFlag := fs.String("worker-id", "", "Worker identifier (default: auto-generated hostname-pid-uuid)")
	workDirFlag := fs.String("workdir", "", "Working directory for temporary transcode files")

	if err := fs.Parse(args); err != nil {
		return err
	}

	dbPath := *dbFlag
	if dbPath == "" {
		dbPath = os.Getenv("DATABASE_PATH")
	}
	if dbPath == "" {
		dbPath = "callimachus.db"
	}

	workerID := *workerIDFlag
	if workerID == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "worker"
		}
		workerID = fmt.Sprintf("%s-%d-%s", host, os.Getpid(), uuid.NewString()[:8])
	}

	workDir := *workDirFlag
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "callimachus-transcoder")
	}

	db, err := database.Open(database.Config{Path: dbPath})
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
		PollInterval: *pollFlag,
		Bitrate:      *bitrateFlag,
		AudioCodec:   *codecFlag,
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
