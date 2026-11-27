package converter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MuxerConfig holds encoding configuration for FFmpeg.
type MuxerConfig struct {
	SampleRate int    // Target sample rate (e.g. 44100, 0 = keep source)
	Channels   int    // Target channel count (e.g. 1=mono, 2=stereo, 0 = keep source)
	TempDir    string // Temporary directory for staging
}

// MuxInput specifies the source files and metadata inputs to be muxed.
type MuxInput struct {
	AudioFiles   []string
	MetadataFile string // Path to FFMETADATA1 chapter and metadata file
	CoverImage   string // Path to cover image to embed as attached_pic
	OutputFile   string // Destination .m4b path
	Metadata     AudioMetadata
}

// Muxer wraps FFmpeg execution and argument building.
type Muxer struct {
	runner CommandRunner
	cfg    MuxerConfig
}

// NewMuxer returns an initialized Muxer.
func NewMuxer(runner CommandRunner, cfg MuxerConfig) *Muxer {
	if runner == nil {
		runner = NewRealRunner()
	}
	return &Muxer{
		runner: runner,
		cfg:    cfg,
	}
}

// BuildFFmpegArgs builds the complete argument list for FFmpeg muxing.
func BuildFFmpegArgs(cfg MuxerConfig, input MuxInput, concatListPath string) []string {
	var args []string

	args = append(args, "-y")

	// Track input indices
	inputIdx := 0

	// Input 0: Audio source(s)
	if concatListPath != "" {
		args = append(args, "-f", "concat", "-safe", "0", "-i", concatListPath)
	} else if len(input.AudioFiles) > 0 {
		args = append(args, "-i", input.AudioFiles[0])
	}
	inputIdx++

	// Input 1: Metadata file (optional)
	metaIdx := -1
	if input.MetadataFile != "" {
		args = append(args, "-i", input.MetadataFile)
		metaIdx = inputIdx
		inputIdx++
	}

	// Input 2: Cover image (optional)
	coverIdx := -1
	if input.CoverImage != "" {
		args = append(args, "-i", input.CoverImage)
		coverIdx = inputIdx
		inputIdx++
	}

	// Stream mapping
	args = append(args, "-map", "0:a")

	// Metadata mapping
	if metaIdx >= 0 {
		idx := fmt.Sprintf("%d", metaIdx)
		args = append(args, "-map_metadata", idx, "-map_chapters", idx)
	} else if isM4BPassthrough(input.AudioFiles) {
		args = append(args, "-map_metadata", "0")
	}

	// Cover image mapping & disposition
	if coverIdx >= 0 {
		args = append(args, "-map", fmt.Sprintf("%d:v", coverIdx))
		args = append(args, "-disposition:v:0", "attached_pic")
		args = append(args, "-c:v", "copy")
	} else if isM4BPassthrough(input.AudioFiles) {
		args = append(args, "-map", "0:v?", "-c:v", "copy")
	} else {
		args = append(args, "-vn")
	}

	// A single M4B only needs tagging/remuxing; never alter its audio.
	if isM4BPassthrough(input.AudioFiles) {
		args = append(args, "-c:a", "copy", "-map_chapters", "0")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "128k")
		if cfg.SampleRate > 0 {
			args = append(args, "-ar", fmt.Sprintf("%d", cfg.SampleRate))
		}
		if cfg.Channels > 0 {
			args = append(args, "-ac", fmt.Sprintf("%d", cfg.Channels))
		}
	}

	// Explicit metadata flags if present
	if input.Metadata.Title != "" {
		args = append(args, "-metadata", "title="+input.Metadata.Title)
	}
	if input.Metadata.Artist != "" {
		args = append(args, "-metadata", "artist="+input.Metadata.Artist)
	}
	if input.Metadata.Album != "" {
		args = append(args, "-metadata", "album="+input.Metadata.Album)
	}
	if input.Metadata.Composer != "" {
		args = append(args, "-metadata", "composer="+input.Metadata.Composer)
	}
	if input.Metadata.Date != "" {
		args = append(args, "-metadata", "date="+input.Metadata.Date)
	}
	if input.Metadata.Genre != "" {
		args = append(args, "-metadata", "genre="+input.Metadata.Genre)
	}

	// Progress reporting
	args = append(args, "-progress", "pipe:1")

	// Container format
	args = append(args, "-f", "mp4")

	// Output file
	args = append(args, input.OutputFile)

	return args
}

// WriteConcatList writes the FFmpeg concat demuxer manifest file.
func WriteConcatList(filePath string, audioFiles []string) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create concat dir: %w", err)
	}

	var sb strings.Builder
	for _, f := range audioFiles {
		escaped := strings.ReplaceAll(filepath.Clean(f), "'", "'\\''")
		sb.WriteString(fmt.Sprintf("file '%s'\n", escaped))
	}

	return os.WriteFile(filePath, []byte(sb.String()), 0644)
}

