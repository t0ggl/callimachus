package indexers_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestSyncer_SyncOnce_And_Deduplication(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	ctx := context.Background()

	// Insert active mock indexer
	_, err := db.ExecContext(ctx, `
INSERT INTO indexers (id, name, protocol, url, api_key, categories, enabled, priority)
VALUES ('mock-indexer-1', 'Mock Indexer', 'torznab', 'https://indexer.example.local/api', 'mockkey', '[7000, 7020, 3030]', 1, 1);
`)
	if err != nil {
		t.Fatalf("failed to insert test indexer: %v", err)
	}

	// Verify last_sync_at is initially null
	var initialSyncAt *time.Time
	err = db.QueryRowContext(ctx, "SELECT last_sync_at FROM indexers WHERE id = 'mock-indexer-1'").Scan(&initialSyncAt)
	if err != nil {
		t.Fatalf("failed to query last_sync_at: %v", err)
	}
	if initialSyncAt != nil {
		t.Errorf("expected initial last_sync_at to be nil, got %v", initialSyncAt)
	}

	mt := testutil.NewMockTransport()
	expectedURL := "https://indexer.example.local/api?apikey=mockkey&cat=7000%2C7020%2C3030&limit=50&t=search"
	mt.RegisterFixture(expectedURL, http.StatusOK, "testdata/torznab/feed_rss.xml", nil)

	cfg := indexers.SyncerConfig{
		Interval:     15 * time.Minute,
		Categories:   []int{7000, 7020, 3030},
		Limit:        50,
		DedupeWindow: 1 * time.Hour,
		HTTPClient:   mt.Client(),
	}

	syncer := indexers.NewSyncer(db.DB, cfg)

	// 1. First Sync: should ingest 3 new releases
	firstPassReleases, err := syncer.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce first pass failed: %v", err)
	}

	if len(firstPassReleases) != 3 {
		t.Fatalf("expected 3 releases on first sync pass, got %d", len(firstPassReleases))
	}

	// Verify releases streamed into channel
	for i := 0; i < 3; i++ {
		select {
		case r := <-syncer.ReleaseChan():
			if r.InfoHash == "" {
				t.Errorf("received release on channel without InfoHash")
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timeout waiting for release %d on channel", i)
		}
	}

	// Verify last_sync_at was updated in the database
	var updatedSyncAt *time.Time
	err = db.QueryRowContext(ctx, "SELECT last_sync_at FROM indexers WHERE id = 'mock-indexer-1'").Scan(&updatedSyncAt)
	if err != nil {
		t.Fatalf("failed to query updated last_sync_at: %v", err)
	}
	if updatedSyncAt == nil {
		t.Fatalf("expected last_sync_at to be updated, but was nil")
	}

	// 2. Second Sync: with identical feed, deduplication must drop all 3 releases
	secondPassReleases, err := syncer.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce second pass failed: %v", err)
	}

	if len(secondPassReleases) != 0 {
		t.Errorf("expected 0 releases due to deduplication, got %d", len(secondPassReleases))
	}

	// Verify channel has no lingering releases
	select {
	case unexpected := <-syncer.ReleaseChan():
		t.Fatalf("unexpected duplicate release received on channel: %v", unexpected)
	default:
		// Channel empty as expected
	}
}

