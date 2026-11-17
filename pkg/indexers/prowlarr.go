package indexers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/database"
)

// DBTX is the common interface satisfied by *sql.DB, *sql.Tx, and *database.DB.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ProwlarrClient manages communication with the Prowlarr service API.
type ProwlarrClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// NewProwlarrClient creates a new ProwlarrClient.
func NewProwlarrClient(baseURL, apiKey string, httpClient *http.Client) *ProwlarrClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &ProwlarrClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: httpClient,
	}
}

// ProwlarrSystemStatus represents the status response from `/api/v1/system/status`.
type ProwlarrSystemStatus struct {
	Version      string `json:"version"`
	AppName      string `json:"appName"`
	InstanceName string `json:"instanceName"`
	IsProduction bool   `json:"isProduction"`
}

// ProwlarrIndexer represents an individual indexer definition from `/api/v1/indexer`.
type ProwlarrIndexer struct {
	ID             int                  `json:"id"`
	Name           string               `json:"name"`
	Protocol       string               `json:"protocol"` // "torrent", "usenet"
	Implementation string               `json:"implementation"`
	Enable         bool                 `json:"enable"`
	Enabled        bool                 `json:"enabled"`
	Priority       int                  `json:"priority"`
	IndexerUrls    []string             `json:"indexerUrls"`
	Capabilities   ProwlarrCapabilities `json:"capabilities"`
	Fields         []ProwlarrField      `json:"fields"`
}

// ProwlarrCapabilities represents indexer capabilities in Prowlarr.
type ProwlarrCapabilities struct {
	Categories []ProwlarrCategory `json:"categories"`
}

// ProwlarrCategory represents a category entry in Prowlarr indexer capabilities.
type ProwlarrCategory struct {
	ID            int                `json:"id"`
	Name          string             `json:"name"`
	SubCategories []ProwlarrCategory `json:"subCategories,omitempty"`
}

// ProwlarrField represents a key-value configuration field in Prowlarr.
type ProwlarrField struct {
	Name  string      `json:"name"`
	Value interface{} `json:"value"`
}

// GetSystemStatus fetches the current Prowlarr system status.
func (c *ProwlarrClient) GetSystemStatus(ctx context.Context) (*ProwlarrSystemStatus, error) {
	endpoint := fmt.Sprintf("%s/api/v1/system/status", c.BaseURL)
	reqURL, err := c.buildURLWithKey(endpoint)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("prowlarr: failed to build system status request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prowlarr: system status request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("prowlarr: failed to read system status body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr: system status returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var status ProwlarrSystemStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("prowlarr: failed to parse system status JSON: %w", err)
	}

	return &status, nil
}

