package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/requests"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestDiscoveryAndAutomaticAcquisition(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	db := testutil.NewTestDB(t)
	mt := testutil.NewMockTransport()
	mt.RegisterError("/v1/graphql", errors.New("upstream echoed private-hardcover-key"))
	mt.RegisterString("/search.json", 200, `{"docs":[{"key":"/works/1","title":"Book","author_name":["Author"],"first_publish_year":2024}]}`, nil)
	mt.RegisterString("/books/v1/volumes", 200, `{"items":[{"volumeInfo":{"title":"Other","authors":["Writer"],"publishedDate":"2023"}}]}`, nil)
	mt.RegisterString("/api/v1/search", 200, `[{"guid":"release","indexerId":1,"title":"Author - Book EPUB 2024","seeders":50}]`, nil)
	for k, v := range map[string]string{"prowlarr_url": "https://prowlarr.test", "prowlarr_api_key": "private-prowlarr-key", "google_books_api_key": "private-google-key", "hardcover_api_key": "private-hardcover-key"} {
		if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, k, v); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
	s.acquisition = s.newAcquisition()
	w := httptest.NewRecorder()
	s.handleDiscover(w, httptest.NewRequest("GET", "/api/discover?q=Book", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Other") {
		t.Fatalf("discovery %d %s", w.Code, w.Body.String())
	}
	// A URL-bearing Google Books transport error must not leak its query-string key.
	mt.RegisterError("/books/v1/volumes", errors.New("Google Books transport failed"))
	w = httptest.NewRecorder()
	s.handleDiscover(w, httptest.NewRequest("GET", "/api/discover?q=private-query", nil))
	if w.Code != 200 {
		t.Fatalf("remaining provider should still return results, got %d", w.Code)
	}
	if _, err := db.Exec(`INSERT INTO users(id,username,email,role,auth_source) VALUES('r','r','r@example.test','requester','local')`); err != nil {
		t.Fatal(err)
	}
	svc := requests.NewRequestService(db)
	r, err := svc.SubmitRequest(context.Background(), requests.CreateRequestInput{UserID: "r", Title: "Book", Author: "Author", Format: "epub", Year: 2024})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.acquisition.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRow(`SELECT queue_id FROM acquisition_candidates WHERE request_id=?`, r.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	for _, role := range []string{auth.RoleRequester, auth.RoleCurator} {
		req := httptest.NewRequest("POST", "/api/acquisition/failure", strings.NewReader(`{"queue_id":"`+id+`","error":"download failed"}`))
		req = req.WithContext(auth.ContextWithUser(context.Background(), &auth.User{ID: "r", Role: role}))
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		want := 403
		if role == auth.RoleCurator {
			want = 202
		}
		if w.Code != want {
			t.Fatalf("failure %s: %d", role, w.Code)
		}
	}
	if err := s.acquisition.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs, err := s.acquisition.Candidates(context.Background(), r.ID)
	if err != nil || len(cs) != 1 || cs[0].Penalty != -100 {
		t.Fatalf("candidates %+v %v", cs, err)
	}
	// Manual refresh uses the same search and must log a safe failure when Prowlarr is unavailable.
	mt.RegisterError("/api/v1/search", errors.New("indexer echoed private-prowlarr-key"))
	if err := s.acquisition.Refresh(context.Background(), r.ID); err == nil {
		t.Fatal("expected search failure")
	}
	for _, event := range []string{
		"metadata discovery search started", "metadata provider search completed", "metadata provider search failed",
		"book request created", "acquisition search started", "acquisition candidate scored", "acquisition search completed",
		"requesting Prowlarr grab", "acquisition dispatched", "acquisition search failed", "download client reported acquisition failure",
	} {
		if !strings.Contains(logs.String(), event) {
			t.Errorf("missing log event %q", event)
		}
	}
	for _, private := range []string{"private-prowlarr-key", "private-google-key", "private-hardcover-key", "private-query"} {
		if strings.Contains(logs.String(), private) {
			t.Error("sensitive value appeared in discovery or acquisition logs")
		}
	}
}
