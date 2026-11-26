package metadata_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestIsCollectionTitle(t *testing.T) {
	t.Parallel()

	truePositives := []struct {
		title    string
		subtitle string
	}{
		// Explicit issue #108 examples
		{"The Stormlight Archive Series. 4 Books Collection Set. The Way of Kings, Words of Radiance, Oathbringer, Rhythm of War by Brandon Sanderson", ""},
		{"Brandon Sanderson's White Sand Omnibus", ""},

		// Box sets, slipcases, gift sets, collection sets
		{"The Stormlight Archive Box Set", ""},
		{"Mistborn Trilogy Boxed Set", ""},
		{"Dune Saga Box-Set", ""},
		{"The First Law Boxset", ""},
		{"Court", "Boxed Set"},
		{"Harry Potter Collection Set", ""},
		{"The Lord of the Rings Gift Set", ""},
		{"The Chronicles of Narnia 7-Book Slipcase", ""},
		{"The Expanse Slipcase Set", ""},
		{"The Witcher Boxed Collection", ""},

		// Omnibus editions
		{"The Foundation Omnibus", ""},
		{"White Sand", "Omnibus Edition"},
		{"Horus Heresy Omnibuses", ""},

		// Counted multi-book sets, collections, bundles, and bind-ups
		{"Court Three Book Set", ""},
		{"Red Rising 3-Book Box Set", ""},
		{"The Witcher 5-Book Paperback Collection", ""},
		{"Mistborn 3-Book Collection", ""},
		{"The Expanse 4-Book Bundle", ""},
		{"Two-Volume Set", ""},
		{"A Collection of 4 Books", ""},
		{"Set of Three Novels", ""},
		{"Foundation 3-in-1", ""},
		{"Wheel of Time: Three Novels in One", ""},
		{"Dune 2 Books in 1", ""},

		// Numbered ranges and series/complete bundles
		{"Court Books 1-4", ""},
		{"Court Books 1–5", ""},
		{"The Expanse Books 1, 2, and 3", ""},
		{"Mistborn Books 1, 2 & 3", ""},
		{"The First Law #1-3", ""},
		{"Discworld: Volumes I-III", ""},
		{"The Wheel of Time Complete Series", ""},
		{"Mistborn: The Complete Trilogy", ""},
		{"The Complete Mistborn Trilogy", ""},
		{"The Complete Stormlight Archive Series", ""},
		{"Dune Complete Collection", ""},
		{"The Expanse Books Collection", ""},
		{"The Broken Earth Trilogy Collection", ""},
		{"The Expanse Series Bundle", ""},
		{"The Collected Novels of Jane Austen", ""},
		{"The Complete Works of H. P. Lovecraft", ""},
	}

	for _, tc := range truePositives {
		if !metadata.IsCollectionTitle(tc.title, tc.subtitle) {
			t.Errorf("IsCollectionTitle(%q, %q) = false, want true", tc.title, tc.subtitle)
		}
	}

	falsePositives := []struct {
		title    string
		subtitle string
	}{
		// Ordinary single books containing collection-related words
		{"Arcanum Unbounded: The Cosmere Collection", ""},
		{"The Collection", ""},
		{"A Collection of Lies", ""},
		{"The Memory Collection", ""},
		{"The Bone Collection", ""},
		{"The Collector", ""},
		{"The Book Collector", ""},
		{"A Collection of Short Stories", ""},
		{"The Rare Book Collection", ""},
		{"Bird Box", ""},
		{"Heart-Shaped Box", ""},
		{"The Box", "Set in Victorian London"},
		{"Set in Stone", ""},
		{"The Set-Up", ""},
		{"Ready, Set, Go", ""},
		{"A Bundle of Trouble", ""},
		{"Pack of Lies", ""},
		{"The Complete Guide to Gardening", ""},
		{"The Complete Book of Chess", ""},
		{"A Complete Unknown", ""},
		{"Dune", "Complete and Unabridged"},
		{"Dune: Deluxe Slipcase Edition", ""},
		{"The Final Empire", "Book One of the Mistborn Trilogy"},
		{"The Way of Kings (The Stormlight Archive, Book 1)", ""},
		{"The New York Trilogy", ""},
		{"White Sand, Volume 1", ""},
		{"The Shadow of the Wind (Book 1 of 4)", ""},
		{"Dune: Book 1, 1965", ""},
		{"Foundations: Book 1, 2nd Edition", ""},
		{"The Three-Body Problem", ""},
		{"A Tale of Two Cities", ""},
		{"The Book of Five Rings", ""},
		{"Catch-22", ""},
		{"1984", ""},
		{"Collected Poems", ""},
		{"The Complete Stories", ""},
		{"Court Companion", ""},
		{"Companion Edition", ""},
		{"A Trade of Blood", "2089–2099"},
		{"Untitled (Part 1 of 2)", ""},
	}

	for _, tc := range falsePositives {
		if metadata.IsCollectionTitle(tc.title, tc.subtitle) {
			t.Errorf("IsCollectionTitle(%q, %q) = true, want false", tc.title, tc.subtitle)
		}
	}
}

