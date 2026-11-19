package converter

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestWorker_EndToEndWithMockRunner(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	q, _ := setupTestQueueDB(t)
	ctx := context.Background()

	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "staging")
	targetDir := filepath.Join(tempDir, "audiobooks")
	_ = os.MkdirAll(sourceDir, 0755)
	_ = os.MkdirAll(targetDir, 0755)

	// Copy real 1-second audio fixture to staging
	realFixturePath, err := filepath.Abs("../../testdata/audio/mock_mp3.mp3")
	if err != nil {
		t.Fatalf("resolve real fixture path failed: %v", err)
	}
	stagedTrack := filepath.Join(sourceDir, "track01.mp3")
	fixtureBytes, err := os.ReadFile(realFixturePath)
	if err != nil {
		t.Fatalf("read fixture bytes failed: %v", err)
	}
	if err := os.WriteFile(stagedTrack, fixtureBytes, 0644); err != nil {
		t.Fatalf("write staged track failed: %v", err)
	}

	targetPath := filepath.Join(targetDir, "Synthetic Author One", "Synthetic Book Alpha", "Synthetic Book Alpha - Synthetic Author One.m4b")

	// Setup MockRunner
	mockRunner := NewMockRunner()

	// ffprobe handler: returns probe JSON with 1.0 second duration
	mockRunner.RegisterHandler("ffprobe", func(ctx context.Context, opt RunOptions) error {
		jsonResp := `{
			"streams": [
				{
					"codec_name": "mp3",
					"codec_type": "audio",
					"sample_rate": "44100",
					"channels": 1,
					"bit_rate": "64000",
					"duration": "1.000000",
					"tags": {"title": "Synthetic Chapter 1"}
				}
			],
			"format": {
				"duration": "1.000000",
				"bit_rate": "64000",
				"tags": {
					"title": "Synthetic Audio Title",
					"artist": "Synthetic Audio Author"
				}
			}
		}`
		if opt.Stdout != nil {
			_, _ = opt.Stdout.Write([]byte(jsonResp))
		}
		return nil
	})

	// ffmpeg handler: writes progress lines and creates output file
	mockRunner.RegisterHandler("ffmpeg", func(ctx context.Context, opt RunOptions) error {
		if opt.Stdout != nil {
			progressLines := "out_time_ms=500\nprogress=continue\nout_time_ms=1000\nprogress=end\n"
			_, _ = opt.Stdout.Write([]byte(progressLines))
		}
		// The last argument is the output file path
		outPath := opt.Args[len(opt.Args)-1]
		return os.WriteFile(outPath, []byte("synthesized m4b container"), 0644)
	})

	var mu sync.Mutex
	var stageHistory []string
	onStageChange := func(stage string, percent float64) {
		mu.Lock()
		defer mu.Unlock()
		if len(stageHistory) == 0 || stageHistory[len(stageHistory)-1] != stage {
			stageHistory = append(stageHistory, stage)
		}
	}

	worker := NewWorker(q, mockRunner, WorkerConfig{
		WorkerID:      "test-mock-worker-1",
		WorkDir:       filepath.Join(tempDir, "work"),
		OnStageChange: onStageChange,
	})

	// Enqueue job
	item, err := q.Enqueue(ctx, "synth-ed-1", sourceDir, targetPath, 1)
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	// Process job
	processed, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}
	if !processed {
		t.Fatalf("expected job to be processed, got false")
	}

	// Verify stage progression: preparing -> encoding -> muxing -> tagging -> moving -> completed
	mu.Lock()
	stages := append([]string(nil), stageHistory...)
	mu.Unlock()

	expectedStages := []string{
		StagePreparing,
		StageConverting,
		StageMuxing,
		StageTagging,
		StageMoving,
		StageCompleted,
	}

	if len(stages) != len(expectedStages) {
		t.Fatalf("expected %d stages (%v), got %d (%v)", len(expectedStages), expectedStages, len(stages), stages)
	}
	for i, exp := range expectedStages {
		if stages[i] != exp {
			t.Errorf("stage %d mismatch: expected %s, got %s", i, exp, stages[i])
		}
	}

	// Verify target file was moved atomically to canonical destination
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("expected target file %s to exist: %v", targetPath, err)
	}

	// Verify queue item completed in DB
	finalItem, err := q.GetJob(ctx, item.ID)
	if err != nil {
		t.Fatalf("get job failed: %v", err)
	}
	if finalItem.Status != StatusCompleted {
		t.Errorf("expected final status 'completed', got '%s'", finalItem.Status)
	}
	if finalItem.ProgressPercent != 100.0 {
		t.Errorf("expected final progress 100.0, got %f", finalItem.ProgressPercent)
	}
	for _, event := range []string{"conversion job queued", "conversion job started", "conversion job stage changed", "conversion job completed"} {
		if !strings.Contains(logs.String(), event) {
			t.Errorf("missing log event %q", event)
		}
	}
}

