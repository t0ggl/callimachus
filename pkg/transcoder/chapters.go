package transcoder

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// Chapter represents an individual chapter entry with second-based boundaries.
type Chapter struct {
	Title        string  `json:"title"`
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
}

// AudioMetadata represents descriptive tags embedded into the audiobook container.
type AudioMetadata struct {
	Title       string `json:"title,omitempty"`
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	Composer    string `json:"composer,omitempty"` // Narrator
	Date        string `json:"date,omitempty"`     // Release year or date
	Genre       string `json:"genre,omitempty"`
	Comment     string `json:"comment,omitempty"`
	Description string `json:"description,omitempty"`
}

// EscapeFFMetadata escapes special characters in FFMETADATA1 format (=, ;, #, \, \n).
func EscapeFFMetadata(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '=', ';', '#', '\\':
			sb.WriteRune('\\')
			sb.WriteRune(r)
		case '\n':
			sb.WriteString("\\\n")
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// GenerateFFMetadata formats metadata tags and chapters into the FFMETADATA1 specification.
func GenerateFFMetadata(meta AudioMetadata, chapters []Chapter) string {
	var sb strings.Builder

	// Header and ISO/MP4 brand flags
	sb.WriteString(";FFMETADATA1\n")
	sb.WriteString("major_brand=isom\n")
	sb.WriteString("minor_version=512\n")
	sb.WriteString("compatible_brands=isomiso2mp41\n")

	// Global tags
	if meta.Title != "" {
		sb.WriteString("title=" + EscapeFFMetadata(meta.Title) + "\n")
	}
	if meta.Artist != "" {
		sb.WriteString("artist=" + EscapeFFMetadata(meta.Artist) + "\n")
	}
	if meta.Album != "" {
		sb.WriteString("album=" + EscapeFFMetadata(meta.Album) + "\n")
	}
	if meta.Composer != "" {
		sb.WriteString("composer=" + EscapeFFMetadata(meta.Composer) + "\n")
	}
	if meta.Date != "" {
		sb.WriteString("date=" + EscapeFFMetadata(meta.Date) + "\n")
	}
	if meta.Genre != "" {
		sb.WriteString("genre=" + EscapeFFMetadata(meta.Genre) + "\n")
	}
	if meta.Comment != "" {
		sb.WriteString("comment=" + EscapeFFMetadata(meta.Comment) + "\n")
	}
	if meta.Description != "" {
		sb.WriteString("description=" + EscapeFFMetadata(meta.Description) + "\n")
	}

	// Chapters
	for _, ch := range chapters {
		startMS := int64(math.Round(ch.StartSeconds * 1000.0))
		endMS := int64(math.Round(ch.EndSeconds * 1000.0))
		if endMS <= startMS {
			endMS = startMS + 1
		}

		sb.WriteString("\n[CHAPTER]\n")
		sb.WriteString("TIMEBASE=1/1000\n")
		sb.WriteString(fmt.Sprintf("START=%d\n", startMS))
		sb.WriteString(fmt.Sprintf("END=%d\n", endMS))
		title := ch.Title
		if strings.TrimSpace(title) == "" {
			title = "Chapter"
		}
		sb.WriteString("title=" + EscapeFFMetadata(title) + "\n")
	}

	return sb.String()
}

// WriteFFMetadataFile generates and writes the FFMETADATA1 content to a file on disk.
func WriteFFMetadataFile(filePath string, meta AudioMetadata, chapters []Chapter) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create metadata directory: %w", err)
	}

	content := GenerateFFMetadata(meta, chapters)
	return os.WriteFile(filePath, []byte(content), 0644)
}

// ChaptersFromProbes generates a sequence of contiguous chapters from probed track segments.
func ChaptersFromProbes(probes []*AudioProbe) []Chapter {
	if len(probes) == 0 {
		return nil
	}

	chapters := make([]Chapter, 0, len(probes))
	var offset float64

	for i, p := range probes {
		dur := p.Duration
		if dur < 0.001 {
			dur = 1.0 // fallback duration if probe was 0
		}
		end := offset + dur

		title := p.Title
		if strings.TrimSpace(title) == "" {
			if p.FilePath != "" {
				base := filepath.Base(p.FilePath)
				title = strings.TrimSuffix(base, filepath.Ext(base))
			} else {
				title = fmt.Sprintf("Chapter %d", i+1)
			}
		}

		chapters = append(chapters, Chapter{
			Title:        title,
			StartSeconds: offset,
			EndSeconds:   end,
		})
		offset = end
	}

	return chapters
}
