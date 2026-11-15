package indexers_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_callimachus.db")

	cfg := database.Config{
		Path:         dbPath,
		MaxOpenConns: 5,
		MaxIdleConns: 5,
	}

	db, err := database.Open(cfg)
	if err != nil {
		t.Fatalf("setupTestDB: Open failed: %v", err)
	}

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		db.Close()
		t.Fatalf("setupTestDB: Migrate failed: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func TestProwlarrClient_GetSystemStatus(t *testing.T) {
	t.Parallel()

	baseURL := "https://prowlarr.example.local"
	apiKey := "prowlarr-api-key-123"

	statusJSON := `{
		"version": "1.8.6.3986",
		"appName": "Prowlarr",
		"instanceName": "Prowlarr",
		"isProduction": true
	}`

	mt := testutil.NewMockTransport()
	statusURL := "https://prowlarr.example.local/api/v1/system/status?apikey=prowlarr-api-key-123"
	mt.RegisterResponse(statusURL, http.StatusOK, []byte(statusJSON), nil)

	client := indexers.NewProwlarrClient(baseURL, apiKey, mt.Client())

	status, err := client.GetSystemStatus(context.Background())
	if err != nil {
		t.Fatalf("GetSystemStatus failed: %v", err)
	}

	if status.Version != "1.8.6.3986" {
		t.Errorf("expected version 1.8.6.3986, got %q", status.Version)
	}
	if status.AppName != "Prowlarr" {
		t.Errorf("expected appName Prowlarr, got %q", status.AppName)
	}
	if !status.IsProduction {
		t.Errorf("expected isProduction to be true")
	}
}

