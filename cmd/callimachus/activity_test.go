package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/t0ggl/callimachus/pkg/auth"
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

func TestActivityScopeAndStandaloneDismissal(t *testing.T) {
	db := testutil.NewTestDB(t)
	_, err := db.Exec(`
		INSERT INTO users(id,username,email,role,auth_source) VALUES('owner','owner','owner@example.test','requester','local');
		INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author');
		INSERT INTO books(id,title,author_id) VALUES('b','Book','a');
		INSERT INTO book_editions(id,book_id,format) VALUES('e','b','epub');
		INSERT INTO book_requests(id,user_id,book_id,title,author,format,status)
			VALUES(1,'owner','b','Book','Author','epub','downloading');
		INSERT INTO download_queue(id,request_id,download_client_task_id,title,protocol,status)
			VALUES('owned',1,'task','Book EPUB','torrent','importing'),
			('standalone-completed',NULL,'task2','Other Book','torrent','completed'),
			('standalone-active',NULL,'task3','Active Book','torrent','downloading');
		INSERT INTO conversion_queue(id,download_id,edition_id,source_path,target_path,status)
			VALUES('owned-job','owned','e','/source','/target','pending'),
			('standalone-job',NULL,'e','/other','/target2','pending'),
			('standalone-done-job',NULL,'e','/done','/target3','completed');`)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	call := func(method, path, user, role string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r = r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{ID: user, Role: role}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		user, role string
		requests   int
	}{{"other", auth.RoleRequester, 0}, {"owner", auth.RoleRequester, 1}, {"curator", auth.RoleCurator, 1}} {
		w := call(http.MethodGet, "/api/activity", tc.user, tc.role)
		var snapshot APIActivityResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &snapshot) != nil ||
			len(snapshot.Requests) != tc.requests || len(snapshot.ConversionJobs) != 2+tc.requests ||
			len(snapshot.Downloads) != 2+tc.requests {
			t.Fatalf("%s snapshot: %d %s", tc.user, w.Code, w.Body)
		}
		if tc.requests == 1 && (snapshot.Requests[0].Stage != "importing" || snapshot.Requests[0].BookID == nil || *snapshot.Requests[0].BookID != "b") {
			t.Fatalf("request linkage: %+v", snapshot.Requests[0])
		}
	}
	for _, tc := range []struct {
		path, role string
		want       int
	}{
		{"/api/activity/downloads/standalone-active", auth.RoleRequester, 403},
		{"/api/activity/downloads/owned", auth.RoleCurator, 409},
		{"/api/activity/downloads/standalone-completed", auth.RoleCurator, 409},
		{"/api/activity/downloads/standalone-active", auth.RoleCurator, 204},
		{"/api/activity/conversions/standalone-done-job", auth.RoleCurator, 409},
		{"/api/activity/conversions/standalone-job", auth.RoleCurator, 204},
	} {
		w := call(http.MethodDelete, tc.path, "curator", tc.role)
		if w.Code != tc.want {
			t.Fatalf("DELETE %s: %d %s", tc.path, w.Code, w.Body)
		}
	}
	var status string
	if err := db.QueryRow("SELECT status FROM conversion_queue WHERE id='standalone-job'").Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("cancelled state: %s %v", status, err)
	}
	if err := db.QueryRow("SELECT status FROM download_queue WHERE id='standalone-active'").Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("cancelled download state: %s %v", status, err)
	}
	var dismissed int
	if err := db.QueryRow("SELECT dismissed FROM download_queue WHERE id='standalone-completed'").Scan(&dismissed); err != nil || dismissed != 0 {
		t.Fatalf("completed download should not be dismissed: %d %v", dismissed, err)
	}
	if err := db.QueryRow("SELECT dismissed FROM conversion_queue WHERE id='standalone-done-job'").Scan(&dismissed); err != nil || dismissed != 0 {
		t.Fatalf("completed conversion should not be dismissed: %d %v", dismissed, err)
	}
	var ebookMonitored, ebookExcluded bool
	if err := db.QueryRow("SELECT ebook_monitored, ebook_monitoring_excluded FROM books WHERE id='b'").Scan(&ebookMonitored, &ebookExcluded); err != nil {
		t.Fatal(err)
	}
	if ebookMonitored || !ebookExcluded {
		t.Fatalf("standalone conversion cancellation should unmonitor and exclude book: monitored=%v excluded=%v", ebookMonitored, ebookExcluded)
	}
}
