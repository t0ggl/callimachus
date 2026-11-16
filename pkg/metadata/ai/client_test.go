package ai_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/metadata/ai"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestClient_ProviderCreation(t *testing.T) {
	transport := testutil.NewMockTransportForTest(t)
	httpClient := transport.Client()

	tests := []struct {
		name         string
		provider     string
		expectedName string
		expectErr    bool
	}{
		{
			name:         "openai default",
			provider:     "openai",
			expectedName: "openai",
			expectErr:    false,
		},
		{
			name:         "empty provider defaults to openai",
			provider:     "",
			expectedName: "openai",
			expectErr:    false,
		},
		{
			name:         "anthropic",
			provider:     "anthropic",
			expectedName: "anthropic",
			expectErr:    false,
		},
		{
			name:         "local provider",
			provider:     "local",
			expectedName: "local",
			expectErr:    false,
		},
		{
			name:      "unsupported provider",
			provider:  "unsupported_vendor",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ai.Config{
				Provider: tt.provider,
				APIKey:   "dummy-test-key",
			}
			client, err := ai.NewClient(cfg, httpClient)
			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error for unsupported provider, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error creating client: %v", err)
			}
			if client.Name() != tt.expectedName {
				t.Errorf("expected client name %q, got %q", tt.expectedName, client.Name())
			}
		})
	}
}

func TestOpenAIProvider_Success(t *testing.T) {
	transport := testutil.NewMockTransportForTest(t)
	openaiSuccessFixture := testutil.LoadFixture(t, "testdata/metadata/ai_fixtures/openai_success.json")

	var interceptedAuth string
	var interceptedPath string
	transport.RegisterHandler("/v1/chat/completions", func(req *http.Request) (*http.Response, error) {
		interceptedAuth = req.Header.Get("Authorization")
		interceptedPath = req.URL.Path
		t2 := testutil.NewMockTransport()
		t2.RegisterResponse("/v1/chat/completions", http.StatusOK, openaiSuccessFixture, nil)
		return t2.RoundTrip(req)
	})

	cfg := ai.Config{
		Provider: ai.ProviderOpenAI,
		BaseURL:  "http://mock-openai.local",
		APIKey:   "mock-test-key-12345",
		Model:    "gpt-4o",
		Timeout:  5 * time.Second,
	}

	provider := ai.NewOpenAIProvider(cfg, transport.Client())
	ctx := context.Background()

	result, err := provider.Complete(ctx, "system instructions", "user prompt")
	if err != nil {
		t.Fatalf("unexpected error during OpenAI Complete: %v", err)
	}

	if interceptedAuth != "Bearer mock-test-key-12345" {
		t.Errorf("expected Bearer token in Authorization header, got %q", interceptedAuth)
	}
	if interceptedPath != "/v1/chat/completions" {
		t.Errorf("expected path /v1/chat/completions, got %q", interceptedPath)
	}

	if !strings.Contains(result, "Synthetic Book Alpha") {
		t.Errorf("expected completion to contain 'Synthetic Book Alpha', got %q", result)
	}
	if !strings.Contains(result, "matched_candidate_index") {
		t.Errorf("expected completion to contain 'matched_candidate_index', got %q", result)
	}
}

func TestOpenAIProvider_HTTPError(t *testing.T) {
	transport := testutil.NewMockTransportForTest(t)
	rateLimitFixture := testutil.LoadFixture(t, "testdata/metadata/ai_fixtures/rate_limit_error.json")

	transport.RegisterResponse("http://mock-openai.local/v1/chat/completions", http.StatusTooManyRequests, rateLimitFixture, nil)

	cfg := ai.Config{
		Provider: ai.ProviderOpenAI,
		BaseURL:  "http://mock-openai.local/v1",
		APIKey:   "mock-test-key",
	}

	provider := ai.NewOpenAIProvider(cfg, transport.Client())
	ctx := context.Background()

	_, err := provider.Complete(ctx, "system", "user")
	if err == nil {
		t.Fatal("expected error on HTTP 429 rate limit, got nil")
	}

	if !strings.Contains(err.Error(), "429") {
		t.Errorf("expected error message to contain '429', got: %v", err)
	}
}

func TestAnthropicProvider_Success(t *testing.T) {
	transport := testutil.NewMockTransportForTest(t)
	anthropicSuccessFixture := testutil.LoadFixture(t, "testdata/metadata/ai_fixtures/anthropic_success.json")

	transport.RegisterResponse("http://mock-anthropic.local/v1/messages", http.StatusOK, anthropicSuccessFixture, nil)

	cfg := ai.Config{
		Provider: ai.ProviderAnthropic,
		BaseURL:  "http://mock-anthropic.local",
		APIKey:   "anthropic-mock-key",
		Model:    "claude-3-5-sonnet-20241022",
	}

	provider := ai.NewAnthropicProvider(cfg, transport.Client())
	ctx := context.Background()

	result, err := provider.Complete(ctx, "system prompt", "user query")
	if err != nil {
		t.Fatalf("unexpected error during Anthropic Complete: %v", err)
	}

	if !strings.Contains(result, "Synthetic Anthology Book") {
		t.Errorf("expected completion to contain 'Synthetic Anthology Book', got %q", result)
	}
	if !strings.Contains(result, "is_anthology") {
		t.Errorf("expected completion to contain 'is_anthology', got %q", result)
	}
}

func TestAnthropicProvider_HTTPError(t *testing.T) {
	transport := testutil.NewMockTransportForTest(t)
	transport.RegisterStatusCode("http://mock-anthropic.local/v1/messages", http.StatusInternalServerError)

	cfg := ai.Config{
		Provider: ai.ProviderAnthropic,
		BaseURL:  "http://mock-anthropic.local",
		APIKey:   "anthropic-mock-key",
	}

	provider := ai.NewAnthropicProvider(cfg, transport.Client())
	ctx := context.Background()

	_, err := provider.Complete(ctx, "system", "user")
	if err == nil {
		t.Fatal("expected error on HTTP 500, got nil")
	}

	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error message to contain '500', got: %v", err)
	}
}