func TestSyncer_PeriodicExecution(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.ExecContext(ctx, `
INSERT INTO indexers (id, name, protocol, url, api_key, categories, enabled, priority)
VALUES ('mock-indexer-periodic', 'Periodic Indexer', 'torznab', 'https://indexer.example.local/api', 'key1', '[7000]', 1, 1);
`)
	if err != nil {
		t.Fatalf("failed to insert indexer: %v", err)
	}

	expectedURL := "https://indexer.example.local/api?apikey=key1&cat=7000&limit=50&t=search"
	fixture := testutil.NewMockTransportForTest(t)
	fixture.RegisterFixture(expectedURL, http.StatusOK, "testdata/torznab/feed_rss.xml", nil)
	secondSync := make(chan struct{})
	calls := 0
	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterHandler(expectedURL, func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 2 {
			close(secondSync)
		}
		return fixture.RoundTrip(req)
	})

	cfg := indexers.SyncerConfig{
		Interval:     20 * time.Millisecond,
		Categories:   []int{7000},
		Limit:        50,
		DedupeWindow: 1 * time.Hour,
		HTTPClient:   mt.Client(),
	}

	syncer := indexers.NewSyncer(db.DB, cfg)

	var callbackCount int
	syncer.SetHandler(func(ctx context.Context, release indexers.Release) error {
		callbackCount++
		return nil
	})

	syncer.Start(ctx)
	defer syncer.Stop()
	select {
	case <-secondSync:
	case <-ctx.Done():
		t.Fatal("syncer did not make a periodic request")
	}
	syncer.Stop()

	// Initial sync gives 3 callbacks; subsequent ticks deduplicate, so count should be 3
	if callbackCount != 3 {
		t.Errorf("expected exactly 3 callbacks after deduplicated ticks, got %d", callbackCount)
	}

	// Verify last_sync_at was recorded
	var lastSync *time.Time
	err = db.QueryRowContext(ctx, "SELECT last_sync_at FROM indexers WHERE id = 'mock-indexer-periodic'").Scan(&lastSync)
	if err != nil || lastSync == nil {
		t.Errorf("expected last_sync_at to be populated, err: %v, val: %v", err, lastSync)
	}
}

func TestSyncer_InactiveIndexersSkipped(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	ctx := context.Background()

	// One enabled, one disabled
	_, err := db.ExecContext(ctx, `
INSERT INTO indexers (id, name, protocol, url, api_key, categories, enabled, priority)
VALUES 
    ('enabled-1', 'Active Indexer', 'torznab', 'https://indexer.example.local/api', 'key1', '[7000]', 1, 1),
    ('disabled-2', 'Inactive Indexer', 'torznab', 'https://indexer2.example.local/api', 'key2', '[7000]', 0, 1);
`)
	if err != nil {
		t.Fatalf("failed to insert indexers: %v", err)
	}

	mt := testutil.NewMockTransport()
	activeURL := "https://indexer.example.local/api?apikey=key1&cat=7000&limit=50&t=search"
	mt.RegisterFixture(activeURL, http.StatusOK, "testdata/torznab/feed_rss.xml", nil)
	// If inactive indexer is queried, PanicOnUnmocked in mt will immediately fail the test

	cfg := indexers.SyncerConfig{
		Interval:   15 * time.Minute,
		Categories: []int{7000},
		HTTPClient: mt.Client(),
	}

	syncer := indexers.NewSyncer(db.DB, cfg)
	releases, err := syncer.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce failed: %v", err)
	}

	if len(releases) != 3 {
		t.Errorf("expected 3 releases from active indexer, got %d", len(releases))
	}

	// Verify inactive indexer last_sync_at remains null
	var inactiveSync *time.Time
	err = db.QueryRowContext(ctx, "SELECT last_sync_at FROM indexers WHERE id = 'disabled-2'").Scan(&inactiveSync)
	if err != nil {
		t.Fatalf("failed to query inactive indexer: %v", err)
	}
	if inactiveSync != nil {
		t.Errorf("expected inactive indexer last_sync_at to be nil, got %v", inactiveSync)
	}
}

func TestSyncer_ConcurrentSyncOnce(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `
INSERT INTO indexers (id, name, protocol, url, api_key, categories, enabled, priority)
VALUES ('mock-concurrent', 'Concurrent Indexer', 'torznab', 'https://indexer.example.local/api', 'concurkey', '[7000]', 1, 1);
`)
	if err != nil {
		t.Fatalf("failed to insert indexer: %v", err)
	}

	mt := testutil.NewMockTransport()
	expectedURL := "https://indexer.example.local/api?apikey=concurkey&cat=7000&limit=50&t=search"
	mt.RegisterFixture(expectedURL, http.StatusOK, "testdata/torznab/feed_rss.xml", nil)

	syncer := indexers.NewSyncer(db.DB, indexers.SyncerConfig{
		HTTPClient: mt.Client(),
	})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = syncer.SyncOnce(ctx)
		}()
	}
	wg.Wait()
}
