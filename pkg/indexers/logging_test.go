package indexers_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestSyncLogsExcludeCredentials(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	const key = "private-prowlarr-key"
	mock := testutil.NewMockTransport()
	mock.RegisterString("/api/v1/indexer", 200, `[{"id":2,"name":"Mock","protocol":"torrent","enable":true}]`, nil)
	mock.RegisterError("/2/api", errors.New("transport failed"))
	client := indexers.NewProwlarrClient("https://prowlarr.test", key, mock.Client())
	if _, err := client.SyncIndexers(ctx, db); err != nil {
		t.Fatal(err)
	}
	syncer := indexers.NewSyncer(db, indexers.SyncerConfig{HTTPClient: mock.Client()})
	if _, err := syncer.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	mock.RegisterError("/api/v1/indexer", errors.New("transport failed"))
	if _, err := client.SyncIndexers(ctx, db); err == nil {
		t.Fatal("expected Prowlarr sync failure")
	}
	for _, event := range []string{"Prowlarr indexer sync started", "Prowlarr indexer sync completed", "Prowlarr indexer sync could not fetch indexers",
		"indexer sync started", "indexer sync could not fetch releases", "indexer sync completed"} {
		if !strings.Contains(logs.String(), event) {
			t.Errorf("missing log event %q", event)
		}
	}
	if strings.Contains(logs.String(), key) {
		t.Error("Prowlarr API key appeared in sync logs")
	}
}
