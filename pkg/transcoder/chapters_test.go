package transcoder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFFMetadata_SerializationAndMillisecondTimebase(t *testing.T) {
	meta := AudioMetadata{
		Title:       "Synthetic Book Alpha",
		Artist:      "Synthetic Author One",
		Album:       "Synthetic Book Alpha",
		Composer:    "Synthetic Narrator One",
		Date:        "2024",
		Genre:       "Audiobook",
		Comment:     "Synthesized test audio file",
		Description: "A synthesized test audiobook for offline verification",
	}

	chapters := []Chapter{
		{
			Title:        "Prologue",
			StartSeconds: 0.0,
			EndSeconds:   60.0, // 60,000 ms
		},
		{
			Title:        "Chapter 1",
			StartSeconds: 60.0,
			EndSeconds:   125.5, // 125,500 ms
		},
		{
			Title:        "Chapter 2",
			StartSeconds: 125.5,
			EndSeconds:   180.25, // 180,250 ms
		},
	}

	content := GenerateFFMetadata(meta, chapters)

	// 1. Verify header
	expectedHeader := ";FFMETADATA1\nmajor_brand=isom\nminor_version=512\ncompatible_brands=isomiso2mp41\n"
	if !strings.HasPrefix(content, expectedHeader) {
		t.Fatalf("expected FFMETADATA1 header, got:\n%s", content)
	}

	// 2. Verify global tags
	expectedTags := []string{
		"title=Synthetic Book Alpha\n",
		"artist=Synthetic Author One\n",
		"album=Synthetic Book Alpha\n",
		"composer=Synthetic Narrator One\n",
		"date=2024\n",
		"genre=Audiobook\n",
	}
	for _, tag := range expectedTags {
		if !strings.Contains(content, tag) {
			t.Errorf("expected tag %q not found in output", tag)
		}
	}

	// 3. Verify millisecond conversions and chapter structures
	expectedChapters := []string{
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=60000\ntitle=Prologue\n",
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=60000\nEND=125500\ntitle=Chapter 1\n",
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=125500\nEND=180250\ntitle=Chapter 2\n",
	}
	for _, exp := range expectedChapters {
		if !strings.Contains(content, exp) {
			t.Errorf("expected chapter chunk not found:\n%s\nIn generated content:\n%s", exp, content)
		}
	}
}

func TestFFMetadata_SpecialCharacterEscaping(t *testing.T) {
	meta := AudioMetadata{
		Title: "Synthetic = Title; with #hashes and \\backslashes",
	}

	chapters := []Chapter{
		{
			Title:        "Chapter 1: Equality = Sign; Hash # Tag \\ Slash",
			StartSeconds: 0.0,
			EndSeconds:   10.0,
		},
	}

	content := GenerateFFMetadata(meta, chapters)

	if !strings.Contains(content, `title=Synthetic \= Title\; with \#hashes and \\backslashes`) {
		t.Errorf("global title escaping failed in:\n%s", content)
	}

	if !strings.Contains(content, `title=Chapter 1: Equality \= Sign\; Hash \# Tag \\ Slash`) {
		t.Errorf("chapter title escaping failed in:\n%s", content)
	}
}

func TestFFMetadata_WriteToFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "sub", "metadata.txt")

	meta := AudioMetadata{Title: "Synthetic Dummy"}
	chapters := []Chapter{
		{Title: "Part 1", StartSeconds: 0.0, EndSeconds: 5.0},
	}

	err := WriteFFMetadataFile(filePath, meta, chapters)
	if err != nil {
		t.Fatalf("WriteFFMetadataFile failed: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if !strings.Contains(string(data), ";FFMETADATA1") {
		t.Errorf("written file missing header")
	}
	if !strings.Contains(string(data), "START=0\nEND=5000\ntitle=Part 1") {
		t.Errorf("written file missing chapter: %s", string(data))
	}
}

func TestFFMetadata_ChaptersFromProbes(t *testing.T) {
	probes := []*AudioProbe{
		{
			FilePath: "/tmp/track01.mp3",
			Duration: 10.5,
			Title:    "Introduction",
		},
		{
			FilePath: "/tmp/track02.mp3",
			Duration: 20.0,
			Title:    "", // Should fallback to track02
		},
		{
			FilePath: "/tmp/track03.mp3",
			Duration: 15.2,
			Title:    "Conclusion",
		},
	}

	chapters := ChaptersFromProbes(probes)
	if len(chapters) != 3 {
		t.Fatalf("expected 3 chapters, got %d", len(chapters))
	}

	// Chapter 1: 0 to 10.5
	if chapters[0].Title != "Introduction" {
		t.Errorf("ch[0] title: expected 'Introduction', got '%s'", chapters[0].Title)
	}
	if chapters[0].StartSeconds != 0.0 || chapters[0].EndSeconds != 10.5 {
		t.Errorf("ch[0] timestamps: expected 0.0-10.5, got %f-%f", chapters[0].StartSeconds, chapters[0].EndSeconds)
	}

	// Chapter 2: 10.5 to 30.5
	if chapters[1].Title != "track02" {
		t.Errorf("ch[1] title fallback: expected 'track02', got '%s'", chapters[1].Title)
	}
	if chapters[1].StartSeconds != 10.5 || chapters[1].EndSeconds != 30.5 {
		t.Errorf("ch[1] timestamps: expected 10.5-30.5, got %f-%f", chapters[1].StartSeconds, chapters[1].EndSeconds)
	}

	// Chapter 3: 30.5 to 45.7
	if chapters[2].Title != "Conclusion" {
		t.Errorf("ch[2] title: expected 'Conclusion', got '%s'", chapters[2].Title)
	}
	if chapters[2].StartSeconds != 30.5 || chapters[2].EndSeconds != 45.7 {
		t.Errorf("ch[2] timestamps: expected 30.5-45.7, got %f-%f", chapters[2].StartSeconds, chapters[2].EndSeconds)
	}
}
