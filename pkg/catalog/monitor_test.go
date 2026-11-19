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
			series, err := c.GetOrCreateSeries(ctx, "Example Series", "", 0)
			if err != nil {
				t.Fatal(err)
			}
			id := a.ID
			if kind == "series" {
				id = series.ID
			}
			scans := 0
			m := &catalog.Monitor{DB: db, Discover: func(context.Context, string, string, []metadata.BookMetadata) ([]metadata.BookMetadata, error) {
				scans++
				return nil, nil
			}, Search: func(context.Context, string) error { return nil }}
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
	m := &catalog.Monitor{DB: db, Discover: func(context.Context, string, string, []metadata.BookMetadata) ([]metadata.BookMetadata, error) {
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
	m := &catalog.Monitor{DB: db, Discover: func(context.Context, string, string, []metadata.BookMetadata) ([]metadata.BookMetadata, error) {
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

func TestMonitoringPreservesPartialAndYearOnlyDates(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	c := catalog.New(db)
	a, err := c.GetOrCreateAuthor(ctx, "Example Author")
	if err != nil {
		t.Fatal(err)
	}
	discovered := []metadata.BookMetadata{
		{Title: "Exact Future", Authors: []string{"Example Author"}, PublishDate: "2028-05-10"},
		{Title: "Partial Month ISO", Authors: []string{"Example Author"}, PublishDate: "2028-06"},
		{Title: "Month Year String", Authors: []string{"Example Author"}, PublishDate: "July 2028"},
		{Title: "Year Only Empty Date", Authors: []string{"Example Author"}, PublishDate: "", Year: 2029},
	}
	var searched []string
	m := &catalog.Monitor{
		DB: db,
		Discover: func(context.Context, string, string, []metadata.BookMetadata) ([]metadata.BookMetadata, error) {
			return discovered, nil
		},
		Search: func(_ context.Context, id string) error {
			var title string
			if err := db.QueryRowContext(ctx, "SELECT title FROM books WHERE id=?", id).Scan(&title); err != nil {
				return err
			}
			searched = append(searched, title)
			return nil
		},
	}
	if err = m.SetMonitored(ctx, "authors", a.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = m.Tick(ctx, time.Date(2028, 5, 9, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	gotDates := map[string]string{}
	rows, err := db.QueryContext(ctx, "SELECT title, COALESCE(release_date, '') FROM books")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var title, date string
		if err := rows.Scan(&title, &date); err != nil {
			t.Fatal(err)
		}
		gotDates[title] = date
	}
	wantDates := map[string]string{
		"Exact Future":         "2028-05-10",
		"Partial Month ISO":    "2028-06",
		"Month Year String":    "2028-07",
		"Year Only Empty Date": "2029",
	}
	for title, want := range wantDates {
		if gotDates[title] != want {
			t.Errorf("%s: release_date = %q, want %q", title, gotDates[title], want)
		}
	}
	if len(searched) != 0 {
		t.Fatalf("unexpected searches before release: %v", searched)
	}

	// Re-discovering with coarser year-only metadata for the same year must not erase exact day precision.
	discovered[0] = metadata.BookMetadata{Title: "Exact Future", Authors: []string{"Example Author"}, Year: 2028}
	if err = m.Queue(ctx, "authors", a.ID, "discover"); err != nil {
		t.Fatal(err)
	}
	if err = m.Tick(ctx, time.Date(2028, 5, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var exactDate string
	if err = db.QueryRowContext(ctx, "SELECT COALESCE(release_date, '') FROM books WHERE title='Exact Future'").Scan(&exactDate); err != nil {
		t.Fatal(err)
	}
	if exactDate != "2028-05-10" {
		t.Fatalf("exact day precision was downgraded to %q", exactDate)
	}

	// Once 2028-06 arrives, Partial Month ISO (2028-06) becomes eligible while 2028-07 and 2029 stay scheduled.
	searched = nil
	if err = m.Queue(ctx, "authors", a.ID, "downloads"); err != nil {
		t.Fatal(err)
	}
	if err = m.Tick(ctx, time.Date(2028, 6, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if len(searched) != 2 || searched[0] != "Exact Future" || searched[1] != "Partial Month ISO" {
		t.Fatalf("on 2028-06-01 searched = %v, want [Exact Future Partial Month ISO]", searched)
	}
}

func TestMonitoringKeepsWitnessSeriesSeparate(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	c := catalog.New(db)
	for _, query := range []string{
		`INSERT INTO authors(id,name,sort_name) VALUES('erikson','Steven Erikson','Erikson, Steven'),('monroe','Grace Monroe','Monroe, Grace')`,
		`INSERT INTO series(id,title,description,monitored) VALUES('legacy','The Witness','Local description',1)`,
		`INSERT INTO books(id,author_id,title,series_id,series_index,monitored) VALUES('god','erikson','The God Is Not Willing','legacy',1,0)`,
		`INSERT INTO book_editions(id,book_id,format) VALUES('local-edition','god','epub')`,
		`INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('local-file','local-edition','/library/erikson.epub',1)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	other, err := c.GetOrCreateSeries(ctx, "The Witness", "monroe", 701)
	if err != nil || other.ID == "legacy" {
		t.Fatalf("unrelated author reused legacy series: %+v (%v)", other, err)
	}
	calls := 0
	m := &catalog.Monitor{DB: db, Search: func(context.Context, string) error { return nil }}
	m.Discover = func(_ context.Context, kind, name string, known []metadata.BookMetadata) ([]metadata.BookMetadata, error) {
		calls++
		if kind != "series" || name != "The Witness" || len(known) != 1 {
			t.Fatalf("unexpected discovery context: %s %s %+v", kind, name, known)
		}
		if calls == 1 && (known[0].Title != "The God Is Not Willing" || !metadata.SharesAuthor(known[0].Authors, []string{"Steven Erikson"})) {
			t.Fatalf("legacy author evidence missing: %+v", known)
		}
		if calls == 2 && known[0].HardcoverSeriesID != 703 {
			t.Fatalf("canonical series ID missing: %+v", known)
		}
		return []metadata.BookMetadata{
			{Title: "A Different Witness", Authors: []string{"Grace Monroe"}, Series: name, HardcoverSeriesID: 701},
			{Title: "The God Is Not Willing", Authors: []string{"Steven Erikson"}, Series: name, HardcoverSeriesID: 703, PublishDate: "2021-11-09"},
			{Title: "No Life Forsaken", Authors: []string{"Steven Erikson"}, Series: name, HardcoverSeriesID: 703, PublishDate: "2099-10-23"},
		}, nil
	}
	for _, month := range []time.Month{time.January, time.February} {
		if err := m.Tick(ctx, time.Date(2028, month, 1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("discovery ran %d times", calls)
	}
	aliased, err := c.GetOrCreateSeries(ctx, "The Tales of Witness", "erikson", 703)
	if err != nil || aliased.ID != "legacy" || aliased.Description == nil || *aliased.Description != "Local description" {
		t.Fatalf("canonical ID failed to preserve the catalog row: %+v (%v)", aliased, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM books WHERE series_id='legacy' AND author_id='erikson'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("series books = %d (%v)", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("unrelated or duplicate books persisted: %d (%v)", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM books b JOIN book_editions e ON e.book_id=b.id JOIN media_assets m ON m.edition_id=e.id
  WHERE b.id='god' AND b.series_id='legacy' AND b.series_index=1 AND b.monitored=0 AND m.id='local-file'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing book, flags, or media changed: %d (%v)", count, err)
	}
}
