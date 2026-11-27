package indexers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestProwlarrSearchAndGrab(t *testing.T) {
	t.Parallel()
	mt := testutil.NewMockTransport()
	mt.RegisterHandler("/base/api/v1/search", func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Api-Key") != "secret" {
			t.Error("missing API authentication")
		}
		if r.Method == http.MethodGet {
			q := r.URL.Query()
			if q.Get("query") != "Author & Title #2.5" || q.Get("type") != "search" || strings.Join(q["categories"], ",") != "7020,3030" {
				t.Errorf("bad query: %s", r.URL.RawQuery)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"guid":"release/1","indexerId":12,"title":"Book","protocol":"torrent","size":42,"categories":[{"id":3030,"name":"Audio/Audiobook"}]}]`)), Header: make(http.Header)}, nil
		}
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Error("grab must POST JSON")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != 2 || payload["guid"] != "release/1" || payload["indexerId"] != float64(12) {
			t.Errorf("bad grab payload: %v", payload)
		}
		return httptest.NewRecorder().Result(), nil
	})
	c := indexers.NewProwlarrClient("https://prowlarr.test/base/", "secret", mt.Client())
	releases, err := c.Search(context.Background(), "Author & Title #2.5", []int{7020, 3030})
	if err != nil || len(releases) != 1 {
		t.Fatalf("search: %v, %v", releases, err)
	}
	if ids := releases[0].CategoryIDs(); len(ids) != 1 || ids[0] != 3030 {
		t.Fatalf("expected category ID [3030], got %v", ids)
	}
	if err := c.Grab(context.Background(), releases[0]); err != nil {
		t.Fatal(err)
	}
}

func TestProwlarrAcquisitionFailures(t *testing.T) {
	t.Parallel()
	for _, status := range []int{401, 404, 409, 500} {
		mt := testutil.NewMockTransport()
		mt.RegisterStatusCode("/api/v1/search", status)
		c := indexers.NewProwlarrClient("https://prowlarr.test", "secret", mt.Client())
		if _, err := c.Search(context.Background(), "Book", nil); err == nil {
			t.Errorf("search accepted %d", status)
		}
		err := c.Grab(context.Background(), indexers.ProwlarrRelease{GUID: "g", IndexerID: 1})
		if err == nil {
			t.Errorf("grab accepted %d", status)
		}
		if (status == http.StatusNotFound) != errors.Is(err, indexers.ErrReleaseExpired) {
			t.Errorf("status %d: errors.Is(ErrReleaseExpired) = %v", status, errors.Is(err, indexers.ErrReleaseExpired))
		}
	}
	mt := testutil.NewMockTransport()
	c := indexers.NewProwlarrClient("https://prowlarr.test", "secret", mt.Client())
	for _, r := range []indexers.ProwlarrRelease{{}, {GUID: "g"}, {IndexerID: 1}} {
		if c.Grab(context.Background(), r) == nil {
			t.Error("invalid release accepted")
		}
	}
	mt.RegisterString("/api/v1/search", 200, "invalid JSON", nil)
	if _, err := c.Search(context.Background(), "Book", nil); err == nil {
		t.Error("invalid JSON accepted")
	}
	mt.RegisterError("/api/v1/search", errors.New("offline"))
	if _, err := c.Search(context.Background(), "Book", nil); err == nil {
		t.Error("transport error ignored")
	}
	if err := c.Grab(context.Background(), indexers.ProwlarrRelease{GUID: "g", IndexerID: 1}); err == nil {
		t.Error("grab transport error ignored")
	}
}
