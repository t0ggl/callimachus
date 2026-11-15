package downloaders

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	reMagnetBTIH = regexp.MustCompile(`(?i)xt=urn:btih:([a-z0-9]{32,40})`)
)

// QBittorrentConfig provides configuration parameters for QBittorrentClient.
type QBittorrentConfig struct {
	BaseURL    string
	Username   string
	Password   string
	HTTPClient *http.Client
}

// QBittorrentClient implements the DownloadClient interface for qBittorrent Web API v2.
type QBittorrentClient struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
	mu         sync.RWMutex
	sidCookie  string
}

// NewQBittorrentClient creates a new qBittorrent Web API client driver.
func NewQBittorrentClient(cfg QBittorrentConfig) *QBittorrentClient {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &QBittorrentClient{
		baseURL:    baseURL,
		username:   cfg.Username,
		password:   cfg.Password,
		httpClient: httpClient,
	}
}

// Type returns the client identifier "qbittorrent".
func (c *QBittorrentClient) Type() string {
	return TypeQBittorrent
}

// AddTorrent adds a torrent by magnet URI or .torrent URL and assigns the category.
func (c *QBittorrentClient) AddTorrent(ctx context.Context, urlOrMagnet string, category string) (string, error) {
	parsedHash := parseInfoHash(urlOrMagnet)

	form := url.Values{}
	form.Set("urls", urlOrMagnet)
	if category != "" {
		form.Set("category", category)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v2/torrents/add", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", true)
	if err != nil {
		return "", fmt.Errorf("qbittorrent add torrent request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyStr := strings.TrimSpace(string(bodyBytes))

	if resp.StatusCode != http.StatusOK || strings.EqualFold(bodyStr, "Fails.") {
		return "", fmt.Errorf("qbittorrent failed to add torrent (HTTP %d): %s", resp.StatusCode, bodyStr)
	}

	if parsedHash != "" {
		return parsedHash, nil
	}

	// If no hash was extractable from the URL, query latest torrent in this category
	torrents, err := c.getTorrentsInfo(ctx, "")
	if err == nil && len(torrents) > 0 {
		return torrents[0].Hash, nil
	}

	return "", nil
}

// AddNZB returns ErrUnsupportedOperation as qBittorrent is a BitTorrent client.
func (c *QBittorrentClient) AddNZB(ctx context.Context, nzbURL string, category string) (string, error) {
	return "", ErrUnsupportedOperation
}

// GetTaskStatus queries qBittorrent for torrent state and returns normalized TaskStatus.
func (c *QBittorrentClient) GetTaskStatus(ctx context.Context, taskID string) (*TaskStatus, error) {
	torrents, err := c.getTorrentsInfo(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if len(torrents) == 0 {
		return nil, ErrTaskNotFound
	}

	t := torrents[0]
	progressPct := t.Progress * 100.0
	if progressPct > 100.0 {
		progressPct = 100.0
	}

	isCompleted := t.Progress >= 1.0 || isQbtCompletedState(t.State)

	contentPath := t.ContentPath
	if contentPath == "" {
		contentPath = t.SavePath
	}

	return &TaskStatus{
		TaskID:          t.Hash,
		Name:            t.Name,
		Category:        t.Category,
		Progress:        progressPct,
		DownloadRate:    t.DlSpeed,
		TotalBytes:      t.TotalSize,
		DownloadedBytes: t.Completed,
		EtaSeconds:      t.Eta,
		State:           t.State,
		SavePath:        t.SavePath,
		ContentPath:     contentPath,
		IsCompleted:     isCompleted,
	}, nil
}

// RemoveTask removes a torrent task from qBittorrent, optionally deleting payload files.
func (c *QBittorrentClient) RemoveTask(ctx context.Context, taskID string, deleteFiles bool) error {
	form := url.Values{}
	form.Set("hashes", taskID)
	form.Set("deleteFiles", fmt.Sprintf("%t", deleteFiles))

	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v2/torrents/delete", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", true)
	if err != nil {
		return fmt.Errorf("qbittorrent delete request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qbittorrent failed to delete torrent (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}
	return nil
}

type qbtTorrentJSON struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	Progress    float64 `json:"progress"`
	DlSpeed     int64   `json:"dlspeed"`
	TotalSize   int64   `json:"total_size"`
	Completed   int64   `json:"completed"`
	Eta         int64   `json:"eta"`
	State       string  `json:"state"`
	SavePath    string  `json:"save_path"`
	ContentPath string  `json:"content_path"`
}

func (c *QBittorrentClient) getTorrentsInfo(ctx context.Context, hashFilter string) ([]qbtTorrentJSON, error) {
	relPath := "/api/v2/torrents/info"
	if hashFilter != "" {
		relPath = fmt.Sprintf("/api/v2/torrents/info?hashes=%s", url.QueryEscape(hashFilter))
	}

	resp, err := c.doRequest(ctx, http.MethodGet, relPath, nil, "", true)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent torrents/info failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qbittorrent torrents/info error (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var torrents []qbtTorrentJSON
	if err := json.NewDecoder(resp.Body).Decode(&torrents); err != nil {
		return nil, fmt.Errorf("qbittorrent failed to decode torrents/info JSON: %w", err)
	}
	return torrents, nil
}

func (c *QBittorrentClient) login(ctx context.Context) error {
	form := url.Values{}
	form.Set("username", c.username)
	form.Set("password", c.password)

	endpoint := c.baseURL + "/api/v2/auth/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qbittorrent login request error: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyStr := strings.TrimSpace(string(bodyBytes))

	if resp.StatusCode != http.StatusOK || strings.EqualFold(bodyStr, "Fails.") {
		return fmt.Errorf("%w: status %d: %s", ErrAuthenticationFailed, resp.StatusCode, bodyStr)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Extract SID cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "SID" {
			c.sidCookie = cookie.Value
			break
		}
	}
	// Fallback to checking raw header
	if c.sidCookie == "" {
		for _, raw := range resp.Header["Set-Cookie"] {
			if strings.HasPrefix(raw, "SID=") {
				parts := strings.Split(raw, ";")
				c.sidCookie = strings.TrimPrefix(parts[0], "SID=")
				break
			}
		}
	}

	return nil
}

func (c *QBittorrentClient) doRequest(ctx context.Context, method, relPath string, body io.Reader, contentType string, retryOnAuth bool) (*http.Response, error) {
	// First ensure login if SID is empty and credentials exist
	c.mu.RLock()
	sid := c.sidCookie
	c.mu.RUnlock()

	if sid == "" && (c.username != "" || c.password != "") {
		if err := c.login(ctx); err != nil {
			return nil, err
		}
		c.mu.RLock()
		sid = c.sidCookie
		c.mu.RUnlock()
	}

	endpoint := c.baseURL + relPath
	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = io.ReadAll(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if sid != "" {
		req.Header.Set("Cookie", fmt.Sprintf("SID=%s", sid))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Check if session expired or unauthorized (403 Forbidden or 401 Unauthorized)
	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) && retryOnAuth {
		resp.Body.Close()
		if loginErr := c.login(ctx); loginErr != nil {
			return nil, fmt.Errorf("re-authentication failed: %w", loginErr)
		}
		c.mu.RLock()
		newSid := c.sidCookie
		c.mu.RUnlock()

		retryReq, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(string(bodyBytes)))
		if err != nil {
			return nil, err
		}
		if contentType != "" {
			retryReq.Header.Set("Content-Type", contentType)
		}
		if newSid != "" {
			retryReq.Header.Set("Cookie", fmt.Sprintf("SID=%s", newSid))
		}
		return c.httpClient.Do(retryReq)
	}

	return resp, nil
}

func isQbtCompletedState(state string) bool {
	switch strings.ToLower(state) {
	case "uploading", "pausedup", "queuedup", "stalledup", "checkingup", "forcedup", "complete":
		return true
	default:
		return false
	}
}

func parseInfoHash(urlOrMagnet string) string {
	matches := reMagnetBTIH.FindStringSubmatch(urlOrMagnet)
	if len(matches) > 1 {
		raw := matches[1]
		if len(raw) == 40 {
			return strings.ToLower(raw)
		}
		if len(raw) == 32 {
			decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(raw))
			if err == nil {
				return strings.ToLower(hex.EncodeToString(decoded))
			}
		}
	}
	return ""
}
