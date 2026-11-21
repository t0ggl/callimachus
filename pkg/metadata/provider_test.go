package metadata_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/metadata/audnexus"
	"github.com/t0ggl/callimachus/pkg/metadata/hardcover"
	"github.com/t0ggl/callimachus/pkg/metadata/openlibrary"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

// Ensure compile-time interface adherence.
var (
	_ metadata.Provider = (*metadata.HardcoverProvider)(nil)
	_ metadata.Provider = (*metadata.AudnexusProvider)(nil)
	_ metadata.Provider = (*metadata.OpenLibraryProvider)(nil)
)

func TestHardcoverProvider_Search(t *testing.T) {
	client := testutil.NewMockHTTPClient(map[string]string{
		"https://api.hardcover.app/v1/graphql": "testdata/metadata/hardcover_search.json",
	})

	provider := hardcover.New("mock-api-key", hardcover.WithClient(client))
	if provider.Name() != "hardcover" {
		t.Fatalf("expected provider name 'hardcover', got %q", provider.Name())
	}

	results, err := provider.Search(context.Background(), "Test Book")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 search results, got %d", len(results))
	}

	res0 := results[0]
	if res0.Series != "Test Series" || res0.SeriesTotal != 4 || res0.SeriesIndex == nil || *res0.SeriesIndex != 2.5 {
		t.Fatalf("missing series metadata: %+v", res0)
	}
	if res0.HardcoverID != "1001" {
		t.Errorf("expected HardcoverID 1001, got %s", res0.HardcoverID)
	}
	if res0.Title != "Test Book Alpha" {
		t.Errorf("expected Title 'Test Book Alpha', got %q", res0.Title)
	}
	if res0.Year != 2021 {
		t.Errorf("expected Year 2021, got %d", res0.Year)
	}
	if len(res0.Authors) != 1 || res0.Authors[0] != "Synthetic Author One" {
		t.Errorf("unexpected authors: %+v", res0.Authors)
	}
	if res0.CoverURL != "https://assets.hardcover.app/books/covers/test-book-alpha.jpg" {
		t.Errorf("unexpected cover url: %s", res0.CoverURL)
	}

	res1 := results[1]
	if res1.HardcoverID != "1002" {
		t.Errorf("expected HardcoverID 1002, got %s", res1.HardcoverID)
	}
	if res1.Title != "Test Book Beta" {
		t.Errorf("expected Title 'Test Book Beta', got %q", res1.Title)
	}
	if res1.Year != 2022 {
		t.Errorf("expected Year 2022, got %d", res1.Year)
	}
	if len(res1.Authors) != 1 || res1.Authors[0] != "Synthetic Author Two" {
		t.Errorf("unexpected authors: %+v", res1.Authors)
	}
}

func TestHardcoverProvider_Lookup(t *testing.T) {
	client := testutil.NewMockHTTPClient(map[string]string{
		"https://api.hardcover.app/v1/graphql": "testdata/metadata/hardcover_book.json",
	})

	provider := metadata.NewHardcoverProvider("mock-api-key", metadata.WithHardcoverClient(client))

	t.Run("GetBook by PK", func(t *testing.T) {
		book, err := provider.GetBook(context.Background(), 1001)
		if err != nil {
			t.Fatalf("GetBook failed: %v", err)
		}
		validateHardcoverBook(t, book)
	})

	t.Run("LookupByISBN", func(t *testing.T) {
		book, err := provider.LookupByISBN(context.Background(), "9780123456789")
		if err != nil {
			t.Fatalf("LookupByISBN failed: %v", err)
		}
		validateHardcoverBook(t, book)
	})

	t.Run("LookupByASIN", func(t *testing.T) {
		book, err := provider.LookupByASIN(context.Background(), "B08TEST123")
		if err != nil {
			t.Fatalf("LookupByASIN failed: %v", err)
		}
		validateHardcoverBook(t, book)
	})
}

