package notify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/t0ggl/callimachus/pkg/notify"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestGenericWebhook_JSONPayloadAndHMACSignature(t *testing.T) {
	t.Parallel()

	targetURL := "http://webhook.internal:9000/events"
	secret := "test-hmac-token"

	mt := testutil.NewMockTransportForTest(t)
	var capturedPayload []byte
	var capturedSig string
	var capturedCustomHeader string
	var mu sync.Mutex

	mt.RegisterHandler(targetURL, func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()

		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
		}
		capturedPayload = body
		capturedSig = req.Header.Get(notify.SignatureHeaderName)
		capturedCustomHeader = req.Header.Get("X-Custom-Env")

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	notifier := notify.NewGenericWebhookNotifier(notify.WebhookConfig{
		URLs:   []string{targetURL},
		Secret: secret,
		Headers: map[string]string{
			"X-Custom-Env": "production",
		},
		HTTPClient: mt.Client(),
	})
	defer notifier.Close()

	if notifier.Name() != "webhook" {
		t.Errorf("expected name webhook, got %s", notifier.Name())
	}

	event := dummyAudioImportEvent(t)
	err := notifier.NotifyImport(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected NotifyImport error: %v", err)
	}

	mu.Lock()
	payload := capturedPayload
	sig := capturedSig
	customHeader := capturedCustomHeader
	mu.Unlock()

	if len(payload) == 0 {
		t.Fatal("expected non-empty payload")
	}

	var parsedEvent notify.ImportEvent
	if err := json.Unmarshal(payload, &parsedEvent); err != nil {
		t.Fatalf("failed to unmarshal JSON payload: %v", err)
	}

	if parsedEvent.Title != event.Title || parsedEvent.Author != event.Author || parsedEvent.Checksum != event.Checksum {
		t.Errorf("event fields mismatched: got %+v, want %+v", parsedEvent, event)
	}
	if parsedEvent.LibraryType != event.LibraryType || parsedEvent.Format != event.Format {
		t.Errorf("event type/format mismatched: got %s/%s", parsedEvent.LibraryType, parsedEvent.Format)
	}

	if customHeader != "production" {
		t.Errorf("expected X-Custom-Env: production, got: %s", customHeader)
	}

	if !strings.HasPrefix(sig, "sha256=") {
		t.Errorf("expected sha256= prefix on signature, got: %s", sig)
	}

	if !notify.VerifySignature(secret, payload, sig) {
		t.Errorf("VerifySignature failed for payload and signature %s", sig)
	}

	// Verify tampering detection
	if notify.VerifySignature(secret, []byte("tampered payload"), sig) {
		t.Error("VerifySignature unexpectedly passed on tampered payload")
	}
	if notify.VerifySignature("wrong-secret", payload, sig) {
		t.Error("VerifySignature unexpectedly passed with wrong secret")
	}
}

func TestGenericWebhook_DispatchMultipleURLs(t *testing.T) {
	t.Parallel()

	url1 := "http://webhook1.internal/hook"
	url2 := "http://webhook2.internal/hook"
	url3 := "http://webhook3.internal/hook"

	mt := testutil.NewMockTransportForTest(t)
	var count1, count2, count3 atomic.Int32

	mt.RegisterHandler(url1, func(req *http.Request) (*http.Response, error) {
		count1.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
	})
	mt.RegisterHandler(url2, func(req *http.Request) (*http.Response, error) {
		count2.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
	})
	mt.RegisterHandler(url3, func(req *http.Request) (*http.Response, error) {
		count3.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
	})

	notifier := notify.NewGenericWebhookNotifier(notify.WebhookConfig{
		URLs:       []string{url1, url2, url3},
		HTTPClient: mt.Client(),
	})
	defer notifier.Close()

	err := notifier.NotifyImport(context.Background(), dummyAudioImportEvent(t))
	if err != nil {
		t.Fatalf("NotifyImport failed: %v", err)
	}

	if count1.Load() != 1 || count2.Load() != 1 || count3.Load() != 1 {
		t.Errorf("expected each URL called once: counts=(%d, %d, %d)", count1.Load(), count2.Load(), count3.Load())
	}
}

func TestGenericWebhook_FailureAccumulation(t *testing.T) {
	t.Parallel()

	urlSuccess := "http://webhook.internal/ok"
	urlFail := "http://webhook.internal/fail"

	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterHandler(urlSuccess, func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
	})
	mt.RegisterHandler(urlFail, func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: http.NoBody, Request: req}, nil
	})

	notifier := notify.NewGenericWebhookNotifier(notify.WebhookConfig{
		URLs:       []string{urlSuccess, urlFail},
		HTTPClient: mt.Client(),
	})
	defer notifier.Close()

	err := notifier.NotifyImport(context.Background(), dummyAudioImportEvent(t))
	if err == nil {
		t.Fatal("expected error due to failing URL, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to include status 500, got: %v", err)
	}
}

func TestGenericWebhook_CloseAndEmptyURLs(t *testing.T) {
	t.Parallel()

	emptyNotifier := notify.NewGenericWebhookNotifier(notify.WebhookConfig{
		URLs: []string{},
	})
	if err := emptyNotifier.NotifyImport(context.Background(), dummyAudioImportEvent(t)); err != nil {
		t.Errorf("expected nil error on empty URLs, got: %v", err)
	}

	if err := emptyNotifier.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := emptyNotifier.NotifyImport(context.Background(), dummyAudioImportEvent(t)); err != notify.ErrNotifierClosed {
		t.Errorf("expected ErrNotifierClosed, got: %v", err)
	}
}
