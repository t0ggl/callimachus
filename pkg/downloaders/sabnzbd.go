package downloaders

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SABnzbdConfig provides configuration parameters for SABnzbdClient.
type SABnzbdConfig struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// SABnzbdClient implements the DownloadClient interface for SABnzbd REST API.
type SABnzbdClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewSABnzbdClient creates a new SABnzbd REST client driver.
func NewSABnzbdClient(cfg SABnzbdConfig) *SABnzbdClient {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &SABnzbdClient{
		baseURL:    baseURL,
		apiKey:     cfg.APIKey,
		httpClient: httpClient,
	}
}

// Type returns the client identifier "sabnzbd".
func (c *SABnzbdClient) Type() string {
	return TypeSABnzbd
}

// AddTorrent returns ErrUnsupportedOperation as SABnzbd is a Usenet client.
func (c *SABnzbdClient) AddTorrent(ctx context.Context, urlOrMagnet string, category string) (string, error) {
	return "", ErrUnsupportedOperation
}

type sabAddURLResponse struct {
	Status bool     `json:"status"`
	NzoIDs []string `json:"nzo_ids"`
	Error  string   `json:"error"`
}

// AddNZB enqueues an NZB URL with a category into SABnzbd.
func (c *SABnzbdClient) AddNZB(ctx context.Context, nzbURL string, category string) (string, error) {
	params := url.Values{}
	params.Set("mode", "addurl")
	params.Set("name", nzbURL)
	if category != "" {
		params.Set("cat", category)
	}

	bodyBytes, err := c.apiGet(ctx, params)
	if err != nil {
		return "", fmt.Errorf("sabnzbd addurl failed: %w", err)
	}

	var addResp sabAddURLResponse
	if err := json.Unmarshal(bodyBytes, &addResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal sabnzbd addurl response: %w", err)
	}

	if !addResp.Status || len(addResp.NzoIDs) == 0 {
		errMsg := addResp.Error
		if errMsg == "" {
			errMsg = "addurl returned status false or empty nzo_ids"
		}
		return "", fmt.Errorf("sabnzbd addurl failed: %s", errMsg)
	}

	return addResp.NzoIDs[0], nil
}

type sabQueueSlot struct {
	NzoID      string `json:"nzo_id"`
	Filename   string `json:"filename"`
	Cat        string `json:"cat"`
	Percentage string `json:"percentage"`
	MB         string `json:"mb"`
	MBLeft     string `json:"mbleft"`
	Timeleft   string `json:"timeleft"`
	Status     string `json:"status"`
	Storage    string `json:"storage"`
}

type sabQueueResponse struct {
	Queue struct {
		Status    string         `json:"status"`
		Speed     string         `json:"speed"`
		Kbpersec  string         `json:"kbpersec"`
		Slots     []sabQueueSlot `json:"slots"`
		Timeleft  string         `json:"timeleft"`
		SpeedVal  string         `json:"speedval"`
	} `json:"queue"`
}