func TestWorker_EndToEndMultiStage(t *testing.T) {
	q, _ := setupTestQueueDB(t)
	ctx := context.Background()

	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "staging")
	targetDir := filepath.Join(tempDir, "audiobooks")
	_ = os.MkdirAll(sourceDir, 0755)
	_ = os.MkdirAll(targetDir, 0755)

	// Copy real 1-second mp3 fixture
	fixturePath, err := filepath.Abs("../../testdata/audio/mock_mp3.mp3")
	if err != nil {
		t.Fatalf("resolve fixture failed: %v", err)
	}
	trackPath := filepath.Join(sourceDir, "01 - Synthetic Track.mp3")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture failed: %v", err)
	}
	if err := os.WriteFile(trackPath, fixtureBytes, 0644); err != nil {
		t.Fatalf("write track failed: %v", err)
	}

	targetPath := filepath.Join(targetDir, "Synthetic Author One", "Synthetic Book Alpha", "Synthetic Book Alpha - Synthetic Author One.m4b")

	var mu sync.Mutex
	var stageHistory []string
	onStageChange := func(stage string, percent float64) {
		mu.Lock()
		defer mu.Unlock()
		if len(stageHistory) == 0 || stageHistory[len(stageHistory)-1] != stage {
			stageHistory = append(stageHistory, stage)
		}
	}

	// Use MockRunner
	mockRunner := NewMockRunner()
	mockRunner.RegisterHandler("ffprobe", func(ctx context.Context, opt RunOptions) error {
		jsonResp := `{
			"streams": [{
				"codec_name": "mp3",
				"codec_type": "audio",
				"sample_rate": "44100",
				"channels": 1,
				"bit_rate": "64000",
				"duration": "1.000000",
				"tags": {"title": "Synthetic Chapter"}
			}],
			"format": {
				"format_name": "mp3",
				"duration": "1.000000",
				"bit_rate": "64000"
			}
		}`
		if opt.Stdout != nil {
			_, _ = opt.Stdout.Write([]byte(jsonResp))
		}
		return nil
	})
	mockRunner.RegisterHandler("ffmpeg", func(ctx context.Context, opt RunOptions) error {
		if opt.Stdout != nil {
			progressLines := "out_time_ms=500\nprogress=continue\nout_time_ms=1000\nprogress=end\n"
			_, _ = opt.Stdout.Write([]byte(progressLines))
		}
		outPath := opt.Args[len(opt.Args)-1]
		return os.WriteFile(outPath, []byte("synthesized m4b container"), 0644)
	})

	worker := NewWorker(q, mockRunner, WorkerConfig{
		WorkerID:      "test-mock-worker-2",
		WorkDir:       filepath.Join(tempDir, "work"),
		OnStageChange: onStageChange,
	})

	item, err := q.Enqueue(ctx, "synth-ed-1", sourceDir, targetPath, 5)
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	processed, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce with mock ffmpeg failed: %v", err)
	}
	if !processed {
		t.Fatalf("expected job to be processed")
	}

	// Verify all stages visited
	mu.Lock()
	stages := append([]string(nil), stageHistory...)
	mu.Unlock()

	expectedStages := []string{
		StagePreparing,
		StageConverting,
		StageMuxing,
		StageTagging,
		StageMoving,
		StageCompleted,
	}

	if len(stages) != len(expectedStages) {
		t.Fatalf("expected %d stages (%v), got %d (%v)", len(expectedStages), expectedStages, len(stages), stages)
	}
	for i, exp := range expectedStages {
		if stages[i] != exp {
			t.Errorf("stage %d mismatch: expected %s, got %s", i, exp, stages[i])
		}
	}

	// Verify the generated m4b exists and can be probed by ffprobe
	prober := NewProber(mockRunner)
	probeResult, err := prober.ProbeFile(ctx, targetPath)
	if err != nil {
		t.Fatalf("ProbeFile on generated .m4b failed: %v", err)
	}
	if probeResult.Duration < 0.5 {
		t.Errorf("expected valid audio duration, got %f", probeResult.Duration)
	}

	// Verify database record
	jobRecord, err := q.GetJob(ctx, item.ID)
	if err != nil {
		t.Fatalf("get job record failed: %v", err)
	}
	if jobRecord.Status != StatusCompleted {
		t.Errorf("expected status 'completed', got '%s'", jobRecord.Status)
	}
}

