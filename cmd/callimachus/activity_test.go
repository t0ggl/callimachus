package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestActivityIncludesImportingDownloads(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, status := range []string{"queued", "downloading", "importing", "completed", "failed"} {
		if _, err := db.Exec(`INSERT INTO download_queue (id, download_client_task_id, title, protocol, status) VALUES (?, ?, ?, 'torrent', ?)`, status, status, status, status); err != nil {
			t.Fatal(err)
		}
	}
	recorder := httptest.NewRecorder()
	(&Server{db: db}).handleGetActivity(recorder, httptest.NewRequest("GET", "/api/activity", nil))
	var response APIActivityResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]bool{}
	for _, item := range response.Downloads {
		statuses[item.Status] = true
	}
	if len(response.Downloads) != 3 || !statuses["queued"] || !statuses["downloading"] || !statuses["importing"] {
		t.Fatalf("unexpected downloads: %+v", response.Downloads)
	}
}
