package metadata

import (
	"context"
	"maps"
	"regexp"
	"strings"
	"unicode"
)

// SeriesBooks returns canonical volumes for one unambiguous Hardcover series.
// Known local books can identify a renamed series or disambiguate a shared title.
func (p *HardcoverProvider) SeriesBooks(ctx context.Context, title string, knownBooks ...BookMetadata) ([]BookMetadata, error) {
	series, err := p.findHardcoverSeries(ctx, title, knownBooks)
	if err != nil || series.ID == 0 {
		return nil, err
	}

	const query = `query SeriesBooks($id: Int!, $offset: Int!) {
 books(where: {book_series: {series: {_or: [{id: {_eq: $id}}, {canonical_id: {_eq: $id}}]}}}, limit: 100, offset: $offset, order_by: {id: asc}) {
   ...SeriesBookFields
   canonical { ...SeriesBookFields }
 }
}
fragment SeriesBookFields on books {
 id title subtitle description release_date release_year image: cached_image
 canonical_id compilation users_count is_partial_book parent_book_id
 default_cover_edition { language { code2 code3 } edition_format publisher { name } }
 english_editions: editions(where: {language: {_or: [{code2: {_eq: "en"}}, {code3: {_eq: "eng"}}]}}, limit: 1) { id }
 contributions { author { name } contribution }
 book_series { series_position: position compilation series { id canonical_id books_count canonical { books_count } } }
}`
	raw, err := p.catalogBooks(ctx, query, map[string]interface{}{"id": series.ID})
	if err != nil {
		return nil, err
	}

	books := make([]BookMetadata, 0, len(raw))
	seen := make(map[int]bool)
	positions := make(map[float64]int)
	ranked := make([]hardcoverBookNode, 0, len(raw))
	for _, b := range raw {
		membership := hardcoverSeriesMembership(b, series.ID)
		if membership == nil || membership.Compilation || b.Compilation || b.isSeriesDerivative() {
			continue
		}
		if b.CanonicalID != 0 && b.CanonicalID != b.ID {
			if b.Canonical == nil || b.Canonical.ID != b.CanonicalID {
				continue
			}
			b = *b.Canonical
			if canonicalMembership := hardcoverSeriesMembership(b, series.ID); canonicalMembership != nil {
				membership = canonicalMembership
			}
		}
		if seen[b.ID] || membership.Compilation || b.isCollection() || b.isSeriesDerivative() {
			continue
		}
		english, known := hardcoverSeriesLanguage(b)
		if known && !english {
			continue
		}

		seen[b.ID] = true
		book := mapHardcoverBookNode(&b)
		// Use the requested membership even if the book features another series.
		count := series.BooksCount
		if count == 0 {
			count = membership.Series.BooksCount
			if membership.Series.Canonical != nil {
				count = membership.Series.Canonical.BooksCount
			}
		}
		applyHardcoverMembership(&book, hardcoverBookSeries{
			SeriesPosition: membership.SeriesPosition,
			Series:         hardcoverSeries{ID: series.ID, Name: title, BooksCount: count},
		})
		if book.SeriesIndex != nil {
			position := *book.SeriesIndex
			if i, ok := positions[position]; ok {
				if preferHardcoverSeriesBook(b, ranked[i]) {
					books[i], ranked[i] = book, b
				}
				continue
			}
			positions[position] = len(books)
		}
		books = append(books, book)
		ranked = append(ranked, b)
	}
	return books, nil
}

