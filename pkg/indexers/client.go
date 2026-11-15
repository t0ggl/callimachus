package indexers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TorznabClient handles communication with a Torznab or Newznab indexer.
type TorznabClient struct {
	BaseURL    string
	APIKey     string
	Name       string
	HTTPClient *http.Client
}

// NewTorznabClient creates a new TorznabClient with the given parameters.
// If httpClient is nil, a default client with a 30-second timeout is used.
func NewTorznabClient(baseURL, apiKey string, httpClient *http.Client) *TorznabClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	name := baseURL
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		name = u.Host
	}

	return &TorznabClient{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		Name:       name,
		HTTPClient: httpClient,
	}
}

// GetCapabilities queries the indexer for its supported capabilities via `t=caps`.
func (c *TorznabClient) GetCapabilities(ctx context.Context) (*IndexerCapabilities, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("torznab: invalid base URL: %w", err)
	}

	if u.Path == "" || u.Path == "/" {
		u.Path = "/api"
	} else if !strings.HasSuffix(u.Path, "/api") {
		u.Path = strings.TrimRight(u.Path, "/") + "/api"
	}

	vals := u.Query()
	if c.APIKey != "" {
		vals.Set("apikey", c.APIKey)
	}
	vals.Set("t", "caps")
	u.RawQuery = vals.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("torznab: failed to create caps request: %w", err)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("torznab: caps request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("torznab: failed to read caps response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("torznab: caps returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	return ParseCapabilities(body)
}

// FetchRecent retrieves recently announced releases across the specified categories.
func (c *TorznabClient) FetchRecent(ctx context.Context, categories []int, limit int) ([]Release, error) {
	q := SearchQuery{
		Categories: categories,
		Limit:      limit,
		Mode:       "search",
	}
	return c.Search(ctx, q)
}

// Search performs a Torznab search using the provided query parameters.
func (c *TorznabClient) Search(ctx context.Context, query SearchQuery) ([]Release, error) {
	reqURL := BuildURL(c.BaseURL, c.APIKey, query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("torznab: failed to create search request: %w", err)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("torznab: search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("torznab: failed to read search response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("torznab: indexer returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	sourceName := c.Name
	if sourceName == "" {
		sourceName = c.BaseURL
	}

	return ParseReleases(body, sourceName)
}

// SearchBook executes a book-specific search with automatic fallback to quoted and broad keyword queries.
func (c *TorznabClient) SearchBook(ctx context.Context, author, title string, categories []int) ([]Release, error) {
	// 1. Primary search: structured book search `t=book`
	primaryQuery := SearchQuery{
		Author:     author,
		Title:      title,
		Categories: categories,
		Mode:       "book",
	}

	releases, err := c.Search(ctx, primaryQuery)
	if err == nil && len(releases) > 0 {
		return releases, nil
	}

	// 2. Fallbacks: exact quoted query -> broad keyword query -> title-only query
	fallbacks := FallbackQueries(author, title, categories)
	for _, fb := range fallbacks {
		fbReleases, fbErr := c.Search(ctx, fb)
		if fbErr == nil && len(fbReleases) > 0 {
			return fbReleases, nil
		}
	}

	if err != nil && len(releases) == 0 {
		return nil, err
	}
	return releases, nil
}
