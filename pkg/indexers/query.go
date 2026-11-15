package indexers

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// SearchQuery represents parameters for searching an indexer.
type SearchQuery struct {
	Author     string `json:"author,omitempty"`
	Title      string `json:"title,omitempty"`
	ISBN       string `json:"isbn,omitempty"`
	Query      string `json:"query,omitempty"`
	Categories []int  `json:"categories,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	Mode       string `json:"mode,omitempty"` // "book", "search", etc. Empty for auto-detect.
}

// BuildURL constructs a standard Torznab query URL.
func BuildURL(baseURL, apiKey string, q SearchQuery) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}

	// Ensure API endpoint path
	if u.Path == "" || u.Path == "/" {
		u.Path = "/api"
	} else if !strings.HasSuffix(u.Path, "/api") {
		u.Path = strings.TrimRight(u.Path, "/") + "/api"
	}

	vals := u.Query()
	if apiKey != "" {
		vals.Set("apikey", apiKey)
	}

	// Determine search mode
	mode := q.Mode
	if mode == "" {
		if q.ISBN != "" || q.Title != "" || q.Author != "" {
			mode = "book"
		} else {
			mode = "search"
		}
	}
	vals.Set("t", mode)

	// Apply mode-specific parameters
	switch mode {
	case "book":
		if q.Title != "" {
			vals.Set("title", q.Title)
		}
		if q.Author != "" {
			vals.Set("author", q.Author)
		}
		if q.ISBN != "" {
			vals.Set("isbn", q.ISBN)
		}
		if q.Query != "" {
			vals.Set("q", q.Query)
		}
	case "search":
		if q.Query != "" {
			vals.Set("q", q.Query)
		} else {
			combined := BroadKeywordQuery(q.Author, q.Title)
			if combined != "" {
				vals.Set("q", combined)
			}
		}
	default:
		if q.Query != "" {
			vals.Set("q", q.Query)
		}
	}

	// Categories (e.g. 7000,7020,3030)
	if len(q.Categories) > 0 {
		catStrs := make([]string, len(q.Categories))
		for i, c := range q.Categories {
			catStrs[i] = strconv.Itoa(c)
		}
		vals.Set("cat", strings.Join(catStrs, ","))
	}

	// Pagination limits
	if q.Limit > 0 {
		vals.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		vals.Set("offset", strconv.Itoa(q.Offset))
	}

	u.RawQuery = vals.Encode()
	return u.String()
}

// ExactQuotedQuery formats author and title as an exact quoted query: `"{Author} {Title}"`.
func ExactQuotedQuery(author, title string) string {
	a := strings.TrimSpace(author)
	t := strings.TrimSpace(title)
	if a != "" && t != "" {
		return fmt.Sprintf("\"%s %s\"", a, t)
	}
	if a != "" {
		return fmt.Sprintf("\"%s\"", a)
	}
	if t != "" {
		return fmt.Sprintf("\"%s\"", t)
	}
	return ""
}

// BroadKeywordQuery formats author and title as a space-separated broad keyword query.
func BroadKeywordQuery(author, title string) string {
	a := strings.TrimSpace(author)
	t := strings.TrimSpace(title)
	if a != "" && t != "" {
		return fmt.Sprintf("%s %s", a, t)
	}
	if a != "" {
		return a
	}
	return t
}

// FallbackQueries generates fallback search queries ordered from most specific to broadest:
// 1. Quoted exact query: `"{Author} {Title}"` (mode: "search")
// 2. Broad keyword query: `{Author} {Title}` (mode: "search")
// 3. Title-only broad query: `{Title}` (mode: "search")
func FallbackQueries(author, title string, categories []int) []SearchQuery {
	var queries []SearchQuery
	a := strings.TrimSpace(author)
	t := strings.TrimSpace(title)

	// 1. Exact quoted
	if quoted := ExactQuotedQuery(a, t); quoted != "" {
		queries = append(queries, SearchQuery{
			Query:      quoted,
			Categories: categories,
			Mode:       "search",
		})
	}

	// 2. Broad keyword
	if broad := BroadKeywordQuery(a, t); broad != "" && broad != ExactQuotedQuery(a, t) {
		queries = append(queries, SearchQuery{
			Query:      broad,
			Categories: categories,
			Mode:       "search",
		})
	}

	// 3. Title only (if author was present)
	if a != "" && t != "" {
		queries = append(queries, SearchQuery{
			Query:      t,
			Categories: categories,
			Mode:       "search",
		})
	}

	return queries
}

// BuildFallbackQueries generates fallback search queries for a given SearchQuery.
func BuildFallbackQueries(q SearchQuery) []SearchQuery {
	return FallbackQueries(q.Author, q.Title, q.Categories)
}
