package converter

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMuxer_CLIArgumentGeneration(t *testing.T) {
	// Case 1: Multi-part concat with metadata, cover image, custom audio params
	cfg := MuxerConfig{
		SampleRate: 44100,
		Channels:   2,
	}

	input := MuxInput{
		AudioFiles:   []string{"/tmp/part1.mp3", "/tmp/part2.mp3"},
		MetadataFile: "/tmp/metadata.txt",
		CoverImage:   "/tmp/cover.jpg",
		OutputFile:   "/tmp/out.m4b",
		Metadata: AudioMetadata{
			Title:  "Synthetic Title",
			Artist: "Synthetic Author",
		},
	}

	args := BuildFFmpegArgs(cfg, input, "/tmp/concat_list.txt")
	argsStr := strings.Join(args, " ")

	// Concat demuxer check
	if !strings.Contains(argsStr, "-f concat -safe 0 -i /tmp/concat_list.txt") {
		t.Errorf("missing concat demuxer flags: %s", argsStr)
	}

	// Metadata input & mapping check (Input index 1)
	if !strings.Contains(argsStr, "-i /tmp/metadata.txt") {
		t.Errorf("missing metadata input: %s", argsStr)
	}
	if !strings.Contains(argsStr, "-map_metadata 1") {
		t.Errorf("missing -map_metadata 1: %s", argsStr)
	}

	// Cover image input & attached_pic check (Input index 2)
	if !strings.Contains(argsStr, "-i /tmp/cover.jpg") {
		t.Errorf("missing cover image input: %s", argsStr)
	}
	if !strings.Contains(argsStr, "-map 2:v -disposition:v:0 attached_pic -c:v copy") {
		t.Errorf("missing cover disposition/mapping flags: %s", argsStr)
	}

	// Audio encoding parameters
	if !strings.Contains(argsStr, "-c:a aac -b:a 128k -ar 44100 -ac 2") {
		t.Errorf("missing audio encoding flags: %s", argsStr)
	}

	// Title / Artist tags
	if !strings.Contains(argsStr, "-metadata title=Synthetic Title") {
		t.Errorf("missing title tag: %s", argsStr)
	}
	if !strings.Contains(argsStr, "-metadata artist=Synthetic Author") {
		t.Errorf("missing artist tag: %s", argsStr)
	}

	// Progress and format
	if !strings.Contains(argsStr, "-progress pipe:1 -f mp4 /tmp/out.m4b") {
		t.Errorf("missing progress/format flags: %s", argsStr)
	}

	// Case 2: Single audio file, no cover image -> -vn
	cfgSingle := MuxerConfig{}
	inputSingle := MuxInput{
		AudioFiles: []string{"/tmp/single.mp3"},
		OutputFile: "/tmp/single.m4b",
	}
	argsSingle := BuildFFmpegArgs(cfgSingle, inputSingle, "")
	argsSingleStr := strings.Join(argsSingle, " ")

	if !strings.Contains(argsSingleStr, "-i /tmp/single.mp3") {
		t.Errorf("missing single file input: %s", argsSingleStr)
	}
	if !strings.Contains(argsSingleStr, "-vn") {
		t.Errorf("expected -vn for no-cover input: %s", argsSingleStr)
	}
	if !strings.Contains(argsSingleStr, "-c:a aac -b:a 128k") {
		t.Errorf("missing default aac/128k flags: %s", argsSingleStr)
	}
}

func TestMuxer_ProgressParser(t *testing.T) {
	totalDurationMs := 100000.0 // 100 seconds total

	testCases := []struct {
		line            string
		expectedPercent float64
		expectedOk      bool
	}{
		{
			line:            "out_time_ms=50000",
			expectedPercent: 50.0,
			expectedOk:      true,
		},
		{
			line:            "out_time_ms=25000",
			expectedPercent: 25.0,
			expectedOk:      true,
		},
		{
			line:            "out_time_ms=100000",
			expectedPercent: 100.0,
			expectedOk:      true,
		},
		{
			line:            "out_time_us=75000000",
			expectedPercent: 75.0,
			expectedOk:      true,
		},
		{
			line:            "out_time=00:00:50.000000",
			expectedPercent: 50.0,
			expectedOk:      true,
		},
		{
			line:            "progress=end",
			expectedPercent: 100.0,
			expectedOk:      true,
		},
		{
			line:            "bitrate=64.0kbits/s",
			expectedPercent: 0.0,
			expectedOk:      false,
		},
		{
			line:            "total_size=123456",
			expectedPercent: 0.0,
			expectedOk:      false,
		},
		{
			line:            "speed=12.5x",
			expectedPercent: 0.0,
			expectedOk:      false,
		},
		{
			line:            "",
			expectedPercent: 0.0,
			expectedOk:      false,
		},
	}

	for _, tc := range testCases {
		pct, ok := ParseProgressLine(tc.line, totalDurationMs)
		if ok != tc.expectedOk {
			t.Errorf("line %q: expected ok=%v, got %v", tc.line, tc.expectedOk, ok)
		}
		if ok && math.Abs(pct-tc.expectedPercent) > 0.01 {
			t.Errorf("line %q: expected percent=%.2f, got %.2f", tc.line, tc.expectedPercent, pct)
		}
	}
}

func TestMuxer_WriteConcatList(t *testing.T) {
	tempDir := t.TempDir()
	listPath := filepath.Join(tempDir, "concat.txt")

	files := []string{
		"/path/to/part 1.mp3",
		"/path/to/it's a track.mp3",
	}

	err := WriteConcatList(listPath, files)
	if err != nil {
		t.Fatalf("WriteConcatList failed: %v", err)
	}

	data, err := os.ReadFile(listPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	expected := "file '/path/to/part 1.mp3'\nfile '/path/to/it'\\''s a track.mp3'\n"
	if string(data) != expected {
		t.Errorf("concat list mismatch:\nGot:      %q\nExpected: %q", string(data), expected)
	}
}

func TestMuxer_MuxExecutionWithProgress(t *testing.T) {
	runner := NewMockRunner()

	runner.RegisterHandler("ffmpeg", func(ctx context.Context, opt RunOptions) error {
		// Simulate FFmpeg progress output
		if opt.Stdout != nil {
			progressData := "out_time_ms=25000\nprogress=continue\nout_time_ms=50000\nprogress=continue\nout_time_ms=100000\nprogress=end\n"
			_, _ = opt.Stdout.Write([]byte(progressData))
		}
		// Create output file
		outPath := opt.Args[len(opt.Args)-1]
		return os.WriteFile(outPath, []byte("synthesized m4b"), 0644)
	})

	tempDir := t.TempDir()
	muxer := NewMuxer(runner, MuxerConfig{TempDir: tempDir})

	var reportedProgress []float64
	input := MuxInput{
		AudioFiles: []string{"/mock/track.mp3"},
		OutputFile: filepath.Join(tempDir, "test.m4b"),
	}

	err := muxer.Mux(context.Background(), input, 100000.0, func(pct float64) {
		reportedProgress = append(reportedProgress, pct)
	})
	if err != nil {
		t.Fatalf("Mux failed: %v", err)
	}

	if len(reportedProgress) < 3 {
		t.Fatalf("expected at least 3 progress reports, got %d (%v)", len(reportedProgress), reportedProgress)
	}
	if reportedProgress[len(reportedProgress)-1] != 100.0 {
		t.Errorf("expected final progress 100.0, got %f", reportedProgress[len(reportedProgress)-1])
	}

	// Verify output file was created
	if _, err := os.Stat(input.OutputFile); err != nil {
		t.Errorf("output file does not exist: %v", err)
	}
}
