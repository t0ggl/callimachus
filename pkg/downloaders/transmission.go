package downloaders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TransmissionConfig provides configuration parameters for TransmissionClient.
type TransmissionConfig struct {
	BaseURL    string
	Username   string
	Password   string
	HTTPClient *http.Client
}

// TransmissionClient implements the DownloadClient interface for Transmission JSON-RPC.
type TransmissionClient struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
	mu         sync.RWMutex
	sessionID  string
}

// NewTransmissionClient creates a new Transmission JSON-RPC client driver.
func NewTransmissionClient(cfg TransmissionConfig) *TransmissionClient {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:9091"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &TransmissionClient{
		baseURL:    baseURL,
		username:   cfg.Username,
		password:   cfg.Password,
		httpClient: httpClient,
	}
}

// Type returns the client identifier "transmission".
func (c *TransmissionClient) Type() string {
	return TypeTransmission
}

// AddTorrent enqueues a torrent via .torrent URL or magnet URI, adding category as label.
func (c *TransmissionClient) AddTorrent(ctx context.Context, urlOrMagnet string, category string) (string, error) {
	addArgs := trTorrentAddArgs{
		Filename: urlOrMagnet,
	}
	if category != "" {
		addArgs.Labels = []string{category}
	}

	rawResp, err := c.callRPC(ctx, "torrent-add", addArgs)
	if err != nil {
		return "", fmt.Errorf("transmission torrent-add failed: %w", err)
	}

	var addResp trTorrentAddResponse
	if err := json.Unmarshal(rawResp, &addResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal torrent-add response: %w", err)
	}

	var info *trTorrentInfo
	if addResp.TorrentAdded != nil {
		info = addResp.TorrentAdded
	} else if addResp.TorrentDuplicate != nil {
		info = addResp.TorrentDuplicate
	}

	if info == nil || info.HashString == "" {
		return "", fmt.Errorf("transmission torrent-add did not return valid torrent info")
	}

	return info.HashString, nil
}

// AddNZB returns ErrUnsupportedOperation as Transmission is a BitTorrent client.
func (c *TransmissionClient) AddNZB(ctx context.Context, nzbURL string, category string) (string, error) {
	return "", ErrUnsupportedOperation
}

// GetTaskStatus retrieves the normalized status of a task by its hash or id.
func (c *TransmissionClient) GetTaskStatus(ctx context.Context, taskID string) (*TaskStatus, error) {
	var idArg interface{} = taskID
	if num, err := strconv.ParseInt(taskID, 10, 64); err == nil {
		idArg = num
	}

	getArgs := trTorrentGetArgs{
		Fields: []string{
			"id", "name", "hashString", "status", "percentDone",
			"rateDownload", "totalSize", "downloadedEver", "eta",
			"downloadDir", "labels",
		},
		IDs: []interface{}{idArg},
	}

	rawResp, err := c.callRPC(ctx, "torrent-get", getArgs)
	if err != nil {
		return nil, fmt.Errorf("transmission torrent-get failed: %w", err)
	}

	var getResp trTorrentGetResponse
	if err := json.Unmarshal(rawResp, &getResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal torrent-get response: %w", err)
	}

	if len(getResp.Torrents) == 0 {
		return nil, ErrTaskNotFound
	}

	t := getResp.Torrents[0]
	progressPct := t.PercentDone * 100.0
	if progressPct > 100.0 {
		progressPct = 100.0
	}

	// Status: 0=stopped, 1=check_wait, 2=check, 3=download_wait, 4=download, 5=seed_wait, 6=seed
	isCompleted := t.PercentDone >= 1.0 || t.Status == 5 || t.Status == 6 || (t.Status == 0 && t.PercentDone >= 1.0)

	category := ""
	if len(t.Labels) > 0 {
		category = t.Labels[0]
	}

	contentPath := filepath.Join(t.DownloadDir, t.Name)

	return &TaskStatus{
		TaskID:          t.HashString,
		Name:            t.Name,
		Category:        category,
		Progress:        progressPct,
		DownloadRate:    t.RateDownload,
		TotalBytes:      t.TotalSize,
		DownloadedBytes: t.DownloadedEver,
		EtaSeconds:      t.Eta,
		State:           trStatusToString(t.Status),
		SavePath:        t.DownloadDir,
		ContentPath:     contentPath,
		IsCompleted:     isCompleted,
	}, nil
}