func validateHardcoverBook(t *testing.T, book *metadata.BookMetadata) {
	t.Helper()
	if book.HardcoverID != "1001" {
		t.Errorf("expected HardcoverID 1001, got %s", book.HardcoverID)
	}
	if book.Title != "Test Book Alpha" {
		t.Errorf("expected Title 'Test Book Alpha', got %q", book.Title)
	}
	if book.Subtitle != "The Genesis of Testing" {
		t.Errorf("expected Subtitle 'The Genesis of Testing', got %q", book.Subtitle)
	}
	if book.Year != 2021 {
		t.Errorf("expected Year 2021, got %d", book.Year)
	}
	if book.PublishDate != "2021-06-15" {
		t.Errorf("expected PublishDate '2021-06-15', got %q", book.PublishDate)
	}
	if len(book.Authors) != 1 || book.Authors[0] != "Synthetic Author One" {
		t.Errorf("unexpected authors: %+v", book.Authors)
	}
	if book.Series != "Synthetic Chronicles" {
		t.Errorf("expected Series 'Synthetic Chronicles', got %q", book.Series)
	}
	if book.SeriesIndex == nil || *book.SeriesIndex != 1.0 {
		t.Errorf("expected SeriesIndex 1.0, got %v", book.SeriesIndex)
	}
	if book.ISBN13 != "9780123456789" {
		t.Errorf("expected ISBN13 '9780123456789', got %q", book.ISBN13)
	}
	if book.ASIN != "B08TEST123" {
		t.Errorf("expected ASIN 'B08TEST123', got %q", book.ASIN)
	}
	if book.Publisher != "Synthetic Press" {
		t.Errorf("expected Publisher 'Synthetic Press', got %q", book.Publisher)
	}
	if book.CoverURL != "https://assets.hardcover.app/books/covers/test-book-alpha.jpg" {
		t.Errorf("unexpected CoverURL: %s", book.CoverURL)
	}
	if len(book.Genres) != 3 || book.Genres[0] != "Science Fiction" {
		t.Errorf("unexpected genres: %+v", book.Genres)
	}
}

