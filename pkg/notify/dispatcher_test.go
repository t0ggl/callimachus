package notify_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/notify"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

type mockNotifier struct {
	name      string
	notifyErr error
	closeErr  error
	called    atomic.Int32
	closed    atomic.Bool
}

func (m *mockNotifier) Name() string {
	return m.name
}

func (m *mockNotifier) NotifyImport(ctx context.Context, event notify.ImportEvent) error {
	m.called.Add(1)
	return m.notifyErr
}

func (m *mockNotifier) Close() error {
	m.closed.Store(true)
	return m.closeErr
}

func TestDispatcher_FanOutToMultipleNotifiers(t *testing.T) {
	t.Parallel()

	n1 := &mockNotifier{name: "target-one"}
	n2 := &mockNotifier{name: "target-two"}
	n3 := &mockNotifier{name: "target-three"}

	d := notify.NewDispatcher(n1, n2)
	d.Register(n3)

	if len(d.Notifiers()) != 3 {
		t.Fatalf("expected 3 registered notifiers, got %d", len(d.Notifiers()))
	}

	event := dummyAudioImportEvent(t)
	err := d.Dispatch(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected dispatch error: %v", err)
	}

	if n1.called.Load() != 1 || n2.called.Load() != 1 || n3.called.Load() != 1 {
		t.Errorf("expected all notifiers called: (%d, %d, %d)", n1.called.Load(), n2.called.Load(), n3.called.Load())
	}
}

func TestDispatcher_GracefulHandlingOfIndividualNotifierFailures(t *testing.T) {
	t.Parallel()

	errTargetFail := errors.New("synthetic downstream connection refused")

	n1 := &mockNotifier{name: "failing-notifier", notifyErr: errTargetFail}
	n2 := &mockNotifier{name: "healthy-notifier-1"}
	n3 := &mockNotifier{name: "healthy-notifier-2"}

	d := notify.NewDispatcher(n1, n2, n3)
	defer d.Close()

	err := d.Dispatch(context.Background(), dummyAudioImportEvent(t))
	if err == nil {
		t.Fatal("expected dispatch error from failing notifier, got nil")
	}

	// Ensure error identifies the failed notifier
	if !strings.Contains(err.Error(), "failing-notifier") {
		t.Errorf("expected error message to identify failing-notifier, got: %v", err)
	}

	// Healthy notifiers must still have received the notification!
	if n2.called.Load() != 1 {
		t.Errorf("expected healthy-notifier-1 to be called despite n1 failure")
	}
	if n3.called.Load() != 1 {
		t.Errorf("expected healthy-notifier-2 to be called despite n1 failure")
	}
}

func TestDispatcher_CleanCloseAndIdempotence(t *testing.T) {
	t.Parallel()

	n1 := &mockNotifier{name: "n1"}
	n2 := &mockNotifier{name: "n2", closeErr: errors.New("close failure")}

	d := notify.NewDispatcher(n1, n2)

	err := d.Close()
	if err == nil {
		t.Fatal("expected close error from n2, got nil")
	}
	if !strings.Contains(err.Error(), "close failure") {
		t.Errorf("expected error to contain close failure, got: %v", err)
	}

	if !n1.closed.Load() || !n2.closed.Load() {
		t.Errorf("expected both notifiers closed: n1=%v, n2=%v", n1.closed.Load(), n2.closed.Load())
	}

	// Dispatching on closed dispatcher fails immediately
	err = d.Dispatch(context.Background(), dummyAudioImportEvent(t))
	if err != notify.ErrDispatcherClosed {
		t.Errorf("expected ErrDispatcherClosed, got: %v", err)
	}

	// Closing again is idempotent and returns nil
	if err := d.Close(); err != nil {
		t.Errorf("expected nil on second Close(), got: %v", err)
	}
}

func TestDispatcher_IntegrationWithConcreteNotifiers(t *testing.T) {
	t.Parallel()

	mt := testutil.NewMockTransportForTest(t)
	var absCalled, calibreCalled, hookCalled atomic.Int32

	mt.RegisterHandler("http://abs.test/api/libraries/lib-abs/scan", func(req *http.Request) (*http.Response, error) {
		absCalled.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
	})

	mt.RegisterHandler("http://calibre.test/reconnect", func(req *http.Request) (*http.Response, error) {
		calibreCalled.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
	})

	mt.RegisterHandler("http://webhook.test/imported", func(req *http.Request) (*http.Response, error) {
		hookCalled.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
	})

	absNotifier := notify.NewAudiobookshelfNotifier(notify.AudiobookshelfConfig{
		BaseURL:          "http://abs.test",
		Token:            "secret-token",
		LibraryID:        "lib-abs",
		DebounceDuration: -1, // immediate
		HTTPClient:       mt.Client(),
	})

	calibreNotifier := notify.NewCalibreWebNotifier(notify.CalibreWebConfig{
		BaseURL:          "http://calibre.test",
		DebounceDuration: -1, // immediate
		HTTPClient:       mt.Client(),
	})

	webhookNotifier := notify.NewGenericWebhookNotifier(notify.WebhookConfig{
		URLs:       []string{"http://webhook.test/imported"},
		HTTPClient: mt.Client(),
	})

	dispatcher := notify.NewDispatcher(absNotifier, calibreNotifier, webhookNotifier)
	defer dispatcher.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := dispatcher.Dispatch(ctx, dummyEBookImportEvent(t))
	if err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}

	if absCalled.Load() != 1 {
		t.Errorf("expected ABS called once, got %d", absCalled.Load())
	}
	if calibreCalled.Load() != 1 {
		t.Errorf("expected Calibre called once, got %d", calibreCalled.Load())
	}
	if hookCalled.Load() != 1 {
		t.Errorf("expected Webhook called once, got %d", hookCalled.Load())
	}
}
