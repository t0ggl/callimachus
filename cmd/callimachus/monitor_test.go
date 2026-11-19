package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/requests"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestCatalogMonitoringManualBackgroundDiscovery(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	db := testutil.NewTestDB(t)
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('hardcover_api_key','mock')`); err != nil {
		t.Fatal(err)
	}
	mt := testutil.NewMockTransport()
	mt.RegisterString("/v1/graphql", 200, `{"data":{"books":[{"id":99,"title":"Future Work","release_date":"2099-05-10","contributions":[{"author":{"name":"Author"}}]}]}}`, nil)
	s := &Server{db: db, prowlarrHTTPClient: mt.Client(), monitorWake: make(chan struct{}, 1)}
	s.acquisition = s.newAcquisition()
	s.catalogMonitor = s.newCatalogMonitor()
	w := httptest.NewRecorder()
	s.handleCatalogMonitor(w, httptest.NewRequest("PUT", "/api/catalog/entity?kind=authors&name=Author", strings.NewReader(`{"monitored":true}`)))
	if w.Code != 204 {
		t.Fatalf("toggle: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.handleCatalogAction(w, httptest.NewRequest("POST", "/api/catalog/entity?kind=authors&name=Author&action=discover", nil))
	if w.Code != 202 {
		t.Fatalf("queue: %d %s", w.Code, w.Body.String())
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&count); err != nil || count != 0 {
		t.Fatal("HTTP request executed discovery synchronously")
	}
	ctx := context.Background()
	if err := s.catalogMonitor.Tick(ctx, time.Date(2028, 5, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.handleCatalogEntity(w, httptest.NewRequest("GET", "/api/catalog/entity?kind=authors&name=Author", nil))
	var result struct {
		Monitored bool
		Books     []struct {
			State       string
			ReleaseDate string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Monitored || len(result.Books) != 1 || result.Books[0].State != "Scheduled" || result.Books[0].ReleaseDate != "2099-05-10" {
		t.Fatalf("view: %s", w.Body.String())
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_actions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("manual work was not consumed")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM book_requests`).Scan(&count); err != nil || count != 0 {
		t.Fatal("future title entered acquisition")
	}
	for _, event := range []string{"catalog monitoring check started", "catalog discovery check", "catalog availability check", "catalog monitoring check completed"} {
		if !strings.Contains(logs.String(), event) {
			t.Errorf("missing log event %q", event)
		}
	}
}

func TestCatalogMonitoringRejectsInvalidInput(t *testing.T) {
	s := &Server{db: testutil.NewTestDB(t)}
	for _, url := range []string{"/api/catalog/entity?kind=users&name=x", "/api/catalog/entity?kind=authors&name="} {
		w := httptest.NewRecorder()
		s.handleCatalogEntity(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 400 {
			t.Fatalf("%s: %d", url, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.handleCatalogMonitor(w, httptest.NewRequest("PUT", "/api/catalog/entity?kind=authors&name=x", strings.NewReader(`{}`)))
	if w.Code != 400 {
		t.Fatalf("missing toggle: %d", w.Code)
	}
}

func TestCatalogMonitoringReusesAcquisitionRequest(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{`INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author')`, `INSERT INTO books(id,author_id,title) VALUES('b','a','Book')`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	s := &Server{db: db, acquisition: &requests.Acquisition{DB: db, Search: func(context.Context, string) ([]indexers.ProwlarrRelease, error) { calls++; return nil, nil }}}
	for i := 0; i < 2; i++ {
		if err := s.searchMonitoredBook(context.Background(), "b"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM book_requests WHERE book_id='b'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate requests: %d %v", count, err)
	}
	if calls != 2 {
		t.Fatal("manual refresh did not search immediately")
	}
	if _, err := db.Exec(`UPDATE book_requests SET status='downloading'`); err != nil {
		t.Fatal(err)
	}
	if err := s.searchMonitoredBook(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("active acquisition searched again")
	}
}
