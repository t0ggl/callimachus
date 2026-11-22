package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var reYear = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)

// OpenLibraryProvider queries the OpenLibrary REST API.
type OpenLibraryProvider struct {
	baseURL string
	client  *http.Client
}

// OpenLibraryOption configures an OpenLibraryProvider.
type OpenLibraryOption func(*OpenLibraryProvider)

// WithOpenLibraryClient sets the HTTP client.
func WithOpenLibraryClient(client *http.Client) OpenLibraryOption {
	return func(p *OpenLibraryProvider) {
		if client != nil {
			p.client = client
		}
	}
}

// WithOpenLibraryBaseURL sets the base URL for OpenLibrary requests.
func WithOpenLibraryBaseURL(url string) OpenLibraryOption {
	return func(p *OpenLibraryProvider) {
		if url != "" {
			p.baseURL = strings.TrimRight(url, "/")
		}
	}
}

// NewOpenLibraryProvider creates an initialized OpenLibraryProvider.
func NewOpenLibraryProvider(opts ...OpenLibraryOption) *OpenLibraryProvider {
	p := &OpenLibraryProvider{
		baseURL: "https://openlibrary.org",
		client:  http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns the provider identifier.
func (p *OpenLibraryProvider) Name() string {
	return "openlibrary"
}

type openlibraryAuthorRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type openlibraryLanguageRef struct {
	Key string `json:"key"`
}

type openlibraryWorkRef struct {
	Key string `json:"key"`
}

type openlibraryDescription struct {
	Value string
}

func (d *openlibraryDescription) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		d.Value = s
		return nil
	}
	var obj struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(b, &obj); err == nil {
		d.Value = obj.Value
		return nil
	}
	return nil
}

type openlibraryBookResponse struct {
	Key            string                   `json:"key"`
	Title          string                   `json:"title"`
	Subtitle       string                   `json:"subtitle"`
	Authors        []openlibraryAuthorRef   `json:"authors"`
	Publishers     []string                 `json:"publishers"`
	PublishDate    string                   `json:"publish_date"`
	NumberOfPages  int                      `json:"number_of_pages"`
	ISBN10         []string                 `json:"isbn_10"`
	ISBN13         []string                 `json:"isbn_13"`
	PhysicalFormat string                   `json:"physical_format"`
	Covers         []int                    `json:"covers"`
	Works          []openlibraryWorkRef     `json:"works"`
	Languages      []openlibraryLanguageRef `json:"languages"`
	Subjects       []string                 `json:"subjects"`
	Description    *openlibraryDescription  `json:"description"`
}

type openlibrarySearchDoc struct {
	Key              string   `json:"key"`
	Title            string   `json:"title"`
	Subtitle         string   `json:"subtitle"`
	AuthorName       []string `json:"author_name"`
	FirstPublishYear int      `json:"first_publish_year"`
	Publisher        []string `json:"publisher"`
	ISBN             []string `json:"isbn"`
	CoverI           int      `json:"cover_i"`
	Language         []string `json:"language"`
	Subject          []string `json:"subject"`
}

type openlibrarySearchResponse struct {
	NumFound int                    `json:"num_found"`
	Docs     []openlibrarySearchDoc `json:"docs"`
}

// LookupByISBN queries OpenLibrary for a book matching an ISBN.
func (p *OpenLibraryProvider) LookupByISBN(ctx context.Context, isbn string) (*BookMetadata, error) {
	cleanISBN := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(isbn), "-", ""), " ", "")
	if cleanISBN == "" {
		return nil, fmt.Errorf("openlibrary: empty isbn: %w", ErrNotFound)
	}

	reqURL := fmt.Sprintf("%s/isbn/%s.json", p.baseURL, cleanISBN)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("openlibrary: create request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openlibrary: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openlibrary: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openlibrary: read body: %w", err)
	}

	var book openlibraryBookResponse
	if err := json.Unmarshal(bodyBytes, &book); err != nil {
		return nil, fmt.Errorf("openlibrary: unmarshal book: %w", err)
	}

	meta := BookMetadata{
		Title:         book.Title,
		Subtitle:      book.Subtitle,
		PublishDate:   book.PublishDate,
		OpenLibraryID: strings.TrimPrefix(book.Key, "/books/"),
		Genres:        book.Subjects,
	}

	if book.Description != nil {
		meta.Description = book.Description.Value
	}

	if match := reYear.FindString(book.PublishDate); match != "" {
		meta.Year, _ = strconv.Atoi(match)
	}

	if len(book.Publishers) > 0 {
		meta.Publisher = book.Publishers[0]
	}

	if len(book.ISBN13) > 0 {
		meta.ISBN13 = book.ISBN13[0]
	} else if len(book.ISBN10) > 0 {
		meta.ISBN13 = book.ISBN10[0]
	}

	if len(book.Covers) > 0 && book.Covers[0] > 0 {
		meta.CoverURL = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-L.jpg", book.Covers[0])
	}

	for _, a := range book.Authors {
		if a.Name != "" {
			meta.Authors = append(meta.Authors, a.Name)
		}
	}

	if len(book.Languages) > 0 {
		for _, lang := range book.Languages {
			if cleaned := strings.TrimSpace(strings.TrimPrefix(lang.Key, "/languages/")); cleaned != "" {
				if meta.Language == "" {
					meta.Language = cleaned
				}
				meta.Languages = append(meta.Languages, cleaned)
			}
		}
	}

	return &meta, nil
}

