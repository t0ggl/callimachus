package catalog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestMonitoringPersistenceAndMonthlyCadence(t *testing.T) {
	for _, kind := range []string{"authors", "series"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			db := testutil.NewTestDB(t)
			c := catalog.New(db)
			a, err := c.GetOrCreateAuthor(ctx, "Example Author")
			if err != nil {
				t.Fatal(err)
			}
			series, err := c.GetOrCreateSeries(ctx, "Example Series")
			if err != nil {
				t.Fatal(err)
			}
			id := a.ID
			if kind == "series" {
				id = series.ID
			}
			scans := 0
			m := &catalog.Monitor{DB: db, Discover: func(context.Context, string, string) ([]metadata.BookMetadata, error) { scans++; return nil, nil }, Search: func(context.Context, string) error { return nil }}
			if err = m.SetMonitored(ctx, kind, id, true); err != nil {
				t.Fatal(err)
			}
			var enabled bool
			if err = db.QueryRowContext(ctx, "SELECT monitored FROM "+kind+" WHERE id=?", id).Scan(&enabled); err != nil || !enabled {
				t.Fatalf("flag did not persist: %v", err)
			}
			tick := func(date string, want int) {
				t.Helper()
				now, err := time.Parse(time.RFC3339, date)
				if err != nil {
					t.Fatal(err)
				}
				if err = m.Tick(ctx, now); err != nil {
					t.Fatal(err)
				}
				if scans != want {
					t.Fatalf("%s: got %d scans want %d", date, scans, want)
				}
			}
			tick("2028-01-31T12:00:00Z", 1)
			// New instance proves cadence survives service restarts.
			m = &catalog.Monitor{DB: db, Discover: m.Discover, Search: m.Search}
			tick("2028-02-29T11:59:59Z", 1)
			tick("2028-02-29T12:00:00Z", 2)
			if err = m.Queue(ctx, kind, id, "discover"); err != nil {
				t.Fatal(err)
			}
			tick("2028-03-01T00:00:00Z", 3)
			if err = m.SetMonitored(ctx, kind, id, false); err != nil {
				t.Fatal(err)
			}
			tick("2029-01-01T00:00:00Z", 3)
			if err = db.QueryRowContext(ctx, "SELECT monitored FROM "+kind+" WHERE id=?", id).Scan(&enabled); err != nil || enabled {
				t.Fatalf("disable did not persist: %v", err)
			}
		})
	}
}

func TestMonitoringReleaseDayDailyChecksAndAcquisition(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	c := catalog.New(db)
	a, err := c.GetOrCreateAuthor(ctx, "Example Author")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var bookID string
	m := &catalog.Monitor{DB: db, Discover: func(context.Context, string, string) ([]metadata.BookMetadata, error) {
		return []metadata.BookMetadata{
			{Title: "Upcoming", Authors: []string{"Example Author"}, PublishDate: "2028-05-10", Series: "Sequence"},
			{Title: "Wrong author", Authors: []string{"Someone Else"}},
		}, nil
	}, Search: func(_ context.Context, id string) error { calls++; bookID = id; return nil }}
	if err = m.SetMonitored(ctx, "authors", a.ID, true); err != nil {
		t.Fatal(err)
	}
	tick := func(date string, want int) {
		t.Helper()
		now, _ := time.Parse(time.RFC3339, date)
		if err := m.Tick(ctx, now); err != nil {
			t.Fatal(err)
		}
		if calls != want {
			t.Fatalf("%s: checks=%d want %d", date, calls, want)
		}
	}
	tick("2028-05-09T00:00:00Z", 0)
	var count int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM books WHERE monitored=1").Scan(&count); err != nil || count != 1 {
		t.Fatalf("unexpected discoveries %d: %v", count, err)
	}
	// Even a manual download search cannot acquire unreleased titles.
	if err = m.Queue(ctx, "authors", a.ID, "downloads"); err != nil {
		t.Fatal(err)
	}
	tick("2028-05-09T23:59:59Z", 0)
	tick("2028-05-10T00:00:00Z", 1)
	tick("2028-05-10T23:59:59Z", 1)
	tick("2028-05-11T00:00:00Z", 2)
	if err = m.Queue(ctx, "authors", a.ID, "downloads"); err != nil {
		t.Fatal(err)
	}
	tick("2028-05-11T01:00:00Z", 3)
	ed, err := c.GetOrCreateEdition(ctx, &database.BookEdition{BookID: bookID, Format: "epub", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.UpsertMediaAsset(ctx, &database.MediaAsset{EditionID: ed.ID, FilePath: "/library/acquired.epub"}); err != nil {
		t.Fatal(err)
	}
	tick("2028-05-12T01:00:00Z", 3)
}

func TestMonitoringRetriesFailedDiscovery(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	a, err := catalog.New(db).GetOrCreateAuthor(ctx, "Author")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	m := &catalog.Monitor{DB: db, Discover: func(context.Context, string, string) ([]metadata.BookMetadata, error) {
		calls++
		return nil, errors.New("offline")
	}, Search: func(context.Context, string) error { return nil }}
	if err = m.Queue(ctx, "authors", a.ID, "discover"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = m.Tick(ctx, time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil {
			t.Fatal("expected error")
		}
	}
	if calls != 2 {
		t.Fatal("failed scan was lost")
	}
	if err = m.SetMonitored(ctx, "invalid", "x", true); err == nil {
		t.Fatal("invalid kind accepted")
	}
}
