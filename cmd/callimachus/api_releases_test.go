package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestAcquisitionAPI(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	for key, value := range map[string]string{"prowlarr_url": "https://prowlarr.test", "prowlarr_api_key": "secret"} {
		if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, key, value); err != nil {
			t.Fatal(err)
		}
	}
	mt := testutil.NewMockTransport()
	mt.RegisterString("/api/v1/search", 200, `[{"guid":"g","indexerId":1,"title":"Author - Book EPUB"}]`, nil)
	srv := &Server{db: db, prowlarrHTTPClient: mt.Client()}
	mux := http.NewServeMux()
	srv.registerAPIRoutes(mux)
	request := func(method, path, body, role string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r = r.WithContext(auth.ContextWithUser(context.Background(), &auth.User{ID: "u", Role: role}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/releases?query=Book", "", auth.RoleCurator); w.Code != 200 {
		t.Fatalf("search: %d %s", w.Code, w.Body.String())
	}
	payload := `{"guid":"g","indexerId":1,"title":"Author - Book EPUB","protocol":"torrent"}`
	if w := request("POST", "/api/releases/grab", payload, auth.RoleRequester); w.Code != 403 {
		t.Fatalf("requester grab: %d", w.Code)
	}
	if w := request("POST", "/api/releases/grab", `{}`, auth.RoleCurator); w.Code != 400 {
		t.Fatalf("invalid grab: %d", w.Code)
	}
	if w := request("POST", "/api/releases/grab", strings.Replace(payload, "EPUB", "EPUB French", 1), auth.RoleCurator); w.Code != 400 {
		t.Fatalf("foreign release: %d", w.Code)
	}
	if w := request("POST", "/api/releases/grab", payload, auth.RoleCurator); w.Code != 202 {
		t.Fatalf("grab: %d %s", w.Code, w.Body.String())
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM download_queue WHERE title = 'Author - Book EPUB'`).Scan(&status); err != nil || status != "queued" {
		t.Fatalf("queue: %s %v", status, err)
	}
	mt.RegisterStatusCode("/api/v1/search", 409)
	if w := request("POST", "/api/releases/grab", payload, auth.RoleAdmin); w.Code != 502 {
		t.Fatalf("rejected grab: %d", w.Code)
	}
	var failed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM download_queue WHERE status = 'failed'`).Scan(&failed); err != nil || failed != 1 {
		t.Fatalf("failed grabs: %d %v", failed, err)
	}
}