func TestIsMultiBookRelease(t *testing.T) {
	t.Parallel()

	multiBook := []string{
		"Philip.Pullman-His.Dark.Materials.01-03.His.Dark.Materials.(The.Golden.Compass",
		"Philip Pullman - His Dark Materials 01-03 (EPUB)",
		"Branden.Sanderson.Mistborn.1-3.EPUB",
		"The Expanse [01-09] EPUB",
		"Dune.Books.1-6.EPUB",
		"The.Witcher.Complete.Series.EPUB",
		"His.Dark.Materials.Box.Set.EPUB",
		"Foundation Omnibus EPUB",
		"Wheel of Time 01–04 M4B",
		"Harry Potter #1-7 EPUB",
	}
	for _, title := range multiBook {
		if !metadata.IsMultiBookRelease(title) {
			t.Errorf("IsMultiBookRelease(%q) = false, want true", title)
		}
	}

	singleBook := []string{
		"Philip.Pullman-The.Golden.Compass.(His.Dark.Materials.01).EPUB",
		"Philip Pullman - The Golden Compass (His Dark Materials Trilogy Book 1) [EPUB]",
		"Dune (1965) EPUB",
		"Book Title 2024-01-03 EPUB",
		"Book Title 01-03-2024 EPUB",
		"1-2-3 Magic EPUB",
		"Catch-22 EPUB",
		"A Trade of Blood 2089-2099 EPUB",
		"Untitled (Part 1 of 2) M4B",
		"Audiobook 64-128kbps MP3",
		"Audiobook 64-128 kbps MP3",
		"ISBN 978-0-14-028333-4 EPUB",
	}
	for _, title := range singleBook {
		if metadata.IsMultiBookRelease(title) {
			t.Errorf("IsMultiBookRelease(%q) = true, want false", title)
		}
	}
}

