package notify_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/notify"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func dummyEBookImportEvent(t *testing.T) notify.ImportEvent {
	t.Helper()
	return notify.ImportEvent{
		Title:       "Synthetic EBook Title Alpha",
		Author:      "Synthetic Author Beta",
		Series:      "Synthetic Series Gamma",
		Format:      notify.FormatEPUB,
		FilePath:    filepath.Join(t.TempDir(), "synthetic_book.epub"),
		FileSize:    2345678,
		Checksum:    "11223344556677889900aabbccddeeff",
		Timestamp:   time.Date(2026, 2, 20, 12, 0, 0, 0, time.UTC),
		LibraryType: notify.LibraryTypeEBook,
	}
}

func TestCalibreWeb_BasicAuthTrigger(t *testing.T) {
	t.Parallel()

	baseURL := "http://calibre.internal:8083"
	expectedURL := baseURL + "/reconnect"

	mt := testutil.NewMockTransportForTest(t)
	var handled atomic.Bool

	mt.RegisterHandler(expectedURL, func(req *http.Request) (*http.Response, error) {
		handled.Store(true)

		if req.Method != http.MethodPost {
			t.Errorf("expected POST method, got %s", req.Method)
		}

		user, pass, ok := req.BasicAuth()
		if !ok || user != "synthetic-user" || pass != "synthetic-secret" {
			t.Errorf("unexpected basic auth: ok=%v, user=%s, pass=%s", ok, user, pass)
		}

		// Calibre-Web reconnect often responds with redirect 302
		return &http.Response{
			StatusCode: http.StatusFound,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	notifier := notify.NewCalibreWebNotifier(notify.CalibreWebConfig{
		BaseURL:          baseURL,
		Username:         "synthetic-user",
		Password:         "synthetic-secret",
		DebounceDuration: -1, // immediate
		HTTPClient:       mt.Client(),
	})
	defer notifier.Close()

	if notifier.Name() != "calibre-web" {
		t.Errorf("expected name calibre-web, got %s", notifier.Name())
	}

	err := notifier.NotifyImport(context.Background(), dummyEBookImportEvent(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !handled.Load() {
		t.Errorf("expected calibre-web /reconnect endpoint to be called")
	}
}

func TestCalibreWeb_APIKeyAuthAndCustomEndpoint(t *testing.T) {
	t.Parallel()

	baseURL := "http://calibre.internal:8083"
	customEndpoint := "/api/v1/trigger/rescan"
	apiKey := "synthetic-calibre-api-key-999"

	mt := testutil.NewMockTransportForTest(t)
	var handled atomic.Bool

	mt.RegisterHandler(baseURL+customEndpoint, func(req *http.Request) (*http.Response, error) {
		handled.Store(true)

		if req.Header.Get("X-API-Key") != apiKey {
			t.Errorf("expected X-API-Key %s, got: %s", apiKey, req.Header.Get("X-API-Key"))
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	notifier := notify.NewCalibreWebNotifier(notify.CalibreWebConfig{
		BaseURL:          baseURL,
		APIKey:           apiKey,
		Endpoint:         customEndpoint,
		DebounceDuration: -1,
		HTTPClient:       mt.Client(),
	})
	defer notifier.Close()

	err := notifier.NotifyImport(context.Background(), dummyEBookImportEvent(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !handled.Load() {
		t.Errorf("expected custom endpoint to be invoked")
	}
}

func TestCalibreWeb_DebounceCollapsesRapidSuccessiveCalls(t *testing.T) {
	t.Parallel()

	baseURL := "http://calibre.internal:8083"
	mt := testutil.NewMockTransportForTest(t)
	var requestCount atomic.Int32

	mt.RegisterHandler(baseURL+"/reconnect", func(req *http.Request) (*http.Response, error) {
		requestCount.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	debounceWindow := 40 * time.Millisecond
	notifier := notify.NewCalibreWebNotifier(notify.CalibreWebConfig{
		BaseURL:          baseURL,
		DebounceDuration: debounceWindow,
		HTTPClient:       mt.Client(),
	})
	defer notifier.Close()

	ctx := context.Background()

	// 5 rapid import events
	for i := 0; i < 5; i++ {
		if err := notifier.NotifyImport(ctx, dummyEBookImportEvent(t)); err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Should not have triggered yet
	if count := requestCount.Load(); count != 0 {
		t.Fatalf("expected 0 requests before debounce expiration, got %d", count)
	}

	// Wait past debounce window
	time.Sleep(debounceWindow + 40*time.Millisecond)

	// Exactly 1 request collapsed
	if count := requestCount.Load(); count != 1 {
		t.Fatalf("expected exactly 1 request after debounce window, got %d", count)
	}
}

func TestCalibreWeb_ServerErrorHandling(t *testing.T) {
	t.Parallel()

	baseURL := "http://calibre.internal:8083"
	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterString(baseURL+"/reconnect", http.StatusBadGateway, "502 Bad Gateway", nil)

	notifier := notify.NewCalibreWebNotifier(notify.CalibreWebConfig{
		BaseURL:          baseURL,
		DebounceDuration: -1,
		HTTPClient:       mt.Client(),
	})
	defer notifier.Close()

	err := notifier.NotifyImport(context.Background(), dummyEBookImportEvent(t))
	if err == nil {
		t.Fatal("expected error on HTTP 502, got nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("expected error to mention 502, got: %v", err)
	}
}

func TestCalibreWeb_CloseAndOperations(t *testing.T) {
	t.Parallel()

	notifier := notify.NewCalibreWebNotifier(notify.CalibreWebConfig{
		BaseURL:          "http://calibre.internal:8083",
		DebounceDuration: 1 * time.Second,
	})

	if err := notifier.NotifyImport(context.Background(), dummyEBookImportEvent(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := notifier.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := notifier.NotifyImport(context.Background(), dummyEBookImportEvent(t)); err != notify.ErrNotifierClosed {
		t.Errorf("expected ErrNotifierClosed on NotifyImport, got: %v", err)
	}
	if err := notifier.TriggerScan(context.Background()); err != notify.ErrNotifierClosed {
		t.Errorf("expected ErrNotifierClosed on TriggerScan, got: %v", err)
	}
	if err := notifier.Flush(context.Background()); err != notify.ErrNotifierClosed {
		t.Errorf("expected ErrNotifierClosed on Flush, got: %v", err)
	}
}

func TestCalibreWeb_BasicAuthHeaderParsing(t *testing.T) {
	t.Parallel()

	baseURL := "http://calibre.internal:8083"
	mt := testutil.NewMockTransportForTest(t)

	var authHeader string
	mt.RegisterHandler(baseURL+"/reconnect", func(req *http.Request) (*http.Response, error) {
		authHeader = req.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	notifier := notify.NewCalibreWebNotifier(notify.CalibreWebConfig{
		BaseURL:          baseURL,
		Username:         "admin",
		Password:         "secret123",
		DebounceDuration: -1,
		HTTPClient:       mt.Client(),
	})
	defer notifier.Close()

	if err := notifier.TriggerScan(context.Background()); err != nil {
		t.Fatalf("TriggerScan failed: %v", err)
	}

	expectedPrefix := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:secret123"))
	if authHeader != expectedPrefix {
		t.Errorf("expected auth header %q, got %q", expectedPrefix, authHeader)
	}
}
