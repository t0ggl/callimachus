package requests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScoreCandidate(t *testing.T) {
	c := ScoreCandidate(
		indexers.ProwlarrRelease{Title: "Book EPUB 2024", Seeders: 10, Verified: true},
		2024,
		FormatEPUB,
		Evaluation{Format: "epub", EbookPreferences: []string{"EPUB", "PDF"}},
	)
	if c.Score != 75 || c.Format != "epub" || c.Breakdown["format"] != 30 || c.Breakdown["seeders"] != 20 {
		t.Fatalf("%+v", c)
	}
	if c := ScoreCandidate(indexers.ProwlarrRelease{Title: "Book"}, 0, "", Evaluation{}); c.Score != 0 {
		t.Fatal(c)
	}
	c = ScoreCandidate(
		indexers.ProwlarrRelease{Title: "Book PDF", Seeders: 9},
		0,
		FormatEPUB,
		Evaluation{Format: "pdf", EbookPreferences: []string{"EPUB", "PDF"}},
	)
	if c.Score != 35 || c.Breakdown["format"] != 25 || c.Breakdown["seeders"] != 10 {
		t.Fatal(c)
	}
	for _, proto := range []string{"usenet", "Usenet", "newznab"} {
		usenet := ScoreCandidate(
			indexers.ProwlarrRelease{Title: "Book EPUB", Protocol: proto, Seeders: 0},
			0,
			FormatEPUB,
			Evaluation{Format: "epub", EbookPreferences: []string{"EPUB", "PDF"}},
		)
		if usenet.Score != 50 || usenet.Breakdown["usenet"] != 20 {
			t.Fatalf("protocol %q: expected score 50 with usenet=+20, got %+v", proto, usenet)
		}
		if _, hasSeeders := usenet.Breakdown["seeders"]; hasSeeders {
			t.Fatalf("protocol %q: unexpected seeders key in breakdown: %+v", proto, usenet.Breakdown)
		}
	}
}