func TestAudnexusProvider_LookupByASIN(t *testing.T) {
	client := testutil.NewMockHTTPClient(map[string]string{
		"^https://api\\.audnexus\\.com/books/.*$": "testdata/metadata/audnexus_asin.json",
	})

	provider := audnexus.New(audnexus.WithClient(client))
	if provider.Name() != "audnexus" {
		t.Fatalf("expected provider name 'audnexus', got %q", provider.Name())
	}

	book, err := provider.LookupByASIN(context.Background(), "B08TEST123")
	if err != nil {
		t.Fatalf("LookupByASIN failed: %v", err)
	}

	if book.ASIN != "B08TEST123" {
		t.Errorf("expected ASIN 'B08TEST123', got %q", book.ASIN)
	}
	if book.Title != "Test Book Alpha" {
		t.Errorf("expected Title 'Test Book Alpha', got %q", book.Title)
	}
	if book.Subtitle != "The Genesis of Testing" {
		t.Errorf("expected Subtitle 'The Genesis of Testing', got %q", book.Subtitle)
	}
	if book.Year != 2021 {
		t.Errorf("expected Year 2021, got %d", book.Year)
	}
	if len(book.Authors) != 1 || book.Authors[0] != "Synthetic Author One" {
		t.Errorf("unexpected authors: %+v", book.Authors)
	}
	if len(book.Narrators) != 2 || book.Narrators[0] != "Synthetic Narrator One" || book.Narrators[1] != "Synthetic Narrator Two" {
		t.Errorf("unexpected narrators: %+v", book.Narrators)
	}
	if book.Series != "Synthetic Chronicles" {
		t.Errorf("expected Series 'Synthetic Chronicles', got %q", book.Series)
	}
	if book.SeriesIndex == nil || *book.SeriesIndex != 1.0 {
		t.Errorf("expected SeriesIndex 1.0, got %v", book.SeriesIndex)
	}
	if book.Publisher != "Synthetic Audio Press" {
		t.Errorf("expected Publisher 'Synthetic Audio Press', got %q", book.Publisher)
	}
	if book.Language != "english" {
		t.Errorf("expected Language 'english', got %q", book.Language)
	}
	if book.DurationSec != 37440 {
		t.Errorf("expected DurationSec 37440 (624 min * 60), got %v", book.DurationSec)
	}
	if book.CoverURL != "https://m.media-amazon.com/images/I/synthetic-cover-test.jpg" {
		t.Errorf("unexpected CoverURL: %s", book.CoverURL)
	}

	// Validate chapters
	if len(book.Chapters) != 5 {
		t.Fatalf("expected 5 chapters, got %d", len(book.Chapters))
	}
	if book.Chapters[0].Title != "Opening Credits" || book.Chapters[0].StartSec != 0 || book.Chapters[0].EndSec != 15 {
		t.Errorf("unexpected chapter 0: %+v", book.Chapters[0])
	}
	if book.Chapters[1].Title != "Prologue" || book.Chapters[1].StartSec != 15 || book.Chapters[1].EndSec != 1815 {
		t.Errorf("unexpected chapter 1: %+v", book.Chapters[1])
	}
	if book.Chapters[2].Title != "Chapter 1: The Departure" || book.Chapters[2].StartSec != 1815 || book.Chapters[2].EndSec != 4215 {
		t.Errorf("unexpected chapter 2: %+v", book.Chapters[2])
	}
	if book.Chapters[4].Title != "Closing Credits" || book.Chapters[4].StartSec != 6315 || book.Chapters[4].EndSec != 6335 {
		t.Errorf("unexpected chapter 4: %+v", book.Chapters[4])
	}

	// Test Search with ASIN
	searchResults, err := provider.Search(context.Background(), "B08TEST123")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].ASIN != "B08TEST123" {
		t.Errorf("unexpected search results: %+v", searchResults)
	}

	// Test LookupByISBN returns ErrNotFound
	if _, err := provider.LookupByISBN(context.Background(), "9780123456789"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("expected ErrNotFound for Audnexus LookupByISBN, got %v", err)
	}
}

func TestOpenLibraryProvider_LookupByISBN(t *testing.T) {
	client := testutil.NewMockHTTPClient(map[string]string{
		"^https://openlibrary\\.org/isbn/.*\\.json$": "testdata/metadata/openlibrary_isbn.json",
	})

	provider := openlibrary.New(openlibrary.WithClient(client))
	if provider.Name() != "openlibrary" {
		t.Fatalf("expected provider name 'openlibrary', got %q", provider.Name())
	}

	book, err := provider.LookupByISBN(context.Background(), "978-0-12345678-9")
	if err != nil {
		t.Fatalf("LookupByISBN failed: %v", err)
	}

	if book.OpenLibraryID != "OL12345678M" {
		t.Errorf("expected OpenLibraryID 'OL12345678M', got %q", book.OpenLibraryID)
	}
	if book.Title != "Test Book Alpha" {
		t.Errorf("expected Title 'Test Book Alpha', got %q", book.Title)
	}
	if book.Subtitle != "The Genesis of Testing" {
		t.Errorf("expected Subtitle 'The Genesis of Testing', got %q", book.Subtitle)
	}
	if book.Year != 2021 {
		t.Errorf("expected Year 2021, got %d", book.Year)
	}
	if book.PublishDate != "June 15, 2021" {
		t.Errorf("expected PublishDate 'June 15, 2021', got %q", book.PublishDate)
	}
	if book.ISBN13 != "9780123456789" {
		t.Errorf("expected ISBN13 '9780123456789', got %q", book.ISBN13)
	}
	if book.Publisher != "Synthetic Press" {
		t.Errorf("expected Publisher 'Synthetic Press', got %q", book.Publisher)
	}
	if book.CoverURL != "https://covers.openlibrary.org/b/id/10987654-L.jpg" {
		t.Errorf("expected CoverURL with cover id 10987654, got %q", book.CoverURL)
	}
	if len(book.Genres) != 3 || book.Genres[0] != "Science Fiction" {
		t.Errorf("unexpected genres: %+v", book.Genres)
	}

	// LookupByASIN returns ErrNotFound
	if _, err := provider.LookupByASIN(context.Background(), "B08TEST123"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("expected ErrNotFound for OpenLibrary LookupByASIN, got %v", err)
	}
}

