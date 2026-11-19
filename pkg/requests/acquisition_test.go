package requests

import (
	"context"
	"fmt"
	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/testutil"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScoreCandidate(t *testing.T) {
	c := ScoreCandidate(indexers.ProwlarrRelease{Title: "Book EPUB 2024", Seeders: 50, Verified: true}, 2024, []string{"EPUB", "PDF"})
	if c.Score != 75 || c.Format != "epub" {
		t.Fatalf("%+v", c)
	}
	if c := ScoreCandidate(indexers.ProwlarrRelease{Title: "Book"}, 0, nil); c.Score != 0 {
		t.Fatal(c)
	}
	c = ScoreCandidate(indexers.ProwlarrRelease{Title: "Book PDF", Seeders: 1}, 0, []string{"EPUB", "PDF"})
	if c.Score != 35 {
		t.Fatal(c)
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
	}, Grab: func(context.Context, indexers.ProwlarrRelease) error { grabs++; return nil }, Evaluate: func(context.Context, indexers.ProwlarrRelease) (bool, []string, error) {
		return true, []string{"EPUB"}, nil
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
	a.Evaluate = func(context.Context, indexers.ProwlarrRelease) (bool, []string, error) { return false, nil, nil }
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
	a.Evaluate = func(_ context.Context, r indexers.ProwlarrRelease) (bool, []string, error) {
		return r.GUID != "excluded", []string{"EPUB"}, nil
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