// ParseProgressLine parses a single line of FFmpeg progress output into a progress percentage (0.0 to 100.0).
// Returns (percent, true) if progress was parsed, or (0.0, false) for non-progress lines.
func ParseProgressLine(line string, totalDurationMs float64) (float64, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0.0, false
	}

	if line == "progress=end" {
		return 100.0, true
	}

	if strings.HasPrefix(line, "out_time_ms=") {
		valStr := strings.TrimPrefix(line, "out_time_ms=")
		val, err := strconv.ParseFloat(strings.TrimSpace(valStr), 64)
		if err != nil || totalDurationMs <= 0 {
			return 0.0, false
		}

		// Some FFmpeg builds output out_time_ms as microseconds
		if val > totalDurationMs && (val/1000.0) <= (totalDurationMs*1.05) {
			val = val / 1000.0
		}

		pct := (val / totalDurationMs) * 100.0
		if pct > 100.0 {
			pct = 100.0
		}
		if pct < 0.0 {
			pct = 0.0
		}
		return pct, true
	}

	if strings.HasPrefix(line, "out_time_us=") {
		valStr := strings.TrimPrefix(line, "out_time_us=")
		val, err := strconv.ParseFloat(strings.TrimSpace(valStr), 64)
		if err != nil || totalDurationMs <= 0 {
			return 0.0, false
		}
		valMs := val / 1000.0
		pct := (valMs / totalDurationMs) * 100.0
		if pct > 100.0 {
			pct = 100.0
		}
		if pct < 0.0 {
			pct = 0.0
		}
		return pct, true
	}

	if strings.HasPrefix(line, "out_time=") {
		valStr := strings.TrimPrefix(line, "out_time=")
		ms, ok := parseTimeStringToMS(strings.TrimSpace(valStr))
		if !ok || totalDurationMs <= 0 {
			return 0.0, false
		}
		pct := (ms / totalDurationMs) * 100.0
		if pct > 100.0 {
			pct = 100.0
		}
		if pct < 0.0 {
			pct = 0.0
		}
		return pct, true
	}

	return 0.0, false
}

func parseTimeStringToMS(s string) (float64, bool) {
	// Format: HH:MM:SS.fraction
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0.0, false
	}
	hours, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0.0, false
	}
	minutes, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0.0, false
	}
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0.0, false
	}
	totalSeconds := hours*3600 + minutes*60 + seconds
	return totalSeconds * 1000.0, true
}

type progressWriter struct {
	totalDurationMs float64
	onProgress      func(percent float64)
	buf             bytes.Buffer
}

func (pw *progressWriter) Write(p []byte) (n int, err error) {
	pw.buf.Write(p)
	for {
		line, err := pw.buf.ReadString('\n')
		if err != nil {
			pw.buf.WriteString(line)
			break
		}
		pct, ok := ParseProgressLine(line, pw.totalDurationMs)
		if ok && pw.onProgress != nil {
			pw.onProgress(pct)
		}
	}
	return len(p), nil
}

// Mux executes the FFmpeg command, writing the muxed output and streaming progress updates.
func (m *Muxer) Mux(ctx context.Context, input MuxInput, totalDurationMs float64, onProgress func(percent float64)) error {
	if len(input.AudioFiles) == 0 {
		return errors.New("no audio files specified for muxing")
	}
	if input.OutputFile == "" {
		return errors.New("output file path cannot be empty")
	}

	outDir := filepath.Dir(input.OutputFile)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// If multiple audio files, write concat list
	var concatListPath string
	if len(input.AudioFiles) > 1 {
		concatListPath = filepath.Join(outDir, "concat_list.txt")
		if err := WriteConcatList(concatListPath, input.AudioFiles); err != nil {
			return fmt.Errorf("write concat list: %w", err)
		}
		defer func() {
			_ = os.Remove(concatListPath)
		}()
	}

	args := BuildFFmpegArgs(m.cfg, input, concatListPath)

	pw := &progressWriter{
		totalDurationMs: totalDurationMs,
		onProgress:      onProgress,
	}

	var stderrBuf bytes.Buffer
	opt := RunOptions{
		Name:   "ffmpeg",
		Args:   args,
		Stdout: pw,
		Stderr: &stderrBuf,
	}

	err := m.runner.RunCustom(ctx, opt)
	if err != nil {
		if stderrBuf.Len() > 0 {
			return fmt.Errorf("ffmpeg execution failed: %w (stderr: %s)", err, stderrBuf.String())
		}
		return fmt.Errorf("ffmpeg execution failed: %w", err)
	}

	return nil
}

// isM4BPassthrough excludes multipart inputs, which still need conversion.
func isM4BPassthrough(files []string) bool {
	return len(files) == 1 && strings.EqualFold(filepath.Ext(files[0]), ".m4b")
}
