package ai_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/metadata/ai"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestIntegration_ScorerAmbiguousBand(t *testing.T) {
	candidates := []metadata.BookMetadata{
		{
			Title:   "Synthetic Book Alpha",
			Authors: []string{"Synthetic Author One"},
			Year:    2021,
		},
		{
			Title:   "Synthetic Anthology Book",
			Authors: []string{"Test Author"},
			Year:    2023,
		},
	}

	t.Run("Score >= AutoMatchThreshold skips AI agent entirely", func(t *testing.T) {
		// Mock transport with PanicOnUnmocked: if any HTTP request is attempted, it will panic
		transport := testutil.NewMockTransportForTest(t)

		cfg := ai.Config{
			Enabled:  true,
			Provider: ai.ProviderOpenAI,
			BaseURL:  "http://mock-ai.local",
			APIKey:   "mock-ai-key",
		}
		agent := ai.NewDisambiguationAgent(cfg, transport.Client())

		// Perfect match release
		parsed := metadata.ParsedRelease{
			Title:  "Synthetic Book Alpha",
			Author: "Synthetic Author One",
			Year:   2021,
		}

		exactScore := metadata.Score(parsed, candidates[0])
		if exactScore < metadata.AutoMatchThreshold {
			t.Fatalf("expected exact match score >= %f, got %f", metadata.AutoMatchThreshold, exactScore)
		}

		ctx := context.Background()
		res, err := agent.DisambiguateWithScorer(ctx, "Synthetic Book Alpha 2021", parsed, candidates)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil DisambiguationResult")
		}
		if res.MatchedCandidateIndex != 0 {
			t.Errorf("expected candidate index 0, got %d", res.MatchedCandidateIndex)
		}
		if res.Confidence < metadata.AutoMatchThreshold {
			t.Errorf("expected confidence >= %f, got %f", metadata.AutoMatchThreshold, res.Confidence)
		}
	})

	t.Run("Ambiguous band falls back to ErrAIDisabled when agent is disabled", func(t *testing.T) {
		transport := testutil.NewMockTransportForTest(t)

		// Agent disabled
		cfg := ai.Config{
			Enabled: false,
		}
		agent := ai.NewDisambiguationAgent(cfg, transport.Client())

		// Release with title match and year, but missing author -> score = 0.65 (in ambiguous band [0.50, 0.85))
		parsed := metadata.ParsedRelease{
			Title:  "Synthetic Book Alpha",
			Author: "",     // missing author
			Year:   2021,   // year matches candidate 0
		}

		score := metadata.Score(parsed, candidates[0])
		if score >= metadata.AutoMatchThreshold {
			t.Fatalf("expected score in ambiguous band (< %f), got %f", metadata.AutoMatchThreshold, score)
		}
		if !metadata.IsAmbiguous(score) {
			t.Fatalf("expected IsAmbiguous to be true for score %f", score)
		}

		ctx := context.Background()
		res, err := agent.DisambiguateWithScorer(ctx, "Synthetic.Book.Alpha-UNKNOWN", parsed, candidates)
		if res != nil {
			t.Errorf("expected nil result when agent is disabled, got: %+v", res)
		}
		if !errors.Is(err, ai.ErrAIDisabled) {
			t.Errorf("expected ErrAIDisabled, got: %v", err)
		}
	})

	t.Run("Ambiguous band invokes AI agent when enabled", func(t *testing.T) {
		transport := testutil.NewMockTransportForTest(t)
		openaiFixture := testutil.LoadFixture(t, "testdata/metadata/ai_fixtures/openai_success.json")
		transport.RegisterResponse("http://mock-ai.local/v1/chat/completions", http.StatusOK, openaiFixture, nil)

		cfg := ai.Config{
			Enabled:  true,
			Provider: ai.ProviderOpenAI,
			BaseURL:  "http://mock-ai.local",
			APIKey:   "mock-ai-key",
		}
		agent := ai.NewDisambiguationAgent(cfg, transport.Client())

		// Ambiguous release: title match but missing author and year
		parsed := metadata.ParsedRelease{
			Title:  "Synthetic Book Alpha",
			Author: "",
			Year:   0,
		}

		ctx := context.Background()
		res, err := agent.DisambiguateWithScorer(ctx, "Synthetic.Book.Alpha-UNKNOWN", parsed, candidates)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil DisambiguationResult")
		}
		if res.MatchedCandidateIndex != 0 {
			t.Errorf("expected matched candidate 0, got %d", res.MatchedCandidateIndex)
		}
		if res.Confidence != 0.95 {
			t.Errorf("expected confidence 0.95, got %f", res.Confidence)
		}
		if res.CleanedTitle != "Synthetic Book Alpha" {
			t.Errorf("expected cleaned title 'Synthetic Book Alpha', got %q", res.CleanedTitle)
		}
	})

	t.Run("Ambiguous band with AI failure falls back to manual approval", func(t *testing.T) {
		transport := testutil.NewMockTransportForTest(t)
		transport.RegisterStatusCode("http://mock-ai.local/v1/chat/completions", http.StatusServiceUnavailable)

		cfg := ai.Config{
			Enabled:  true,
			Provider: ai.ProviderOpenAI,
			BaseURL:  "http://mock-ai.local",
			APIKey:   "mock-ai-key",
		}
		agent := ai.NewDisambiguationAgent(cfg, transport.Client())

		parsed := metadata.ParsedRelease{
			Title:  "Synthetic Book Alpha",
			Author: "",
			Year:   0,
		}

		ctx := context.Background()
		res, err := agent.DisambiguateWithScorer(ctx, "Synthetic.Book.Alpha-UNKNOWN", parsed, candidates)
		if res != nil {
			t.Errorf("expected nil result on upstream failure, got %+v", res)
		}
		if !errors.Is(err, ai.ErrFallbackToManualApproval) {
			t.Errorf("expected ErrFallbackToManualApproval, got %v", err)
		}
	})
}

func TestIsAmbiguous(t *testing.T) {
	tests := []struct {
		score    float64
		expected bool
	}{
		{0.00, false},
		{0.49, false},
		{0.50, true},
		{0.65, true},
		{0.84, true},
		{0.85, false}, // AutoMatchThreshold
		{0.95, false},
		{1.00, false},
	}

	for _, tt := range tests {
		got := metadata.IsAmbiguous(tt.score)
		if got != tt.expected {
			t.Errorf("IsAmbiguous(%f) = %v, expected %v", tt.score, got, tt.expected)
		}
	}
}
