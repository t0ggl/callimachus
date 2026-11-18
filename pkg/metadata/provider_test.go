package metadata_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
					Variables map[string]interface{} `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				if int(input.Variables["offset"].(float64)) != calls*100 {
					t.Error("wrong page offset")
				}
				calls++
				books := []map[string]interface{}{}
				if calls == 1 {
					for i := 0; i < 100; i++ {
						books = append(books, map[string]interface{}{"id": i + 1, "title": "Book", "release_date": "2028-01-01", "contributions": []map[string]interface{}{{"author": map[string]string{"name": "Author"}}}})
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
