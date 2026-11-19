package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

type hardcoverSeries struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	BooksCount int    `json:"books_count"`
}

type hardcoverBookSeries struct {
	SeriesPosition *float64        `json:"series_position"`
	Series         hardcoverSeries `json:"series"`
}

type hardcoverPublisher struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type hardcoverEdition struct {
	ID          int                 `json:"id"`
	Title       string              `json:"title"`
	ISBN10      *string             `json:"isbn_10"`
	ISBN13      *string             `json:"isbn_13"`
	ASIN        *string             `json:"asin"`
	Format      string              `json:"format"`
	Pages       int                 `json:"pages"`
	ReleaseDate string              `json:"release_date"`
	Publisher   *hardcoverPublisher `json:"publisher"`
}

type hardcoverImage struct {
	URL   string `json:"url"`
	Color string `json:"color"`
}

type hardcoverBookNode struct {
	ID             int                     `json:"id"`
	Title          string                  `json:"title"`
	Subtitle       string                  `json:"subtitle"`
	Slug           string                  `json:"slug"`
	Description    string                  `json:"description"`
	ReleaseDate    string                  `json:"release_date"`
	ReleaseYear    int                     `json:"release_year"`
	Pages          int                     `json:"pages"`
	Rating         float64                 `json:"rating"`
	RatingsCount   int                     `json:"ratings_count"`
	UsersReadCount int                     `json:"users_read_count"`
	CachedTags     []hardcoverTag          `json:"cached_tags"`
	Contributions  []hardcoverContribution `json:"contributions"`
	BookSeries     []hardcoverBookSeries   `json:"book_series"`
	Editions       []hardcoverEdition      `json:"editions"`
	Image          *hardcoverImage         `json:"image"`
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

	if meta.Year == 0 && len(meta.PublishDate) >= 4 {
		meta.Year, _ = strconv.Atoi(meta.PublishDate[:4])
	}

	if b.Image != nil {
		meta.CoverURL = b.Image.URL
	}

	for _, tag := range b.CachedTags {
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

	for _, bs := range b.BookSeries {
		if bs.Series.Name != "" {
			meta.Series = bs.Series.Name
			meta.SeriesTotal = bs.Series.BooksCount
			if bs.SeriesPosition != nil {
				pos := *bs.SeriesPosition
				meta.SeriesIndex = &pos
			}
			break
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
	}

	return meta
}

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
 book_series { series_position: position series { id name books_count } }
 editions(where: {isbn_13: {_is_null: false}}, order_by: {id: asc}, limit: 1) { isbn_13 }
 } }`, map[string]interface{}{"ids": search.Data.Search.IDs}, &res); err != nil {
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
    cached_tags {
      tag
    }
    contributions {
      author {
        id
        name
        slug
        bio
      }
      contribution
    }
    book_series {
      series_position: position
      series {
        id
        name
        slug
        books_count
      }
    }
    editions {
      id
      title
      isbn_10
      isbn_13
      asin
      format
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
}`

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
      cached_tags {
        tag
      }
      contributions {
        author {
          id
          name
        }
        contribution
      }
      book_series {
        series_position: position
        series {
          id
          name
          books_count
        }
      }
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
}`

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
      cached_tags {
        tag
      }
      contributions {
        author {
          id
          name
        }
        contribution
      }
      book_series {
        series_position: position
        series {
          id
          name
          books_count
        }
      }
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
}`

	var res hardcoverBookResponse
	if err := p.doQuery(ctx, gql, map[string]interface{}{"asin": asin}, &res); err != nil {
		return nil, err
	}
	return p.parseBookResponse(res)
}

// SeriesBooks returns the provider's reading order, including books not held locally.
func (p *HardcoverProvider) SeriesBooks(ctx context.Context, title string) ([]BookMetadata, error) {
	const query = `query SeriesBooks($title: String!, $offset: Int!) {
 books(where: {book_series: {series: {name: {_eq: $title}}}}, limit: 100, offset: $offset, order_by: {id: asc}) {
 id title subtitle description release_date release_year image: cached_image
 contributions { author { name } contribution }
 book_series { series_position: position series { name books_count } }
 }
 }`
	raw, err := p.catalogBooks(ctx, query, map[string]interface{}{"title": title})
	if err != nil {
		return nil, err
	}
	res := hardcoverBookResponse{}
	res.Data.Books = raw

	books := make([]BookMetadata, 0, len(res.Data.Books))
	for _, b := range res.Data.Books {
		// A book may belong to several series; select the requested membership.
		for _, bs := range b.BookSeries {
			if bs.Series.Name == title {
				b.BookSeries = []hardcoverBookSeries{bs}
				break
			}
		}
		books = append(books, mapHardcoverBookNode(&b))
	}
	return books, nil
}

// AuthorBooks fetches works credited to an exact author, including announcements.
func (p *HardcoverProvider) AuthorBooks(ctx context.Context, name string) ([]BookMetadata, error) {
	const query = `query AuthorBooks($name: String!, $offset: Int!) {
 books(where: {contributions: {author: {name: {_eq: $name}}}}, limit: 100, offset: $offset, order_by: {id: asc}) {
 id title release_date release_year
 contributions { author { name } contribution }
 book_series { series_position: position series { name books_count } }
 }
 }`
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
