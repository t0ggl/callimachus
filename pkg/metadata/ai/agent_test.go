package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/metadata/ai"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func sampleCandidates() []metadata.BookMetadata {
	idx1 := 1.0
	return []metadata.BookMetadata{
		{
			Title:       "Synthetic Book Alpha",
			Authors:     []string{"Synthetic Author One"},
			Year:        2021,
			Publisher:   "Synthetic Press",
			ISBN13:      "9781000000001",
			ASIN:        "B000TEST01",
			Series:      "Synthetic Chronicles",
			SeriesIndex: &idx1,
		},
		{
			Title:     "Synthetic Anthology Book",
			Authors:   []string{"Test Author"},
			Year:      2023,
			Publisher: "Synthetic House",
			ISBN13:    "9781000000002",
		},
	}
}

func TestDisambiguationAgent_DisabledByDefault(t *testing.T) {
	// Zero config agent: zero HTTP requests made.
	// Transport's PanicOnUnmocked ensures if any HTTP call is made, test panics immediately.
	transport := testutil.NewMockTransportForTest(t)
	agent := ai.NewDisambiguationAgent(ai.Config{}, transport.Client())

	if agent.IsEnabled() {
		t.Error("expected agent with zero config to be disabled")
	}

	ctx := context.Background()
	result, err := agent.Disambiguate(ctx, "Synthetic.Book.Alpha.2021-SYNTH", sampleCandidates())
	if result != nil {
		t.Errorf("expected nil result when disabled, got %+v", result)
	}
	if !errors.Is(err, ai.ErrAIDisabled) {
		t.Errorf("expected ErrAIDisabled, got: %v", err)
	}

	// Verify unconfigured / missing API key returns disabled
	agentUnconfigured := ai.NewDisambiguationAgent(ai.Config{
		Enabled:  true,
		Provider: "openai",
		APIKey:   "", // empty API key
	}, transport.Client())

	if agentUnconfigured.IsEnabled() {
		t.Error("expected agent with empty APIKey to be disabled")
	}

	_, err2 := agentUnconfigured.Disambiguate(ctx, "Synthetic.Book.Alpha.2021-SYNTH", sampleCandidates())
	if !errors.Is(err2, ai.ErrAIDisabled) {
		t.Errorf("expected ErrAIDisabled for empty API key, got: %v", err2)
	}

	// Nil agent check
	var nilAgent *ai.DisambiguationAgent
	if nilAgent.IsEnabled() {
		t.Error("expected nil agent to return IsEnabled() == false")
	}
	_, errNil := nilAgent.Disambiguate(ctx, "Synthetic.Book.Alpha.2021-SYNTH", sampleCandidates())
	if !errors.Is(errNil, ai.ErrAIDisabled) {
		t.Errorf("expected nil agent to return ErrAIDisabled, got: %v", errNil)
	}
}

func TestDisambiguationAgent_OpenAISuccess(t *testing.T) {
	transport := testutil.NewMockTransportForTest(t)
	openaiSuccessFixture := testutil.LoadFixture(t, "testdata/metadata/ai_fixtures/openai_success.json")

	transport.RegisterResponse("http://mock-ai.local/v1/chat/completions", http.StatusOK, openaiSuccessFixture, nil)

	cfg := ai.Config{
		Enabled:  true,
		Provider: ai.ProviderOpenAI,
		BaseURL:  "http://mock-ai.local",
		APIKey:   "sk-mock-key-12345",
		Model:    "gpt-4o",
		Timeout:  2 * time.Second,
	}

	agent := ai.NewDisambiguationAgent(cfg, transport.Client())
	if !agent.IsEnabled() {
		t.Fatal("expected agent to be enabled")
	}

	candidates := sampleCandidates()
	ctx := context.Background()
	release := "Synthetic.Book.Alpha.2021.RETAIL.EPUB-SYNTH"

	result, err := agent.Disambiguate(ctx, release, candidates)
	if err != nil {
		t.Fatalf("unexpected error during disambiguation: %v", err)
	}

	if result.MatchedCandidateIndex != 0 {
		t.Errorf("expected matched candidate index 0, got %d", result.MatchedCandidateIndex)
	}
	if result.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", result.Confidence)
	}
	if result.IsAnthology {
		t.Errorf("expected is_anthology to be false, got true")
	}
	if result.CleanedTitle != "Synthetic Book Alpha" {
		t.Errorf("expected cleaned title 'Synthetic Book Alpha', got %q", result.CleanedTitle)
	}
	if result.CleanedAuthor != "Synthetic Author One" {
		t.Errorf("expected cleaned author 'Synthetic Author One', got %q", result.CleanedAuthor)
	}
	if result.MatchedCandidate == nil {
		t.Fatal("expected MatchedCandidate pointer to be set, got nil")
	}
	if result.MatchedCandidate.Title != "Synthetic Book Alpha" {
		t.Errorf("expected matched candidate title 'Synthetic Book Alpha', got %q", result.MatchedCandidate.Title)
	}
	if result.Reasoning == "" {
		t.Error("expected non-empty reasoning rationale")
	}
}