func TestScoreCandidate_FormatPreferencesAndExclusions(t *testing.T) {
	prefs := []string{"epub", "azw3", "mobi", "pdf", "cbr", "cbz"}
	var prev int
	for i, fmtName := range prefs {
		c := ScoreCandidate(
			indexers.ProwlarrRelease{Title: "Single Book " + fmtName},
			0,
			FormatEPUB,
			Evaluation{Format: fmtName, EbookPreferences: prefs},
		)
		if i > 0 && c.Breakdown["format"] >= prev {
			t.Fatalf("format %s (%d) should score strictly lower than previous preference (%d)", fmtName, c.Breakdown["format"], prev)
		}
		prev = c.Breakdown["format"]
	}

	// Reordering preferences changes scores accordingly.
	reordered := []string{"pdf", "epub"}
	pdfCand := ScoreCandidate(indexers.ProwlarrRelease{Title: "Book PDF"}, 0, FormatEPUB, Evaluation{Format: "pdf", EbookPreferences: reordered})
	epubCand := ScoreCandidate(indexers.ProwlarrRelease{Title: "Book EPUB"}, 0, FormatEPUB, Evaluation{Format: "epub", EbookPreferences: reordered})
	if pdfCand.Breakdown["format"] <= epubCand.Breakdown["format"] {
		t.Fatalf("reordered preference did not raise PDF (%d) above EPUB (%d)", pdfCand.Breakdown["format"], epubCand.Breakdown["format"])
	}

	// Disabled format gets format: -1000 only — no positive format points and no "quality" key.
	disabled := ScoreCandidate(
		indexers.ProwlarrRelease{Title: "Book AZW3", Seeders: 20},
		0,
		FormatEPUB,
		Evaluation{
			Format:           "azw3",
			EbookPreferences: []string{"epub"},
			Exclusions:       map[string]string{"format": `format "azw3" not allowed`},
		},
	)
	if disabled.Breakdown["format"] != -1000 {
		t.Fatalf("disabled format should score format=-1000, got %+v", disabled.Breakdown)
	}
	if _, hasQuality := disabled.Breakdown["quality"]; hasQuality {
		t.Fatalf("unexpected generic quality key in breakdown: %+v", disabled.Breakdown)
	}

	// Audiobook preference: M4B scores higher than MP3.
	m4b := ScoreCandidate(indexers.ProwlarrRelease{Title: "Book M4B"}, 0, FormatM4B, Evaluation{Format: "m4b"})
	mp3 := ScoreCandidate(indexers.ProwlarrRelease{Title: "Book MP3"}, 0, FormatM4B, Evaluation{Format: "mp3"})
	if m4b.Breakdown["format"] <= mp3.Breakdown["format"] {
		t.Fatalf("M4B (%d) should score higher than MP3 (%d)", m4b.Breakdown["format"], mp3.Breakdown["format"])
	}

	// Multi-book release is marked down under multi-book and excluded.
	multi := ScoreCandidate(
		indexers.ProwlarrRelease{Title: "Philip.Pullman-His.Dark.Materials.01-03.His.Dark.Materials.(The.Golden.Compass.EPUB", Seeders: 50},
		0,
		FormatEPUB,
		Evaluation{Format: "epub", EbookPreferences: []string{"epub"}},
	)
	if multi.Breakdown["multi-book"] != -1000 || !multi.IsExcluded() || multi.Score >= 0 {
		t.Fatalf("multi-book release not excluded: %+v", multi)
	}

	// Unknown format leaves the format breakdown blank (not negative) for both eBook and audiobook requests.
	for _, reqFmt := range []string{FormatEPUB, FormatM4B, FormatBoth} {
		unknown := ScoreCandidate(
			indexers.ProwlarrRelease{Title: "Single Book Without Format", Seeders: 10},
			0,
			reqFmt,
			Evaluation{EbookPreferences: []string{"epub", "pdf"}},
		)
		if _, hasFormat := unknown.Breakdown["format"]; hasFormat {
			t.Fatalf("unknown format for %s request should omit format score, got %+v", reqFmt, unknown.Breakdown)
		}
		if unknown.IsExcluded() || unknown.Score != 20 {
			t.Fatalf("unknown format for %s request should not be excluded, got %+v", reqFmt, unknown)
		}
	}

	// Definite format mismatches still score format=-1000.
	audioForEbook := ScoreCandidate(
		indexers.ProwlarrRelease{Title: "Book M4B", Seeders: 20},
		0,
		FormatEPUB,
		Evaluation{Format: "m4b", EbookPreferences: []string{"epub"}},
	)
	if audioForEbook.Breakdown["format"] != -1000 || !audioForEbook.IsExcluded() {
		t.Fatalf("m4b for epub request should score format=-1000, got %+v", audioForEbook)
	}
	ebookForAudio := ScoreCandidate(
		indexers.ProwlarrRelease{Title: "Book EPUB", Seeders: 20},
		0,
		FormatM4B,
		Evaluation{Format: "epub", EbookPreferences: []string{"epub"}},
	)
	if ebookForAudio.Breakdown["format"] != -1000 || !ebookForAudio.IsExcluded() {
		t.Fatalf("epub for m4b request should score format=-1000, got %+v", ebookForAudio)
	}
}

