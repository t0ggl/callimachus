package requests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/indexers"
)

func activityRequest(mux *http.ServeMux, method string, id int64, user, role string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, fmt.Sprintf("/api/requests/%d", id), nil)
	if user != "" {
		r = r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{ID: user, Role: role}))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestSplitAcquisitionAndSearchLog(t *testing.T) {
	a, first, _, _ := acquisitionFixture(t)
	mustExec(t, a, "DELETE FROM book_requests WHERE id=?", first)
	ctx := context.Background()
	svc := NewRequestService(a.DB, ServiceConfig{WeeklyLimit: 1})
	var triggered []string
	svc.SetSearchTrigger(SearchTriggerFunc(func(_ context.Context, r *BookRequest) error {
		triggered = append(triggered, r.Format)
		return nil
	}))
	input := CreateRequestInput{UserID: "reader", Title: "Book", Author: "Author", Format: FormatBoth, Year: 2024,
		Series: "Series", Synopsis: "Synopsis", CoverURL: "https://example.test/cover.jpg"}
	if _, err := svc.SubmitRequest(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitRequest(ctx, input); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected quota enforcement, got %v", err)
	}
	list, err := svc.ListRequests(ctx, RequestFilter{})
	if err != nil || len(list) != 2 || list[0].ID == list[1].ID || list[0].BookID == nil || list[1].BookID == nil ||
		*list[0].BookID != *list[1].BookID || list[0].Format != FormatM4B || list[1].Format != FormatEPUB ||
		!slices.Equal(triggered, []string{FormatEPUB, FormatM4B}) {
		t.Fatalf("unexpected requests %+v, triggers %v, error %v", list, triggered, err)
	}
	a.Search = func(context.Context, string, string) ([]indexers.ProwlarrRelease, error) {
		return []indexers.ProwlarrRelease{
			{GUID: "epub-fails", IndexerID: 1, Title: "Book EPUB 2024", Seeders: 100},
			{GUID: "epub", IndexerID: 2, Title: "Book EPUB"},
			{GUID: "m4b", IndexerID: 2, Title: "Book M4B"},
		}, nil
	}
	a.Evaluate = func(context.Context, indexers.ProwlarrRelease) (bool, []string, error) {
		return true, []string{"EPUB", "M4B"}, nil
	}
	var grabbed []string
	a.Grab = func(_ context.Context, r indexers.ProwlarrRelease) error {
		grabbed = append(grabbed, r.GUID)
		if r.GUID == "epub-fails" {
			return errors.New("private transport error")
		}
		return nil
	}
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(grabbed, "epub") || !slices.Contains(grabbed, "m4b") || len(grabbed) != 3 {
		t.Fatalf("grabs: %v", grabbed)
	}
	h := NewHandler(HandlerConfig{Service: svc, Acquisition: a})
	t.Cleanup(h.sessionMgr.Close)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	w := activityRequest(mux, "GET", list[1].ID, "reader", auth.RoleRequester)
	var detail struct {
		BookRequest
		Events []Event `json:"events"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Stage != "downloading" || detail.Year != 2024 || detail.CoverURL != input.CoverURL {
		t.Fatalf("unexpected detail: %d %s", w.Code, w.Body)
	}
	var kinds []string
	for i, event := range detail.Events {
		if event.OccurredAt.IsZero() || (i > 0 && event.ID <= detail.Events[i-1].ID) || strings.Contains(event.Message, "private transport") {
			t.Fatalf("invalid event: %+v", event)
		}
		kinds = append(kinds, event.Kind)
	}
	for _, kind := range []string{"queued", "search_started", "indexers_queried", "candidate_scored", "candidate_rejected", "failed", "retry", "release_grabbed"} {
		if !slices.Contains(kinds, kind) {
			t.Errorf("missing %s in %v", kind, kinds)
		}
	}
	for _, other := range []struct {
		user, role string
		want       int
	}{
		{"", "", 401}, {"other", auth.RoleRequester, 403}, {"other", auth.RoleCurator, 204},
	} {
		if w := activityRequest(mux, "DELETE", list[1].ID, other.user, other.role); w.Code != other.want {
			t.Fatalf("DELETE %s: %d %s", other.role, w.Code, w.Body)
		}
	}
	var state string
	if err := a.DB.QueryRow("SELECT status FROM download_queue WHERE request_id=? AND download_client_task_id='epub'", list[1].ID).Scan(&state); err != nil || state != "cancelled" {
		t.Fatalf("ebook download: %s %v", state, err)
	}
	if r, err := svc.GetRequest(ctx, list[0].ID); err != nil || r.Status != StatusDownloading {
		t.Fatalf("audiobook affected by ebook cancellation: %+v %v", r, err)
	}
}

func TestCancelInterruptsSearch(t *testing.T) {
	a, id, _, grabs := acquisitionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan struct{})
	a.Search = func(ctx context.Context, _, _ string) ([]indexers.ProwlarrRelease, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	finished := make(chan error, 1)
	go func() { finished <- a.Tick(ctx) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("search did not start")
	}
	h := NewHandler(HandlerConfig{Service: NewRequestService(a.DB), Acquisition: a})
	t.Cleanup(h.sessionMgr.Close)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	if w := activityRequest(mux, "DELETE", id, "reader", auth.RoleRequester); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	select {
	case err := <-finished:
		if err != nil || *grabs != 0 {
			t.Fatalf("cancelled search continued: %v, %d grabs", err, *grabs)
		}
	case <-ctx.Done():
		t.Fatal("search was not interrupted")
	}
	if w := activityRequest(mux, "GET", id, "reader", auth.RoleRequester); w.Code != 404 {
		t.Fatalf("cancelled request accessible: %d", w.Code)
	}
}

func TestDismissFinishedAndCancelLinkedConversion(t *testing.T) {
	for _, status := range []string{"error", "available", "downloading"} {
		t.Run(status, func(t *testing.T) {
			a, id, _, _ := acquisitionFixture(t)
			mustExec(t, a, "UPDATE book_requests SET status=? WHERE id=?", status, id)
			mustExec(t, a, `INSERT INTO book_editions(id,book_id,format) SELECT 'edition',book_id,'epub' FROM book_requests WHERE id=?`, id)
			mustExec(t, a, `INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('asset','edition','/book.epub',42)`)
			if status == "downloading" {
				mustExec(t, a, `INSERT INTO download_queue(id,request_id,download_client_task_id,title,protocol,status)
					VALUES('download',?,'client','Release','torrent','importing')`, id)
				mustExec(t, a, `INSERT INTO conversion_queue(id,edition_id,download_id,source_path,target_path,status,current_stage)
					VALUES('linked','edition','download','/source','/target.epub','processing','converting'),
					('unrelated','edition',NULL,'/other','/target2.epub','pending','queued')`)
			}
			svc := NewRequestService(a.DB)
			h := NewHandler(HandlerConfig{Service: svc, Acquisition: a})
			t.Cleanup(h.sessionMgr.Close)
			mux := http.NewServeMux()
			h.RegisterRoutes(mux)
			w := activityRequest(mux, "DELETE", id, "reader", auth.RoleRequester)
			if status == "available" {
				if w.Code != http.StatusConflict {
					t.Fatalf("expected 409 for completed activity, got %d %s", w.Code, w.Body)
				}
				list, err := svc.ListRequests(context.Background(), RequestFilter{})
				if err != nil || len(list) != 1 {
					t.Fatalf("completed request should remain listed: %+v %v", list, err)
				}
				var stored string
				if err := a.DB.QueryRow("SELECT status FROM book_requests WHERE id=?", id).Scan(&stored); err != nil || stored != "available" {
					t.Fatalf("request status = %s %v", stored, err)
				}
				return
			}
			if w.Code != 204 {
				t.Fatalf("delete: %d %s", w.Code, w.Body)
			}
			list, err := svc.ListRequests(context.Background(), RequestFilter{})
			if err != nil || len(list) != 0 {
				t.Fatalf("dismissed request listed: %+v %v", list, err)
			}
			var count int
			if err := a.DB.QueryRow("SELECT count(*) FROM media_assets WHERE id='asset'").Scan(&count); err != nil || count != 1 {
				t.Fatalf("library asset removed: %d %v", count, err)
			}
			var stored string
			want := status
			if status == "downloading" {
				want = "cancelled"
				for key, expected := range map[string]string{"linked": "cancelled", "unrelated": "pending"} {
					if err := a.DB.QueryRow("SELECT status FROM conversion_queue WHERE id=?", key).Scan(&stored); err != nil || stored != expected {
						t.Fatalf("conversion %s = %s %v", key, stored, err)
					}
				}
			}
			if err := a.DB.QueryRow("SELECT status FROM book_requests WHERE id=?", id).Scan(&stored); err != nil || stored != want {
				t.Fatalf("request status = %s %v", stored, err)
			}
		})
	}
}