func TestDisambiguationAgent_AnthropicSuccess(t *testing.T) {
	transport := testutil.NewMockTransportForTest(t)
	anthropicSuccessFixture := testutil.LoadFixture(t, "testdata/metadata/ai_fixtures/anthropic_success.json")

	transport.RegisterResponse("http://mock-ai.local/v1/messages", http.StatusOK, anthropicSuccessFixture, nil)

	cfg := ai.Config{
		Enabled:  true,
		Provider: ai.ProviderAnthropic,
		BaseURL:  "http://mock-ai.local",
		APIKey:   "anthropic-mock-key-12345",
		Model:    "claude-3-5-sonnet-20241022",
		Timeout:  2 * time.Second,
	}

	agent := ai.NewDisambiguationAgent(cfg, transport.Client())
	if !agent.IsEnabled() {
		t.Fatal("expected agent to be enabled")
	}

	candidates := sampleCandidates()
	ctx := context.Background()
	release := "Synthetic.Anthology.Book.Complete.Collection.2023-SYNTH"

	result, err := agent.Disambiguate(ctx, release, candidates)
	if err != nil {
		t.Fatalf("unexpected error during anthropic disambiguation: %v", err)
	}

	if result.MatchedCandidateIndex != 1 {
		t.Errorf("expected matched candidate index 1, got %d", result.MatchedCandidateIndex)
	}
	if result.Confidence != 0.92 {
		t.Errorf("expected confidence 0.92, got %f", result.Confidence)
	}
	if !result.IsAnthology {
		t.Errorf("expected is_anthology to be true, got false")
	}
	if result.CleanedTitle != "Synthetic Anthology Book" {
		t.Errorf("expected cleaned title 'Synthetic Anthology Book', got %q", result.CleanedTitle)
	}
	if result.CleanedAuthor != "Test Author" {
		t.Errorf("expected cleaned author 'Test Author', got %q", result.CleanedAuthor)
	}
	if result.MatchedCandidate == nil || result.MatchedCandidate.Title != "Synthetic Anthology Book" {
		t.Errorf("expected MatchedCandidate to be 'Synthetic Anthology Book'")
	}
}

