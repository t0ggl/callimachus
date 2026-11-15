package transcoder

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/database"
)

// WorkerConfig holds configuration options for the transcoder worker instance.
type WorkerConfig struct {
	WorkerID      string
	PollInterval  time.Duration
	AudioCodec    string
	Bitrate       string
	SampleRate    int
	Channels      int
	WorkDir       string
	OnStageChange func(stage string, percent float64)
}

// Worker executes single-job transcoder processing pipelines.
type Worker struct {
	queue  *Queue
	runner CommandRunner
	prober *Prober
	muxer  *Muxer
	cfg    WorkerConfig
}

// NewWorker initializes a new Worker instance.
func NewWorker(queue *Queue, runner CommandRunner, cfg WorkerConfig) *Worker {
	if runner == nil {
		runner = NewRealRunner()
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	if cfg.AudioCodec == "" {
		cfg.AudioCodec = "aac"
	}
	if cfg.Bitrate == "" {
		cfg.Bitrate = "64k"
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = filepath.Join(os.TempDir(), "callimachus-transcoder")
	}

	prober := NewProber(runner)
	muxer := NewMuxer(runner, MuxerConfig{
		AudioCodec: cfg.AudioCodec,
		Bitrate:    cfg.Bitrate,
		SampleRate: cfg.SampleRate,
		Channels:   cfg.Channels,
		TempDir:    cfg.WorkDir,
	})

	return &Worker{
		queue:  queue,
		runner: runner,
		prober: prober,
		muxer:  muxer,
		cfg:    cfg,
	}
}

// Run starts the continuous worker loop, leasing and processing one job at a time
// until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		processed, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			// Log error or continue to next iteration after poll interval
		}

		if !processed {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(w.cfg.PollInterval):
			}
		}
	}
}

// RunOnce attempts to lease and process strictly one pending transcode job.
// Returns true if a job was leased and processed, false if no pending jobs were available.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	job, err := w.queue.LeaseJob(ctx, w.cfg.WorkerID)
	if err != nil {
		return false, fmt.Errorf("lease job failed: %w", err)
	}
	if job == nil {
		return false, nil
	}

	err = w.ProcessJob(ctx, job)
	if err != nil {
		failErr := w.queue.FailJob(ctx, job.ID, err)
		if failErr != nil {
			return true, fmt.Errorf("job failed with: %v, queue fail update failed: %w", err, failErr)
		}
		return true, err
	}

	completeErr := w.queue.CompleteJob(ctx, job.ID)
	if completeErr != nil {
		return true, fmt.Errorf("complete job failed: %w", completeErr)
	}

	if w.cfg.OnStageChange != nil {
		w.cfg.OnStageChange(StageCompleted, 100.0)
	}

	return true, nil
}

// ProcessJob executes the end-to-end transcode pipeline for a single leased job:
// 1. Probing audio files and ordering
// 2. Compiling FFMETADATA1 chapters
// 3. Transcoding / muxing to temporary .m4b
// 4. Tagging
// 5. Atomic move to canonical destination
func (w *Worker) ProcessJob(ctx context.Context, job *database.TranscodeQueueItem) error {
	if job == nil {
		return errors.New("cannot process nil job")
	}

	// Step 1: Probing & Playlist ordering
	w.reportStage(ctx, job.ID, StageProbing, 10.0)

	audioFiles, err := ResolveAudioFiles(job.SourcePath)
	if err != nil {
		return fmt.Errorf("resolve audio files: %w", err)
	}
	if len(audioFiles) == 0 {
		return errors.New("no audio files found in source path")
	}

	probes, err := w.prober.ProbeFiles(ctx, audioFiles)
	if err != nil {
		return fmt.Errorf("probe audio files: %w", err)
	}

	var totalDuration float64
	for _, p := range probes {
		totalDuration += p.Duration
	}
	totalDurationMs := totalDuration * 1000.0

	w.reportStage(ctx, job.ID, StageProbing, 20.0)

	// Step 2: Encoding / Chapter preparation
	w.reportStage(ctx, job.ID, StageEncoding, 25.0)

	jobWorkDir := filepath.Join(w.cfg.WorkDir, fmt.Sprintf("job-%s", job.ID))
	if err := os.MkdirAll(jobWorkDir, 0755); err != nil {
		return fmt.Errorf("create job work dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(jobWorkDir)
	}()

	chapters := ChaptersFromProbes(probes)

	meta := AudioMetadata{}
	if len(probes) > 0 {
		meta.Title = probes[0].Title
		meta.Artist = probes[0].Artist
		meta.Album = probes[0].Album
		meta.Composer = probes[0].Composer
		meta.Date = probes[0].Date
	}
	if meta.Title == "" {
		baseName := filepath.Base(job.TargetPath)
		meta.Title = strings.TrimSuffix(baseName, filepath.Ext(baseName))
	}

	metaPath := filepath.Join(jobWorkDir, "metadata.txt")
	if err := WriteFFMetadataFile(metaPath, meta, chapters); err != nil {
		return fmt.Errorf("write ffmetadata file: %w", err)
	}

	coverImage := findCoverImage(job.SourcePath)

	// Step 3: Muxing
	w.reportStage(ctx, job.ID, StageMuxing, 30.0)

	tempOutputFile := filepath.Join(jobWorkDir, "output.m4b")
	muxInput := MuxInput{
		AudioFiles:   audioFiles,
		MetadataFile: metaPath,
		CoverImage:   coverImage,
		OutputFile:   tempOutputFile,
		Metadata:     meta,
	}

	progressCb := func(pct float64) {
		// Scale progress from 30.0% to 85.0%
		scaledPct := 30.0 + (pct * 0.55)
		if scaledPct > 85.0 {
			scaledPct = 85.0
		}
		_ = w.queue.UpdateProgress(ctx, job.ID, StageMuxing, scaledPct, nil)
		if w.cfg.OnStageChange != nil {
			w.cfg.OnStageChange(StageMuxing, scaledPct)
		}
	}

	err = w.muxer.Mux(ctx, muxInput, totalDurationMs, progressCb)
	if err != nil {
		return fmt.Errorf("mux audio: %w", err)
	}

	// Step 4: Tagging
	w.reportStage(ctx, job.ID, StageTagging, 90.0)

	// Step 5: Moving atomically to canonical destination
	w.reportStage(ctx, job.ID, StageMoving, 95.0)

	targetDir := filepath.Dir(job.TargetPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create target directory: %w", err)
	}

	if err := catalog.AtomicMove(tempOutputFile, job.TargetPath); err != nil {
		return fmt.Errorf("atomic move to target path: %w", err)
	}

	return nil
}

func (w *Worker) reportStage(ctx context.Context, jobID, stage string, percent float64) {
	_ = w.queue.UpdateProgress(ctx, jobID, stage, percent, nil)
	if w.cfg.OnStageChange != nil {
		w.cfg.OnStageChange(stage, percent)
	}
}

func findCoverImage(sourcePath string) string {
	fi, err := os.Stat(sourcePath)
	if err != nil {
		return ""
	}

	var dir string
	if fi.IsDir() {
		dir = sourcePath
	} else {
		dir = filepath.Dir(sourcePath)
	}

	candidates := []string{
		"cover.jpg", "cover.jpeg", "cover.png",
		"folder.jpg", "folder.jpeg", "folder.png",
		"front.jpg", "front.jpeg", "front.png",
	}

	for _, c := range candidates {
		p := filepath.Join(dir, c)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}

	// Also check case-insensitive match in directory
	var found string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		for _, c := range candidates {
			if name == c {
				found = path
				return filepath.SkipAll
			}
		}
		return nil
	})

	return found
}
