package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Supported provider identifiers.
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderLocal     = "local"
)

// Config configures the AI metadata disambiguation client and agent.
type Config struct {
	Provider      string        `json:"provider"`       // "openai", "anthropic", "local"
	BaseURL       string        `json:"base_url"`       // Endpoint URL
	APIKey        string        `json:"api_key"`        // API key or token
	Model         string        `json:"model"`          // Model name, e.g. "gpt-4o", "claude-3-5-sonnet-20241022"
	Timeout       time.Duration `json:"timeout"`        // HTTP request timeout
	Enabled       bool          `json:"enabled"`        // Opt-in flag
	MinConfidence float64       `json:"min_confidence"` // Minimum confidence threshold (defaults to 0.70)
}

// Provider defines the interface for communicating with an LLM backend.
type Provider interface {
	Name() string
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// OpenAIProvider interacts with OpenAI-compatible chat completion endpoints (/v1/chat/completions).
type OpenAIProvider struct {
	name       string
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewOpenAIProvider creates a new provider for OpenAI or OpenAI-compatible endpoints.
func NewOpenAIProvider(cfg Config, httpClient *http.Client) *OpenAIProvider {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		if strings.ToLower(cfg.Provider) == ProviderLocal {
			baseURL = "http://localhost:11434"
		} else {
			baseURL = "https://api.openai.com"
		}
	}
	model := cfg.Model
	if model == "" {
		if strings.ToLower(cfg.Provider) == ProviderLocal {
			model = "llama3"
		} else {
			model = "gpt-4o-mini"
		}
	}
	name := strings.ToLower(cfg.Provider)
	if name == "" {
		name = ProviderOpenAI
	}
	return &OpenAIProvider{
		name:       name,
		baseURL:    baseURL,
		apiKey:     cfg.APIKey,
		model:      model,
		httpClient: httpClient,
	}
}

// Name returns the provider name.
func (p *OpenAIProvider) Name() string {
	return p.name
}

// EndpointURL constructs the full URL for chat completions.
func (p *OpenAIProvider) EndpointURL() string {
	if strings.HasSuffix(p.baseURL, "/chat/completions") {
		return p.baseURL
	}
	if strings.HasSuffix(p.baseURL, "/v1") {
		return p.baseURL + "/chat/completions"
	}
	return p.baseURL + "/v1/chat/completions"
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatRequest struct {
	Model          string              `json:"model"`
	Messages       []openAIChatMessage `json:"messages"`
	Temperature    float64             `json:"temperature"`
	ResponseFormat *openAIResponseFmt  `json:"response_format,omitempty"`
}

type openAIResponseFmt struct {
	Type string `json:"type"`
}

type openAIChatResponse struct {
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

// Complete executes a chat completion request.
func (p *OpenAIProvider) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqBody := openAIChatRequest{
		Model: p.model,
		Messages: []openAIChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature:    0.0,
		ResponseFormat: &openAIResponseFmt{Type: "json_object"},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal openai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.EndpointURL(), bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read openai response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("openai upstream error HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var chatResp openAIChatResponse
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return "", fmt.Errorf("failed to decode openai response: %w", err)
	}

	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return "", fmt.Errorf("openai api error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", errors.New("openai response contained no choices")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// AnthropicProvider interacts with Anthropic's messages API (/v1/messages).
type AnthropicProvider struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewAnthropicProvider creates a new provider for Anthropic.
func NewAnthropicProvider(cfg Config, httpClient *http.Client) *AnthropicProvider {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	model := cfg.Model
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}
	return &AnthropicProvider{
		baseURL:    baseURL,
		apiKey:     cfg.APIKey,
		model:      model,
		httpClient: httpClient,
	}
}

// Name returns the provider name.
func (p *AnthropicProvider) Name() string {
	return ProviderAnthropic
}

// EndpointURL constructs the full URL for messages.
func (p *AnthropicProvider) EndpointURL() string {
	if strings.HasSuffix(p.baseURL, "/messages") {
		return p.baseURL
	}
	if strings.HasSuffix(p.baseURL, "/v1") {
		return p.baseURL + "/messages"
	}
	return p.baseURL + "/v1/messages"
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature float64            `json:"temperature"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete executes a messages completion request against Anthropic API.
func (p *AnthropicProvider) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqBody := anthropicRequest{
		Model:     p.model,
		MaxTokens: 1024,
		System:    systemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.0,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal anthropic request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.EndpointURL(), bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if p.apiKey != "" {
		req.Header.Set("x-api-key", p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read anthropic response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("anthropic upstream error HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var msgResp anthropicResponse
	if err := json.Unmarshal(respBytes, &msgResp); err != nil {
		return "", fmt.Errorf("failed to decode anthropic response: %w", err)
	}

	if msgResp.Error != nil && msgResp.Error.Message != "" {
		return "", fmt.Errorf("anthropic api error: %s", msgResp.Error.Message)
	}

	var sb strings.Builder
	for _, block := range msgResp.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}

	if sb.Len() == 0 {
		return "", errors.New("anthropic response contained no text content")
	}

	return sb.String(), nil
}

// Client provides a unified client implementing Provider.
type Client struct {
	provider Provider
	cfg      Config
	http     *http.Client
}

// NewClient creates a new unified Client based on the provided configuration.
func NewClient(cfg Config, httpClient *http.Client) (*Client, error) {
	if httpClient == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	}

	var p Provider
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case ProviderOpenAI, "":
		p = NewOpenAIProvider(cfg, httpClient)
	case ProviderAnthropic:
		p = NewAnthropicProvider(cfg, httpClient)
	case ProviderLocal:
		p = NewOpenAIProvider(cfg, httpClient)
	default:
		return nil, fmt.Errorf("unsupported AI provider: %q", cfg.Provider)
	}

	return &Client{
		provider: p,
		cfg:      cfg,
		http:     httpClient,
	}, nil
}

// Name returns the active provider's name.
func (c *Client) Name() string {
	if c.provider != nil {
		return c.provider.Name()
	}
	return c.cfg.Provider
}

// Complete delegates completion to the underlying configured provider.
func (c *Client) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if c.provider == nil {
		return "", errors.New("ai client provider not initialized")
	}
	return c.provider.Complete(ctx, systemPrompt, userPrompt)
}