func TestDisambiguationAgent_ErrorFallbacks(t *testing.T) {
	candidates := sampleCandidates()
	ctx := context.Background()
	release := "Synthetic.Book.Alpha.2021-SYNTH"

	t.Run("HTTP 429 Rate Limit Error", func(t *testing.T) {
		transport := testutil.NewMockTransportForTest(t)
		rateLimitFixture := testutil.LoadFixture(t, "testdata/metadata/ai_fixtures/rate_limit_error.json")
		transport.RegisterResponse("http://mock-ai.local/v1/chat/completions", http.StatusTooManyRequests, rateLimitFixture, nil)

		cfg := ai.Config{
			Enabled:  true,
			Provider: ai.ProviderOpenAI,
			BaseURL:  "http://mock-ai.local",
			APIKey:   "sk-mock-key",
		}
		agent := ai.NewDisambiguationAgent(cfg, transport.Client())

		result, err := agent.Disambiguate(ctx, release, candidates)
		if result != nil {
			t.Errorf("expected nil result on HTTP 429, got: %+v", result)
		}
		if !errors.Is(err, ai.ErrFallbackToManualApproval) {
			t.Errorf("expected ErrFallbackToManualApproval, got: %v", err)
		}
	})

	t.Run("HTTP 500 Internal Server Error", func(t *testing.T) {
		transport := testutil.NewMockTransportForTest(t)
		transport.RegisterStatusCode("http://mock-ai.local/v1/chat/completions", http.StatusInternalServerError)

		cfg := ai.Config{
			Enabled:  true,
			Provider: ai.ProviderOpenAI,
			BaseURL:  "http://mock-ai.local",
			APIKey:   "sk-mock-key",
		}
		agent := ai.NewDisambiguationAgent(cfg, transport.Client())

		result, err := agent.Disambiguate(ctx, release, candidates)
		if result != nil {
			t.Errorf("expected nil result on HTTP 500, got: %+v", result)
		}
		if !errors.Is(err, ai.ErrFallbackToManualApproval) {
			t.Errorf("expected ErrFallbackToManualApproval, got: %v", err)
		}
	})

	t.Run("Corrupt Malformed JSON Response", func(t *testing.T) {
		transport := testutil.NewMockTransportForTest(t)
		malformedFixture := testutil.LoadFixture(t, "testdata/metadata/ai_fixtures/malformed_json.json")
		transport.RegisterResponse("http://mock-ai.local/v1/chat/completions", http.StatusOK, malformedFixture, nil)

		cfg := ai.Config{
			Enabled:  true,
			Provider: ai.ProviderOpenAI,
			BaseURL:  "http://mock-ai.local",
			APIKey:   "sk-mock-key",
		}
		agent := ai.NewDisambiguationAgent(cfg, transport.Client())

		result, err := agent.Disambiguate(ctx, release, candidates)
		if result != nil {
			t.Errorf("expected nil result on malformed JSON, got: %+v", result)
		}
		if !errors.Is(err, ai.ErrFallbackToManualApproval) {
			t.Errorf("expected ErrFallbackToManualApproval, got: %v", err)
		}
	})

	t.Run("Candidate Index Out of Bounds", func(t *testing.T) {
		transport := testutil.NewMockTransportForTest(t)
		// Model returns candidate index 99 when only 2 candidates exist
		outOfBoundsResponse := []byte(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "{\"matched_candidate_index\": 99, \"confidence\": 0.95, \"reasoning\": \"invalid index\"}"
				}
			}]
		}`)
		transport.RegisterResponse("http://mock-ai.local/v1/chat/completions", http.StatusOK, outOfBoundsResponse, nil)

		cfg := ai.Config{
			Enabled:  true,
			Provider: ai.ProviderOpenAI,
			BaseURL:  "http://mock-ai.local",
			APIKey:   "sk-mock-key",
		}
		agent := ai.NewDisambiguationAgent(cfg, transport.Client())

		result, err := agent.Disambiguate(ctx, release, candidates)
		if result != nil {
			t.Errorf("expected nil result on index out of bounds, got: %+v", result)
		}
		if !errors.Is(err, ai.ErrFallbackToManualApproval) {
			t.Errorf("expected ErrFallbackToManualApproval, got: %v", err)
		}
	})

	t.Run("Negative Candidate Index", func(t *testing.T) {
		transport := testutil.NewMockTransportForTest(t)
		negIndexResponse := []byte(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "{\"matched_candidate_index\": -1, \"confidence\": 0.95, \"reasoning\": \"no candidate matches\"}"
				}
			}]
		}`)
		transport.RegisterResponse("http://mock-ai.local/v1/chat/completions", http.StatusOK, negIndexResponse, nil)

		cfg := ai.Config{
			Enabled:  true,
			Provider: ai.ProviderOpenAI,
			BaseURL:  "http://mock-ai.local",
			APIKey:   "sk-mock-key",
		}
		agent := ai.NewDisambiguationAgent(cfg, transport.Client())

		result, err := agent.Disambiguate(ctx, release, candidates)
		if result != nil {
			t.Errorf("expected nil result on -1 index, got: %+v", result)
		}
		if !errors.Is(err, ai.ErrFallbackToManualApproval) {
			t.Errorf("expected ErrFallbackToManualApproval, got: %v", err)
		}
	})

	t.Run("Low Confidence Score", func(t *testing.T) {
		transport := testutil.NewMockTransportForTest(t)
		// Model returns confidence 0.45, below threshold 0.70
		lowConfidenceResponse := []byte(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "{\"matched_candidate_index\": 0, \"confidence\": 0.45, \"reasoning\": \"uncertain match\"}"
				}
			}]
		}`)
		transport.RegisterResponse("http://mock-ai.local/v1/chat/completions", http.StatusOK, lowConfidenceResponse, nil)

		cfg := ai.Config{
			Enabled:       true,
			Provider:      ai.ProviderOpenAI,
			BaseURL:       "http://mock-ai.local",
			APIKey:        "sk-mock-key",
			MinConfidence: 0.70,
		}
		agent := ai.NewDisambiguationAgent(cfg, transport.Client())

		result, err := agent.Disambiguate(ctx, release, candidates)
		if result != nil {
			t.Errorf("expected nil result on low confidence, got: %+v", result)
		}
		if !errors.Is(err, ai.ErrFallbackToManualApproval) {
			t.Errorf("expected ErrFallbackToManualApproval, got: %v", err)
		}
	})

	t.Run("Empty Candidates List", func(t *testing.T) {
		cfg := ai.Config{
			Enabled:  true,
			Provider: ai.ProviderOpenAI,
			BaseURL:  "http://mock-ai.local",
			APIKey:   "sk-mock-key",
		}
		agent := ai.NewDisambiguationAgent(cfg, nil)

		result, err := agent.Disambiguate(ctx, release, []metadata.BookMetadata{})
		if result != nil {
			t.Errorf("expected nil result on empty candidates, got: %+v", result)
		}
		if !errors.Is(err, ai.ErrFallbackToManualApproval) {
			t.Errorf("expected ErrFallbackToManualApproval, got: %v", err)
		}
	})

	t.Run("Empty Release String", func(t *testing.T) {
		cfg := ai.Config{
			Enabled:  true,
			Provider: ai.ProviderOpenAI,
			BaseURL:  "http://mock-ai.local",
			APIKey:   "sk-mock-key",
		}
		agent := ai.NewDisambiguationAgent(cfg, nil)

		result, err := agent.Disambiguate(ctx, "   ", candidates)
		if result != nil {
			t.Errorf("expected nil result on empty release, got: %+v", result)
		}
		if !errors.Is(err, ai.ErrFallbackToManualApproval) {
			t.Errorf("expected ErrFallbackToManualApproval, got: %v", err)
		}
	})
}

func TestDisambiguationAgent_MarkdownFencedResponse(t *testing.T) {
	transport := testutil.NewMockTransportForTest(t)
	// Response where model returned markdown code fences around JSON
	fencedContent := "```json\n{\n  \"matched_candidate_index\": 0,\n  \"confidence\": 0.98,\n  \"reasoning\": \"Cleaned markdown block match\",\n  \"is_anthology\": false,\n  \"cleaned_title\": \"Synthetic Book Alpha\",\n  \"cleaned_author\": \"Synthetic Author One\"\n}\n```"
	respObj := map[string]any{
		"id":     "chatcmpl-test",
		"object": "chat.completion",
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": fencedContent,
				},
			},
		},
	}
	markdownFenceResponse, err := json.Marshal(respObj)
	if err != nil {
		t.Fatalf("failed to marshal mock response: %v", err)
	}
	transport.RegisterResponse("http://mock-ai.local/v1/chat/completions", http.StatusOK, markdownFenceResponse, nil)

	cfg := ai.Config{
		Enabled:  true,
		Provider: ai.ProviderOpenAI,
		BaseURL:  "http://mock-ai.local",
		APIKey:   "sk-mock-key",
	}
	agent := ai.NewDisambiguationAgent(cfg, transport.Client())

	candidates := sampleCandidates()
	ctx := context.Background()

	result, err := agent.Disambiguate(ctx, "Synthetic.Book.Alpha.2021-SYNTH", candidates)
	if err != nil {
		t.Fatalf("unexpected error parsing markdown-fenced json: %v", err)
	}

	if result.MatchedCandidateIndex != 0 {
		t.Errorf("expected candidate 0, got %d", result.MatchedCandidateIndex)
	}
	if result.Confidence != 0.98 {
		t.Errorf("expected confidence 0.98, got %f", result.Confidence)
	}
	if result.CleanedTitle != "Synthetic Book Alpha" {
		t.Errorf("expected cleaned title 'Synthetic Book Alpha', got %q", result.CleanedTitle)
	}
}
