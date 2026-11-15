package indexers_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestTorznabClient_GetCapabilities_ExactMatch(t *testing.T) {
	t.Parallel()

	baseURL := "https://indexer.example.local"
	apiKey := "mockkey"

	mt := testutil.NewMockTransport()
	capsURL := "https://indexer.example.local/api?apikey=mockkey&t=caps"
	mt.RegisterFixture(capsURL, http.StatusOK, "testdata/torznab/feed_caps.xml", nil)

	client := indexers.NewTorznabClient(baseURL, apiKey, mt.Client())

	caps, err := client.GetCapabilities(context.Background())
	if err != nil {
		t.Fatalf("GetCapabilities failed: %v", err)
	}

	if caps.ServerTitle != "MockTorznabIndexer" {
		t.Errorf("expected ServerTitle 'MockTorznabIndexer', got %q", caps.ServerTitle)
	}
	if !caps.HasBookSearch() {
		t.Errorf("expected HasBookSearch to be true")
	}
}

func TestTorznabClient_FetchRecent_ExactMatch(t *testing.T) {
	t.Parallel()

	baseURL := "https://indexer.example.local"
	apiKey := "mockkey"

	mt := testutil.NewMockTransport()
	// apikey, cat, limit, t
	feedURL := "https://indexer.example.local/api?apikey=mockkey&cat=7000%2C7020&limit=50&t=search"
	mt.RegisterFixture(feedURL, http.StatusOK, "testdata/torznab/feed_rss.xml", nil)

	client := indexers.NewTorznabClient(baseURL, apiKey, mt.Client())

	releases, err := client.FetchRecent(context.Background(), []int{7000, 7020}, 50)
	if err != nil {
		t.Fatalf("FetchRecent failed: %v", err)
	}

	if len(releases) != 3 {
		t.Fatalf("expected 3 releases, got %d", len(releases))
	}
}

func TestTorznabClient_Search_ExactMatch(t *testing.T) {
	t.Parallel()

	baseURL := "https://indexer.example.local"
	apiKey := "mockkey"

	q := indexers.SearchQuery{
		Title: "Test Book Alpha",
		Mode:  "book",
	}

	expectedURL := indexers.BuildURL(baseURL, apiKey, q)

	mt := testutil.NewMockTransport()
	mt.RegisterFixture(expectedURL, http.StatusOK, "testdata/torznab/search_response.xml", nil)

	client := indexers.NewTorznabClient(baseURL, apiKey, mt.Client())

	releases, err := client.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(releases) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(releases))
	}
}

func TestTorznabClient_SearchBook_Fallback(t *testing.T) {
	t.Parallel()

	baseURL := "https://indexer.example.local"
	apiKey := "mockkey"
	author := "Synthetic Author One"
	title := "Test Book Alpha"
	categories := []int{7000, 7020}

	mt := testutil.NewMockTransport()

	// Primary search returns empty feed
	primaryQuery := indexers.SearchQuery{
		Author:     author,
		Title:      title,
		Categories: categories,
		Mode:       "book",
	}
	primaryURL := indexers.BuildURL(baseURL, apiKey, primaryQuery)
	emptyFeed := []byte(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Empty</title></channel></rss>`)
	mt.RegisterResponse(primaryURL, http.StatusOK, emptyFeed, nil)

	// Fallback 1 (exact quoted query) returns search_response fixture
	fallbackQuery := indexers.SearchQuery{
		Query:      indexers.ExactQuotedQuery(author, title),
		Categories: categories,
		Mode:       "search",
	}
	fallbackURL := indexers.BuildURL(baseURL, apiKey, fallbackQuery)
	mt.RegisterFixture(fallbackURL, http.StatusOK, "testdata/torznab/search_response.xml", nil)

	client := indexers.NewTorznabClient(baseURL, apiKey, mt.Client())

	releases, err := client.SearchBook(context.Background(), author, title, categories)
	if err != nil {
		t.Fatalf("SearchBook failed on fallback: %v", err)
	}

	if len(releases) != 2 {
		t.Fatalf("expected 2 releases via fallback, got %d", len(releases))
	}
}

func TestTorznabClient_ErrorSimulation(t *testing.T) {
	t.Parallel()

	baseURL := "https://indexer.example.local"
	apiKey := "mockkey"

	t.Run("HTTP 500 Internal Server Error", func(t *testing.T) {
		mt := testutil.NewMockTransport()
		capsURL := "https://indexer.example.local/api?apikey=mockkey&t=caps"
		mt.RegisterResponse(capsURL, http.StatusInternalServerError, []byte("Database error"), nil)

		client := indexers.NewTorznabClient(baseURL, apiKey, mt.Client())
		_, err := client.GetCapabilities(context.Background())
		if err == nil {
			t.Fatal("expected error on HTTP 500, got nil")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("expected error message to mention 500, got %q", err.Error())
		}
	})

	t.Run("Simulated Network Error", func(t *testing.T) {
		mt := testutil.NewMockTransport()
		q := indexers.SearchQuery{Query: "test", Mode: "search"}
		reqURL := indexers.BuildURL(baseURL, apiKey, q)
		simulatedErr := errors.New("connection reset by peer")
		mt.RegisterError(reqURL, simulatedErr)

		client := indexers.NewTorznabClient(baseURL, apiKey, mt.Client())
		_, err := client.Search(context.Background(), q)
		if err == nil {
			t.Fatal("expected error on network failure, got nil")
		}
		if !errors.Is(err, simulatedErr) && !strings.Contains(err.Error(), "connection reset") {
			t.Errorf("expected network error, got %v", err)
		}
	})
}

func TestTorznabClient_UnmockedNetworkRejection(t *testing.T) {
	t.Parallel()

	baseURL := "https://unmocked.indexer.local"
	apiKey := "testkey"

	t.Run("Rejection with PanicOnUnmocked enabled", func(t *testing.T) {
		mt := testutil.NewMockTransport()
		client := indexers.NewTorznabClient(baseURL, apiKey, mt.Client())

		defer func() {
			r := recover()
			if r == nil {
				t.Fatalf("expected panic on unmocked network request")
			}
			msg, ok := r.(string)
			if !ok || !strings.Contains(msg, "unmocked outbound HTTP request") {
				t.Errorf("unexpected panic message: %v", r)
			}
		}()

		_, _ = client.GetCapabilities(context.Background())
	})

	t.Run("Rejection with PanicOnUnmocked disabled", func(t *testing.T) {
		mt := testutil.NewMockTransport()
		mt.PanicOnUnmocked = false
		client := indexers.NewTorznabClient(baseURL, apiKey, mt.Client())

		_, err := client.GetCapabilities(context.Background())
		if err == nil {
			t.Fatalf("expected error on unmocked request, got nil")
		}
		if !errors.Is(err, testutil.ErrUnmockedRequest) && !strings.Contains(err.Error(), "unmocked") {
			t.Errorf("expected ErrUnmockedRequest, got %v", err)
		}
	})
}
