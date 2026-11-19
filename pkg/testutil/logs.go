package testutil

import (
	"bytes"
	"log/slog"
	"testing"
)

// CaptureLogs captures all severities for a non-parallel test.
// Read the buffer after any goroutines that produce logs have finished.
func CaptureLogs(t testing.TB) *bytes.Buffer {
	t.Helper()
	previous := slog.Default()
	var out bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &out
}
