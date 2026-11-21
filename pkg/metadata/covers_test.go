package metadata

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

const testWebP200x300B64 = "UklGRiQAAABXRUJQVlA4TBgAAAAvx8BKAAdQqHqUsf8BAEX6/58i+p/6fyk="

func makeTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeTestGIF(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.White, color.Black})
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestValidateExternalImageURL(t *testing.T) {
	valid := []string{
		"https://covers.openlibrary.org/b/id/12345-L.jpg?default=false",
		"http://images.example.com/books/cover.png",
		"https://is1-ssl.mzstatic.com/image/thumb/Publication/v4/1000x1000bb.jpg#frag",
		"https://93.184.216.34/cover.jpg",
	}
	for _, raw := range valid {
		u, err := ValidateExternalImageURL(raw)
		if err != nil {
			t.Errorf("ValidateExternalImageURL(%q) unexpected error: %v", raw, err)
		} else if u.Fragment != "" {
			t.Errorf("ValidateExternalImageURL(%q) did not strip fragment: %q", raw, u.String())
		}
	}

	invalid := []string{
		"",
		"   ",
		"ftp://example.com/cover.jpg",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"data:image/png;base64,AAAA",
		"https://user:pass@example.com/cover.jpg",
		"http://localhost/cover.jpg",
		"https://sub.localhost/cover.jpg",
		"http://printer.local/cover.jpg",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://127.0.0.1/cover.jpg",
		"http://127.0.0.2:8080/cover.jpg",
		"http://10.0.0.1/cover.jpg",
		"http://172.16.5.4/cover.jpg",
		"http://192.168.1.10/cover.jpg",
		"http://169.254.169.254/latest/meta-data/",
		"http://100.64.0.1/cover.jpg",
		"http://0.0.0.0/cover.jpg",
		"http://[::1]/cover.jpg",
		"http://[::ffff:127.0.0.1]/cover.jpg",
		"http://[fe80::1]/cover.jpg",
		"http://[fc00::1]/cover.jpg",
		"https://example.com/" + strings.Repeat("a", maxCoverURLBytes),
	}
	for _, raw := range invalid {
		if u, err := ValidateExternalImageURL(raw); err == nil {
			t.Errorf("ValidateExternalImageURL(%q) succeeded with %q, want error", raw, u.String())
		}
	}
}

func TestSafeImageCheckRedirectAndDownload(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/next.jpg", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Cookie", "sid=secret")
	if err := SafeImageCheckRedirect(req, make([]*http.Request, 1)); err != nil {
		t.Fatalf("valid redirect failed: %v", err)
	}
	if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
		t.Fatal("redirect did not strip sensitive headers")
	}
	if err := SafeImageCheckRedirect(req, make([]*http.Request, maxCoverRedirects)); err == nil {
		t.Fatal("expected error after max redirects")
	}
	badReq, _ := http.NewRequest(http.MethodGet, "https://169.254.169.254/meta", nil)
	if err := SafeImageCheckRedirect(badReq, make([]*http.Request, 1)); !errors.Is(err, ErrDisallowedImageURL) {
		t.Fatalf("expected ErrDisallowedImageURL on private redirect, got %v", err)
	}

	// DownloadExternalImage with MockTransport (PanicOnUnmocked = true)
	mt := testutil.NewMockTransportForTest(t)
	jpegBytes := makeTestJPEG(t, 400, 600)
	mt.RegisterResponse("https://images.example.test/good.jpg", http.StatusOK, jpegBytes, http.Header{"Content-Type": {"image/jpeg"}})
	mt.RegisterHandler("https://images.example.test/huge.jpg", func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: 2048,
			Body:          io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{0xff}, 2048))),
		}, nil
	})
	mt.RegisterString("https://images.example.test/missing.jpg", http.StatusNotFound, "not found", nil)

	// Disallowed URL must fail before hitting transport.
	if _, err := DownloadExternalImage(context.Background(), mt.Client(), "http://127.0.0.1/secret.jpg", 1024); !errors.Is(err, ErrDisallowedImageURL) {
		t.Fatalf("expected ErrDisallowedImageURL, got %v", err)
	}

	got, err := DownloadExternalImage(context.Background(), mt.Client(), "https://images.example.test/good.jpg", 1<<20)
	if err != nil || !bytes.Equal(got, jpegBytes) {
		t.Fatalf("DownloadExternalImage good.jpg failed: err=%v len=%d", err, len(got))
	}

	if _, err := DownloadExternalImage(context.Background(), mt.Client(), "https://images.example.test/huge.jpg", 1024); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("expected ErrImageTooLarge, got %v", err)
	}

	if _, err := DownloadExternalImage(context.Background(), mt.Client(), "https://images.example.test/missing.jpg", 1<<20); err == nil {
		t.Fatal("expected error for 404 image")
	}
}

