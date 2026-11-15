package transcoder

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWorker_EndToEndWithMockRunner(t *testing.T) {
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

	targetPath := filepath.Join(targetDir, "synthetic_audiobook.m4b")

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

	// Verify stage progression: probing -> encoding -> muxing -> tagging -> moving -> completed
	mu.Lock()
	stages := append([]string(nil), stageHistory...)
	mu.Unlock()

	expectedStages := []string{
		StageProbing,
		StageEncoding,
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
}

func TestWorker_EndToEndWithRealRunner(t *testing.T) {
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

	targetPath := filepath.Join(targetDir, "synthetic_real_muxed.m4b")

	var mu sync.Mutex
	var stageHistory []string
	onStageChange := func(stage string, percent float64) {
		mu.Lock()
		defer mu.Unlock()
		if len(stageHistory) == 0 || stageHistory[len(stageHistory)-1] != stage {
			stageHistory = append(stageHistory, stage)
		}
	}

	// Use RealRunner!
	realRunner := NewRealRunner()
	worker := NewWorker(q, realRunner, WorkerConfig{
		WorkerID:      "test-real-worker-1",
		WorkDir:       filepath.Join(tempDir, "work"),
		AudioCodec:    "aac",
		Bitrate:       "64k",
		OnStageChange: onStageChange,
	})

	item, err := q.Enqueue(ctx, "synth-ed-1", sourceDir, targetPath, 5)
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	processed, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce with real ffmpeg failed: %v", err)
	}
	if !processed {
		t.Fatalf("expected job to be processed")
	}

	// Verify all stages visited
	mu.Lock()
	stages := append([]string(nil), stageHistory...)
	mu.Unlock()

	expectedStages := []string{
		StageProbing,
		StageEncoding,
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
	prober := NewProber(realRunner)
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
}