func acquisitionFixture(t *testing.T) (*Acquisition, int64, *int, *int) {
	t.Helper()
	db := testutil.NewTestDB(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,email,role,auth_source) VALUES('reader','reader','reader@example.test','requester','local')`); err != nil {
		t.Fatal(err)
	}
	svc := NewRequestService(db)
	r, err := svc.SubmitRequest(context.Background(), CreateRequestInput{UserID: "reader", Title: "Book", Author: "Author", Format: "epub"})
	if err != nil {
		t.Fatal(err)
	}
	searches, grabs := 0, 0
	a := &Acquisition{DB: db, Search: func(context.Context, string, string) ([]indexers.ProwlarrRelease, error) {
		searches++
		out := []indexers.ProwlarrRelease{}
		for i := 0; i < 5; i++ {
			out = append(out, indexers.ProwlarrRelease{GUID: fmt.Sprint(i), IndexerID: 1, Title: "Book EPUB", Seeders: i + 1})
		}
		return out, nil
	}, Grab: func(context.Context, indexers.ProwlarrRelease) error { grabs++; return nil }, Evaluate: func(context.Context, indexers.ProwlarrRelease, string) (Evaluation, error) {
		return Evaluation{Format: "epub", EbookPreferences: []string{"EPUB"}}, nil
	}}
	return a, r.ID, &searches, &grabs
}
func mustExec(t *testing.T, a *Acquisition, q string, args ...any) {
	t.Helper()
	if _, err := a.DB.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func TestFallbackCeilingAndPermanentPenalty(t *testing.T) {
	a, id, _, grabs := acquisitionFixture(t)
	ctx := context.Background()
	a.Grab = func(context.Context, indexers.ProwlarrRelease) error { *grabs++; return fmt.Errorf("failed") }
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if *grabs != 3 {
		t.Fatalf("attempts %d", *grabs)
	}
	var status string
	a.DB.QueryRow(`SELECT status FROM book_requests WHERE id=?`, id).Scan(&status)
	if status != "error" {
		t.Fatal(status)
	}
	if err := a.Refresh(ctx, id); err != nil {
		t.Fatal(err)
	}
	cs, err := a.Candidates(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, c := range cs {
		if c.Penalty == -100 {
			if c.Score >= 0 {
				t.Fatalf("failed release score must be negative: %+v", c)
			}
			failed++
		}
	}
	if failed != 3 {
		t.Fatalf("penalties %d", failed)
	}
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if *grabs != 3 {
		t.Fatal("retried exhausted request")
	}
}
func TestAsyncFailureAndSuccessRetention(t *testing.T) {
	a, id, _, grabs := acquisitionFixture(t)
	ctx := context.Background()
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	mustExec(t, a, `UPDATE download_queue SET status='failed'`)
	// Reconstruct the engine to ensure the ceiling and exclusions survive restarts.
	b := &Acquisition{DB: a.DB, Search: a.Search, Grab: a.Grab, Evaluate: a.Evaluate}
	if err := b.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if *grabs != 2 {
		t.Fatal(*grabs)
	}
	mustExec(t, a, `UPDATE download_queue SET status='completed' WHERE status='queued'`)
	if err := b.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	cs, _ := a.Candidates(ctx, id)
	if len(cs) != 5 {
		t.Fatal("early eviction")
	}
	mustExec(t, a, `UPDATE book_requests SET completed_at=datetime('now','-29 days') WHERE id=?`, id)
	if err := a.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	cs, _ = a.Candidates(ctx, id)
	if len(cs) != 5 {
		t.Fatal("29 day eviction")
	}
	mustExec(t, a, `UPDATE book_requests SET completed_at=datetime('now','-30 days') WHERE id=?`, id)
	if err := a.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	cs, _ = a.Candidates(ctx, id)
	if len(cs) != 0 {
		t.Fatal("cache retained")
	}
	var failures int
	a.DB.QueryRow(`SELECT count(*) FROM failed_releases`).Scan(&failures)
	if failures != 1 {
		t.Fatal("failure tombstones lost")
	}
}
func TestNoPositiveCandidatesDailyRecheck(t *testing.T) {
	a, id, searches, grabs := acquisitionFixture(t)
	ctx := context.Background()
	a.Evaluate = func(context.Context, indexers.ProwlarrRelease, string) (Evaluation, error) {
		return Evaluation{Exclusions: map[string]string{"format": "not allowed"}}, nil
	}
	for i := 0; i < 2; i++ {
		if err := a.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if *searches != 1 || *grabs != 0 {
		t.Fatalf("searches=%d grabs=%d", *searches, *grabs)
	}
	mustExec(t, a, `UPDATE book_requests SET searched_at=datetime('now','-1 day') WHERE id=?`, id)
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if *searches != 2 {
		t.Fatal(*searches)
	}
}

func TestManualOverrideAndCandidateAuthorization(t *testing.T) {
	a, id, _, grabs := acquisitionFixture(t)
	ctx := context.Background()
	if err := a.Refresh(ctx, id); err != nil {
		t.Fatal(err)
	}
	cs, _ := a.Candidates(ctx, id)
	key := cs[0].Key
	if err := a.fail(ctx, key); err != nil {
		t.Fatal(err)
	}
	mustExec(t, a, `UPDATE book_requests SET status='error' WHERE id=?`, id)
	if err := a.Download(ctx, id, key); err != nil {
		t.Fatal(err)
	}
	if *grabs != 1 {
		t.Fatal("manual override failed")
	}
	if err := a.Download(ctx, id, key); err == nil {
		t.Fatal("duplicate concurrent dispatch")
	}
	svc := NewRequestService(a.DB)
	h := NewHandler(HandlerConfig{Service: svc, DB: a.DB, Acquisition: a})
	t.Cleanup(h.sessionMgr.Close)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	for _, tc := range []struct {
		user string
		role string
		code int
	}{{"other", auth.RoleRequester, 403}, {"reader", auth.RoleRequester, 200}, {"other", auth.RoleCurator, 200}} {
		r := httptest.NewRequest("GET", fmt.Sprintf("/api/requests/%d/candidates", id), nil)
		r = r.WithContext(auth.ContextWithUser(ctx, &auth.User{ID: tc.user, Role: tc.role}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.user, w.Code, w.Body.String())
		}
	}
}

func TestHighestPositiveCandidateWins(t *testing.T) {
	a, id, _, grabs := acquisitionFixture(t)
	a.Search = func(context.Context, string, string) ([]indexers.ProwlarrRelease, error) {
		return []indexers.ProwlarrRelease{{GUID: "low", IndexerID: 1, Title: "Book EPUB"}, {GUID: "best", IndexerID: 1, Title: "Book EPUB", Seeders: 100, Verified: true}, {GUID: "excluded", IndexerID: 1, Title: "foreign EPUB", Seeders: 200}}, nil
	}
	a.Evaluate = func(_ context.Context, r indexers.ProwlarrRelease, _ string) (Evaluation, error) {
		if r.GUID == "excluded" {
			return Evaluation{Format: "epub", EbookPreferences: []string{"EPUB"}, Exclusions: map[string]string{"language": "foreign"}}, nil
		}
		return Evaluation{Format: "epub", EbookPreferences: []string{"EPUB"}}, nil
	}
	a.Grab = func(_ context.Context, r indexers.ProwlarrRelease) error {
		*grabs++
		if r.GUID != "best" {
			t.Fatalf("selected %s", r.GUID)
		}
		return nil
	}
	if err := a.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *grabs != 1 {
		t.Fatal(*grabs)
	}
	cs, err := a.Candidates(context.Background(), id)
	if err != nil || len(cs) != 3 {
		t.Fatalf("%+v %v", cs, err)
	}
	if cs[2].Score >= 0 {
		t.Fatal("quality exclusion not scored")
	}
}

func TestFailurePenaltyMakesMaximumScoreNegative(t *testing.T) {
	a, id, _, _ := acquisitionFixture(t)
	ctx := context.Background()
	mustExec(t, a, `UPDATE book_requests SET year=2024 WHERE id=?`, id)
	a.Search = func(context.Context, string, string) ([]indexers.ProwlarrRelease, error) {
		return []indexers.ProwlarrRelease{{GUID: "max", IndexerID: 1, Title: "Book EPUB 2024", Seeders: 50, Verified: true}}, nil
	}
	if err := a.Refresh(ctx, id); err != nil {
		t.Fatal(err)
	}
	mustExec(t, a, `INSERT INTO failed_releases(release_key,penalty) VALUES('1:max',-50)`)
	if err := a.fail(ctx, "1:max"); err != nil {
		t.Fatal(err)
	}
	cs, err := a.Candidates(ctx, id)
	if err != nil || len(cs) != 1 || cs[0].Penalty != -100 || cs[0].Score != -25 {
		t.Fatalf("maximum-score failure: %+v %v", cs, err)
	}
}

func TestManualDownloadOverridesExclusionsWhileAutoRequiresPositiveScore(t *testing.T) {
	a, id, _, grabs := acquisitionFixture(t)
	ctx := context.Background()
	a.Search = func(context.Context, string, string) ([]indexers.ProwlarrRelease, error) {
		return []indexers.ProwlarrRelease{
			{GUID: "excluded", IndexerID: 1, Title: "Book AZW3 Oversized", Size: 200 * 1024 * 1024, Seeders: 10},
			{GUID: "zero", IndexerID: 1, Title: "Book Unknown"},
		}, nil
	}
	a.Evaluate = func(_ context.Context, r indexers.ProwlarrRelease, _ string) (Evaluation, error) {
		if r.GUID == "excluded" {
			return Evaluation{
				Format:           "azw3",
				EbookPreferences: []string{"epub"},
				Exclusions: map[string]string{
					"format": `format "azw3" not allowed`,
					"size":   "release exceeds max size",
				},
			}, nil
		}
		return Evaluation{EbookPreferences: []string{"epub"}}, nil
	}
	var grabbed string
	a.Grab = func(_ context.Context, r indexers.ProwlarrRelease) error {
		*grabs++
		grabbed = r.GUID
		return nil
	}

	// Automatic acquisition skips both zero-score and excluded (negative-score) candidates.
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if *grabs != 0 {
		t.Fatalf("automatic acquisition should not grab non-positive candidates, got %d grabs", *grabs)
	}

	// Manual download overrides exclusions (e.g. format, size) and dispatches the selected candidate.
	if err := a.Download(ctx, id, "1:excluded"); err != nil {
		t.Fatalf("manual download of excluded candidate failed: %v", err)
	}
	if *grabs != 1 || grabbed != "excluded" {
		t.Fatalf("expected manual grab of excluded candidate, got grabs=%d guid=%q", *grabs, grabbed)
	}
}

func TestDownloadRetriesExpiredReleaseAfterRefresh(t *testing.T) {
	t.Run("succeeds on retry when same guid is still present and refreshes list", func(t *testing.T) {
		a, id, searches, grabs := acquisitionFixture(t)
		ctx := context.Background()
		a.Search = func(context.Context, string, string) ([]indexers.ProwlarrRelease, error) {
			*searches++
			if *searches == 1 {
				return []indexers.ProwlarrRelease{
					{GUID: "g1", IndexerID: 1, Title: "Book EPUB", Seeders: 10},
				}, nil
			}
			return []indexers.ProwlarrRelease{
				{GUID: "g1", IndexerID: 1, Title: "Book EPUB", Seeders: 25},
				{GUID: "g2", IndexerID: 1, Title: "Book EPUB New", Seeders: 50},
			}, nil
		}
		a.Grab = func(_ context.Context, r indexers.ProwlarrRelease) error {
			*grabs++
			if *grabs == 1 {
				return fmt.Errorf("prowlarr grab: HTTP 404: %w", indexers.ErrReleaseExpired)
			}
			if r.GUID != "g1" {
				t.Fatalf("expected retry with guid g1, got %q", r.GUID)
			}
			return nil
		}

		if err := a.Refresh(ctx, id); err != nil {
			t.Fatal(err)
		}

		svc := NewRequestService(a.DB)
		h := NewHandler(HandlerConfig{Service: svc, DB: a.DB, Acquisition: a})
		t.Cleanup(h.sessionMgr.Close)
		mux := http.NewServeMux()
		h.RegisterRoutes(mux)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/requests/%d/download", id), bytes.NewBufferString(`{"key":"1:g1"}`))
		req = req.WithContext(auth.ContextWithUser(ctx, &auth.User{ID: "reader", Role: auth.RoleRequester}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 after automatic retry, got %d: %s", w.Code, w.Body.String())
		}
		if *searches != 2 || *grabs != 2 {
			t.Fatalf("expected 2 searches and 2 grabs, got searches=%d grabs=%d", *searches, *grabs)
		}
		var refreshed []Candidate
		if err := json.Unmarshal(w.Body.Bytes(), &refreshed); err != nil {
			t.Fatal(err)
		}
		if len(refreshed) != 2 {
			t.Fatalf("expected 2 refreshed candidates, got %+v", refreshed)
		}
	})

	t.Run("returns meaningful error and refreshed candidates when release disappears or still 404s", func(t *testing.T) {
		for _, tc := range []struct {
			name            string
			secondSearch    []indexers.ProwlarrRelease
			wantGrabs       int
			wantCandidateID string
		}{
			{
				name: "missing from refreshed search",
				secondSearch: []indexers.ProwlarrRelease{
					{GUID: "g-new", IndexerID: 1, Title: "Book EPUB Fresh", Seeders: 40},
				},
				wantGrabs:       1,
				wantCandidateID: "1:g-new",
			},
			{
				name: "present in refreshed search but grab still 404s",
				secondSearch: []indexers.ProwlarrRelease{
					{GUID: "g1", IndexerID: 1, Title: "Book EPUB", Seeders: 10},
					{GUID: "g-new", IndexerID: 1, Title: "Book EPUB Fresh", Seeders: 40},
				},
				wantGrabs:       2,
				wantCandidateID: "1:g-new",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				a, id, searches, grabs := acquisitionFixture(t)
				ctx := context.Background()
				a.Search = func(context.Context, string, string) ([]indexers.ProwlarrRelease, error) {
					*searches++
					if *searches == 1 {
						return []indexers.ProwlarrRelease{
							{GUID: "g1", IndexerID: 1, Title: "Book EPUB", Seeders: 10},
						}, nil
					}
					return tc.secondSearch, nil
				}
				a.Grab = func(_ context.Context, r indexers.ProwlarrRelease) error {
					*grabs++
					if r.GUID == "g1" {
						return fmt.Errorf("prowlarr grab: HTTP 404: %w", indexers.ErrReleaseExpired)
					}
					return nil
				}

				if err := a.Refresh(ctx, id); err != nil {
					t.Fatal(err)
				}

				svc := NewRequestService(a.DB)
				h := NewHandler(HandlerConfig{Service: svc, DB: a.DB, Acquisition: a})
				t.Cleanup(h.sessionMgr.Close)
				mux := http.NewServeMux()
				h.RegisterRoutes(mux)

				req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/requests/%d/download", id), bytes.NewBufferString(`{"key":"1:g1"}`))
				req = req.WithContext(auth.ContextWithUser(ctx, &auth.User{ID: "reader", Role: auth.RoleRequester}))
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)

				if w.Code != http.StatusNotFound {
					t.Fatalf("expected 404 when release cannot be found after refresh, got %d: %s", w.Code, w.Body.String())
				}
				if *searches != 2 || *grabs != tc.wantGrabs {
					t.Fatalf("expected 2 searches and %d grabs, got searches=%d grabs=%d", tc.wantGrabs, *searches, *grabs)
				}

				var body struct {
					Error      string      `json:"error"`
					Candidates []Candidate `json:"candidates"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(strings.ToLower(body.Error), "could not find the requested download") {
					t.Fatalf("unexpected error message: %q", body.Error)
				}
				foundNew := false
				for _, c := range body.Candidates {
					if c.Key == tc.wantCandidateID {
						foundNew = true
					}
				}
				if !foundNew {
					t.Fatalf("expected refreshed candidate %s in error response, got %+v", tc.wantCandidateID, body.Candidates)
				}

				// Request status must be restored so the user can immediately download from the refreshed list.
				if err := a.Download(ctx, id, tc.wantCandidateID); err != nil {
					t.Fatalf("subsequent download of refreshed candidate failed: %v", err)
				}
			})
		}
	})
}

func TestDownloadDoesNotRetryNon404Errors(t *testing.T) {
	a, id, searches, grabs := acquisitionFixture(t)
	ctx := context.Background()
	if err := a.Refresh(ctx, id); err != nil {
		t.Fatal(err)
	}
	a.Grab = func(context.Context, indexers.ProwlarrRelease) error {
		*grabs++
		return errors.New("prowlarr grab: HTTP 500")
	}
	if err := a.Download(ctx, id, "1:0"); err == nil {
		t.Fatal("expected grab error")
	}
	if *searches != 1 || *grabs != 1 {
		t.Fatalf("non-404 error must not trigger search retry: searches=%d grabs=%d", *searches, *grabs)
	}
}