func TestSearchCoversFilteringPaginationAndDeduplication(t *testing.T) {
	mt := testutil.NewMockTransportForTest(t)

	goodPortrait := makeTestJPEG(t, 400, 600)
	goodSquare := makeTestPNG(t, 500, 500)
	goodWebP, err := base64.StdEncoding.DecodeString(testWebP200x300B64)
	if err != nil {
		t.Fatal(err)
	}
	tooSmall := makeTestJPEG(t, 120, 180)
	tooWide := makeTestJPEG(t, 900, 300)
	gifPlaceholder := makeTestGIF(t, 400, 600)

	// Hardcover GraphQL returns API-provided dimensions (good, tooSmall, tooWide, tooLarge)
	// None of these should trigger image probe requests because width & height are already known!
	mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
		var req struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		var body string
		if strings.Contains(req.Query, "SearchBookCovers") {
			page := int(req.Variables["page"].(float64))
			if page == 1 {
				body = `{"data":{"search":{"ids":[1,2]}}}`
			} else {
				body = `{"data":{"search":{"ids":[3]}}}`
			}
		} else {
			ids := req.Variables["ids"].([]interface{})
			firstID := int(ids[0].(float64))
			if firstID == 1 {
				body = `{"data":{"books":[
					{"id":1,"title":"Dune","image":{"url":"https://assets.hardcover.app/dune-ok.jpg","width":600,"height":900},
					 "editions":[
						{"cached_image":{"url":"https://assets.hardcover.app/dune-tiny.jpg","width":100,"height":150}},
						{"cached_image":{"url":"https://assets.hardcover.app/dune-banner.jpg","width":1200,"height":400}},
						{"cached_image":{"url":"https://assets.hardcover.app/dune-giant.jpg","width":5000,"height":6000}}
					 ]},
					{"id":2,"title":"Dune Duplicate","image":{"url":"https://assets.hardcover.app/dune-ok.jpg","width":600,"height":900}}
				]}}`
			} else {
				body = `{"data":{"books":[
					{"id":3,"title":"Dune Page 2","image":{"url":"https://assets.hardcover.app/dune-p2.jpg","width":800,"height":1200}}
				]}}`
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	// Apple Books (iTunes) returns page 1 & page 2 results
	mt.RegisterHandler("/search", func(r *http.Request) (*http.Response, error) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		var body string
		if offset == 0 {
			body = `{"resultCount":2,"results":[
				{"trackName":"Dune Apple","artworkUrl100":"https://is1-ssl.mzstatic.com/image/thumb/book1/100x100bb.jpg"},
				{"trackName":"Dune Wide","artworkUrl100":"https://is1-ssl.mzstatic.com/image/thumb/wide/100x100bb.jpg"}
			]}`
		} else {
			body = `{"resultCount":1,"results":[
				{"trackName":"Dune Apple Page 2","artworkUrl100":"https://is1-ssl.mzstatic.com/image/thumb/book2/100x100bb.png"}
			]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	// OpenLibrary returns page 1 (with valid, tiny, gif, and missing 404 covers) and page 2
	mt.RegisterHandler("/search.json", func(r *http.Request) (*http.Response, error) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		var body string
		if page == 1 {
			body = `{"num_found":5,"docs":[
				{"key":"/works/OL1W","title":"Dune OL","cover_i":101},
				{"key":"/works/OL2W","title":"Dune Small","cover_i":102},
				{"key":"/works/OL3W","title":"Dune GIF","cover_i":103},
				{"key":"/works/OL4W","title":"Dune Missing","cover_i":104}
			]}`
		} else {
			body = `{"num_found":5,"docs":[
				{"key":"/works/OL5W","title":"Dune OL Page 2","cover_i":105}
			]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	// Google Books returns a WebP cover and an oversized Content-Range cover
	mt.RegisterHandler("/books/v1/volumes", func(r *http.Request) (*http.Response, error) {
		startIndex, _ := strconv.Atoi(r.URL.Query().Get("startIndex"))
		var body string
		if startIndex == 0 {
			body = `{"totalItems":2,"items":[
				{"volumeInfo":{"title":"Dune Google","imageLinks":{"large":"http://books.example.test/google-webp.webp?edge=curl","thumbnail":"http://books.example.test/google-thumb.webp"}}},
				{"volumeInfo":{"title":"Dune Oversized","imageLinks":{"large":"https://books.example.test/oversized.jpg"}}}
			]}`
		} else {
			body = `{"totalItems":2,"items":[]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	// Register image header probes (verifies Range probing works on 206 and 200)
	mt.RegisterResponse("https://is1-ssl.mzstatic.com/image/thumb/book1/1000x1000bb.jpg", http.StatusPartialContent, goodPortrait, http.Header{
		"Content-Type":  {"image/jpeg"},
		"Content-Range": {"bytes 0-1000/12000"},
	})
	mt.RegisterResponse("https://is1-ssl.mzstatic.com/image/thumb/wide/1000x1000bb.jpg", http.StatusOK, tooWide, http.Header{"Content-Type": {"image/jpeg"}})
	mt.RegisterResponse("https://is1-ssl.mzstatic.com/image/thumb/book2/1000x1000bb.png", http.StatusOK, goodSquare, http.Header{"Content-Type": {"image/png"}})
	mt.RegisterResponse("https://covers.openlibrary.org/b/id/101-L.jpg?default=false", http.StatusOK, goodPortrait, http.Header{"Content-Type": {"image/jpeg"}})
	mt.RegisterResponse("https://covers.openlibrary.org/b/id/102-L.jpg?default=false", http.StatusOK, tooSmall, http.Header{"Content-Type": {"image/jpeg"}})
	mt.RegisterResponse("https://covers.openlibrary.org/b/id/103-L.jpg?default=false", http.StatusOK, gifPlaceholder, http.Header{"Content-Type": {"image/gif"}})
	mt.RegisterString("https://covers.openlibrary.org/b/id/104-L.jpg?default=false", http.StatusNotFound, "not found", nil)
	mt.RegisterResponse("https://covers.openlibrary.org/b/id/105-L.jpg?default=false", http.StatusOK, goodPortrait, http.Header{"Content-Type": {"image/jpeg"}})
	mt.RegisterResponse("https://books.example.test/google-webp.webp", http.StatusOK, goodWebP, http.Header{"Content-Type": {"image/webp"}})
	mt.RegisterResponse("https://books.example.test/oversized.jpg", http.StatusPartialContent, goodPortrait, http.Header{
		"Content-Type":  {"image/jpeg"},
		"Content-Range": {"bytes 0-1000/20971520"}, // 20 MiB > 10 MiB limit
	})

	opts := CoverSearchOptions{
		Client:             mt.Client(),
		OpenLibraryEnabled: true,
		HardcoverAPIKey:    "hc-test-key",
		GoogleBooksAPIKey:  "gb-test-key",
		Page:               1,
		PageSize:           2,
	}

	page1, err := SearchCovers(context.Background(), "Dune Frank Herbert", opts)
	if err != nil {
		t.Fatalf("SearchCovers page 1 failed: %v", err)
	}
	if page1.Page != 1 || !page1.HasMore || page1.NextPage != 2 {
		t.Fatalf("unexpected page 1 pagination: %+v", page1)
	}
	// Expect exactly 4 valid filtered candidates on page 1:
	// Hardcover dune-ok (600x900), Apple Books book1 (400x600), OpenLibrary 101 (400x600), Google Books webp (200x300).
	if len(page1.Candidates) != 4 {
		t.Fatalf("expected 4 filtered candidates on page 1, got %d: %+v", len(page1.Candidates), page1.Candidates)
	}
	gotURLs := map[string]CoverCandidate{}
	for _, c := range page1.Candidates {
		gotURLs[c.URL] = c
	}
	if c, ok := gotURLs["https://assets.hardcover.app/dune-ok.jpg"]; !ok || c.Width != 600 || c.Height != 900 || c.Source != "Hardcover" {
		t.Errorf("missing or wrong Hardcover candidate: %+v", c)
	}
	if c, ok := gotURLs["https://is1-ssl.mzstatic.com/image/thumb/book1/1000x1000bb.jpg"]; !ok || c.Width != 400 || c.Height != 600 || c.Source != "Apple Books" || !strings.Contains(c.ThumbnailURL, "400x400bb") {
		t.Errorf("missing or wrong Apple Books candidate: %+v", c)
	}
	if c, ok := gotURLs["https://covers.openlibrary.org/b/id/101-L.jpg?default=false"]; !ok || c.Width != 400 || c.Height != 600 || c.Source != "OpenLibrary" {
		t.Errorf("missing or wrong OpenLibrary candidate: %+v", c)
	}
	if c, ok := gotURLs["https://books.example.test/google-webp.webp"]; !ok || c.Width != 200 || c.Height != 300 || c.Source != "Google Books" {
		t.Errorf("missing or wrong Google Books candidate: %+v", c)
	}

	// Page 2
	opts.Page = 2
	page2, err := SearchCovers(context.Background(), "Dune Frank Herbert", opts)
	if err != nil {
		t.Fatalf("SearchCovers page 2 failed: %v", err)
	}
	if page2.Page != 2 || !page2.HasMore || page2.NextPage != 3 {
		t.Fatalf("unexpected page 2 pagination: %+v", page2)
	}
	if len(page2.Candidates) != 3 {
		t.Fatalf("expected 3 filtered candidates on page 2, got %d: %+v", len(page2.Candidates), page2.Candidates)
	}
}

func TestSearchCoversAudiobookPreferenceAndUnavailable(t *testing.T) {
	mt := testutil.NewMockTransportForTest(t)
	var seenMedia string
	mt.RegisterHandler("/search", func(r *http.Request) (*http.Response, error) {
		seenMedia = r.URL.Query().Get("media")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"resultCount":0,"results":[]}`)),
		}, nil
	})

	res, err := SearchCovers(context.Background(), "Project Hail Mary", CoverSearchOptions{
		Client:             mt.Client(),
		OpenLibraryEnabled: false,
		PreferSquare:       true,
		Page:               MaxCoverSearchPage,
	})
	if err != nil {
		t.Fatalf("SearchCovers failed: %v", err)
	}
	if seenMedia != "audiobook" {
		t.Fatalf("expected media=audiobook when PreferSquare=true, got %q", seenMedia)
	}
	if res.HasMore || res.NextPage != 0 {
		t.Fatalf("expected HasMore=false at MaxCoverSearchPage, got %+v", res)
	}

	// When all providers error, return ErrCoverSearchUnavailable
	mtErr := testutil.NewMockTransportForTest(t)
	mtErr.RegisterError("/search", errors.New("network down"))
	if _, err := SearchCovers(context.Background(), "Dune", CoverSearchOptions{
		Client:             mtErr.Client(),
		OpenLibraryEnabled: false,
	}); !errors.Is(err, ErrCoverSearchUnavailable) {
		t.Fatalf("expected ErrCoverSearchUnavailable, got %v", err)
	}
}
