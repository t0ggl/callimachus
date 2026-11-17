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
	if len(response.Downloads) != 5 || !statuses["queued"] || !statuses["downloading"] || !statuses["importing"] || !statuses["completed"] || !statuses["error"] {
		t.Fatalf("unexpected downloads: %+v", response.Downloads)
	}
}

func TestActivityLinksEditionAndRequestThroughConversion(t *testing.T) {
	db := testutil.NewTestDB(t)
	_, err := db.Exec(`
 INSERT INTO authors(id,name,sort_name) VALUES('author','Author','Author');
 INSERT INTO books(id,title,author_id) VALUES('book','Book','author');
 INSERT INTO book_editions(id,book_id,format) VALUES('edition','book','epub');
 INSERT INTO conversion_queue(id,edition_id,source_path,target_path,status,current_stage) VALUES('job','edition','/source','/target.epub','pending','preparing');
 INSERT INTO download_queue(id,download_client_task_id,title,protocol,status,edition_id) VALUES('download','client','Book','torrent','completed','edition');`)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	(&Server{db: db}).handleGetActivity(recorder, httptest.NewRequest("GET", "/api/activity", nil))
	var response APIActivityResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.ConversionJobs) != 1 || len(response.Downloads) != 1 {
		t.Fatalf("missing activity: %+v", response)
	}
	job := response.ConversionJobs[0]
	if job.BookID != "book" || job.EditionID != "edition" || job.Status != "queued" || job.TargetFormat != "EPUB" {
		t.Fatalf("unexpected job: %+v", job)
	}
	if response.Downloads[0].EditionID != job.EditionID || response.Downloads[0].Category != "callimachus-ebooks" {
		t.Fatalf("edition link lost: %+v", response.Downloads[0])
	}
}

func TestActivityDatabaseFailureIsVisible(t *testing.T) {
	db := testutil.NewTestDB(t)
	if _, err := db.Exec(`DROP TABLE conversion_queue`); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	(&Server{db: db}).handleGetActivity(recorder, httptest.NewRequest("GET", "/api/activity", nil))
	if recorder.Code != 500 {
		t.Fatalf("expected visible failure, got %d", recorder.Code)
	}
}
