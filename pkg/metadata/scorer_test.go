package metadata_test

import (
	"testing"

	"github.com/t0ggl/callimachus/pkg/metadata"
)

func TestScorer_ExactMatch(t *testing.T) {
	parsed := metadata.ParsedRelease{
		Title:  "Test Book Alpha",
		Author: "Synthetic Author One",
		Year:   2021,
	}

	candidate := metadata.BookMetadata{
		Title:   "Test Book Alpha",
		Authors: []string{"Synthetic Author One"},
		Year:    2021,
	}

	score := metadata.Score(parsed, candidate)
	if score < 0.95 {
		t.Fatalf("expected exact match score >= 0.95, got %f", score)
	}
	if score != 1.0 {
		t.Errorf("expected perfect match 1.0, got %f", score)
	}
}

func TestScorer_SubtitleVariations(t *testing.T) {
	candidate := metadata.BookMetadata{
		Title:    "Test Book Alpha",
		Subtitle: "The Genesis of Testing",
		Authors:  []string{"Synthetic Author One"},
		Year:     2021,
	}

	tests := []struct {
		name        string
		parsedTitle string
	}{
		{
			name:        "title only (candidate has subtitle)",
			parsedTitle: "Test Book Alpha",
		},
		{
			name:        "full title with colon separator",
			parsedTitle: "Test Book Alpha: The Genesis of Testing",
		},
		{
			name:        "full title with hyphen separator",
			parsedTitle: "Test Book Alpha - The Genesis of Testing",
		},
		{
			name:        "subtitle variation missing article",
			parsedTitle: "Test Book Alpha: Genesis of Testing",
		},
		{
			name:        "title with punctuation differences",
			parsedTitle: "Test Book Alpha: The Genesis of Testing!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := metadata.ParsedRelease{
				Title:  tt.parsedTitle,
				Author: "Synthetic Author One",
				Year:   2021,
			}

			score := metadata.Score(parsed, candidate)
			if score <= metadata.AutoMatchThreshold {
				t.Errorf("expected score > %f (AutoMatchThreshold), got %f for title %q", metadata.AutoMatchThreshold, score, tt.parsedTitle)
			}
			if score <= 0.85 {
				t.Errorf("expected score > 0.85, got %f", score)
			}
		})
	}
}

func TestScorer_LowScoreOnWrongAuthorOrUnrelatedTitle(t *testing.T) {
	candidate := metadata.BookMetadata{
		Title:    "Test Book Alpha",
		Subtitle: "The Genesis of Testing",
		Authors:  []string{"Synthetic Author One"},
		Year:     2021,
	}

	tests := []struct {
		name   string
		parsed metadata.ParsedRelease
	}{
		{
			name: "exact title but completely wrong author with missing year",
			parsed: metadata.ParsedRelease{
				Title:  "Test Book Alpha",
				Author: "Completely Different Person",
				Year:   0,
			},
		},
		{
			name: "exact title, wrong author, different year (>5 years)",
			parsed: metadata.ParsedRelease{
				Title:  "Test Book Alpha",
				Author: "Unrelated Person",
				Year:   2010,
			},
		},
		{
			name: "unrelated title with same author and missing year",
			parsed: metadata.ParsedRelease{
				Title:  "Completely Unrelated Subject Matter",
				Author: "Synthetic Author One",
				Year:   0,
			},
		},
		{
			name: "unrelated title and wrong author with same year",
			parsed: metadata.ParsedRelease{
				Title:  "Totally Different Galactic Story",
				Author: "Another Anonymous Person",
				Year:   2021,
			},
		},
		{
			name: "unrelated title, wrong author, missing year",
			parsed: metadata.ParsedRelease{
				Title:  "Completely Different Novel",
				Author: "Unknown Person",
				Year:   0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := metadata.Score(tt.parsed, candidate)
			if score >= 0.50 {
				t.Errorf("expected score < 0.50, got %f", score)
			}
		})
	}
}

func TestScorer_AuthorMatchingVariations(t *testing.T) {
	candidate := metadata.BookMetadata{
		Title:   "Test Book Alpha",
		Authors: []string{"Synthetic Author One", "Synthetic Author Two"},
		Year:    2021,
	}

	tests := []struct {
		name         string
		parsedAuthor string
		minAuthorSim float64
	}{
		{
			name:         "inverted last, first author",
			parsedAuthor: "Author One, Synthetic",
			minAuthorSim: 0.95,
		},
		{
			name:         "author with middle initial",
			parsedAuthor: "Synthetic A. Author One",
			minAuthorSim: 0.95,
		},
		{
			name:         "author with first initial",
			parsedAuthor: "S. Author One",
			minAuthorSim: 0.95,
		},
		{
			name:         "co-authors with ampersand",
			parsedAuthor: "Synthetic Author One & Synthetic Author Two",
			minAuthorSim: 0.95,
		},
		{
			name:         "first co-author only",
			parsedAuthor: "Synthetic Author One",
			minAuthorSim: 0.95,
		},
		{
			name:         "second co-author only",
			parsedAuthor: "Synthetic Author Two",
			minAuthorSim: 0.95,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sim := metadata.AuthorSimilarity(tt.parsedAuthor, candidate.Authors)
			if sim < tt.minAuthorSim {
				t.Errorf("expected author similarity >= %f, got %f for author %q", tt.minAuthorSim, sim, tt.parsedAuthor)
			}

			parsed := metadata.ParsedRelease{
				Title:  candidate.Title,
				Author: tt.parsedAuthor,
				Year:   candidate.Year,
			}
			score := metadata.Score(parsed, candidate)
			if score < metadata.AutoMatchThreshold {
				t.Errorf("expected total score >= AutoMatchThreshold (%f), got %f", metadata.AutoMatchThreshold, score)
			}
		})
	}
}

func TestScorer_YearProximityEdgeCases(t *testing.T) {
	tests := []struct {
		y1       int
		y2       int
		expected float64
	}{
		{2021, 2021, 1.0}, // exact match
		{2021, 2020, 0.8}, // -1 year
		{2021, 2022, 0.8}, // +1 year
		{2021, 2019, 0.5}, // -2 years
		{2021, 2023, 0.5}, // +2 years
		{2021, 2018, 0.3}, // -3 years
		{2021, 2017, 0.1}, // -4 years
		{2021, 2015, 0.0}, // -6 years (>5 years)
		{2021, 2030, 0.0}, // +9 years (>5 years)
		{0, 2021, 0.0},    // missing parsed year
		{2021, 0, 0.0},    // missing candidate year
		{0, 0, 0.0},       // missing both years
		{-1, 2021, 0.0},   // negative year
	}

	for _, tt := range tests {
		prox := metadata.YearProximity(tt.y1, tt.y2)
		if prox != tt.expected {
			t.Errorf("YearProximity(%d, %d) = %f, expected %f", tt.y1, tt.y2, prox, tt.expected)
		}
	}
}

func TestScorer_AutoMatchThresholdConstant(t *testing.T) {
	if metadata.AutoMatchThreshold != 0.85 {
		t.Fatalf("expected AutoMatchThreshold to equal 0.85, got %f", metadata.AutoMatchThreshold)
	}
}
