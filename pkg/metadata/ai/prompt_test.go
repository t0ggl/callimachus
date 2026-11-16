package ai_test

import (
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/metadata/ai"
)

func TestBuildSystemPrompt(t *testing.T) {
	prompt := ai.BuildSystemPrompt()
	if prompt == "" {
		t.Fatal("expected non-empty system prompt")
	}

	requiredSubstrings := []string{
		"matched_candidate_index",
		"confidence",
		"reasoning",
		"is_anthology",
		"cleaned_title",
		"cleaned_author",
	}

	for _, sub := range requiredSubstrings {
		if !strings.Contains(prompt, sub) {
			t.Errorf("system prompt missing required schema key: %q", sub)
		}
	}
}

func TestBuildUserPrompt(t *testing.T) {
	idx := 1.5
	candidates := []metadata.BookMetadata{
		{
			Title:       "Synthetic Book Alpha",
			Subtitle:    "A Synthetic Subtitle",
			Authors:     []string{"Synthetic Author One", "Synthetic Author Two"},
			Year:        2022,
			Publisher:   "Synthetic Publishing House",
			ISBN13:      "9780000000001",
			ASIN:        "B000SYNTH1",
			Series:      "Synthetic Saga",
			SeriesIndex: &idx,
		},
		{
			Title:     "Synthetic Anthology Book",
			Authors:   []string{"Test Author"},
			Year:      2023,
			Publisher: "Synthetic Press",
		},
	}

	release := "Synthetic.Book.Alpha.2022.RETAIL.EPUB-SYNTH"
	userPrompt := ai.BuildUserPrompt(release, candidates)

	if !strings.Contains(userPrompt, release) {
		t.Errorf("user prompt missing raw release string: %s", release)
	}

	expectedStrings := []string{
		"Synthetic Book Alpha",
		"A Synthetic Subtitle",
		"Synthetic Author One, Synthetic Author Two",
		"2022",
		"Synthetic Publishing House",
		"9780000000001",
		"B000SYNTH1",
		"Synthetic Saga #1.5",
		"Synthetic Anthology Book",
		"Test Author",
		"2023",
	}

	for _, exp := range expectedStrings {
		if !strings.Contains(userPrompt, exp) {
			t.Errorf("user prompt missing expected content: %q", exp)
		}
	}
}
