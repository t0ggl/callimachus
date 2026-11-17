package main

import (
	"context"
	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/requests"
	"github.com/t0ggl/callimachus/pkg/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoveryAndAutomaticAcquisition(t *testing.T) {
	db := testutil.NewTestDB(t)
	mt := testutil.NewMockTransport()
	mt.RegisterString("/search.json", 200, `{"docs":[{"key":"/works/1","title":"Book","author_name":["Author"],"first_publish_year":2024}]}`, nil)
	mt.RegisterString("/books/v1/volumes", 200, `{"items":[{"volumeInfo":{"title":"Other","authors":["Writer"],"publishedDate":"2023"}}]}`, nil)
	mt.RegisterString("/api/v1/search", 200, `[{"guid":"release","indexerId":1,"title":"Author - Book EPUB 2024","seeders":50}]`, nil)
	for k, v := range map[string]string{"prowlarr_url": "https://prowlarr.test", "prowlarr_api_key": "secret", "google_books_api_key": "test-key"} {
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
}