func TestProvidersFilterCollectionsAndKeepOrdinaryBooks(t *testing.T) {
	t.Parallel()

	t.Run("Hardcover SearchPage filters compilations and collections while preserving hasMore", func(t *testing.T) {
		mt := testutil.NewMockTransportForTest(t)
		mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
			var req struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(req.Query, "SearchBooks") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"data":{"search":{"ids":[1,2,3,4,5]}}}`)),
				}, nil
			}
			if !strings.Contains(req.Query, "compilation") {
				t.Errorf("SearchDetails query missing compilation field")
			}
			body := `{"data":{"books":[
				{"id":1,"title":"The Way of Kings","contributions":[{"author":{"name":"Brandon Sanderson"}}]},
				{"id":2,"title":"The Stormlight Archive Series. 4 Books Collection Set. The Way of Kings, Words of Radiance, Oathbringer, Rhythm of War by Brandon Sanderson","contributions":[{"author":{"name":"Brandon Sanderson"}}]},
				{"id":3,"title":"Brandon Sanderson's White Sand Omnibus","contributions":[{"author":{"name":"Brandon Sanderson"}}]},
				{"id":4,"title":"Stormlight Archive","compilation":true,"contributions":[{"author":{"name":"Brandon Sanderson"}}]},
				{"id":5,"title":"Arcanum Unbounded: The Cosmere Collection","contributions":[{"author":{"name":"Brandon Sanderson"}}]}
			]}}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})

		hc := metadata.NewHardcoverProvider("mock-key", metadata.WithHardcoverClient(mt.Client()))
		books, hasMore, err := hc.SearchPage(context.Background(), "Brandon Sanderson", 1, 5)
		if err != nil {
			t.Fatal(err)
		}
		if !hasMore {
			t.Fatalf("expected hasMore=true based on raw search page size")
		}
		var titles []string
		for _, b := range books {
			titles = append(titles, b.Title)
		}
		want := []string{"The Way of Kings", "Arcanum Unbounded: The Cosmere Collection"}
		if !slices.Equal(titles, want) {
			t.Fatalf("titles = %v, want %v", titles, want)
		}
	})

	t.Run("Hardcover AuthorBooks filters collections, compilations, and derivatives", func(t *testing.T) {
		mt := testutil.NewMockTransportForTest(t)
		mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
			var req struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(req.Query, "compilation") {
				t.Errorf("AuthorBooks query missing compilation field")
			}
			body := `{"data":{"books":[
				{"id":1,"title":"The Way of Kings","contributions":[{"author":{"name":"Brandon Sanderson"}}]},
				{"id":2,"title":"White Sand, Volume 1","contributions":[{"author":{"name":"Brandon Sanderson"}}]},
				{"id":3,"title":"Brandon Sanderson's White Sand Omnibus","contributions":[{"author":{"name":"Brandon Sanderson"}}]},
				{"id":4,"title":"The Stormlight Archive Series. 4 Books Collection Set. The Way of Kings, Words of Radiance, Oathbringer, Rhythm of War by Brandon Sanderson","contributions":[{"author":{"name":"Brandon Sanderson"}}]},
				{"id":5,"title":"Mistborn Box","compilation":true,"contributions":[{"author":{"name":"Brandon Sanderson"}}]},
				{"id":6,"title":"The Way of Kings","subtitle":"Bonus Chapter","contributions":[{"author":{"name":"Brandon Sanderson"}}]},
				{"id":7,"title":"Arcanum Unbounded: The Cosmere Collection","contributions":[{"author":{"name":"Brandon Sanderson"}}]},
				{"id":8,"title":"Series Sampler","book_series":[{"compilation":true,"series":{"id":10,"name":"Stormlight"}}],"contributions":[{"author":{"name":"Brandon Sanderson"}}]}
			]}}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})

		hc := metadata.NewHardcoverProvider("mock-key", metadata.WithHardcoverClient(mt.Client()))
		books, err := hc.AuthorBooks(context.Background(), "Brandon Sanderson")
		if err != nil {
			t.Fatal(err)
		}
		var titles []string
		for _, b := range books {
			titles = append(titles, b.Title)
		}
		want := []string{
			"The Way of Kings",
			"White Sand, Volume 1",
			"Arcanum Unbounded: The Cosmere Collection",
		}
		if !slices.Equal(titles, want) {
			t.Fatalf("titles = %v, want %v", titles, want)
		}
	})

	t.Run("OpenLibrary SearchPage filters collections while keeping ordinary books and hasMore", func(t *testing.T) {
		mt := testutil.NewMockTransportForTest(t)
		mt.RegisterString("/search.json", http.StatusOK, `{"num_found":10,"docs":[
			{"key":"/works/OL1W","title":"The Way of Kings","author_name":["Brandon Sanderson"]},
			{"key":"/works/OL2W","title":"The Stormlight Archive Series. 4 Books Collection Set. The Way of Kings, Words of Radiance, Oathbringer, Rhythm of War by Brandon Sanderson","author_name":["Brandon Sanderson"]},
			{"key":"/works/OL3W","title":"Brandon Sanderson's White Sand Omnibus","author_name":["Brandon Sanderson"]},
			{"key":"/works/OL4W","title":"The Collection","author_name":["Nina Manning"]}
		]}`, nil)

		ol := metadata.NewOpenLibraryProvider(metadata.WithOpenLibraryClient(mt.Client()))
		books, hasMore, err := ol.SearchPage(context.Background(), "Sanderson", 1, 4)
		if err != nil {
			t.Fatal(err)
		}
		if !hasMore {
			t.Fatalf("expected hasMore=true from num_found")
		}
		var titles []string
		for _, b := range books {
			titles = append(titles, b.Title)
		}
		want := []string{"The Way of Kings", "The Collection"}
		if !slices.Equal(titles, want) {
			t.Fatalf("titles = %v, want %v", titles, want)
		}
	})
}