func TestWorker_FailureHandling(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	q, _ := setupTestQueueDB(t)
	ctx := context.Background()

	tempDir := t.TempDir()
	nonExistentDir := filepath.Join(tempDir, "does_not_exist")
	targetPath := filepath.Join(tempDir, "out.m4b")

	mockRunner := NewMockRunner()
	worker := NewWorker(q, mockRunner, WorkerConfig{
		WorkerID: "test-fail-worker",
		WorkDir:  filepath.Join(tempDir, "work"),
	})

	item, err := q.Enqueue(ctx, "synth-ed-1", nonExistentDir, targetPath, 0)
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	// RunOnce should report error and fail the job
	processed, err := worker.RunOnce(ctx)
	if err == nil {
		t.Fatalf("expected error processing nonexistent source path, got nil")
	}
	if !processed {
		t.Fatalf("expected processed=true even on error")
	}

	// Job should now be pending with attempt=1
	failedJob, err := q.GetJob(ctx, item.ID)
	if err != nil {
		t.Fatalf("get job failed: %v", err)
	}
	if failedJob.Status != StatusPending {
		t.Errorf("expected retryable status 'pending', got '%s'", failedJob.Status)
	}
	if failedJob.Attempts != 1 {
		t.Errorf("expected attempts 1, got %d", failedJob.Attempts)
	}
	if !strings.Contains(logs.String(), "conversion job failed") || strings.Contains(logs.String(), nonExistentDir) {
		t.Error("expected a conversion failure event without the raw error path")
	}
}

