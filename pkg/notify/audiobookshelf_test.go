package notify_test

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/notify"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func dummyAudioImportEvent(t *testing.T) notify.ImportEvent {
	t.Helper()
	return notify.ImportEvent{
		Title:       "Synthetic Audio Title One",
		Author:      "Synthetic Author Alpha",
		Series:      "Synthetic Series X",
		Format:      notify.FormatM4B,
		FilePath:    filepath.Join(t.TempDir(), "synthetic_audio.m4b"),
		FileSize:    12345678,
		Checksum:    "abcdef1234567890abcdef1234567890",
		Timestamp:   time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
		LibraryType: notify.LibraryTypeAudiobook,
	}
}

func TestAudiobookshelf_ScanRequestHeadersAndURL(t *testing.T) {
	t.Parallel()

	libraryID := "synthetic-lib-42"
	token := "synthetic-secret-token-abc"
	baseURL := "http://abs.internal:13378"
	expectedPath := "/api/libraries/" + libraryID + "/scan"

	mt := testutil.NewMockTransportForTest(t)
	var called atomic.Bool

	mt.RegisterHandler(baseURL+expectedPath, func(req *http.Request) (*http.Response, error) {
		called.Store(true)

		if req.Method != http.MethodPost {
			t.Errorf("expected POST method, got %s", req.Method)
		}

		authHeader := req.Header.Get("Authorization")
		if authHeader != "Bearer "+token {
			t.Errorf("expected Authorization: Bearer %s, got: %s", token, authHeader)
		}

		contentType := req.Header.Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("expected Content-Type: application/json, got: %s", contentType)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	notifier := notify.NewAudiobookshelfNotifier(notify.AudiobookshelfConfig{
		BaseURL:          baseURL,
		Token:            token,
		LibraryID:        libraryID,
		DebounceDuration: -1, // immediate execution
		HTTPClient:       mt.Client(),
	})
	defer notifier.Close()

	if notifier.Name() != "audiobookshelf" {
		t.Errorf("expected name audiobookshelf, got %s", notifier.Name())
	}

	ctx := context.Background()
	err := notifier.NotifyImport(ctx, dummyAudioImportEvent(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !called.Load() {
		t.Errorf("expected HTTP scan endpoint to be called")
	}
}

func TestAudiobookshelf_DebounceCollapsesRapidSuccessiveCalls(t *testing.T) {
	t.Parallel()

	libraryID := "synthetic-lib-debounce"
	baseURL := "http://abs.internal:13378"
	expectedURL := baseURL + "/api/libraries/" + libraryID + "/scan"

	mt := testutil.NewMockTransportForTest(t)
	var requestCount atomic.Int32

	mt.RegisterHandler(expectedURL, func(req *http.Request) (*http.Response, error) {
		requestCount.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	debounceWindow := 40 * time.Millisecond
	notifier := notify.NewAudiobookshelfNotifier(notify.AudiobookshelfConfig{
		BaseURL:          baseURL,
		Token:            "test-token",
		LibraryID:        libraryID,
		DebounceDuration: debounceWindow,
		HTTPClient:       mt.Client(),
	})
	defer notifier.Close()

	ctx := context.Background()

	// Dispatch 5 rapid calls in quick succession
	for i := 0; i < 5; i++ {
		if err := notifier.NotifyImport(ctx, dummyAudioImportEvent(t)); err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Immediately after calls, debounce window hasn't expired since last call
	if count := requestCount.Load(); count != 0 {
		t.Fatalf("expected 0 requests before debounce expiration, got %d", count)
	}

	// Wait for debounce window to expire (+ buffer)
	time.Sleep(debounceWindow + 40*time.Millisecond)

	// Exactly 1 scan request should have been dispatched
	if count := requestCount.Load(); count != 1 {
		t.Fatalf("expected exactly 1 collapsed scan request after debounce, got %d", count)
	}
}

func TestAudiobookshelf_TriggerScanAndFlush(t *testing.T) {
	t.Parallel()

	libraryID := "synthetic-lib-flush"
	baseURL := "http://abs.internal:13378"
	expectedURL := baseURL + "/api/libraries/" + libraryID + "/scan"

	mt := testutil.NewMockTransportForTest(t)
	var requestCount atomic.Int32

	mt.RegisterHandler(expectedURL, func(req *http.Request) (*http.Response, error) {
		requestCount.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	notifier := notify.NewAudiobookshelfNotifier(notify.AudiobookshelfConfig{
		BaseURL:          baseURL,
		Token:            "Bearer mock-abs-bearer",
		LibraryID:        libraryID,
		DebounceDuration: 5 * time.Second, // long debounce
		HTTPClient:       mt.Client(),
	})
	defer notifier.Close()

	ctx := context.Background()

	// Enqueue an import event into debounce timer
	if err := notifier.NotifyImport(ctx, dummyAudioImportEvent(t)); err != nil {
		t.Fatalf("NotifyImport failed: %v", err)
	}
	if count := requestCount.Load(); count != 0 {
		t.Fatalf("expected 0 requests before flush, got %d", count)
	}

	// Flush immediately
	if err := notifier.Flush(ctx); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if count := requestCount.Load(); count != 1 {
		t.Fatalf("expected 1 request after flush, got %d", count)
	}

	// TriggerScan direct invocation
	if err := notifier.TriggerScan(ctx); err != nil {
		t.Fatalf("TriggerScan failed: %v", err)
	}
	if count := requestCount.Load(); count != 2 {
		t.Fatalf("expected 2 requests after direct TriggerScan, got %d", count)
	}
}

func TestAudiobookshelf_ServerErrorHandling(t *testing.T) {
	t.Parallel()

	baseURL := "http://abs.internal:13378"
	libraryID := "synthetic-lib-err"
	expectedURL := baseURL + "/api/libraries/" + libraryID + "/scan"

	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterString(expectedURL, http.StatusInternalServerError, `{"error":"internal server failure"}`, nil)

	notifier := notify.NewAudiobookshelfNotifier(notify.AudiobookshelfConfig{
		BaseURL:          baseURL,
		LibraryID:        libraryID,
		DebounceDuration: -1,
		HTTPClient:       mt.Client(),
	})
	defer notifier.Close()

	err := notifier.NotifyImport(context.Background(), dummyAudioImportEvent(t))
	if err == nil {
		t.Fatal("expected error on HTTP 500, got nil")
	}
}

func TestAudiobookshelf_CloseAndOperations(t *testing.T) {
	t.Parallel()

	notifier := notify.NewAudiobookshelfNotifier(notify.AudiobookshelfConfig{
		BaseURL:          "http://abs.internal:13378",
		LibraryID:        "lib-closed",
		DebounceDuration: 1 * time.Second,
	})

	if err := notifier.NotifyImport(context.Background(), dummyAudioImportEvent(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Close cancels pending timer
	if err := notifier.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Subsequent operations return ErrNotifierClosed
	if err := notifier.NotifyImport(context.Background(), dummyAudioImportEvent(t)); err != notify.ErrNotifierClosed {
		t.Errorf("expected ErrNotifierClosed on NotifyImport, got: %v", err)
	}
	if err := notifier.TriggerScan(context.Background()); err != notify.ErrNotifierClosed {
		t.Errorf("expected ErrNotifierClosed on TriggerScan, got: %v", err)
	}
	if err := notifier.Flush(context.Background()); err != notify.ErrNotifierClosed {
		t.Errorf("expected ErrNotifierClosed on Flush, got: %v", err)
	}
	// Repeated Close is idempotent
	if err := notifier.Close(); err != nil {
		t.Fatalf("repeated Close failed: %v", err)
	}
}

func TestAudiobookshelf_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	libraryID := "synthetic-lib-concurrent"
	baseURL := "http://abs.internal:13378"
	expectedURL := baseURL + "/api/libraries/" + libraryID + "/scan"

	mt := testutil.NewMockTransportForTest(t)
	var requestCount atomic.Int32
	mt.RegisterHandler(expectedURL, func(req *http.Request) (*http.Response, error) {
		requestCount.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	notifier := notify.NewAudiobookshelfNotifier(notify.AudiobookshelfConfig{
		BaseURL:          baseURL,
		LibraryID:        libraryID,
		DebounceDuration: 20 * time.Millisecond,
		HTTPClient:       mt.Client(),
	})
	defer notifier.Close()

	var wg sync.WaitGroup
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = notifier.NotifyImport(ctx, dummyAudioImportEvent(t))
		}()
	}
	wg.Wait()

	time.Sleep(60 * time.Millisecond)

	if count := requestCount.Load(); count != 1 {
		t.Errorf("expected exactly 1 request from concurrent imports, got %d", count)
	}
}
