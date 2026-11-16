package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/t0ggl/callimachus/pkg/metadata"
)

const (
	// DefaultMinConfidence is the default confidence floor required for an AI match.
	DefaultMinConfidence = 0.70
)

var (
	// ErrAIDisabled is returned when AI disambiguation is called while disabled or unconfigured.
	ErrAIDisabled = errors.New("ai disambiguation is disabled")

	// ErrFallbackToManualApproval is returned when AI disambiguation fails, is malformed,
	// or has insufficient confidence, signaling that manual review is required.
	ErrFallbackToManualApproval = errors.New("ai disambiguation fallback to manual approval")
)

// DisambiguationResult contains the outcome of an LLM metadata disambiguation evaluation.
type DisambiguationResult struct {
	MatchedCandidateIndex int                    `json:"matched_candidate_index"`
	Confidence            float64                `json:"confidence"`
	Reasoning             string                 `json:"reasoning"`
	IsAnthology           bool                   `json:"is_anthology"`
	CleanedTitle          string                 `json:"cleaned_title"`
	CleanedAuthor         string                 `json:"cleaned_author"`
	MatchedCandidate      *metadata.BookMetadata `json:"matched_candidate,omitempty"`
}

// DisambiguationAgent coordinates metadata disambiguation with an LLM provider.
type DisambiguationAgent struct {
	cfg        Config
	provider   Provider
	httpClient *http.Client
}

// NewDisambiguationAgent constructs a DisambiguationAgent with the given config and HTTP client.
func NewDisambiguationAgent(cfg Config, httpClient *http.Client) *DisambiguationAgent {
	var p Provider
	if cfg.Enabled {
		client, err := NewClient(cfg, httpClient)
		if err == nil {
			p = client
		}
	}
	return &DisambiguationAgent{
		cfg:        cfg,
		provider:   p,
		httpClient: httpClient,
	}
}

// NewDisambiguationAgentWithProvider constructs an agent using a specific Provider implementation.
func NewDisambiguationAgentWithProvider(cfg Config, provider Provider) *DisambiguationAgent {
	return &DisambiguationAgent{
		cfg:      cfg,
		provider: provider,
	}
}

// IsEnabled returns true if AI disambiguation is explicitly enabled and properly configured
// with a non-empty API key or endpoint.
func (a *DisambiguationAgent) IsEnabled() bool {
	if a == nil || !a.cfg.Enabled {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(a.cfg.Provider))
	switch provider {
	case ProviderOpenAI, "":
		return a.cfg.APIKey != ""
	case ProviderAnthropic:
		return a.cfg.APIKey != ""
	case ProviderLocal:
		return a.cfg.BaseURL != "" || a.cfg.APIKey != ""
	default:
		return false
	}
}

func (a *DisambiguationAgent) getProvider() Provider {
	if a.provider != nil {
		return a.provider
	}
	if a.IsEnabled() {
		client, err := NewClient(a.cfg, a.httpClient)
		if err == nil {
			a.provider = client
			return a.provider
		}
	}
	return nil
}

// Disambiguate evaluates a release string against a list of candidates.
// Returns ErrAIDisabled if the agent is disabled, or ErrFallbackToManualApproval
// if parsing fails, upstream errors occur, confidence is low, or index is out of bounds.
func (a *DisambiguationAgent) Disambiguate(ctx context.Context, release string, candidates []metadata.BookMetadata) (*DisambiguationResult, error) {
	if !a.IsEnabled() {
		return nil, ErrAIDisabled
	}
	if len(candidates) == 0 || strings.TrimSpace(release) == "" {
		return nil, ErrFallbackToManualApproval
	}

	provider := a.getProvider()
	if provider == nil {
		return nil, ErrFallbackToManualApproval
	}

	sysPrompt := BuildSystemPrompt()
	userPrompt := BuildUserPrompt(release, candidates)

	rawResp, err := provider.Complete(ctx, sysPrompt, userPrompt)
	if err != nil {
		return nil, ErrFallbackToManualApproval
	}

	cleanedJSON := extractJSON(rawResp)
	var res DisambiguationResult
	if err := json.Unmarshal([]byte(cleanedJSON), &res); err != nil {
		return nil, ErrFallbackToManualApproval
	}

	// Validate candidate index within bounds
	if res.MatchedCandidateIndex < 0 || res.MatchedCandidateIndex >= len(candidates) {
		return nil, ErrFallbackToManualApproval
	}

	// Validate confidence threshold
	minConf := a.cfg.MinConfidence
	if minConf <= 0 {
		minConf = DefaultMinConfidence
	}
	if res.Confidence < minConf {
		return nil, ErrFallbackToManualApproval
	}

	// Populate matched candidate pointer
	matched := candidates[res.MatchedCandidateIndex]
	res.MatchedCandidate = &matched

	// Default cleaned title and author if omitted
	if res.CleanedTitle == "" {
		res.CleanedTitle = matched.Title
	}
	if res.CleanedAuthor == "" && len(matched.Authors) > 0 {
		res.CleanedAuthor = matched.Authors[0]
	}

	return &res, nil
}

// DisambiguateWithScorer evaluates candidates using metadata.Score.
// If a candidate reaches AutoMatchThreshold (0.85), it is accepted deterministically without calling AI.
// If no candidate reaches AutoMatchThreshold, but candidates are available:
// - If the AI agent is enabled, it attempts AI disambiguation.
// - If disabled or if AI disambiguation fails, it returns ErrAIDisabled or ErrFallbackToManualApproval.
func (a *DisambiguationAgent) DisambiguateWithScorer(ctx context.Context, release string, parsed metadata.ParsedRelease, candidates []metadata.BookMetadata) (*DisambiguationResult, error) {
	if len(candidates) == 0 {
		return nil, ErrFallbackToManualApproval
	}

	bestIdx := -1
	bestScore := -1.0
	for i, c := range candidates {
		score := metadata.Score(parsed, c)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	if bestScore >= metadata.AutoMatchThreshold && bestIdx >= 0 {
		matched := candidates[bestIdx]
		author := ""
		if len(matched.Authors) > 0 {
			author = matched.Authors[0]
		}
		return &DisambiguationResult{
			MatchedCandidateIndex: bestIdx,
			Confidence:            bestScore,
			Reasoning:             "Deterministic heuristic confidence score >= AutoMatchThreshold",
			CleanedTitle:          matched.Title,
			CleanedAuthor:         author,
			MatchedCandidate:      &matched,
		}, nil
	}

	// Candidates fall below AutoMatchThreshold (ambiguous band)
	if !a.IsEnabled() {
		return nil, ErrAIDisabled
	}

	return a.Disambiguate(ctx, release, candidates)
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	// Strip markdown code fences
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}

	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end != -1 && end > start {
		return s[start : end+1]
	}
	return s
}
