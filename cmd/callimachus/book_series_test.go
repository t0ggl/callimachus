package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestSharedBookKeepsPrimaryAndAppearsInBothSeries(t *testing.T) {
	ctx := context.Background()
	client := testutil.NewMockTransportForTest(t).Client()
	s, handler, admin, _ := setupBookRefreshServer(t, t.TempDir(), client)
	const originalPath = "/library/Orson Scott Card/Ender/The Last Shadow (2021)/The Last Shadow (2021) - Orson Scott Card.epub"
	if _, err := s.db.Exec(`
		INSERT INTO authors(id,name,sort_name) VALUES ('card','Orson Scott Card','Card, Orson Scott');
		INSERT INTO series(id,title,author_id,hardcover_id) VALUES ('ender','Ender','card',100), ('shadow','Shadow Series','card',101);
		INSERT INTO books(id,author_id,title,series_id,series_index) VALUES ('last','card','The Last Shadow','ender',6);
		INSERT INTO book_editions(id,book_id,format) VALUES ('ebook','last','epub');
		INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES ('file','ebook',?,1);
	`, originalPath); err != nil {
		t.Fatal(err)
	}
	index := 6.5
	monitor := &catalog.Monitor{DB: s.db, Discover: func(context.Context, string, string, []metadata.BookMetadata) ([]metadata.BookMetadata, error) {
		return []metadata.BookMetadata{{
			Title: "The Last Shadow", Authors: []string{"Orson Scott Card"}, Series: "Shadow Series",
			HardcoverSeriesID: 101, SeriesIndex: &index, ContextualSeries: true,
			PrimarySeries: &metadata.SeriesMembership{Title: "Ender", HardcoverID: 100},
		}}, nil
	}}
	if err := monitor.SetMonitored(ctx, "series", "shadow", true); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Tick(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}

	for seriesID, position := range map[string]float64{"ender": 6, "shadow": index} {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/series/books?id="+seriesID, nil)
		req.AddCookie(admin)
		handler.ServeHTTP(response, req)
		var series seriesBooksResponse
		if err := json.Unmarshal(response.Body.Bytes(), &series); err != nil || response.Code != http.StatusOK ||
			series.OwnedCount != 1 || len(series.Books) != 1 || series.Books[0].LibraryBookID != "last" ||
			series.Books[0].SeriesIndex == nil || *series.Books[0].SeriesIndex != position {
			t.Fatalf("series %s: status %d, response %s, error %v", seriesID, response.Code, response.Body, err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/books?id=last", nil)
	req.AddCookie(admin)
	books, err := s.queryBooks(ctx, req, "last")
	if err != nil || len(books) != 1 || books[0].SeriesID != "ender" || books[0].SeriesIndex == nil || *books[0].SeriesIndex != 6 {
		t.Fatalf("primary series changed: %+v (%v)", books, err)
	}
	var memberships []struct {
		SeriesID  string
		IsPrimary bool
		Index     *float64
	}
	if err := json.Unmarshal(books[0].SeriesMemberships, &memberships); err != nil || len(memberships) != 2 ||
		memberships[0].SeriesID != "ender" || !memberships[0].IsPrimary ||
		memberships[1].SeriesID != "shadow" || memberships[1].IsPrimary || memberships[1].Index == nil ||
		*memberships[1].Index != index {
		t.Fatalf("unexpected memberships: %s (%v)", books[0].SeriesMemberships, err)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/books/last/additional-series", strings.NewReader(`{"seriesId":"shadow","remove":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("remove membership: %d %s", response.Code, response.Body)
	}
	if err := catalog.New(s.db).SaveObservedSeries(ctx, "last", "shadow", &index); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM book_series WHERE book_id = 'last'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("removed membership returned after refresh: %d (%v)", count, err)
	}
	var path string
	if err := s.db.QueryRow(`SELECT file_path FROM media_assets WHERE id = 'file'`).Scan(&path); err != nil || path != originalPath {
		t.Fatalf("canonical path changed: %q (%v)", path, err)
	}
}

func TestClearPrimarySeriesKeepsMembershipAndRelocatesEPUB(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, handler, admin, _ := setupBookRefreshServer(t, root, testutil.NewMockTransportForTest(t).Client())
	path := filepath.Join(root, "Author", "Series", "Book (2024)", "Book (2024) - Author.epub")
	index := 2.5
	createTestEPUBFile(t, path, epub.Metadata{
		Title: "Book", Authors: []string{"Author"}, Series: "Series", SeriesIndex: &index, PublishDate: "2024", Language: "en",
	})
	if _, err := s.db.Exec(`
		INSERT INTO authors(id,name,sort_name) VALUES ('a','Author','Author');
		INSERT INTO series(id,title,author_id) VALUES ('s','Series','a');
		INSERT INTO books(id,author_id,series_id,series_index,title,release_date) VALUES ('b','a','s',2.5,'Book','2024');
		INSERT INTO book_editions(id,book_id,format,language) VALUES ('e','b','epub','en');
		INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES ('f','e',?,1);
	`, path); err != nil {
		t.Fatal(err)
	}
	run := func(endpoint string, payload repairRequest) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/books/b/"+endpoint, strings.NewReader(string(data)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(admin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", endpoint, response.Code, response.Body)
		}
		return response
	}
	edit := repairRequest{PrimarySeries: &primarySeriesOverride{}}
	var preview repairPreview
	if err := json.Unmarshal(run("metadata-diff", edit).Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	edit.Preview = preview.Preview
	run("fix-metadata", edit)
	standalone := filepath.Join(root, "Author", "Book (2024)", "Book (2024) - Author.epub")
	opened, err := epub.OpenNative(standalone)
	if err != nil {
		t.Fatal(err)
	}
	if meta := opened.Metadata(); meta.Series != "" || meta.SeriesIndex != nil {
		t.Fatalf("standalone EPUB still tags a series: %+v", meta)
	}
	c := catalog.New(s.db)
	book, err := c.GetOrCreateBook(ctx, &database.Book{AuthorID: "a", Title: "Book", SeriesID: new("s"), SeriesIndex: &index})
	if err != nil || book.SeriesID != nil {
		t.Fatalf("background backfill restored primary: %+v (%v)", book, err)
	}
	var primary bool
	var position float64
	if err := s.db.QueryRow(`SELECT is_primary, series_index FROM book_series WHERE book_id = 'b' AND series_id = 's'`).
		Scan(&primary, &position); err != nil || primary || position != index {
		t.Fatalf("previous membership: primary %t, position %v, error %v", primary, position, err)
	}
}