// All subprocesses are mocked: M4B tagging may remux, but must never encode.
func TestWorker_FixedEncodingAndM4BPassthrough(t *testing.T) {
	for _, ext := range []string{".mp3", ".flac", ".m4b", ".M4B"} {
		t.Run(ext, func(t *testing.T) {
			q, _ := setupTestQueueDB(t)
			ctx := context.Background()
			// Paths can contain flag substrings, including UUID segments like "-ac".
			dir := filepath.Join(t.TempDir(), "job-ac123")
			sourceDir := filepath.Join(dir, "download")
			if err := os.MkdirAll(sourceDir, 0755); err != nil {
				t.Fatal(err)
			}
			parts := 2
			passthrough := strings.EqualFold(ext, ".m4b")
			if passthrough {
				parts = 1
			}
			for i := 0; i < parts; i++ {
				if err := os.WriteFile(filepath.Join(sourceDir, fmt.Sprintf("%02d%s", i, ext)), []byte("source"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			runner := NewMockRunner()
			runner.SetDefaultHandler(func(_ context.Context, opt RunOptions) error {
				t.Fatalf("unexpected subprocess: %s", opt.Name)
				return nil
			})
			runner.RegisterHandler("ffprobe", func(_ context.Context, opt RunOptions) error {
				_, err := io.WriteString(opt.Stdout, `{"streams":[{"codec_type":"audio","codec_name":"aac","duration":"10"}],"format":{"duration":"10"}}`)
				return err
			})
			muxCalls := 0
			runner.RegisterHandler("ffmpeg", func(_ context.Context, opt RunOptions) error {
				muxCalls++
				args := strings.Join(opt.Args, " ")
				if passthrough {
					for _, flag := range []string{"-c:a copy", "-map_chapters 0", "-map_metadata 0", "-map 0:v? -c:v copy"} {
						if !strings.Contains(args, flag) {
							t.Errorf("missing %s: %s", flag, args)
						}
					}
					for _, flag := range []string{"-b:a", "-ar", "-ac", "concat"} {
						if slices.Contains(opt.Args, flag) {
							t.Errorf("unexpected %s in passthrough: %s", flag, args)
						}
					}
					for _, arg := range opt.Args {
						if filepath.Base(arg) == "metadata.txt" {
							t.Errorf("unexpected metadata file in passthrough: %s", args)
						}
					}
				} else {
					if !strings.Contains(args, "-c:a aac -b:a 128k") || !strings.Contains(args, "-f concat") {
						t.Errorf("expected multipart AAC 128k conversion: %s", args)
					}
				}
				return os.WriteFile(opt.Args[len(opt.Args)-1], []byte("published"), 0644)
			})
			target := filepath.Join(dir, "Synthetic Author One", "Synthetic Book Alpha", "Synthetic Book Alpha - Synthetic Author One.m4b")
			var stages []string
			worker := NewWorker(q, runner, WorkerConfig{
				WorkerID: "fixed-audio-test", WorkDir: filepath.Join(dir, "work"),
				SampleRate: 22050, Channels: 1,
				OnStageChange: func(stage string, _ float64) { stages = append(stages, stage) },
			})
			item, err := q.Enqueue(ctx, "synth-ed-1", sourceDir, target, 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := worker.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if muxCalls != 1 {
				t.Fatalf("expected one tagging/conversion command, got %d", muxCalls)
			}
			for _, stage := range stages {
				if passthrough && stage == StageConverting {
					t.Error("M4B entered encoding stage")
				}
			}
			final, err := q.GetJob(ctx, item.ID)
			if err != nil || final.Status != StatusCompleted {
				t.Fatalf("job not completed: %+v, %v", final, err)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "published" {
				t.Fatalf("canonical output missing: %q, %v", data, err)
			}
			entries, err := os.ReadDir(sourceDir)
			if err != nil || len(entries) != parts {
				t.Fatalf("source files were changed: %v", err)
			}
		})
	}
}

func TestWorkerEbookRealCalibre(t *testing.T) {
	if testing.Short() || os.Getenv("CALLIMACHUS_REAL_CALIBRE") != "1" {
		t.Skip("opt in with CALLIMACHUS_REAL_CALIBRE=1")
	}
	for _, format := range []string{"epub", "azw3"} {
		t.Run(format, func(t *testing.T) {
			q, _ := setupTestQueueDB(t)
			dir := t.TempDir()
			source := filepath.Join(dir, "input.fb2")
			if err := os.WriteFile(source, []byte(`<?xml version="1.0" encoding="UTF-8"?><FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0"><description><title-info><genre>sf</genre><author><first-name>Test</first-name><last-name>Author</last-name></author><book-title>Test Book</book-title><lang>en</lang></title-info></description><body><section><title><p>Chapter One</p></title><p>A book for conversion testing.</p></section></body></FictionBook>`), 0644); err != nil {
				t.Fatal(err)
			}
			job, err := q.EnqueueEbook(context.Background(), "synth-ed-1", source, filepath.Join(dir, "library"), catalog.BookDetails{Title: "Test Book", Author: "Test Author", Format: format})
			if err != nil {
				t.Fatal(err)
			}
			w := NewWorker(q, nil, WorkerConfig{WorkerID: "real-calibre-test", WorkDir: filepath.Join(dir, "work")})
			if _, err := w.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(job.TargetPath); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type cancellableEbookRunner struct {
	*MockRunner
	started chan struct{}
}

func (r cancellableEbookRunner) Run(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	close(r.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestWorkerStopsCancelledConversion(t *testing.T) {
	queue, db := setupTestQueueDB(t)
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	job, err := queue.EnqueueEbook(ctx, "synth-ed-1", filepath.Join(t.TempDir(), "source.mobi"), t.TempDir(),
		catalog.BookDetails{Title: "Book", Author: "Author", Format: "epub"})
	if err != nil {
		t.Fatal(err)
	}
	runner := cancellableEbookRunner{MockRunner: NewMockRunner(), started: make(chan struct{})}
	worker := NewWorker(queue, runner, WorkerConfig{WorkerID: "worker", WorkDir: t.TempDir()})
	done := make(chan error, 1)
	go func() {
		_, err := worker.RunOnce(ctx)
		done <- err
	}()
	select {
	case <-runner.started:
	case <-ctx.Done():
		t.Fatal("conversion did not start")
	}
	if _, err := db.ExecContext(ctx, "UPDATE conversion_queue SET status='cancelled' WHERE id=?", job.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("mock conversion command was not interrupted")
	}
	got, err := queue.GetJob(ctx, job.ID)
	if err != nil || got.Status != StatusCancelled || got.Attempts != 0 {
		t.Fatalf("cancelled conversion was retried: %+v %v", got, err)
	}
	if _, err := os.Stat(job.TargetPath); !os.IsNotExist(err) {
		t.Fatalf("cancelled output was published: %v", err)
	}
}
