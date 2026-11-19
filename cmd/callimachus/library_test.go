package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/requests"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestLibraryScanDiscoveryAndMissingFiles(t *testing.T) {
	logs := testutil.CaptureLogs(t)
	db := testutil.NewTestDB(t)
	root := t.TempDir()
	dir := filepath.Join(root, "Author", "Series", "Book (2024)")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Book (2024) - Author.epub")
	if err := os.WriteFile(path, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	mt := testutil.NewMockTransport()
	mt.RegisterString("/search.json", 200, `{"docs":[{"key":"/works/1","title":"Book","author_name":["Author"]}]}`, nil)
	s := &Server{db: db, baseOverrides: config.Overrides{LibraryRoot: root}, prowlarrHTTPClient: mt.Client()}
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)
	for _, role := range []string{auth.RoleRequester, auth.RoleCurator} {
		req := httptest.NewRequest("POST", "/api/library/scan", nil)
		req = req.WithContext(auth.ContextWithUser(context.Background(), &auth.User{ID: "user", Role: role}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		want := 403
		if role == auth.RoleCurator {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("%s scan %d: %s", role, w.Code, w.Body)
		}
	}
	if n, err := s.scanLibrary(context.Background()); err != nil || n != 1 {
		t.Fatalf("rescan: %d %v", n, err)
	}
	discover := func() string {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleDiscover(w, httptest.NewRequest("GET", "/api/discover?q=Book", nil))
		var matches []struct {
			LibraryBookID string `json:"library_book_id"`
		}
		if w.Code != 200 {
			t.Fatalf("discovery: %d %s", w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &matches); err != nil || len(matches) != 1 {
			t.Fatalf("matches %s %v", w.Body, err)
		}
		return matches[0].LibraryBookID
	}
	if discover() == "" {
		t.Fatal("local match missing")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.scanLibrary(context.Background()); err != nil {
		t.Fatal(err)
	}
	if discover() != "" {
		t.Fatal("missing media still marked in library")
	}
	for _, event := range []string{"library scan started", "library scan completed"} {
		if !strings.Contains(logs.String(), event) {
			t.Errorf("missing log event %q", event)
		}
	}
}

func TestUpgradeDispatchRetainsLibraryEdition(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, query := range []string{
		`INSERT INTO users(id,username,email,role,auth_source) VALUES('u','u','u@example.test','admin','local')`,
		`INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author')`,
		`INSERT INTO books(id,author_id,title) VALUES('b','a','Book')`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('e','b','epub')`,
		`INSERT INTO book_requests(id,user_id,book_id,title,author,format,status) VALUES(1,'u','b','Book','Author','epub','available')`,
		`INSERT INTO acquisition_candidates(request_id,release_key,release_json,score_json,score,format) VALUES(1,'1:release','{"guid":"release","indexerId":1,"title":"Author - Book EPUB","protocol":"torrent"}','{}',10,'epub')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	a := &requests.Acquisition{DB: db, Evaluate: func(context.Context, indexers.ProwlarrRelease) (bool, []string, error) {
		return true, []string{"epub"}, nil
	}, Grab: func(context.Context, indexers.ProwlarrRelease) error { return nil }}
	if err := a.Download(context.Background(), 1, "1:release"); err != nil {
		t.Fatal(err)
	}
	var edition string
	if err := db.QueryRow("SELECT edition_id FROM download_queue").Scan(&edition); err != nil || edition != "e" {
		t.Fatalf("edition %s %v", edition, err)
	}
}
