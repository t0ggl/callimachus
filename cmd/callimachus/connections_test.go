package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func withAdminUser(req *http.Request) *http.Request {
	return req.WithContext(auth.ContextWithUser(context.Background(), &auth.User{
		ID:       "admin-test",
		Username: "admin",
		Role:     auth.RoleAdmin,
	}))
}

func TestConnectionChecks(t *testing.T) {
	for _, service := range []string{"prowlarr", "google-books", "hardcover"} {
		for _, outcome := range []string{"success", "unauthorized", "forbidden", "rate-limit", "server-error", "malformed", "wrong-shape", "graphql-error", "network", "redirect"} {
			t.Run(service+"/"+outcome, func(t *testing.T) {
				db := testutil.NewTestDB(t)
				for key, value := range map[string]string{"prowlarr_url": "http://stored", "prowlarr_api_key": "stored-secret", "google_books_api_key": "stored-secret", "hardcover_api_key": "stored-secret"} {
					if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, key, value); err != nil {
						t.Fatal(err)
					}
				}
				mt := testutil.NewMockTransportForTest(t)
				path := "/api/v1/system/status"
				body := `{"version":"1.0"}`
				if service == "google-books" {
					path, body = "/books/v1/volumes", `{"kind":"books#volumes","totalItems":0}`
				}
				if service == "hardcover" {
					path, body = "/v1/graphql", `{"data":{"me":[{"id":1}]}}`
				}
				calls := 0
				mt.RegisterHandler(path, func(req *http.Request) (*http.Response, error) {
					calls++
					switch service {
					case "prowlarr":
						if req.URL.Host != "draft" || req.Header.Get("X-Api-Key") != "draft-secret" {
							t.Fatal("draft values not used")
						}
					case "google-books":
						if req.URL.Query().Get("key") != "draft-secret" {
							t.Fatal("missing draft key")
						}
					case "hardcover":
						if req.Method != "POST" || req.Header.Get("Authorization") != "Bearer draft-secret" {
							t.Fatal("missing authorization")
						}
						b, _ := io.ReadAll(req.Body)
						if !strings.Contains(string(b), "me { id }") {
							t.Fatal("unexpected query")
						}
					}
					code := 200
					switch outcome {
					case "unauthorized":
						code = 401
					case "forbidden":
						code = 403
					case "rate-limit":
						code = 429
					case "server-error":
						code = 500
					case "malformed":
						body = "not json draft-secret"
					case "wrong-shape":
						body = `{}`
					case "graphql-error":
						body = `{"errors":[{"message":"draft-secret"}]}`
					case "network":
						return nil, errors.New("transport includes draft-secret")
					case "redirect":
						code = 302
					}
					return &http.Response{StatusCode: code, Header: http.Header{"Location": []string{"https://elsewhere/"}}, Body: io.NopCloser(strings.NewReader(body + " ")), Request: req}, nil
				})
				s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
				mux := http.NewServeMux()
				s.registerAPIRoutes(mux)
				req := withAdminUser(httptest.NewRequest("POST", "/api/settings/"+service+"/test", strings.NewReader(`{"url":"http://draft","apiKey":"draft-secret"}`)))
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)
				want := 502
				if outcome == "success" {
					want = 200
				}
				if w.Code != want || calls != 1 {
					t.Fatalf("status %d want %d; calls %d; %s", w.Code, want, calls, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "secret") {
					t.Fatal("secret exposed")
				}
				var stored string
				_ = db.QueryRow(`SELECT value FROM settings WHERE key='prowlarr_api_key'`).Scan(&stored)
				if stored != "stored-secret" {
					t.Fatal("settings mutated")
				}
				var count int
				_ = db.QueryRow(`SELECT count(*) FROM indexers`).Scan(&count)
				if count != 0 {
					t.Fatal("indexers synchronized")
				}
			})
		}
	}
}

func TestConnectionCredentialPrecedence(t *testing.T) {
	for _, service := range []string{"prowlarr", "google-books", "hardcover"} {
		for _, mode := range []string{"stored", "environment", "missing"} {
			t.Run(service+"/"+mode, func(t *testing.T) {
				db := testutil.NewTestDB(t)
				setting, env, path := "prowlarr_api_key", "CALLIMACHUS_PROWLARR_API_KEY", "/api/v1/system/status"
				if service == "google-books" {
					setting, env, path = "google_books_api_key", "CALLIMACHUS_GOOGLE_BOOKS_API_KEY", "/books/v1/volumes"
				}
				if service == "hardcover" {
					setting, env, path = "hardcover_api_key", "CALLIMACHUS_HARDCOVER_API_KEY", "/v1/graphql"
				}
				t.Setenv(env, "")
				t.Setenv("CALLIMACHUS_PROWLARR_URL", "")
				_, err := db.Exec(`INSERT INTO settings(key,value) VALUES('prowlarr_url','http://stored')`)
				if err != nil {
					t.Fatal(err)
				}
				wantKey := "stored-secret"
				if mode != "missing" {
					if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, setting, wantKey); err != nil {
						t.Fatal(err)
					}
				} else {
					wantKey = ""
				}
				draft := `{}`
				if mode == "environment" {
					t.Setenv(env, "env-secret")
					wantKey = "env-secret"
					draft = `{"apiKey":"ignored-secret","url":"http://ignored"}`
					if service == "prowlarr" {
						t.Setenv("CALLIMACHUS_PROWLARR_URL", "http://environment")
					}
				}
				mt := testutil.NewMockTransportForTest(t)
				mt.RegisterHandler(path, func(req *http.Request) (*http.Response, error) {
					got := req.Header.Get("X-Api-Key")
					body := `{"version":"1"}`
					if service == "google-books" {
						got = req.URL.Query().Get("key")
						body = `{"kind":"books#volumes"}`
					}
					if service == "hardcover" {
						got = strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
						body = `{"data":{"me":[{"id":1}]}}`
					}
					if got != wantKey {
						t.Fatalf("incorrect credential precedence")
					}
					if service == "prowlarr" && mode == "environment" && req.URL.Host != "environment" {
						t.Fatal("URL override ignored")
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
				})
				s := &Server{db: db, prowlarrHTTPClient: mt.Client()}
				mux := http.NewServeMux()
				s.registerAPIRoutes(mux)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, withAdminUser(httptest.NewRequest("POST", "/api/settings/"+service+"/test", strings.NewReader(draft))))
				want := 200
				if mode == "missing" && service != "google-books" {
					want = 400
				}
				if w.Code != want {
					t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
				}
			})
		}
	}
}

func TestConnectionInputValidation(t *testing.T) {
	for _, body := range []string{`bad`, `{"url":"file:///etc/passwd"}`, `{"url":"http://user:secret@host"}`, `{"url":"http://host?key=secret"}`, `{"url":""}`} {
		s := &Server{}
		mux := http.NewServeMux()
		s.registerAPIRoutes(mux)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, withAdminUser(httptest.NewRequest("POST", "/api/settings/prowlarr/test", strings.NewReader(body))))
		if w.Code != 400 || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
		}
	}
}