func findSeriesNamePattern(title string) string {
	words := strings.FieldsFunc(title, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(words) > 1 && strings.EqualFold(words[0], "the") {
		words = words[1:]
	}
	if len(words) == 0 {
		return ""
	}
	return `^[^[:alnum:]]*(the[^[:alnum:]]+)?` + strings.Join(words, `[^[:alnum:]]+`) + `[^[:alnum:]]*$`
}

func (p *HardcoverProvider) findHardcoverSeries(ctx context.Context, title string, knownBooks []BookMetadata) (hardcoverSeries, error) {
	var authors []string
	for _, b := range knownBooks {
		if b.HardcoverSeriesID > 0 {
			return hardcoverSeries{ID: b.HardcoverSeriesID}, nil
		}
		authors = append(authors, b.Authors...)
	}
	pattern := findSeriesNamePattern(title)
	if pattern == "" {
		return hardcoverSeries{}, nil
	}
	known := make([]map[string]any, 0, len(knownBooks))
	for _, b := range knownBooks {
		for _, author := range b.Authors {
			if b.Title == "" || author == "" {
				continue
			}
			known = append(known, map[string]any{
				"title": map[string]string{"_iregex": "^" + regexp.QuoteMeta(b.Title) + "$"},
				"contributions": map[string]any{"author": map[string]any{
					"name": map[string]string{"_iregex": "^" + regexp.QuoteMeta(author) + "$"},
				}},
			})
		}
	}

	const query = `query FindSeries($name: String!, $known: [books_bool_exp!]!, $hasKnown: Boolean!) {
 named: series(where: {name: {_iregex: $name}}) { ...SeriesIdentity }
 linked: series(where: {book_series: {compilation: {_eq: false}, book: {_or: $known}}}) @include(if: $hasKnown) { ...SeriesIdentity }
}
fragment SeriesIdentity on series {
 id name books_count author { name }
 canonical { id name books_count author { name } }
}`
	var res struct {
		Data struct {
			Named  []hardcoverSeries `json:"named"`
			Linked []hardcoverSeries `json:"linked"`
		} `json:"data"`
	}
	if err := p.doQuery(ctx, query, map[string]any{"name": pattern, "known": known, "hasKnown": len(known) != 0}, &res); err != nil {
		return hardcoverSeries{}, err
	}

	byID := func(matches []hardcoverSeries) map[int]hardcoverSeries {
		series := make(map[int]hardcoverSeries, len(matches))
		for _, s := range matches {
			if s.Canonical != nil {
				s = *s.Canonical
			}
			if s.ID > 0 && (len(authors) == 0 || s.Author == nil ||
				SharesAuthor(authors, []string{s.Author.Name})) {
				series[s.ID] = s
			}
		}
		return series
	}
	named, linked := byID(res.Data.Named), byID(res.Data.Linked)

	// When both lookups find candidates, require them to agree on the series.
	candidates := named
	if len(named) == 0 {
		candidates = linked
	} else if len(linked) > 0 {
		maps.DeleteFunc(candidates, func(id int, _ hardcoverSeries) bool {
			_, matches := linked[id]
			return !matches
		})
	}
	if len(authors) > 0 {
		// An ownerless named result needs a matching local book as evidence.
		maps.DeleteFunc(candidates, func(id int, series hardcoverSeries) bool {
			_, linkedMatch := linked[id]
			return series.Author == nil && !linkedMatch
		})
	}
	if len(candidates) == 1 {
		for _, s := range candidates {
			return s, nil
		}
	}
	return hardcoverSeries{}, nil
}

func hardcoverSeriesMembership(b hardcoverBookNode, id int) *hardcoverBookSeries {
	var alias *hardcoverBookSeries
	for i := range b.BookSeries {
		membership := &b.BookSeries[i]
		if membership.Series.ID == id {
			return membership
		}
		if alias == nil && membership.Series.CanonicalID == id {
			alias = membership
		}
	}
	return alias
}

func hardcoverSeriesLanguage(b hardcoverBookNode) (english, known bool) {
	if len(b.EnglishEditions) > 0 {
		return true, true
	}
	if b.DefaultCoverEdition == nil || b.DefaultCoverEdition.Language == nil {
		return false, false
	}
	language := b.DefaultCoverEdition.Language
	return strings.EqualFold(language.Code2, "en") || strings.EqualFold(language.Code3, "eng"),
		language.Code2 != "" || language.Code3 != ""
}

func preferHardcoverSeriesBook(candidate, current hardcoverBookNode) bool {
	candidateEnglish, _ := hardcoverSeriesLanguage(candidate)
	currentEnglish, _ := hardcoverSeriesLanguage(current)
	if candidateEnglish != currentEnglish {
		return candidateEnglish
	}
	if candidate.UsersCount != current.UsersCount {
		return candidate.UsersCount > current.UsersCount
	}
	candidateCover := candidate.Image != nil && candidate.Image.URL != ""
	currentCover := current.Image != nil && current.Image.URL != ""
	if candidateCover != currentCover {
		return candidateCover
	}
	return candidate.Description != "" && current.Description == ""
}

// SearchSeries searches Hardcover series by query string and returns ranked series metadata.
func (p *HardcoverProvider) SearchSeries(ctx context.Context, query string, limit int) ([]SeriesMetadata, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return []SeriesMetadata{}, nil
	}
	if limit <= 0 {
		limit = 5
	}
	var search struct {
		Data struct {
			Search struct {
				IDs []int `json:"ids"`
			} `json:"search"`
		} `json:"data"`
	}
	if err := p.doQuery(
		ctx,
		`query SearchSeries($query: String!, $perPage: Int!) { search(query: $query, query_type: "Series", per_page: $perPage) { ids } }`,
		map[string]interface{}{"query": trimmed, "perPage": limit},
		&search,
	); err != nil {
		return nil, err
	}
	ids := search.Data.Search.IDs
	if len(ids) == 0 {
		return []SeriesMetadata{}, nil
	}
	const detailsQuery = `query SearchSeriesDetails($ids: [Int!]!) {
 series(where: {id: {_in: $ids}}) {
  id canonical_id name books_count
  author { name }
  canonical { id name books_count author { name } }
  book_series(where: {compilation: {_eq: false}}, order_by: {position: asc_nulls_last}, limit: 1) {
   book { image: cached_image }
  }
 }
}`
	var res struct {
		Data struct {
			Series []struct {
				hardcoverSeries
				BookSeries []struct {
					Book *struct {
						Image *hardcoverImage `json:"image"`
					} `json:"book"`
				} `json:"book_series"`
			} `json:"series"`
		} `json:"data"`
	}
	if err := p.doQuery(ctx, detailsQuery, map[string]interface{}{"ids": ids}, &res); err != nil {
		return nil, err
	}
	byID := make(map[int]int, len(res.Data.Series))
	for i, s := range res.Data.Series {
		byID[s.ID] = i
	}
	results := make([]SeriesMetadata, 0, len(ids))
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		idx, ok := byID[id]
		if !ok {
			continue
		}
		node := res.Data.Series[idx]
		canon := node.hardcoverSeries
		if canon.Canonical != nil && strings.TrimSpace(canon.Canonical.Name) != "" {
			canon = *canon.Canonical
		}
		canonID := node.hardcoverSeries.identity()
		if canonID > 0 && seen[canonID] {
			continue
		}
		name := strings.TrimSpace(canon.Name)
		if name == "" {
			continue
		}
		if canonID > 0 {
			seen[canonID] = true
		}
		item := SeriesMetadata{
			Title:             name,
			BooksCount:        canon.BooksCount,
			HardcoverSeriesID: canonID,
		}
		author := canon.Author
		if (author == nil || strings.TrimSpace(author.Name) == "") && node.Author != nil {
			author = node.Author
		}
		if author != nil && strings.TrimSpace(author.Name) != "" {
			item.Authors = []string{strings.TrimSpace(author.Name)}
		}
		for _, bs := range node.BookSeries {
			if bs.Book != nil && bs.Book.Image != nil && strings.TrimSpace(bs.Book.Image.URL) != "" {
				item.CoverURL = strings.TrimSpace(bs.Book.Image.URL)
				break
			}
		}
		results = append(results, item)
	}
	return results, nil
}
