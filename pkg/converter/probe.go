package converter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// AudioProbe encapsulates extracted technical audio properties and embedded metadata.
type AudioProbe struct {
	FilePath    string  `json:"file_path"`
	Duration    float64 `json:"duration"`     // duration in seconds
	Bitrate     int     `json:"bitrate"`      // bitrate in bits per second
	SampleRate  int     `json:"sample_rate"`  // sample rate in Hz
	Channels    int     `json:"channels"`     // channel count (1=mono, 2=stereo)
	CodecFormat string  `json:"codec_format"` // codec name (e.g. "mp3", "aac")
	Title       string  `json:"title,omitempty"`
	Artist      string  `json:"artist,omitempty"`
	Album       string  `json:"album,omitempty"`
	Composer    string  `json:"composer,omitempty"` // Narrator
	ASIN        string  `json:"asin,omitempty"`
	AlbumArtist string  `json:"album_artist,omitempty"`
	Date        string  `json:"date,omitempty"`
}

type ffprobeStream struct {
	Index         int               `json:"index"`
	CodecName     string            `json:"codec_name"`
	CodecLongName string            `json:"codec_long_name"`
	CodecType     string            `json:"codec_type"`
	SampleRate    string            `json:"sample_rate"`
	Channels      int               `json:"channels"`
	ChannelLayout string            `json:"channel_layout"`
	BitRate       string            `json:"bit_rate"`
	Duration      string            `json:"duration"`
	Tags          map[string]string `json:"tags"`
}

type ffprobeFormat struct {
	Filename   string            `json:"filename"`
	FormatName string            `json:"format_name"`
	Duration   string            `json:"duration"`
	Size       string            `json:"size"`
	BitRate    string            `json:"bit_rate"`
	Tags       map[string]string `json:"tags"`
}

type ffprobeResult struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

// Prober runs ffprobe and extracts AudioProbe metadata.
type Prober struct {
	runner CommandRunner
}

// NewProber creates a new Prober backed by the given CommandRunner.
func NewProber(runner CommandRunner) *Prober {
	if runner == nil {
		runner = NewRealRunner()
	}
	return &Prober{runner: runner}
}

// ProbeFile probes a single audio file and parses its technical specifications and tags.
func (p *Prober) ProbeFile(ctx context.Context, filePath string) (*AudioProbe, error) {
	if strings.TrimSpace(filePath) == "" {
		return nil, errors.New("filePath cannot be empty")
	}

	if _, err := os.Stat(filePath); err != nil {
		return nil, fmt.Errorf("stat audio file %s: %w", filePath, err)
	}

	out, err := p.runner.Run(ctx, "ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", filePath)
	if err != nil {
		return nil, fmt.Errorf("ffprobe probe failed on %s: %w", filePath, err)
	}

	return ParseFFprobeJSON(filePath, out)
}

// ProbeFiles probes multiple audio files in sequence and returns their probes.
func (p *Prober) ProbeFiles(ctx context.Context, filePaths []string) ([]*AudioProbe, error) {
	if len(filePaths) == 0 {
		return nil, errors.New("no files provided for preparing")
	}

	probes := make([]*AudioProbe, 0, len(filePaths))
	for _, fp := range filePaths {
		probe, err := p.ProbeFile(ctx, fp)
		if err != nil {
			return nil, err
		}
		probes = append(probes, probe)
	}
	return probes, nil
}

// ParseFFprobeJSON parses the JSON output of ffprobe.
func ParseFFprobeJSON(filePath string, data []byte) (*AudioProbe, error) {
	var res ffprobeResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("parse ffprobe json: %w", err)
	}

	// Locate primary audio stream
	var audioStream *ffprobeStream
	for i := range res.Streams {
		if strings.EqualFold(res.Streams[i].CodecType, "audio") {
			audioStream = &res.Streams[i]
			break
		}
	}

	if audioStream == nil {
		return nil, fmt.Errorf("no audio stream found in %s", filePath)
	}

	probe := &AudioProbe{
		FilePath:    filePath,
		Channels:    audioStream.Channels,
		CodecFormat: audioStream.CodecName,
	}

	if probe.CodecFormat == "" {
		probe.CodecFormat = res.Format.FormatName
	}

	// Sample Rate
	if audioStream.SampleRate != "" {
		if sr, err := strconv.Atoi(audioStream.SampleRate); err == nil {
			probe.SampleRate = sr
		}
	}

	// Bitrate: check stream, fallback to format
	if audioStream.BitRate != "" {
		if br, err := strconv.Atoi(audioStream.BitRate); err == nil {
			probe.Bitrate = br
		}
	}
	if probe.Bitrate == 0 && res.Format.BitRate != "" {
		if br, err := strconv.Atoi(res.Format.BitRate); err == nil {
			probe.Bitrate = br
		}
	}

	// Duration: check stream, fallback to format
	if audioStream.Duration != "" {
		if d, err := strconv.ParseFloat(audioStream.Duration, 64); err == nil {
			probe.Duration = d
		}
	}
	if probe.Duration == 0 && res.Format.Duration != "" {
		if d, err := strconv.ParseFloat(res.Format.Duration, 64); err == nil {
			probe.Duration = d
		}
	}

	// Extract tags from stream first, fallback to format tags
	probe.Title = findTag(audioStream.Tags, res.Format.Tags, "title")
	probe.Artist = findTag(audioStream.Tags, res.Format.Tags, "artist", "album_artist")
	probe.Album = findTag(audioStream.Tags, res.Format.Tags, "album")
	probe.AlbumArtist = findTag(audioStream.Tags, res.Format.Tags, "album_artist")
	probe.ASIN = findTag(audioStream.Tags, res.Format.Tags, "asin", "audible_asin")
	probe.Composer = findTag(audioStream.Tags, res.Format.Tags, "composer", "narrator")
	probe.Date = findTag(audioStream.Tags, res.Format.Tags, "date", "year")

	return probe, nil
}

func findTag(primary, fallback map[string]string, keys ...string) string {
	for _, k := range keys {
		if val := getTagValue(primary, k); val != "" {
			return val
		}
	}
	for _, k := range keys {
		if val := getTagValue(fallback, k); val != "" {
			return val
		}
	}
	return ""
}

func getTagValue(tags map[string]string, targetKey string) string {
	if tags == nil {
		return ""
	}
	for k, v := range tags {
		if strings.EqualFold(k, targetKey) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
