package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestServer_StartupHealthAndShutdown(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_callimachus.db")
	libraryDir := filepath.Join(tempDir, "books")
	if err := os.MkdirAll(libraryDir, 0755); err != nil {
		t.Fatalf("mkdir libraryDir: %v", err)
	}

	cfg := Config{
		ConfigDir:           tempDir,
		DBPath:              dbPath,
		LibraryPath:         libraryDir,
		Host:                "127.0.0.1",
		Port:                0, // Dynamic port
		IndexerSyncInterval: 1 * time.Hour,
		StagingPollInterval: 1 * time.Hour,
		Version:             "test-1.0.0",
		Commit:              "testcommit123",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("srv.Start failed: %v", err)
	}

	baseURL := "http://" + srv.Addr()

	// 1. Verify /healthz endpoint
	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /healthz, got %d", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if health.Status != "ok" {
		t.Errorf("expected health status 'ok', got %q", health.Status)
	}
	if health.Database != "ok" {
		t.Errorf("expected health database 'ok', got %q", health.Database)
	}
	if health.Version != "test-1.0.0" {
		t.Errorf("expected health version 'test-1.0.0', got %q", health.Version)
	}

	// 2. Verify /api/health endpoint
	apiHealthResp, err := http.Get(baseURL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health failed: %v", err)
	}
	defer apiHealthResp.Body.Close()
	if apiHealthResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/health, got %d", apiHealthResp.StatusCode)
	}

	// 3. Verify SPA frontend routing: "/" should return 200 with HTML
	spaResp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer spaResp.Body.Close()

	if spaResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for root path, got %d", spaResp.StatusCode)
	}
	body, _ := io.ReadAll(spaResp.Body)
	if !strings.Contains(string(body), "<html") && !strings.Contains(string(body), "<!DOCTYPE") {
		t.Errorf("expected HTML content on root path, got: %s", string(body))
	}

	// 4. Verify SPA frontend routing for non-API subpath: "/requests" returns HTML
	clientRouteResp, err := http.Get(baseURL + "/requests")
	if err != nil {
		t.Fatalf("GET /requests failed: %v", err)
	}
	defer clientRouteResp.Body.Close()
	if clientRouteResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for client route /requests, got %d", clientRouteResp.StatusCode)
	}

	// 5. Verify unknown /api/ route returns 404 JSON, NOT SPA HTML
	unknownAPIResp, err := http.Get(baseURL + "/api/nonexistent-route")
	if err != nil {
		t.Fatalf("GET /api/nonexistent-route failed: %v", err)
	}
	defer unknownAPIResp.Body.Close()
	if unknownAPIResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found for unknown API route, got %d", unknownAPIResp.StatusCode)
	}
	var errMap map[string]string
	if err := json.NewDecoder(unknownAPIResp.Body).Decode(&errMap); err != nil {
		t.Fatalf("expected JSON error response for 404 API route: %v", err)
	}
	if errMap["error"] == "" {
		t.Errorf("expected non-empty error message in 404 JSON response")
	}

	// 6. Verify Auth endpoints are registered (e.g. /api/auth/me unauthenticated returns 401)
	authMeResp, err := http.Get(baseURL + "/api/auth/me")
	if err != nil {
		t.Fatalf("GET /api/auth/me failed: %v", err)
	}
	defer authMeResp.Body.Close()
	if authMeResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for unauthenticated /api/auth/me, got %d", authMeResp.StatusCode)
	}

	// 7. Verify Graceful Server Shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("srv.Shutdown failed: %v", err)
	}
}

func TestServer_FallbackHTMLWhenDistEmpty(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "fallback_test.db")

	// Provide empty mock static FS
	emptyFS := fstest.MapFS{}

	cfg := Config{
		ConfigDir:           tempDir,
		DBPath:              dbPath,
		LibraryPath:         tempDir,
		Host:                "127.0.0.1",
		Port:                0,
		StaticFS:            emptyFS,
		IndexerSyncInterval: 1 * time.Hour,
		StagingPollInterval: 1 * time.Hour,
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("srv.Start failed: %v", err)
	}
	defer func() {
		_ = srv.Shutdown(context.Background())
	}()

	baseURL := "http://" + srv.Addr()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET / with empty FS failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from fallback HTML, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "Callimachus") {
		t.Errorf("expected fallback HTML to contain 'Callimachus', got: %s", string(body))
	}
}

func TestRun_CommandExecutionAndCancel(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "run_test.db")
	booksDir := filepath.Join(tempDir, "books")
	_ = os.MkdirAll(booksDir, 0755)

	ctx, cancel := context.WithCancel(context.Background())

	// Run in background and cancel context after server starts
	errCh := make(chan error, 1)
	go func() {
		args := []string{
			"-db", dbPath,
			"-library", booksDir,
			"-port", "0",
			"-host", "127.0.0.1",
		}
		errCh <- run(ctx, args)
	}()

	// Allow server to start up
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run() returned error on graceful shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() timed out waiting for shutdown")
	}
}
