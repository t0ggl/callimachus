package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultCalibreWebEndpoint is the standard Calibre-Web database reconnect/reload endpoint.
const DefaultCalibreWebEndpoint = "/reconnect"

// CalibreWebConfig holds configuration for the Calibre-Web library reload trigger.
type CalibreWebConfig struct {
	BaseURL          string
	Username         string
	Password         string
	APIKey           string
	Endpoint         string // Optional override, defaults to "/reconnect"
	HTTPMethod       string // Optional, defaults to POST
	DebounceDuration time.Duration
	HTTPClient       *http.Client
}

// CalibreWebNotifier triggers database reload or custom scan endpoints on Calibre-Web upon media import.
type CalibreWebNotifier struct {
	baseURL          string
	username         string
	password         string
	apiKey           string
	endpoint         string
	httpMethod       string
	debounceDuration time.Duration
	httpClient       *http.Client

	mu     sync.Mutex
	timer  *time.Timer
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewCalibreWebNotifier creates a new CalibreWebNotifier instance.
func NewCalibreWebNotifier(cfg CalibreWebConfig) *CalibreWebNotifier {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:8083"
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = DefaultCalibreWebEndpoint
	}
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.HTTPMethod))
	if method == "" {
		method = http.MethodPost
	}

	debounceDuration := cfg.DebounceDuration
	if debounceDuration == 0 {
		debounceDuration = DefaultDebounceDuration
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &CalibreWebNotifier{
		baseURL:          baseURL,
		username:         cfg.Username,
		password:         cfg.Password,
		apiKey:           cfg.APIKey,
		endpoint:         endpoint,
		httpMethod:       method,
		debounceDuration: debounceDuration,
		httpClient:       client,
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Name returns the identifier for this notifier.
func (c *CalibreWebNotifier) Name() string {
	return "calibre-web"
}

// NotifyImport records an import event and debounces the subsequent reload trigger.
func (c *CalibreWebNotifier) NotifyImport(ctx context.Context, event ImportEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrNotifierClosed
	}

	// If debounce is disabled (< 0), trigger reload immediately.
	if c.debounceDuration < 0 {
		return c.triggerReloadLocked(ctx)
	}

	// Reset or start debounce timer.
	if c.timer != nil {
		c.timer.Stop()
	}

	c.timer = time.AfterFunc(c.debounceDuration, c.onDebounceTimer)
	return nil
}

func (c *CalibreWebNotifier) onDebounceTimer() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.timer = nil
	c.wg.Add(1)
	ctx := c.ctx
	c.mu.Unlock()

	defer c.wg.Done()
	_ = c.triggerReload(ctx)
}

// TriggerScan dispatches the reload/scan request to Calibre-Web immediately.
func (c *CalibreWebNotifier) TriggerScan(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrNotifierClosed
	}
	c.mu.Unlock()

	return c.triggerReload(ctx)
}

// Flush immediately executes any pending debounced reload trigger.
func (c *CalibreWebNotifier) Flush(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrNotifierClosed
	}

	hasPending := c.timer != nil
	if hasPending {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()

	if hasPending {
		return c.triggerReload(ctx)
	}
	return nil
}

func (c *CalibreWebNotifier) triggerReloadLocked(ctx context.Context) error {
	return c.triggerReload(ctx)
}

func (c *CalibreWebNotifier) triggerReload(ctx context.Context) error {
	targetURL := c.baseURL + c.endpoint
	req, err := http.NewRequestWithContext(ctx, c.httpMethod, targetURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create calibre-web reload request: %w", err)
	}

	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
		if c.username == "" && c.password == "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calibre-web reload request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("calibre-web reload returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// Close cancels pending timers, cancels in-flight requests, and waits for background workers to finish.
func (c *CalibreWebNotifier) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true

	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.cancel()
	c.mu.Unlock()

	c.wg.Wait()
	return nil
}
