package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// AudnexusProvider queries the Audnexus API for Audible metadata.
type AudnexusProvider struct {
	baseURL string
	client  *http.Client
}

// AudnexusOption configures an AudnexusProvider.
type AudnexusOption func(*AudnexusProvider)

// WithAudnexusClient sets the HTTP client.
func WithAudnexusClient(client *http.Client) AudnexusOption {
	return func(p *AudnexusProvider) {
		if client != nil {
			p.client = client
		}
	}
}

// WithAudnexusBaseURL sets the base URL for Audnexus requests.
func WithAudnexusBaseURL(url string) AudnexusOption {
	return func(p *AudnexusProvider) {
		if url != "" {
			p.baseURL = strings.TrimRight(url, "/")
		}
	}
}

// NewAudnexusProvider creates an initialized AudnexusProvider.
func NewAudnexusProvider(opts ...AudnexusOption) *AudnexusProvider {
	p := &AudnexusProvider{
		baseURL: "https://api.audnexus.com",
		client:  http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns the provider identifier.
func (p *AudnexusProvider) Name() string {
	return "audnexus"
}

type audnexusAuthor struct {
	Name string `json:"name"`
	ASIN string `json:"asin"`
}

type audnexusNarrator struct {
	Name string `json:"name"`
}

type audnexusSeries struct {
	Name     string `json:"name"`
	Position string `json:"position"`
	ASIN     string `json:"asin"`
}

type audnexusGenre struct {
	Name string `json:"name"`
	ASIN string `json:"asin"`
}

type audnexusChapter struct {
	Title          string  `json:"title"`
	StartOffsetMs  int64   `json:"startOffsetMs"`
	LengthMs       int64   `json:"lengthMs"`
	StartOffsetSec float64 `json:"startOffsetSec"`
	DurationSec    float64 `json:"durationSec"`
}

type audnexusBookResponse struct {
	ASIN             string             `json:"asin"`
	Title            string             `json:"title"`
	Subtitle         string             `json:"subtitle"`
	Summary          string             `json:"summary"`
	ReleaseDate      string             `json:"releaseDate"`
	Authors          []audnexusAuthor   `json:"authors"`
	Narrators        []audnexusNarrator `json:"narrators"`
	Series           []audnexusSeries   `json:"series"`
	Genres           []audnexusGenre    `json:"genres"`
	PublisherName    string             `json:"publisherName"`
	RuntimeLengthMin int                `json:"runtimeLengthMin"`
	Language         string             `json:"language"`
	FormatType       string             `json:"formatType"`
	CoverURL         string             `json:"coverUrl"`
	SKU              string             `json:"sku"`
	Chapters         []audnexusChapter  `json:"chapters"`
}

func mapAudnexusChapters(raw []audnexusChapter) []ChapterMetadata {
	chapters := make([]ChapterMetadata, 0, len(raw))
	for _, c := range raw {
		start := c.StartOffsetSec
		if start == 0 && c.StartOffsetMs > 0 {
			start = float64(c.StartOffsetMs) / 1000.0
		}

		dur := c.DurationSec
		if dur == 0 && c.LengthMs > 0 {
			dur = float64(c.LengthMs) / 1000.0
		}

		end := start + dur
		chapters = append(chapters, ChapterMetadata{
			Title:    c.Title,
			StartSec: start,
			EndSec:   end,
		})
	}
	return chapters
}

func mapAudnexusBook(res *audnexusBookResponse) BookMetadata {
	meta := BookMetadata{
		Title:       res.Title,
		Subtitle:    res.Subtitle,
		ASIN:        res.ASIN,
		Description: res.Summary,
		PublishDate: res.ReleaseDate,
		Publisher:   res.PublisherName,
		Language:    res.Language,
		CoverURL:    res.CoverURL,
	}

	if t, err := time.Parse(time.RFC3339, res.ReleaseDate); err == nil {
		meta.Year = t.Year()
	} else if len(res.ReleaseDate) >= 4 {
		meta.Year, _ = strconv.Atoi(res.ReleaseDate[:4])
	}

	for _, a := range res.Authors {
		if a.Name != "" {
			meta.Authors = append(meta.Authors, a.Name)
		}
	}

	for _, n := range res.Narrators {
		if n.Name != "" {
			meta.Narrators = append(meta.Narrators, n.Name)
		}
	}

	if len(res.Series) > 0 {
		meta.Series = res.Series[0].Name
		if res.Series[0].Position != "" {
			if pos, err := strconv.ParseFloat(res.Series[0].Position, 64); err == nil {
				meta.SeriesIndex = &pos
			}
		}
	}

	for _, g := range res.Genres {
		if g.Name != "" {
			meta.Genres = append(meta.Genres, g.Name)
		}
	}

	if res.RuntimeLengthMin > 0 {
		meta.DurationSec = float64(res.RuntimeLengthMin * 60)
	}

	meta.Chapters = mapAudnexusChapters(res.Chapters)
	if meta.DurationSec == 0 && len(meta.Chapters) > 0 {
		meta.DurationSec = meta.Chapters[len(meta.Chapters)-1].EndSec
	}

	return meta
}

// LookupByASIN retrieves audiobook metadata for a given Audible ASIN.
func (p *AudnexusProvider) LookupByASIN(ctx context.Context, asin string) (*BookMetadata, error) {
	if asin == "" {
		return nil, fmt.Errorf("audnexus: empty asin: %w", ErrNotFound)
	}

	reqURL := fmt.Sprintf("%s/books/%s", p.baseURL, asin)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("audnexus: create request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("audnexus: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("audnexus: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("audnexus: read body: %w", err)
	}

	var bookRes audnexusBookResponse
	if err := json.Unmarshal(bodyBytes, &bookRes); err != nil {
		return nil, fmt.Errorf("audnexus: unmarshal book: %w", err)
	}

	meta := mapAudnexusBook(&bookRes)
	return &meta, nil
}

// GetChapters retrieves chapter timestamps and titles for a given Audible ASIN.
func (p *AudnexusProvider) GetChapters(ctx context.Context, asin string) ([]ChapterMetadata, error) {
	if asin == "" {
		return nil, fmt.Errorf("audnexus: empty asin: %w", ErrNotFound)
	}

	reqURL := fmt.Sprintf("%s/books/%s/chapters", p.baseURL, asin)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("audnexus: create chapters request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("audnexus: do chapters request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("audnexus: unexpected chapters status %d: %s", resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("audnexus: read chapters body: %w", err)
	}

	var rawResp struct {
		Chapters []audnexusChapter `json:"chapters"`
	}
	if err := json.Unmarshal(bodyBytes, &rawResp); err == nil && len(rawResp.Chapters) > 0 {
		return mapAudnexusChapters(rawResp.Chapters), nil
	}

	var directArray []audnexusChapter
	if err := json.Unmarshal(bodyBytes, &directArray); err == nil && len(directArray) > 0 {
		return mapAudnexusChapters(directArray), nil
	}

	return nil, nil
}

// LookupByISBN is not supported by Audnexus (ASIN-only catalog).
func (p *AudnexusProvider) LookupByISBN(ctx context.Context, isbn string) (*BookMetadata, error) {
	return nil, ErrNotFound
}

// Search checks if query looks like an ASIN and performs lookup, otherwise returns empty.
func (p *AudnexusProvider) Search(ctx context.Context, query string) ([]BookMetadata, error) {
	trimmed := strings.TrimSpace(query)
	if len(trimmed) == 10 && strings.HasPrefix(strings.ToUpper(trimmed), "B0") {
		meta, err := p.LookupByASIN(ctx, trimmed)
		if err != nil {
			if err == ErrNotFound {
				return nil, nil
			}
			return nil, err
		}
		return []BookMetadata{*meta}, nil
	}
	return nil, nil
}