// Search queries OpenLibrary search API by query text.
func (p *OpenLibraryProvider) Search(ctx context.Context, query string) ([]BookMetadata, error) {
	results, _, err := p.search(ctx, query, 0, 0)
	return results, err
}

// SearchPage queries a single page of OpenLibrary search results and reports whether more pages remain.
func (p *OpenLibraryProvider) SearchPage(ctx context.Context, query string, page, limit int) ([]BookMetadata, bool, error) {
	if limit <= 0 {
		results, err := p.Search(ctx, query)
		return results, false, err
	}
	if page < 1 {
		page = 1
	}
	return p.search(ctx, query, page, limit)
}

func (p *OpenLibraryProvider) search(ctx context.Context, query string, page, limit int) ([]BookMetadata, bool, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, false, nil
	}

	params := url.Values{
		"q":      {trimmed},
		"fields": {"key,title,subtitle,author_name,first_publish_year,isbn,cover_i,publisher,subject,language"},
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
		params.Set("page", strconv.Itoa(page))
	}
	reqURL := p.baseURL + "/search.json?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("openlibrary: create search request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("openlibrary: do search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("openlibrary: search unexpected status %d: %s", resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("openlibrary: read search body: %w", err)
	}

	var searchRes openlibrarySearchResponse
	if err := json.Unmarshal(bodyBytes, &searchRes); err != nil {
		return nil, false, fmt.Errorf("openlibrary: unmarshal search: %w", err)
	}

	var results []BookMetadata
	for _, doc := range searchRes.Docs {
		meta := BookMetadata{
			Title:         doc.Title,
			Subtitle:      doc.Subtitle,
			Authors:       doc.AuthorName,
			Year:          doc.FirstPublishYear,
			OpenLibraryID: strings.TrimPrefix(doc.Key, "/works/"),
			Genres:        doc.Subject,
		}
		if len(doc.Publisher) > 0 {
			meta.Publisher = doc.Publisher[0]
		}
		if len(doc.ISBN) > 0 {
			meta.ISBN13 = doc.ISBN[0]
			meta.ISBNs = doc.ISBN
			for _, isbn := range doc.ISBN {
				if len(isbn) == 13 {
					meta.ISBN13 = isbn
					break
				}
			}
		}
		if doc.CoverI > 0 {
			meta.CoverURL = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-L.jpg", doc.CoverI)
		}
		if len(doc.Language) > 0 {
			for _, lang := range doc.Language {
				if cleaned := strings.TrimSpace(lang); cleaned != "" {
					if meta.Language == "" {
						meta.Language = cleaned
					}
					meta.Languages = append(meta.Languages, cleaned)
				}
			}
		}
		results = append(results, meta)
	}

	hasMore := false
	if limit > 0 {
		hasMore = searchRes.NumFound > page*limit || (searchRes.NumFound == 0 && len(searchRes.Docs) >= limit)
	}
	return results, hasMore, nil
}

// LookupByWorkID retrieves work-level metadata (including description) from OpenLibrary.
func (p *OpenLibraryProvider) LookupByWorkID(ctx context.Context, workID string) (*BookMetadata, error) {
	cleanID := strings.TrimSpace(workID)
	cleanID = strings.TrimPrefix(cleanID, "/works/")
	cleanID = strings.TrimPrefix(cleanID, "works/")
	if cleanID == "" || strings.ContainsAny(cleanID, "/\\?# ") {
		return nil, fmt.Errorf("openlibrary: invalid work id: %w", ErrNotFound)
	}

	reqURL := fmt.Sprintf("%s/works/%s.json", p.baseURL, url.PathEscape(cleanID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("openlibrary: create work request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openlibrary: do work request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openlibrary: work unexpected status %d: %s", resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openlibrary: read work body: %w", err)
	}

	var work struct {
		Key         string                  `json:"key"`
		Title       string                  `json:"title"`
		Subtitle    string                  `json:"subtitle"`
		Description *openlibraryDescription `json:"description"`
		Subjects    []string                `json:"subjects"`
		Covers      []int                   `json:"covers"`
	}
	if err := json.Unmarshal(bodyBytes, &work); err != nil {
		return nil, fmt.Errorf("openlibrary: unmarshal work: %w", err)
	}

	meta := BookMetadata{
		Title:         work.Title,
		Subtitle:      work.Subtitle,
		OpenLibraryID: strings.TrimPrefix(work.Key, "/works/"),
		Genres:        work.Subjects,
	}
	if meta.OpenLibraryID == "" {
		meta.OpenLibraryID = cleanID
	}
	if work.Description != nil {
		meta.Description = work.Description.Value
	}
	if len(work.Covers) > 0 && work.Covers[0] > 0 {
		meta.CoverURL = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-L.jpg", work.Covers[0])
	}
	return &meta, nil
}

// LookupByASIN is not supported by OpenLibrary.
func (p *OpenLibraryProvider) LookupByASIN(ctx context.Context, asin string) (*BookMetadata, error) {
	return nil, ErrNotFound
}