func TestUnmockedNetworkCallsFailImmediately(t *testing.T) {
	// Create an empty MockTransport with PanicOnUnmocked = false
	emptyTransport := testutil.NewMockTransport()
	emptyTransport.PanicOnUnmocked = false
	unmockedClient := &http.Client{Transport: emptyTransport}

	t.Run("Hardcover unmocked call returns ErrUnmockedRequest", func(t *testing.T) {
		hc := metadata.NewHardcoverProvider("api-key", metadata.WithHardcoverClient(unmockedClient))
		_, err := hc.Search(context.Background(), "Any Query")
		if err == nil {
			t.Fatal("expected error on unmocked network call, got nil")
		}
		if !errors.Is(err, testutil.ErrUnmockedRequest) {
			t.Errorf("expected error wrapping ErrUnmockedRequest, got %v", err)
		}
	})

	t.Run("Audnexus unmocked call returns ErrUnmockedRequest", func(t *testing.T) {
		an := metadata.NewAudnexusProvider(metadata.WithAudnexusClient(unmockedClient))
		_, err := an.LookupByASIN(context.Background(), "B08TEST123")
		if err == nil {
			t.Fatal("expected error on unmocked network call, got nil")
		}
		if !errors.Is(err, testutil.ErrUnmockedRequest) {
			t.Errorf("expected error wrapping ErrUnmockedRequest, got %v", err)
		}
	})

	t.Run("OpenLibrary unmocked call returns ErrUnmockedRequest", func(t *testing.T) {
		ol := metadata.NewOpenLibraryProvider(metadata.WithOpenLibraryClient(unmockedClient))
		_, err := ol.LookupByISBN(context.Background(), "9780123456789")
		if err == nil {
			t.Fatal("expected error on unmocked network call, got nil")
		}
		if !errors.Is(err, testutil.ErrUnmockedRequest) {
			t.Errorf("expected error wrapping ErrUnmockedRequest, got %v", err)
		}
	})

	t.Run("PanicOnUnmocked panics immediately", func(t *testing.T) {
		panicTransport := testutil.NewMockTransport()
		panicTransport.PanicOnUnmocked = true
		panicClient := &http.Client{Transport: panicTransport}

		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic on unmocked call with PanicOnUnmocked=true, got none")
			}
		}()

		ol := metadata.NewOpenLibraryProvider(metadata.WithOpenLibraryClient(panicClient))
		_, _ = ol.LookupByISBN(context.Background(), "9780123456789")
	})
}

