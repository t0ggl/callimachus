package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SignatureHeaderName is the HTTP header used for HMAC-SHA256 payload verification.
const SignatureHeaderName = "X-Callimachus-Signature"

// WebhookConfig holds configuration for the generic webhook notifier.
type WebhookConfig struct {
	URLs       []string
	Secret     string
	Headers    map[string]string
	HTTPClient *http.Client
}

// GenericWebhookNotifier dispatches JSON-serialized ImportEvent payloads to one or more endpoints.
type GenericWebhookNotifier struct {
	urls       []string
	secret     string
	headers    map[string]string
	httpClient *http.Client

	mu     sync.RWMutex
	closed bool
}

// NewGenericWebhookNotifier creates a new GenericWebhookNotifier instance.
func NewGenericWebhookNotifier(cfg WebhookConfig) *GenericWebhookNotifier {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	urls := make([]string, 0, len(cfg.URLs))
	for _, u := range cfg.URLs {
		trimmed := strings.TrimSpace(u)
		if trimmed != "" {
			urls = append(urls, trimmed)
		}
	}

	headers := make(map[string]string, len(cfg.Headers))
	for k, v := range cfg.Headers {
		headers[k] = v
	}

	return &GenericWebhookNotifier{
		urls:       urls,
		secret:     cfg.Secret,
		headers:    headers,
		httpClient: client,
	}
}

// Name returns the identifier for this notifier.
func (w *GenericWebhookNotifier) Name() string {
	return "webhook"
}

// ComputeSignature calculates the HMAC-SHA256 signature for a payload with a secret.
// Returns "sha256=<hex>".
func ComputeSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature validates a signature header against a payload and secret.
// Accepts both "sha256=<hex>" and raw hex strings.
func VerifySignature(secret string, payload []byte, signatureHeader string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedHex := hex.EncodeToString(mac.Sum(nil))
	expectedPrefixed := "sha256=" + expectedHex

	sig := strings.TrimSpace(signatureHeader)
	return hmac.Equal([]byte(sig), []byte(expectedHex)) || hmac.Equal([]byte(sig), []byte(expectedPrefixed))
}

// NotifyImport serializes event to JSON and dispatches it to all configured webhook URLs.
func (w *GenericWebhookNotifier) NotifyImport(ctx context.Context, event ImportEvent) error {
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return ErrNotifierClosed
	}
	urls := make([]string, len(w.urls))
	copy(urls, w.urls)
	w.mu.RUnlock()

	if len(urls) == 0 {
		return nil
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal import event payload: %w", err)
	}

	var sig string
	if w.secret != "" {
		sig = ComputeSignature(w.secret, payload)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for _, targetURL := range urls {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			if err := w.send(ctx, u, payload, sig); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", u, err))
				mu.Unlock()
			}
		}(targetURL)
	}
	wg.Wait()

	return errors.Join(errs...)
}

func (w *GenericWebhookNotifier) send(ctx context.Context, targetURL string, payload []byte, signature string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Callimachus-Notifier/1.0")

	if signature != "" {
		req.Header.Set(SignatureHeaderName, signature)
	}

	for k, v := range w.headers {
		req.Header.Set(k, v)
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// Close marks the webhook notifier as closed.
func (w *GenericWebhookNotifier) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return nil
}
