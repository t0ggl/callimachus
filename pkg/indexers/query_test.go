package indexers_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/indexers"
)

func TestBuildURL_BookSearch(t *testing.T) {
	t.Parallel()

	baseURL := "https://indexer.example.local"
	apiKey := "secret-api-key-123"

	q := indexers.SearchQuery{
		Author:     "Synthetic Author",
		Title:      "Test Title & Volume 1",
		ISBN:       "9780123456789",
		Categories: []int{7000, 7020},
		Limit:      50,
		Offset:     10,
	}

	resultURL := indexers.BuildURL(baseURL, apiKey, q)
	u, err := url.Parse(resultURL)
	if err != nil {
		t.Fatalf("failed to parse built URL %q: %v", resultURL, err)
	}

	if u.Scheme != "https" || u.Host != "indexer.example.local" || u.Path != "/api" {
		t.Errorf("unexpected URL base/path: %s", u.String())
	}

	query := u.Query()
	if query.Get("apikey") != "secret-api-key-123" {
		t.Errorf("expected apikey secret-api-key-123, got %q", query.Get("apikey"))
	}
	if query.Get("t") != "book" {
		t.Errorf("expected t=book, got %q", query.Get("t"))
	}
	if query.Get("author") != "Synthetic Author" {
		t.Errorf("expected author 'Synthetic Author', got %q", query.Get("author"))
	}
	if query.Get("title") != "Test Title & Volume 1" {
		t.Errorf("expected title 'Test Title & Volume 1', got %q", query.Get("title"))
	}
	if query.Get("isbn") != "9780123456789" {
		t.Errorf("expected isbn '9780123456789', got %q", query.Get("isbn"))
	}
	if query.Get("cat") != "7000,7020" {
		t.Errorf("expected cat '7000,7020', got %q", query.Get("cat"))
	}
	if query.Get("limit") != "50" {
		t.Errorf("expected limit '50', got %q", query.Get("limit"))
	}
	if query.Get("offset") != "10" {
		t.Errorf("expected offset '10', got %q", query.Get("offset"))
	}
}

func TestBuildURL_SearchModeWithEscaping(t *testing.T) {
	t.Parallel()

	baseURL := "http://indexer.local:9696/api"
	apiKey := "key-xyz"

	q := indexers.SearchQuery{
		Query:      `"Quoted Author" "Special & Title"`,
		Categories: []int{indexers.CategoryAudioAudiobook},
		Mode:       "search",
	}

	resultURL := indexers.BuildURL(baseURL, apiKey, q)
	u, err := url.Parse(resultURL)
	if err != nil {
		t.Fatalf("failed to parse built URL %q: %v", resultURL, err)
	}

	if u.Path != "/api" {
		t.Errorf("expected /api, got %s", u.Path)
	}

	query := u.Query()
	if query.Get("t") != "search" {
		t.Errorf("expected t=search, got %q", query.Get("t"))
	}
	if query.Get("q") != `"Quoted Author" "Special & Title"` {
		t.Errorf("expected escaped query preserving quotes, got %q", query.Get("q"))
	}
	if query.Get("cat") != "3030" {
		t.Errorf("expected cat 3030, got %q", query.Get("cat"))
	}
	if !strings.Contains(resultURL, "cat=3030") {
		t.Errorf("resultURL missing cat parameter: %s", resultURL)
	}
}

func TestExactQuotedQuery(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		author   string
		title    string
		expected string
	}{
		{"Author Name", "Book Title", `"Author Name Book Title"`},
		{"Author Name", "", `"Author Name"`},
		{"", "Book Title", `"Book Title"`},
		{"  Author  ", "  Title  ", `"Author Title"`},
		{"", "", ""},
	}

	for _, tc := range testCases {
		got := indexers.ExactQuotedQuery(tc.author, tc.title)
		if got != tc.expected {
			t.Errorf("ExactQuotedQuery(%q, %q) = %q, expected %q", tc.author, tc.title, got, tc.expected)
		}
	}
}

func TestBroadKeywordQuery(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		author   string
		title    string
		expected string
	}{
		{"Author Name", "Book Title", "Author Name Book Title"},
		{"Author Name", "", "Author Name"},
		{"", "Book Title", "Book Title"},
		{"", "", ""},
	}

	for _, tc := range testCases {
		got := indexers.BroadKeywordQuery(tc.author, tc.title)
		if got != tc.expected {
			t.Errorf("BroadKeywordQuery(%q, %q) = %q, expected %q", tc.author, tc.title, got, tc.expected)
		}
	}
}

func TestFallbackQueries(t *testing.T) {
	t.Parallel()

	author := "Synthetic Author"
	title := "Synthetic Title"
	cats := []int{7000, 7020}

	fallbacks := indexers.FallbackQueries(author, title, cats)
	if len(fallbacks) != 3 {
		t.Fatalf("expected 3 fallback queries, got %d", len(fallbacks))
	}

	// 1. Quoted exact query
	if fallbacks[0].Query != `"Synthetic Author Synthetic Title"` {
		t.Errorf("expected quoted query, got %q", fallbacks[0].Query)
	}
	if fallbacks[0].Mode != "search" {
		t.Errorf("expected mode search, got %q", fallbacks[0].Mode)
	}
	if len(fallbacks[0].Categories) != 2 {
		t.Errorf("expected 2 categories in fallback query")
	}

	// 2. Broad keyword query
	if fallbacks[1].Query != "Synthetic Author Synthetic Title" {
		t.Errorf("expected broad query, got %q", fallbacks[1].Query)
	}

	// 3. Title-only query
	if fallbacks[2].Query != "Synthetic Title" {
		t.Errorf("expected title query, got %q", fallbacks[2].Query)
	}
}
