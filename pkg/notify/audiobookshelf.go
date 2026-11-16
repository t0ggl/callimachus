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

// Default debounce duration for library scan triggers.
const DefaultDebounceDuration = 30 * time.Second

// AudiobookshelfConfig holds configuration for the Audiobookshelf library scan trigger.
type AudiobookshelfConfig struct {
	BaseURL          string
	Token            string
	LibraryID        string
	DebounceDuration time.Duration
	HTTPClient       *http.Client
}

// AudiobookshelfNotifier triggers library scans on an Audiobookshelf instance upon media import.
type AudiobookshelfNotifier struct {
	baseURL          string
	token            string
	libraryID        string
	debounceDuration time.Duration
	httpClient       *http.Client

	mu     sync.Mutex
	timer  *time.Timer
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewAudiobookshelfNotifier creates a new AudiobookshelfNotifier instance.
func NewAudiobookshelfNotifier(cfg AudiobookshelfConfig) *AudiobookshelfNotifier {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:13378"
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

	return &AudiobookshelfNotifier{
		baseURL:          baseURL,
		token:            cfg.Token,
		libraryID:        cfg.LibraryID,
		debounceDuration: debounceDuration,
		httpClient:       client,
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Name returns the identifier for this notifier.
func (a *AudiobookshelfNotifier) Name() string {
	return "audiobookshelf"
}

// NotifyImport records an import event and debounces the subsequent scan trigger.
func (a *AudiobookshelfNotifier) NotifyImport(ctx context.Context, event ImportEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return ErrNotifierClosed
	}

	// If debounce is disabled (< 0), trigger scan immediately.
	if a.debounceDuration < 0 {
		return a.triggerScanLocked(ctx)
	}

	// Reset or start debounce timer.
	if a.timer != nil {
		a.timer.Stop()
	}

	a.timer = time.AfterFunc(a.debounceDuration, a.onDebounceTimer)
	return nil
}

func (a *AudiobookshelfNotifier) onDebounceTimer() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.timer = nil
	a.wg.Add(1)
	ctx := a.ctx
	a.mu.Unlock()

	defer a.wg.Done()
	_ = a.triggerScan(ctx)
}

// TriggerScan dispatches the scan request to the Audiobookshelf API immediately.
func (a *AudiobookshelfNotifier) TriggerScan(ctx context.Context) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrNotifierClosed
	}
	a.mu.Unlock()

	return a.triggerScan(ctx)
}

// Flush immediately executes any pending debounced scan trigger.
func (a *AudiobookshelfNotifier) Flush(ctx context.Context) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrNotifierClosed
	}

	hasPending := a.timer != nil
	if hasPending {
		a.timer.Stop()
		a.timer = nil
	}
	a.mu.Unlock()

	if hasPending {
		return a.triggerScan(ctx)
	}
	return nil
}

func (a *AudiobookshelfNotifier) triggerScanLocked(ctx context.Context) error {
	return a.triggerScan(ctx)
}

func (a *AudiobookshelfNotifier) triggerScan(ctx context.Context) error {
	scanURL := fmt.Sprintf("%s/api/libraries/%s/scan", a.baseURL, a.libraryID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, scanURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create audiobookshelf scan request: %w", err)
	}

	token := strings.TrimSpace(a.token)
	if token != "" {
		if !strings.HasPrefix(strings.ToLower(token), "bearer ") {
			req.Header.Set("Authorization", "Bearer "+token)
		} else {
			req.Header.Set("Authorization", token)
		}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("audiobookshelf scan request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("audiobookshelf scan returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// Close cancels pending timers, cancels in-flight scan requests, and waits for background workers to finish.
func (a *AudiobookshelfNotifier) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true

	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	a.cancel()
	a.mu.Unlock()

	a.wg.Wait()
	return nil
}
