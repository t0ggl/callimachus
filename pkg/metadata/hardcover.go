package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// HardcoverProvider queries the Hardcover GraphQL API.
type HardcoverProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// HardcoverOption configures a HardcoverProvider.
type HardcoverOption func(*HardcoverProvider)

// WithHardcoverClient injects a custom HTTP client (e.g. mock client).
func WithHardcoverClient(client *http.Client) HardcoverOption {
	return func(p *HardcoverProvider) {
		if client != nil {
			p.client = client
		}
	}
}

// WithHardcoverBaseURL overrides the default GraphQL endpoint URL.
func WithHardcoverBaseURL(url string) HardcoverOption {
	return func(p *HardcoverProvider) {
		if url != "" {
			p.baseURL = url
		}
	}
}

// NewHardcoverProvider creates an initialized HardcoverProvider.
func NewHardcoverProvider(apiKey string, opts ...HardcoverOption) *HardcoverProvider {
	p := &HardcoverProvider{
		apiKey:  apiKey,
		baseURL: "https://api.hardcover.app/v1/graphql",
		client:  http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns the provider identifier.
func (p *HardcoverProvider) Name() string {
	return "hardcover"
}

type graphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

type hardcoverAuthor struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Bio  string `json:"bio"`
}

type hardcoverContribution struct {
	Author       hardcoverAuthor `json:"author"`
	Contribution string          `json:"contribution"`
}

type hardcoverTag struct {
	Tag string `json:"tag"`
}

// Hardcover returns categorized tags as JSON. Accept the flat list seen in
// older responses, and treat unknown tag shapes as optional metadata.
func hardcoverGenres(raw json.RawMessage) []hardcoverTag {
	if len(raw) == 0 {
		return nil
	}
	var genres []hardcoverTag
	if err := json.Unmarshal(raw, &genres); err == nil {
		return genres
	}
	var categorized struct {
		Genre []hardcoverTag
	}
	if err := json.Unmarshal(raw, &categorized); err != nil {
		return nil
	}
	return categorized.Genre
}

type hardcoverSeries struct {
	ID          int              `json:"id"`
	CanonicalID int              `json:"canonical_id"`
	Canonical   *hardcoverSeries `json:"canonical"`
	Author      *hardcoverAuthor `json:"author"`
	Name        string           `json:"name"`
	Slug        string           `json:"slug"`
	BooksCount  int              `json:"books_count"`
}

type hardcoverBookSeries struct {
	Compilation    bool            `json:"compilation"`
	Featured       bool            `json:"featured"`
	SeriesPosition *float64        `json:"series_position"`
	Series         hardcoverSeries `json:"series"`
}

type hardcoverPublisher struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type hardcoverLanguage struct {
	Code2 string `json:"code2"`
	Code3 string `json:"code3"`
}

func (l *hardcoverLanguage) preferredCode() string {
	if l == nil {
		return ""
	}
	if code := NormalizeLanguage(l.Code2); code != "" {
		return code
	}
	return NormalizeLanguage(l.Code3)
}

type hardcoverEdition struct {
	ID          int                 `json:"id"`
	Title       string              `json:"title"`
	ISBN10      *string             `json:"isbn_10"`
	ISBN13      *string             `json:"isbn_13"`
	ASIN        *string             `json:"asin"`
	Pages       int                 `json:"pages"`
	ReleaseDate string              `json:"release_date"`
	Publisher   *hardcoverPublisher `json:"publisher"`
	Language    *hardcoverLanguage  `json:"language"`
}

type hardcoverImage struct {
	URL   string `json:"url"`
	Color string `json:"color"`
}

type hardcoverBookNode struct {
	Canonical           *hardcoverBookNode `json:"canonical"`
	EnglishEditions     []hardcoverEdition `json:"english_editions"`
	CanonicalID         int                `json:"canonical_id"`
	Compilation         bool               `json:"compilation"`
	IsPartialBook       bool               `json:"is_partial_book"`
	ParentBookID        int                `json:"parent_book_id"`
	DefaultCoverEdition *struct {
		EditionFormat string              `json:"edition_format"`
		Publisher     *hardcoverPublisher `json:"publisher"`
		Language      *hardcoverLanguage  `json:"language"`
	} `json:"default_cover_edition"`
	ID                 int                     `json:"id"`
	Title              string                  `json:"title"`
	Subtitle           string                  `json:"subtitle"`
	Slug               string                  `json:"slug"`
	Description        string                  `json:"description"`
	ReleaseDate        string                  `json:"release_date"`
	ReleaseYear        int                     `json:"release_year"`
	Pages              int                     `json:"pages"`
	Rating             float64                 `json:"rating"`
	RatingsCount       int                     `json:"ratings_count"`
	UsersCount         int                     `json:"users_count"`
	UsersReadCount     int                     `json:"users_read_count"`
	CachedTags         json.RawMessage         `json:"cached_tags"`
	Contributions      []hardcoverContribution `json:"contributions"`
	FeaturedBookSeries *hardcoverBookSeries    `json:"featured_book_series"`
	BookSeries         []hardcoverBookSeries   `json:"book_series"`
	Editions           []hardcoverEdition      `json:"editions"`
	Image              *hardcoverImage         `json:"image"`
}

func (p *HardcoverProvider) doQuery(ctx context.Context, query string, vars map[string]interface{}, out interface{}) error {
	reqBody, err := json.Marshal(graphQLRequest{
		Query:     query,
		Variables: vars,
	})
	if err != nil {
		return fmt.Errorf("hardcover: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("hardcover: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		auth := p.apiKey
		if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			auth = "Bearer " + auth
		}
		req.Header.Set("Authorization", auth)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("hardcover: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("hardcover: unexpected HTTP %d: %s", resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("hardcover: read response body: %w", err)
	}

	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil {
		return fmt.Errorf("hardcover: invalid response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("hardcover: GraphQL: %s", envelope.Errors[0].Message)
	}
	if err := json.Unmarshal(bodyBytes, out); err != nil {
		return fmt.Errorf("hardcover: unmarshal response: %w", err)
	}

	return nil
}

func mapHardcoverBookNode(b *hardcoverBookNode) BookMetadata {
	meta := BookMetadata{
		HardcoverID: strconv.Itoa(b.ID),
		Title:       b.Title,
		Subtitle:    b.Subtitle,
		Description: b.Description,
		PublishDate: b.ReleaseDate,
		Year:        b.ReleaseYear,
	}

	if meta.Year == 0 {
		meta.Year = ReleaseYear(meta.PublishDate, 0)
	}

	if b.Image != nil {
		meta.CoverURL = b.Image.URL
	}

	for _, tag := range hardcoverGenres(b.CachedTags) {
		if tag.Tag != "" {
			meta.Genres = append(meta.Genres, tag.Tag)
		}
	}

	for _, c := range b.Contributions {
		if c.Author.Name != "" {
			if strings.EqualFold(c.Contribution, "Narrator") {
				meta.Narrators = append(meta.Narrators, c.Author.Name)
			} else {
				meta.Authors = append(meta.Authors, c.Author.Name)
			}
		}
	}

	if bs := primaryHardcoverMembership(b, meta.Authors); bs != nil {
		applyHardcoverMembership(&meta, *bs)
	}

	addLanguage := func(lang *hardcoverLanguage) {
		code := lang.preferredCode()
		if code == "" {
			return
		}
		if meta.Language == "" {
			meta.Language = code
		}
		for _, existing := range meta.Languages {
			if existing == code {
				return
			}
		}
		meta.Languages = append(meta.Languages, code)
	}
	if b.DefaultCoverEdition != nil {
		addLanguage(b.DefaultCoverEdition.Language)
		if meta.Publisher == "" && b.DefaultCoverEdition.Publisher != nil && b.DefaultCoverEdition.Publisher.Name != "" {
			meta.Publisher = b.DefaultCoverEdition.Publisher.Name
		}
	}

	for _, ed := range b.Editions {
		if meta.ISBN13 == "" && ed.ISBN13 != nil && *ed.ISBN13 != "" {
			meta.ISBN13 = *ed.ISBN13
		}
		if meta.ASIN == "" && ed.ASIN != nil && *ed.ASIN != "" {
			meta.ASIN = *ed.ASIN
		}
		if meta.Publisher == "" && ed.Publisher != nil && ed.Publisher.Name != "" {
			meta.Publisher = ed.Publisher.Name
		}
		addLanguage(ed.Language)
	}

	return meta
}

// Use the explicit featured relationship first. Without it, prefer a unique
// flagged membership or one whose credited series author matches the book.
func primaryHardcoverMembership(b *hardcoverBookNode, authors []string) *hardcoverBookSeries {
	compatible := func(bs *hardcoverBookSeries) bool {
		if bs == nil || bs.Compilation || bs.Series.Name == "" {
			return false
		}
		seriesAuthor := bs.Series.Author
		if bs.Series.Canonical != nil && bs.Series.Canonical.Author != nil {
			seriesAuthor = bs.Series.Canonical.Author
		}
		return seriesAuthor == nil || seriesAuthor.Name == "" ||
			SharesAuthor(authors, []string{seriesAuthor.Name})
	}
	if compatible(b.FeaturedBookSeries) {
		return b.FeaturedBookSeries
	}
	var best *hardcoverBookSeries
	bestRank, tied := 0, false
	for i := range b.BookSeries {
		bs := &b.BookSeries[i]
		if !compatible(bs) {
			continue
		}
		rank := 1
		if bs.Featured {
			rank = 3
		} else if bs.Series.Author != nil && bs.Series.Author.Name != "" {
			rank = 2
		}
		if rank > bestRank {
			best, bestRank, tied = bs, rank, false
		} else if rank == bestRank && (bs.Series.identity() == 0 || bs.Series.identity() != best.Series.identity()) {
			tied = true
		}
	}
	if tied {
		return nil
	}
	return best
}

func (s hardcoverSeries) identity() int {
	if s.CanonicalID > 0 {
		return s.CanonicalID
	}
	return s.ID
}

func applyHardcoverMembership(meta *BookMetadata, bs hardcoverBookSeries) {
	series := bs.Series
	if series.Canonical != nil && series.Canonical.Name != "" {
		series = *series.Canonical
	}
	meta.Series = series.Name
	meta.SeriesTotal = series.BooksCount
	meta.SeriesIndex = bs.SeriesPosition
	meta.HardcoverSeriesID = bs.Series.identity()
}

const hardcoverMembershipFields = `
 featured_book_series { ...SeriesMembership }
 book_series { ...SeriesMembership }`

const hardcoverMembershipFragment = `
 fragment SeriesMembership on book_series {
  series_position: position compilation featured
  series { id canonical_id name books_count author { name } canonical { id name books_count author { name } } }
 }`

// Search searches books on Hardcover by title/query string.
func (p *HardcoverProvider) Search(ctx context.Context, query string) ([]BookMetadata, error) {
	var search struct {
		Data struct {
			Search struct {
				IDs []int `json:"ids"`
			} `json:"search"`
		} `json:"data"`
	}
	if err := p.doQuery(ctx, `query SearchBooks($query: String!) { search(query: $query, query_type: "Book", per_page: 25) { ids } }`, map[string]interface{}{"query": query}, &search); err != nil {
		return nil, err
	}
	if len(search.Data.Search.IDs) == 0 {
		return []BookMetadata{}, nil
	}
	var res hardcoverBookResponse
	if err := p.doQuery(ctx, `query SearchDetails($ids: [Int!]!) { books(where: {id: {_in: $ids}}) {
 id title subtitle description release_year release_date image: cached_image
 contributions { author { name } contribution }
 `+hardcoverMembershipFields+`
 default_cover_edition { language { code2 code3 } publisher { name } }
 editions(where: {isbn_13: {_is_null: false}}, order_by: {id: asc}, limit: 1) { isbn_13 language { code2 code3 } }
 } }`+hardcoverMembershipFragment, map[string]interface{}{"ids": search.Data.Search.IDs}, &res); err != nil {
		return nil, err
	}
	results := make([]BookMetadata, 0, len(res.Data.Books))
	for _, id := range search.Data.Search.IDs {
		for _, b := range res.Data.Books {
			if b.ID == id {
				results = append(results, mapHardcoverBookNode(&b))
				break
			}
		}
	}
	return results, nil
}

type hardcoverBookResponse struct {
	Data struct {
		BooksByPK *hardcoverBookNode `json:"books_by_pk"`
		Editions  []struct {
			Book *hardcoverBookNode `json:"book"`
		} `json:"editions"`
		Books []hardcoverBookNode `json:"books"`
	} `json:"data"`
}

func (p *HardcoverProvider) parseBookResponse(res hardcoverBookResponse) (*BookMetadata, error) {
	if res.Data.BooksByPK != nil {
		meta := mapHardcoverBookNode(res.Data.BooksByPK)
		return &meta, nil
	}
	if len(res.Data.Editions) > 0 && res.Data.Editions[0].Book != nil {
		meta := mapHardcoverBookNode(res.Data.Editions[0].Book)
		return &meta, nil
	}
	if len(res.Data.Books) > 0 {
		meta := mapHardcoverBookNode(&res.Data.Books[0])
		return &meta, nil
	}
	return nil, ErrNotFound
}

// GetBook retrieves a book by Hardcover primary key ID.
func (p *HardcoverProvider) GetBook(ctx context.Context, id int) (*BookMetadata, error) {
	const gql = `
query GetBook($id: Int!) {
  books_by_pk(id: $id) {
    id
    title
    subtitle
    slug
    description
    release_date
    release_year
    pages
    rating
    ratings_count
    users_read_count
    cached_tags
    contributions {
      author {
        id
        name
        slug
        bio
      }
      contribution
    }
    ` + hardcoverMembershipFields + `
    editions {
      id
      title
      isbn_10
      isbn_13
      asin
      pages
      release_date
      publisher {
        id
        name
      }
    }
    image {
      url
      color
    }
  }
}` + hardcoverMembershipFragment

	var res hardcoverBookResponse
	if err := p.doQuery(ctx, gql, map[string]interface{}{"id": id}, &res); err != nil {
		return nil, err
	}
	return p.parseBookResponse(res)
}

// LookupByISBN queries Hardcover for a book matching an ISBN-10 or ISBN-13.
func (p *HardcoverProvider) LookupByISBN(ctx context.Context, isbn string) (*BookMetadata, error) {
	const gql = `
query BookByISBN($isbn: String!) {
  editions(where: { _or: [{ isbn_10: { _eq: $isbn } }, { isbn_13: { _eq: $isbn } }] }, limit: 1) {
    book {
      id
      title
      subtitle
      slug
      description
      release_date
      release_year
      pages
      cached_tags
      contributions {
        author {
          id
          name
        }
        contribution
      }
    ` + hardcoverMembershipFields + `
      editions {
        id
        isbn_10
        isbn_13
        asin
        publisher {
          id
          name
        }
      }
      image {
        url
      }
    }
  }
}` + hardcoverMembershipFragment

	var res hardcoverBookResponse
	if err := p.doQuery(ctx, gql, map[string]interface{}{"isbn": isbn}, &res); err != nil {
		return nil, err
	}
	return p.parseBookResponse(res)
}

// LookupByASIN queries Hardcover for a book matching an ASIN.
func (p *HardcoverProvider) LookupByASIN(ctx context.Context, asin string) (*BookMetadata, error) {
	const gql = `
query BookByASIN($asin: String!) {
  editions(where: { asin: { _eq: $asin } }, limit: 1) {
    book {
      id
      title
      subtitle
      slug
      description
      release_date
      release_year
      pages
      cached_tags
      contributions {
        author {
          id
          name
        }
        contribution
      }
    ` + hardcoverMembershipFields + `
      editions {
        id
        isbn_10
        isbn_13
        asin
        publisher {
          id
          name
        }
      }
      image {
        url
      }
    }
  }
}` + hardcoverMembershipFragment

	var res hardcoverBookResponse
	if err := p.doQuery(ctx, gql, map[string]interface{}{"asin": asin}, &res); err != nil {
		return nil, err
	}
	return p.parseBookResponse(res)
}

const seriesExcerptKind = `(?:deleted\s+scene|bonus\s+(?:chapter|scene|excerpt|interlude)|promotional\s+(?:excerpt|snippet)|sample\s+chapter|sneak\s+peek)s?`

var (
	seriesAudioAdaptation = regexp.MustCompile(`(?i)\b(?:dramati[sz]ed\s+(?:adaptation|audio(?:book)?|edition)|audio\s+drama|graphic\s*audio)\b`)
	seriesAudioPart       = regexp.MustCompile(`(?i)\bpart\s+\d+\b`)
	seriesExcerptLabel    = regexp.MustCompile(`(?i)(?:^|[:(\[–—-]\s*)(?:an?\s+)?` + seriesExcerptKind + `(?:\s*$|\s*[])\].,:–—-]|\s+(?:from|of|for|in)\b)`)
	seriesExcerptBlurb    = regexp.MustCompile(`(?i)\bis\s+an?\s+` + seriesExcerptKind + `\b`)
)

// IsSeriesDerivativeTitle reports explicit adaptation or excerpt labels in
// series listings, including blurbs mistakenly stored as titles.
func IsSeriesDerivativeTitle(title, subtitle string) bool {
	for _, text := range []string{title, subtitle} {
		text = strings.TrimSpace(text)
		if seriesAudioAdaptation.MatchString(text) || seriesExcerptLabel.MatchString(text) || seriesExcerptBlurb.MatchString(text) {
			return true
		}
	}
	return false
}

func (b hardcoverBookNode) isSeriesDerivative() bool {
	if b.IsPartialBook || (b.ParentBookID != 0 && b.ParentBookID != b.ID) ||
		IsSeriesDerivativeTitle(b.Title, b.Subtitle) {
		return true
	}

	// A numbered part or a GraphicAudio edition alone can belong to a real work.
	edition := b.DefaultCoverEdition
	return edition != nil && (seriesAudioPart.MatchString(b.Title) || seriesAudioPart.MatchString(b.Subtitle)) &&
		(seriesAudioAdaptation.MatchString(edition.EditionFormat) ||
			(edition.Publisher != nil && seriesAudioAdaptation.MatchString(edition.Publisher.Name)))
}

// AuthorBooks fetches works credited to an exact author, including announcements.
func (p *HardcoverProvider) AuthorBooks(ctx context.Context, name string) ([]BookMetadata, error) {
	const query = `query AuthorBooks($name: String!, $offset: Int!) {
 books(where: {contributions: {author: {name: {_eq: $name}}}}, limit: 100, offset: $offset, order_by: {id: asc}) {
 id title subtitle description release_date release_year
 contributions { author { name } contribution }
 ` + hardcoverMembershipFields + `
 }
 }` + hardcoverMembershipFragment
	raw, err := p.catalogBooks(ctx, query, map[string]interface{}{"name": name})
	if err != nil {
		return nil, err
	}
	res := hardcoverBookResponse{}
	res.Data.Books = raw

	books := make([]BookMetadata, 0, len(res.Data.Books))
	for _, b := range res.Data.Books {
		books = append(books, mapHardcoverBookNode(&b))
	}
	return books, nil
}

func (p *HardcoverProvider) catalogBooks(ctx context.Context, query string, variables map[string]interface{}) ([]hardcoverBookNode, error) {
	var books []hardcoverBookNode
	for offset := 0; ; offset += 100 {
		variables["offset"] = offset
		var res hardcoverBookResponse
		if err := p.doQuery(ctx, query, variables, &res); err != nil {
			return nil, err
		}
		books = append(books, res.Data.Books...)
		if len(res.Data.Books) < 100 {
			return books, nil
		}
	}
}