type sabHistorySlot struct {
	NzoID       string `json:"nzo_id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Status      string `json:"status"`
	Bytes       int64  `json:"bytes"`
	Storage     string `json:"storage"`
	Path        string `json:"path"`
	FailMessage string `json:"fail_message"`
}

type sabHistoryResponse struct {
	History struct {
		Slots []sabHistorySlot `json:"slots"`
	} `json:"history"`
}

// GetTaskStatus queries SABnzbd queue first, then history if completed.
func (c *SABnzbdClient) GetTaskStatus(ctx context.Context, taskID string) (*TaskStatus, error) {
	// 1. Check active queue
	queueParams := url.Values{}
	queueParams.Set("mode", "queue")
	queueParams.Set("search", taskID)

	qBytes, err := c.apiGet(ctx, queueParams)
	if err == nil {
		var qResp sabQueueResponse
		if err := json.Unmarshal(qBytes, &qResp); err == nil {
			for _, slot := range qResp.Queue.Slots {
				if slot.NzoID == taskID {
					pct, _ := strconv.ParseFloat(slot.Percentage, 64)
					totalMB, _ := strconv.ParseFloat(slot.MB, 64)
					leftMB, _ := strconv.ParseFloat(slot.MBLeft, 64)
					kbSec, _ := strconv.ParseFloat(qResp.Queue.Kbpersec, 64)

					totalBytes := int64(totalMB * 1024 * 1024)
					downloadedBytes := int64((totalMB - leftMB) * 1024 * 1024)
					if downloadedBytes < 0 {
						downloadedBytes = 0
					}

					return &TaskStatus{
						TaskID:          slot.NzoID,
						Name:            slot.Filename,
						Category:        slot.Cat,
						Progress:        pct,
						DownloadRate:    int64(kbSec * 1024),
						TotalBytes:      totalBytes,
						DownloadedBytes: downloadedBytes,
						EtaSeconds:      parseTimeLeft(slot.Timeleft),
						State:           slot.Status,
						SavePath:        slot.Storage,
						ContentPath:     slot.Storage,
						IsCompleted:     false,
					}, nil
				}
			}
		}
	}

	// 2. Check history (when completed, SABnzbd moves tasks out of queue into history)
	histParams := url.Values{}
	histParams.Set("mode", "history")
	histParams.Set("search", taskID)

	hBytes, err := c.apiGet(ctx, histParams)
	if err == nil {
		var hResp sabHistoryResponse
		if err := json.Unmarshal(hBytes, &hResp); err == nil {
			for _, slot := range hResp.History.Slots {
				if slot.NzoID == taskID {
					isCompleted := strings.EqualFold(slot.Status, "Completed")
					storage := slot.Storage
					if storage == "" {
						storage = slot.Path
					}

					return &TaskStatus{
						TaskID:          slot.NzoID,
						Name:            slot.Name,
						Category:        slot.Category,
						Progress:        100.0,
						DownloadRate:    0,
						TotalBytes:      slot.Bytes,
						DownloadedBytes: slot.Bytes,
						EtaSeconds:      0,
						State:           slot.Status,
						SavePath:        storage,
						ContentPath:     storage,
						IsCompleted:     isCompleted,
					}, nil
				}
			}
		}
	}

	return nil, ErrTaskNotFound
}

// RemoveTask removes a task from SABnzbd queue and history.
func (c *SABnzbdClient) RemoveTask(ctx context.Context, taskID string, deleteFiles bool) error {
	delVal := "0"
	if deleteFiles {
		delVal = "1"
	}

	// Attempt deletion from queue
	qParams := url.Values{}
	qParams.Set("mode", "queue")
	qParams.Set("name", "delete")
	qParams.Set("value", taskID)
	qParams.Set("del_files", delVal)
	_, _ = c.apiGet(ctx, qParams)

	// Attempt deletion from history
	hParams := url.Values{}
	hParams.Set("mode", "history")
	hParams.Set("name", "delete")
	hParams.Set("value", taskID)
	hParams.Set("del_files", delVal)
	_, _ = c.apiGet(ctx, hParams)

	return nil
}

func (c *SABnzbdClient) apiGet(ctx context.Context, params url.Values) ([]byte, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("output", "json")
	if c.apiKey != "" {
		params.Set("apikey", c.apiKey)
	}

	fullURL := fmt.Sprintf("%s/api?%s", c.baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sabnzbd HTTP error %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

func parseTimeLeft(t string) int64 {
	clean := strings.TrimSpace(t)
	if clean == "" || clean == "0" {
		return 0
	}
	parts := strings.Split(clean, ":")
	if len(parts) == 3 {
		h, _ := strconv.ParseInt(parts[0], 10, 64)
		m, _ := strconv.ParseInt(parts[1], 10, 64)
		s, _ := strconv.ParseInt(parts[2], 10, 64)
		return h*3600 + m*60 + s
	} else if len(parts) == 2 {
		m, _ := strconv.ParseInt(parts[0], 10, 64)
		s, _ := strconv.ParseInt(parts[1], 10, 64)
		return m*60 + s
	} else if len(parts) == 1 {
		s, _ := strconv.ParseInt(parts[0], 10, 64)
		return s
	}
	return 0
}