func TestHardcoverCatalogPagination(t *testing.T) {
	for _, kind := range []string{"author", "series"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					Query     string                 `json:"query"`
					Variables map[string]interface{} `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				if strings.Contains(input.Query, "query FindSeries") {
					_, _ = w.Write([]byte(`{"data":{"named":[{"id":7,"name":"Sequence"}]}}`))
					return
				}
				if int(input.Variables["offset"].(float64)) != calls*100 {
					t.Error("wrong page offset")
				}
				calls++
				books := []map[string]interface{}{}
				if calls == 1 {
					for i := 0; i < 100; i++ {
						books = append(books, map[string]interface{}{"id": i + 1, "title": "Book", "release_date": "2028-01-01", "book_series": []map[string]any{{"series": map[string]any{"id": 7, "name": "Sequence"}}}, "contributions": []map[string]interface{}{{"author": map[string]string{"name": "Author"}}}})
					}
				}
				json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"books": books}})
			}))
			defer server.Close()
			p := metadata.NewHardcoverProvider("mock", metadata.WithHardcoverBaseURL(server.URL), metadata.WithHardcoverClient(server.Client()))
			var found []metadata.BookMetadata
			var err error
			if kind == "author" {
				found, err = p.AuthorBooks(context.Background(), "Author")
			} else {
				found, err = p.SeriesBooks(context.Background(), "Sequence")
			}
			if err != nil || len(found) != 100 || calls != 2 {
				t.Fatalf("pagination: %d books, %d calls: %v", len(found), calls, err)
			}
		})
	}
}

func TestHardcoverSeriesCanonicalEnglishVolumes(t *testing.T) {
	mt := testutil.NewMockTransport()
	mt.PanicOnUnmocked = true
	registerHardcoverSeries(t, mt, `{"data":{"named":[{"id":7,"name":"Court"}]}}`, `{"data":{"books":[
  {"id":1,"title":"Cour de roses","default_cover_edition":{"language":{"code2":"fr"}},"book_series":[{"series_position":1,"series":{"id":7,"name":"Court"}}]},
  {"id":2,"title":"Court: Alternate Edition","canonical_id":5,"book_series":[{"series_position":1,"series":{"id":7,"name":"Court"}}]},
  {"id":3,"title":"Court Books 1-4","book_series":[{"series_position":4,"series":{"id":7,"name":"Court"}}]},
  {"id":4,"title":"Court Books 1–5","book_series":[{"series":{"id":7,"name":"Court"}}]},
  {"id":5,"title":"Court of Roses","image":{"url":"https://example.com/cover.jpg"},"default_cover_edition":{"language":{"code3":"eng"}},"book_series":[{"series_position":1,"series":{"name":"Another Series"}},{"series_position":1,"series":{"id":7,"name":"Court"}}]},
  {"id":6,"title":"Court Three Book Set","book_series":[{"series_position":1.5,"series":{"id":7,"name":"Court"}}]},
  {"id":7,"title":"Court Saga","compilation":true,"book_series":[{"series_position":7,"series":{"id":7,"name":"Court"}}]},
  {"id":8,"title":"Corte de rosas","default_cover_edition":{"language":{"code2":"es"}},"book_series":[{"series_position":8,"series":{"id":7,"name":"Court"}}]},
  {"id":9,"title":"Wrong Series","book_series":[{"series_position":9,"series":{"name":"Another Series"}}]},
  {"id":10,"title":"Court variant","users_count":1000,"book_series":[{"series_position":1,"series":{"id":7,"name":"Court"}}]},
  {"id":11,"title":"Court Novella","book_series":[{"series_position":2.5,"series":{"id":7,"name":"Court"}}]},
  {"id":12,"title":"Court Companion","book_series":[{"series":{"id":7,"name":"Court"}}]},
  {"id":13,"title":"Court","subtitle":"Boxed Set","book_series":[{"series":{"id":7,"name":"Court"}}]}
 ]}}`)
	p := metadata.NewHardcoverProvider("mock", metadata.WithHardcoverClient(mt.Client()))
	books, err := p.SeriesBooks(context.Background(), "Court")
	if err != nil || len(books) != 3 {
		t.Fatalf("expected three canonical volumes, got %+v: %v", books, err)
	}
	if books[0].HardcoverID != "5" || books[0].Series != "Court" || books[0].CoverURL == "" ||
		books[1].SeriesIndex == nil || *books[1].SeriesIndex != 2.5 || books[2].SeriesIndex != nil {
		t.Fatalf("unexpected series volumes: %+v", books)
	}
}

func TestHardcoverSeriesExcludesDerivatives(t *testing.T) {
	mt := testutil.NewMockTransport()
	mt.PanicOnUnmocked = true
	mt.RegisterFixture("/v1/graphql", http.StatusOK, "testdata/metadata/hardcover_series_acotar.json", nil)
	p := metadata.NewHardcoverProvider("mock", metadata.WithHardcoverClient(mt.Client()))
	books, err := p.SeriesBooks(context.Background(), "A Court of Thorns and Roses", metadata.BookMetadata{HardcoverSeriesID: 41})
	if err != nil {
		t.Fatal(err)
	}

	var ids []string
	for _, book := range books {
		ids = append(ids, book.HardcoverID)
	}
	if want := []string{"1", "4", "7", "8", "11", "14"}; !slices.Equal(ids, want) {
		t.Fatalf("series IDs = %v, want %v", ids, want)
	}
	if novella := books[3]; novella.Title != "A Court of Frost and Starlight" || novella.SeriesIndex == nil || *novella.SeriesIndex != 3.5 {
		t.Fatalf("unexpected novella: %+v", novella)
	}
}

func registerHardcoverSeries(t *testing.T, mt *testutil.MockTransport, seriesJSON, booksJSON string) {
	t.Helper()
	mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
		var input struct {
			Query     string `json:"query"`
			Variables struct {
				Name string `json:"name"`
				ID   int    `json:"id"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		var body string
		switch {
		case strings.Contains(input.Query, "query FindSeries"):
			body = seriesJSON
		case strings.Contains(input.Query, "query SeriesBooks") && input.Variables.ID == 7 && booksJSON != "":
			body = booksJSON
		default:
			t.Fatalf("unexpected Hardcover request: %+v", input)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}

func TestHardcoverSeriesNameVariantsAndForthcomingVolumes(t *testing.T) {
	mt := testutil.NewMockTransport()
	mt.PanicOnUnmocked = true
	mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
		var input struct {
			Query     string `json:"query"`
			Variables struct {
				Name string `json:"name"`
				ID   int    `json:"id"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		var body string
		switch {
		case strings.Contains(input.Query, "query FindSeries"):
			match := regexp.MustCompile("(?i)" + input.Variables.Name)
			if !match.MatchString("The Shadow of the Leviathan") || match.MatchString("Shadow of the Leviathan Companion") {
				t.Fatalf("unexpected series name pattern %q", input.Variables.Name)
			}
			body = `{"data":{"named":[{"id":8,"name":"The Shadow of the Leviathan","canonical":{"id":7,"name":"Ana and Din Mysteries","books_count":2}}]}}`
		case strings.Contains(input.Query, "query SeriesBooks") && input.Variables.ID == 7:
			body = `{"data":{"books":[
				{"id":1,"title":"The Tainted Cup","book_series":[{"series":{"id":55},"series_position":3},{"series":{"id":7},"series_position":1}]},
				{"id":2,"title":"A Drop of Corruption","default_cover_edition":{"language":{"code2":"de"}},"english_editions":[{"id":12}],"book_series":[{"series":{"id":7},"series_position":2}]},
				{"id":30,"title":"Advance listing","canonical_id":3,"book_series":[{"series":{"id":8,"canonical_id":7},"series_position":3}],
				 "canonical":{"id":3,"title":"A Trade of Blood","subtitle":"2089–2099","release_date":"2099-08-04"}},
				{"id":4,"title":"A separate book","book_series":[{"series":{"id":55},"series_position":4}]},
				{"id":5,"title":"Series sampler","book_series":[{"series":{"id":7},"series_position":4,"compilation":true}]}
			]}}`
		default:
			t.Fatalf("unexpected Hardcover request: %+v", input)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	p := metadata.NewHardcoverProvider("mock", metadata.WithHardcoverClient(mt.Client()))
	books, err := p.SeriesBooks(context.Background(), "shadow of the—leviathan")
	if err != nil || len(books) != 3 {
		t.Fatalf("expected three volumes, got %+v: %v", books, err)
	}
	for i, b := range books {
		if b.Series != "shadow of the—leviathan" || b.SeriesIndex == nil || *b.SeriesIndex != float64(i+1) {
			t.Fatalf("book %d has unexpected membership: %+v", i+1, b)
		}
	}
	if b := books[2]; b.HardcoverID != "3" || b.Title != "A Trade of Blood" || b.PublishDate != "2099-08-04" {
		t.Fatalf("unexpected canonical forthcoming volume: %+v", b)
	}
}

func TestHardcoverSeriesAmbiguousNames(t *testing.T) {
	const named = `"named":[{"id":7,"name":"Court"},{"id":9,"name":"Court"}]`
	for _, tc := range []struct {
		name, linked string
		wantBook     bool
	}{
		{name: "ambiguous", linked: "[]"},
		{name: "conflicting evidence", linked: `[{"id":8,"name":"Court Universe"}]`},
		{name: "known book disambiguates", linked: `[{"id":7,"name":"Court"},{"id":8,"name":"Court Universe"}]`, wantBook: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := testutil.NewMockTransport()
			mt.PanicOnUnmocked = true
			var booksJSON string
			if tc.wantBook {
				booksJSON = `{"data":{"books":[{"id":1,"title":"First","book_series":[{"series":{"id":7}},{"series":{"id":9}}]}]}}`
			}
			registerHardcoverSeries(t, mt, `{"data":{`+named+`,"linked":`+tc.linked+`}}`, booksJSON)
			p := metadata.NewHardcoverProvider("mock", metadata.WithHardcoverClient(mt.Client()))
			books, err := p.SeriesBooks(context.Background(), "Court", metadata.BookMetadata{Title: "First", Authors: []string{"Author"}})
			if err != nil || (len(books) == 1) != tc.wantBook {
				t.Fatalf("unexpected lookup result: %+v, %v", books, err)
			}
		})
	}
}

func TestHardcoverWitnessPrimaryMembership(t *testing.T) {
	const other = `{"series_position":9,"featured":true,"series":{"id":701,"name":"The Witness","author":{"name":"Grace Monroe"}}}`
	const witness = `{"series_position":1,"series":{"id":702,"canonical_id":703,"name":"The Tales of Witness","books_count":3,"author":{"name":"Steven Erikson"}}}`
	const umbrella = `{"series_position":21,"series":{"id":704,"name":"Malazan","author":{"name":"Steven Erikson"}}}`
	for _, tc := range []struct {
		name, featured, memberships string
		wantID                      int
	}{
		{"featured relationship supersedes array order", witness, other + "," + umbrella + "," + witness, 703},
		{"wrong-author featured relationship is ignored", other, other + "," + witness, 703},
		{"featured flag breaks same-author tie", "null", other + "," + umbrella + "," + strings.Replace(witness, `"series_position":1`, `"series_position":1,"featured":true`, 1), 703},
		{"ambiguous unfeatured memberships", "null", other + "," + witness + "," + umbrella, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := testutil.NewMockTransportForTest(t)
			mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
				var input struct{ Query string }
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"featured_book_series", "canonical_id", "author { name }"} {
					if !strings.Contains(input.Query, field) {
						t.Fatalf("query missing %s", field)
					}
				}
				body := fmt.Sprintf(`{"data":{"books_by_pk":{"id":801,"title":"The God Is Not Willing","contributions":[{"author":{"name":"Steven Erikson"}},{"author":{"name":"Grace Monroe"},"contribution":"Narrator"}],"featured_book_series":%s,"book_series":[%s]}}}`, tc.featured, tc.memberships)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			p := metadata.NewHardcoverProvider("mock", metadata.WithHardcoverClient(mt.Client()))
			book, err := p.GetBook(context.Background(), 801)
			if err != nil || book.HardcoverSeriesID != tc.wantID {
				t.Fatalf("book = %+v, err = %v", book, err)
			}
			if tc.wantID != 0 && (book.Series != "The Tales of Witness" || book.SeriesIndex == nil || *book.SeriesIndex != 1 || book.SeriesTotal != 3) {
				t.Fatalf("unexpected primary membership: %+v", book)
			}
		})
	}
}

func TestHardcoverSeriesIdentityBeforeVolumeDeduplication(t *testing.T) {
	mt := testutil.NewMockTransportForTest(t)
	calls := 0
	mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
		calls++
		var input struct {
			Query     string
			Variables struct {
				ID   int
				Name string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		var body string
		switch {
		case strings.Contains(input.Query, "query FindSeries") && regexp.MustCompile("(?i)"+input.Variables.Name).MatchString("The Witness"):
			body = `{"data":{"named":[
    {"id":701,"name":"The Witness","author":{"name":"Grace Monroe"}},
    {"id":702,"name":"The Witness","canonical":{"id":703,"name":"The Tales of Witness","author":{"name":"Steven Erikson"}}}
   ]}}`
		case strings.Contains(input.Query, "query SeriesBooks") && input.Variables.ID == 703:
			body = `{"data":{"books":[
    {"id":801,"title":"The God Is Not Willing","contributions":[{"author":{"name":"Steven Erikson"}}],"book_series":[{"series_position":1,"series":{"id":702,"canonical_id":703}}]},
    {"id":802,"title":"A Different Witness","users_count":10000,"contributions":[{"author":{"name":"Grace Monroe"}}],"book_series":[{"series_position":1,"series":{"id":701}}]}
   ]}}`
		default:
			t.Fatalf("unexpected GraphQL request: %+v", input)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	p := metadata.NewHardcoverProvider("mock", metadata.WithHardcoverClient(mt.Client()))
	ctx := context.Background()
	if found, err := p.SeriesBooks(ctx, "The Witness"); err != nil || len(found) != 0 || calls != 1 {
		t.Fatalf("title-only lookup should be ambiguous: %+v (%v), %d calls", found, err, calls)
	}
	for _, known := range []metadata.BookMetadata{
		{Title: "The God Is Not Willing", Authors: []string{"Steven Erikson"}},
		{HardcoverSeriesID: 703},
	} {
		found, err := p.SeriesBooks(ctx, "The Witness", known)
		if err != nil || len(found) != 1 || found[0].Title != "The God Is Not Willing" || found[0].HardcoverSeriesID != 703 {
			t.Fatalf("unexpected reading order: %+v (%v)", found, err)
		}
	}
	if calls != 4 {
		t.Fatalf("pinned lookup should skip name resolution: %d calls", calls)
	}
}

func TestSharesAuthorCoAuthors(t *testing.T) {
	if !metadata.SharesAuthor([]string{"Raymond E. Feist & William R. Forstchen"}, []string{"Feist, Raymond E."}) {
		t.Fatal("expected co-authored credit to share author with individual primary author")
	}
	if !metadata.SharesAuthor([]string{"Raymond E. Feist"}, []string{"Raymond E. Feist & Joel Rosenberg"}) {
		t.Fatal("expected individual author to match co-authored credit")
	}
	if metadata.SharesAuthor([]string{"Steven Erikson"}, []string{"Grace Monroe"}) {
		t.Fatal("unrelated authors must not match")
	}
}

func TestOpenLibraryLookupByWorkID(t *testing.T) {
	t.Parallel()
	mt := testutil.NewMockTransportForTest(t)
	mt.RegisterString("https://openlibrary.org/works/OL999W.json", http.StatusOK, `{
		"key": "/works/OL999W",
		"title": "Earthsea",
		"subtitle": "A Cycle",
		"description": {"type": "/type/text", "value": "A young mage travels the archipelago."},
		"subjects": ["Fantasy", "Magic"]
	}`, nil)
	mt.RegisterString("https://openlibrary.org/works/OL404W.json", http.StatusNotFound, `{"error":"notfound"}`, nil)

	p := metadata.NewOpenLibraryProvider(metadata.WithOpenLibraryClient(mt.Client()))
	meta, err := p.LookupByWorkID(context.Background(), "/works/OL999W")
	if err != nil {
		t.Fatalf("LookupByWorkID failed: %v", err)
	}
	if meta.Description != "A young mage travels the archipelago." || meta.Subtitle != "A Cycle" || meta.OpenLibraryID != "OL999W" {
		t.Fatalf("unexpected work metadata: %+v", meta)
	}
	if _, err := p.LookupByWorkID(context.Background(), "OL404W"); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := p.LookupByWorkID(context.Background(), "../bad"); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for invalid work id, got %v", err)
	}
}