// RemoveTask removes a torrent task from Transmission, optionally deleting downloaded data.
func (c *TransmissionClient) RemoveTask(ctx context.Context, taskID string, deleteFiles bool) error {
	var idArg interface{} = taskID
	if num, err := strconv.ParseInt(taskID, 10, 64); err == nil {
		idArg = num
	}

	removeArgs := trTorrentRemoveArgs{
		IDs:             []interface{}{idArg},
		DeleteLocalData: deleteFiles,
	}

	_, err := c.callRPC(ctx, "torrent-remove", removeArgs)
	if err != nil {
		return fmt.Errorf("transmission torrent-remove failed: %w", err)
	}
	return nil
}

func trStatusToString(status int) string {
	switch status {
	case 0:
		return "stopped"
	case 1:
		return "check_wait"
	case 2:
		return "checking"
	case 3:
		return "download_wait"
	case 4:
		return "downloading"
	case 5:
		return "seed_wait"
	case 6:
		return "seeding"
	default:
		return "unknown"
	}
}

type trRPCRequest struct {
	Method    string      `json:"method"`
	Arguments interface{} `json:"arguments,omitempty"`
	Tag       int         `json:"tag,omitempty"`
}

type trRPCResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
	Tag       int             `json:"tag"`
}

type trTorrentAddArgs struct {
	Filename string   `json:"filename"`
	Labels   []string `json:"labels,omitempty"`
}

type trTorrentAddResponse struct {
	TorrentAdded     *trTorrentInfo `json:"torrent-added"`
	TorrentDuplicate *trTorrentInfo `json:"torrent-duplicate"`
}

type trTorrentInfo struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	HashString string `json:"hashString"`
}

type trTorrentGetArgs struct {
	Fields []string      `json:"fields"`
	IDs    []interface{} `json:"ids,omitempty"`
}

type trTorrentGetResponse struct {
	Torrents []trTorrentItem `json:"torrents"`
}

type trTorrentItem struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	HashString     string   `json:"hashString"`
	Status         int      `json:"status"`
	PercentDone    float64  `json:"percentDone"`
	RateDownload   int64    `json:"rateDownload"`
	TotalSize      int64    `json:"totalSize"`
	DownloadedEver int64    `json:"downloadedEver"`
	Eta            int64    `json:"eta"`
	DownloadDir    string   `json:"downloadDir"`
	Labels         []string `json:"labels"`
}

type trTorrentRemoveArgs struct {
	IDs             []interface{} `json:"ids"`
	DeleteLocalData bool          `json:"delete-local-data"`
}

func (c *TransmissionClient) callRPC(ctx context.Context, method string, args interface{}) (json.RawMessage, error) {
	reqBody := trRPCRequest{
		Method:    method,
		Arguments: args,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to encode RPC request: %w", err)
	}

	rpcURL := c.baseURL + "/transmission/rpc"

	c.mu.RLock()
	sessionID := c.sessionID
	c.mu.RUnlock()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		httpReq.Header.Set("X-Transmission-Session-Id", sessionID)
	}
	if c.username != "" || c.password != "" {
		httpReq.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	// Handle 409 Conflict CSRF handshake
	if resp.StatusCode == http.StatusConflict {
		newSessionID := resp.Header.Get("X-Transmission-Session-Id")
		resp.Body.Close()
		if newSessionID == "" {
			return nil, fmt.Errorf("transmission returned 409 Conflict without X-Transmission-Session-Id header")
		}

		c.mu.Lock()
		c.sessionID = newSessionID
		c.mu.Unlock()

		retryReq, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		retryReq.Header.Set("Content-Type", "application/json")
		retryReq.Header.Set("X-Transmission-Session-Id", newSessionID)
		if c.username != "" || c.password != "" {
			retryReq.SetBasicAuth(c.username, c.password)
		}

		resp, err = c.httpClient.Do(retryReq)
		if err != nil {
			return nil, fmt.Errorf("transmission retry with session id failed: %w", err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("transmission RPC HTTP error %d: %s", resp.StatusCode, string(respBytes))
	}

	var rpcResp trRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("failed to decode transmission RPC response: %w", err)
	}

	if !strings.EqualFold(rpcResp.Result, "success") {
		return nil, fmt.Errorf("transmission RPC call %s returned result: %s", method, rpcResp.Result)
	}

	return rpcResp.Arguments, nil
}
