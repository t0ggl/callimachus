package converter

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestProber_Fixtures(t *testing.T) {
	// Locate repository testdata directory
	mp3Path, err := filepath.Abs("../../testdata/audio/mock_mp3.mp3")
	if err != nil {
		t.Fatalf("resolve mp3 path failed: %v", err)
	}
	m4bPath, err := filepath.Abs("../../testdata/audio/mock_m4b.m4b")
	if err != nil {
		t.Fatalf("resolve m4b path failed: %v", err)
	}

	runner := NewMockRunner()
	runner.RegisterHandler("ffprobe", func(ctx context.Context, opt RunOptions) error {
		filePath := opt.Args[len(opt.Args)-1]
		var jsonResp string
		if strings.HasSuffix(filePath, "mock_mp3.mp3") {
			jsonResp = `{
				"streams": [{
					"codec_name": "mp3",
					"codec_type": "audio",
					"sample_rate": "44100",
					"channels": 1,
					"bit_rate": "64000",
					"duration": "1.000000"
				}],
				"format": {
					"filename": "mock_mp3.mp3",
					"format_name": "mp3",
					"duration": "1.000000",
					"bit_rate": "64000"
				}
			}`
		} else if strings.HasSuffix(filePath, "mock_m4b.m4b") {
			jsonResp = `{
				"streams": [{
					"codec_name": "aac",
					"codec_type": "audio",
					"sample_rate": "44100",
					"channels": 1,
					"bit_rate": "64000",
					"duration": "1.000000"
				}],
				"format": {
					"filename": "mock_m4b.m4b",
					"format_name": "mov,mp4,m4a,3gp,3g2,mj2",
					"duration": "1.000000",
					"bit_rate": "64000"
				}
			}`
		}
		if opt.Stdout != nil {
			_, _ = opt.Stdout.Write([]byte(jsonResp))
		}
		return nil
	})

	prober := NewProber(runner)
	ctx := context.Background()

	// 1. Probe mock_mp3.mp3
	probeMP3, err := prober.ProbeFile(ctx, mp3Path)
	if err != nil {
		t.Fatalf("ProbeFile on mock_mp3.mp3 failed: %v", err)
	}

	if probeMP3.Duration < 0.5 || probeMP3.Duration > 2.0 {
		t.Errorf("expected duration around 1s, got %f", probeMP3.Duration)
	}
	if probeMP3.SampleRate != 44100 {
		t.Errorf("expected sample rate 44100, got %d", probeMP3.SampleRate)
	}
	if probeMP3.Channels != 1 {
		t.Errorf("expected 1 channel, got %d", probeMP3.Channels)
	}
	if probeMP3.CodecFormat != "mp3" {
		t.Errorf("expected codec mp3, got %s", probeMP3.CodecFormat)
	}

	// 2. Probe mock_m4b.m4b
	probeM4B, err := prober.ProbeFile(ctx, m4bPath)
	if err != nil {
		t.Fatalf("ProbeFile on mock_m4b.m4b failed: %v", err)
	}

	if probeM4B.Duration < 0.5 || probeM4B.Duration > 2.0 {
		t.Errorf("expected duration around 1s, got %f", probeM4B.Duration)
	}
	if probeM4B.CodecFormat != "aac" {
		t.Errorf("expected codec aac, got %s", probeM4B.CodecFormat)
	}
}

func TestProber_MockJSON(t *testing.T) {
	mockJSON := []byte(`{
		"streams": [
			{
				"codec_name": "aac",
				"codec_type": "audio",
				"sample_rate": "48000",
				"channels": 2,
				"bit_rate": "128000",
				"duration": "123.456",
				"tags": {
					"title": "Synthetic Track",
					"artist": "Synthetic Artist"
				}
			}
		],
		"format": {
			"filename": "/tmp/synth.m4a",
			"format_name": "mov,mp4,m4a,3gp,3g2,mj2",
			"duration": "123.456",
			"bit_rate": "130000",
			"tags": {
				"album": "Synthetic Album",
				"composer": "Synthetic Narrator",
				"date": "2024"
			}
		}
	}`)

	probe, err := ParseFFprobeJSON("/tmp/synth.m4a", mockJSON)
	if err != nil {
		t.Fatalf("ParseFFprobeJSON failed: %v", err)
	}

	if probe.CodecFormat != "aac" {
		t.Errorf("expected aac codec, got %s", probe.CodecFormat)
	}
	if probe.SampleRate != 48000 {
		t.Errorf("expected 48000 sample rate, got %d", probe.SampleRate)
	}
	if probe.Channels != 2 {
		t.Errorf("expected 2 channels, got %d", probe.Channels)
	}
	if probe.Bitrate != 128000 {
		t.Errorf("expected 128000 bitrate, got %d", probe.Bitrate)
	}
	if probe.Duration != 123.456 {
		t.Errorf("expected duration 123.456, got %f", probe.Duration)
	}
	if probe.Title != "Synthetic Track" {
		t.Errorf("expected title 'Synthetic Track', got '%s'", probe.Title)
	}
	if probe.Artist != "Synthetic Artist" {
		t.Errorf("expected artist 'Synthetic Artist', got '%s'", probe.Artist)
	}
	if probe.Album != "Synthetic Album" {
		t.Errorf("expected album 'Synthetic Album', got '%s'", probe.Album)
	}
	if probe.Composer != "Synthetic Narrator" {
		t.Errorf("expected composer 'Synthetic Narrator', got '%s'", probe.Composer)
	}
	if probe.Date != "2024" {
		t.Errorf("expected date '2024', got '%s'", probe.Date)
	}
}