func TestProwlarrClient_GetIndexers(t *testing.T) {
	t.Parallel()

	baseURL := "https://prowlarr.example.local"
	apiKey := "prowlarr-api-key-123"

	indexersJSON := `[
		{
			"id": 1,
			"name": "Synthetic Tracker Torrent",
			"protocol": "torrent",
			"implementation": "Torznab",
			"enable": true,
			"priority": 1,
			"capabilities": {
				"categories": [
					{"id": 7000, "name": "Books"},
					{"id": 7020, "name": "Books/EBook"},
					{"id": 3030, "name": "Audio/Audiobook"}
				]
			}
		},
		{
			"id": 2,
			"name": "Synthetic Usenet Indexer",
			"protocol": "usenet",
			"implementation": "Newznab",
			"enable": false,
			"priority": 25,
			"capabilities": {
				"categories": [
					{"id": 7020, "name": "Books/EBook"}
				]
			}
		}
	]`

	mt := testutil.NewMockTransport()
	indexersURL := "https://prowlarr.example.local/api/v1/indexer?apikey=prowlarr-api-key-123"
	mt.RegisterResponse(indexersURL, http.StatusOK, []byte(indexersJSON), nil)

	client := indexers.NewProwlarrClient(baseURL, apiKey, mt.Client())

	list, err := client.GetIndexers(context.Background())
	if err != nil {
		t.Fatalf("GetIndexers failed: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("expected 2 indexers, got %d", len(list))
	}
	if list[0].Name != "Synthetic Tracker Torrent" {
		t.Errorf("expected name Synthetic Tracker Torrent, got %q", list[0].Name)
	}
	if !list[0].Enable {
		t.Errorf("expected list[0].Enable to be true")
	}
	if list[1].Enable {
		t.Errorf("expected list[1].Enable to be false")
	}
}

func TestProwlarrClient_SyncIndexers_DatabaseUpsert(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)

	baseURL := "https://prowlarr.example.local"
	apiKey := "prowlarr-api-key-123"

	initialJSON := `[
		{
			"id": 1,
			"name": "Synthetic Tracker Torrent",
			"protocol": "torrent",
			"implementation": "Torznab",
			"enable": true,
			"priority": 1,
			"capabilities": {
				"categories": [
					{"id": 7000, "name": "Books"},
					{"id": 7020, "name": "Books/EBook"},
					{"id": 3030, "name": "Audio/Audiobook"}
				]
			}
		},
		{
			"id": 2,
			"name": "Synthetic Usenet Indexer",
			"protocol": "usenet",
			"implementation": "Newznab",
			"enable": false,
			"priority": 25,
			"capabilities": {
				"categories": [
					{"id": 7020, "name": "Books/EBook"}
				]
			}
		}
	]`

	updatedJSON := `[
		{
			"id": 1,
			"name": "Synthetic Tracker Torrent Renamed",
			"protocol": "torrent",
			"implementation": "Torznab",
			"enable": true,
			"priority": 5,
			"capabilities": {
				"categories": [
					{"id": 7000, "name": "Books"}
				]
			}
		}
	]`

	mt := testutil.NewMockTransport()
	indexersURL := "https://prowlarr.example.local/api/v1/indexer?apikey=prowlarr-api-key-123"

	// 1. Initial sync
	mt.RegisterResponse(indexersURL, http.StatusOK, []byte(initialJSON), nil)
	client := indexers.NewProwlarrClient(baseURL, apiKey, mt.Client())

	ctx := context.Background()
	synced, err := client.SyncIndexers(ctx, db.DB)
	if err != nil {
		t.Fatalf("SyncIndexers initial pass failed: %v", err)
	}

	if len(synced) != 2 {
		t.Fatalf("expected 2 synced indexers, got %d", len(synced))
	}

	// Verify database rows
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM indexers").Scan(&count); err != nil {
		t.Fatalf("failed to query count from indexers: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows in indexers table, got %d", count)
	}

	var name, protocol, url, storedKey, categories string
	var enabled, priority int
	err = db.QueryRowContext(ctx,
		"SELECT name, protocol, url, api_key, categories, enabled, priority FROM indexers WHERE id = 'prowlarr-1'").
		Scan(&name, &protocol, &url, &storedKey, &categories, &enabled, &priority)
	if err != nil {
		t.Fatalf("failed to query row prowlarr-1: %v", err)
	}

	if name != "Synthetic Tracker Torrent" {
		t.Errorf("expected name Synthetic Tracker Torrent, got %q", name)
	}
	if protocol != "torznab" {
		t.Errorf("expected protocol torznab, got %q", protocol)
	}
	if url != "https://prowlarr.example.local/1/api" {
		t.Errorf("expected url https://prowlarr.example.local/1/api, got %q", url)
	}
	if storedKey != apiKey {
		t.Errorf("expected api_key %q, got %q", apiKey, storedKey)
	}
	if enabled != 1 {
		t.Errorf("expected enabled 1, got %d", enabled)
	}
	if priority != 1 {
		t.Errorf("expected priority 1, got %d", priority)
	}

	// Verify Usenet indexer protocol
	var usenetProtocol string
	if err := db.QueryRowContext(ctx, "SELECT protocol FROM indexers WHERE id = 'prowlarr-2'").Scan(&usenetProtocol); err != nil {
		t.Fatalf("failed to query row prowlarr-2: %v", err)
	}
	if usenetProtocol != "newznab" {
		t.Errorf("expected protocol newznab for usenet indexer, got %q", usenetProtocol)
	}

	// 2. Re-sync with updated properties (verifies ON CONFLICT DO UPDATE)
	mt.RegisterResponse(indexersURL, http.StatusOK, []byte(updatedJSON), nil)
	synced2, err := client.SyncIndexers(ctx, db.DB)
	if err != nil {
		t.Fatalf("SyncIndexers second pass failed: %v", err)
	}
	if len(synced2) != 1 {
		t.Fatalf("expected 1 synced indexer on update, got %d", len(synced2))
	}

	var updatedName string
	var updatedPriority int
	err = db.QueryRowContext(ctx,
		"SELECT name, priority FROM indexers WHERE id = 'prowlarr-1'").
		Scan(&updatedName, &updatedPriority)
	if err != nil {
		t.Fatalf("failed to query updated row prowlarr-1: %v", err)
	}
	if updatedName != "Synthetic Tracker Torrent Renamed" {
		t.Errorf("expected updated name, got %q", updatedName)
	}
	if updatedPriority != 5 {
		t.Errorf("expected updated priority 5, got %d", updatedPriority)
	}
}