// GetIndexers fetches configured indexers from Prowlarr `/api/v1/indexer`.
func (c *ProwlarrClient) GetIndexers(ctx context.Context) ([]ProwlarrIndexer, error) {
	endpoint := fmt.Sprintf("%s/api/v1/indexer", c.BaseURL)
	reqURL, err := c.buildURLWithKey(endpoint)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("prowlarr: failed to build indexer request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prowlarr: indexer request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("prowlarr: failed to read indexers body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr: indexers returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var indexers []ProwlarrIndexer
	if err := json.Unmarshal(body, &indexers); err != nil {
		return nil, fmt.Errorf("prowlarr: failed to parse indexers JSON: %w", err)
	}

	return indexers, nil
}

// SyncIndexers synchronizes indexer configurations from Prowlarr into Callimachus' `indexers` database table.
func (c *ProwlarrClient) SyncIndexers(ctx context.Context, db DBTX) ([]database.Indexer, error) {
	prowlarrIndexers, err := c.GetIndexers(ctx)
	if err != nil {
		return nil, fmt.Errorf("prowlarr sync failed: %w", err)
	}

	var synced []database.Indexer

	const upsertQuery = `
INSERT INTO indexers (id, name, protocol, url, api_key, categories, enabled, priority, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    protocol = excluded.protocol,
    url = excluded.url,
    api_key = excluded.api_key,
    categories = excluded.categories,
    enabled = excluded.enabled,
    priority = excluded.priority,
    updated_at = CURRENT_TIMESTAMP;`

	for _, pIdx := range prowlarrIndexers {
		id := fmt.Sprintf("prowlarr-%d", pIdx.ID)

		protocol := "torznab"
		if strings.EqualFold(pIdx.Protocol, "usenet") || strings.EqualFold(pIdx.Implementation, "newznab") {
			protocol = "newznab"
		}

		// Torznab proxy URL exposed by Prowlarr
		indexerURL := fmt.Sprintf("%s/%d/api", c.BaseURL, pIdx.ID)
		apiKey := c.APIKey

		// Extract relevant book and audiobook categories
		catIDs := extractBookCategories(pIdx.Capabilities.Categories)
		if len(catIDs) == 0 {
			catIDs = DefaultCategories
		}
		catJSON, err := json.Marshal(catIDs)
		if err != nil {
			catJSON = []byte("[7000, 7020, 3030]")
		}

		enabled := pIdx.Enable || pIdx.Enabled
		priority := pIdx.Priority
		if priority <= 0 {
			priority = 1
		}

		_, err = db.ExecContext(ctx, upsertQuery,
			id,
			pIdx.Name,
			protocol,
			indexerURL,
			apiKey,
			string(catJSON),
			enabled,
			priority,
		)
		if err != nil {
			return nil, fmt.Errorf("prowlarr: failed to upsert indexer %q: %w", pIdx.Name, err)
		}

		synced = append(synced, database.Indexer{
			ID:         id,
			Name:       pIdx.Name,
			Protocol:   protocol,
			URL:        indexerURL,
			APIKey:     apiKey,
			Categories: string(catJSON),
			Enabled:    enabled,
			Priority:   priority,
		})
	}

	return synced, nil
}

func (c *ProwlarrClient) buildURLWithKey(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if c.APIKey != "" {
		vals := u.Query()
		vals.Set("apikey", c.APIKey)
		u.RawQuery = vals.Encode()
	}
	return u.String(), nil
}

func (c *ProwlarrClient) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("X-Api-Key", c.APIKey)
	}
}

func extractBookCategories(cats []ProwlarrCategory) []int {
	seen := make(map[int]bool)
	var result []int

	var traverse func(c ProwlarrCategory)
	traverse = func(c ProwlarrCategory) {
		if isRelevantCategory(c.ID) && !seen[c.ID] {
			seen[c.ID] = true
			result = append(result, c.ID)
		}
		for _, sub := range c.SubCategories {
			traverse(sub)
		}
	}

	for _, c := range cats {
		traverse(c)
	}

	return result
}

func isRelevantCategory(id int) bool {
	switch id {
	case CategoryBooks, CategoryBooksMagazines, CategoryBooksEBook, CategoryBooksComics, CategoryBooksTechnical:
		return true
	case CategoryAudio, CategoryAudioAudiobook:
		return true
	default:
		// Also allow standard 7xxx and 3030
		return (id >= 7000 && id < 8000) || id == 3030 || id == 3000
	}
}

// ProwlarrRelease identifies a cached search result. Prowlarr chooses the download client.
type ProwlarrRelease struct {
	GUID      string `json:"guid"`
	IndexerID int    `json:"indexerId"`
	Title     string `json:"title"`
	Protocol  string `json:"protocol"`
	Size      int64  `json:"size"`
}

// Search populates Prowlarr's release cache, which is required for a subsequent grab.
func (c *ProwlarrClient) Search(ctx context.Context, query string, categories []int) ([]ProwlarrRelease, error) {
	values := url.Values{"query": {query}, "type": {"search"}}
	for _, category := range categories {
		values.Add("categories", strconv.Itoa(category))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v1/search?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prowlarr search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr search: HTTP %d", resp.StatusCode)
	}
	releases := []ProwlarrRelease{}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("prowlarr search: %w", err)
	}
	return releases, nil
}

// Grab dispatches a cached release; expired results must be searched again.
func (c *ProwlarrClient) Grab(ctx context.Context, release ProwlarrRelease) error {
	if strings.TrimSpace(release.GUID) == "" || release.IndexerID <= 0 {
		return fmt.Errorf("prowlarr grab requires a search GUID and indexer ID")
	}
	payload, err := json.Marshal(struct {
		GUID      string `json:"guid"`
		IndexerID int    `json:"indexerId"`
	}{release.GUID, release.IndexerID})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/search", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("prowlarr grab: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("prowlarr grab: HTTP %d (search again if the release expired)", resp.StatusCode)
	}
	return nil
}
