package metadata

import (
	"context"
	"maps"
	"regexp"
	"strings"
	"unicode"
)

var seriesBundleTitle = regexp.MustCompile(`(?i)\b(?:books?|volumes?|vols\.?)\s*#?\s*\d+\s*(?:[-–—&]|\bto\b)\s*#?\s*\d+\b|\b(?:(?:\d+|two|three|four|five|six|seven|eight|nine|ten)[\s-]+book[\s-]+set|box(?:ed)?[\s-]+set|omnibus|collection(?:\s+set)?|anthology|complete\s+series)\b`)

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
 book_series { series_position: position compilation series { id canonical_id } }
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
		if seen[b.ID] || membership.Compilation || b.Compilation || b.isSeriesDerivative() ||
			seriesBundleTitle.MatchString(b.Title) || seriesBundleTitle.MatchString(b.Subtitle) {
			continue
		}
		english, known := hardcoverSeriesLanguage(b)
		if known && !english {
			continue
		}

		seen[b.ID] = true
		// Preserve the local name so discovery persists books under the requested series.
		b.BookSeries = []hardcoverBookSeries{{
			SeriesPosition: membership.SeriesPosition,
			Series:         hardcoverSeries{Name: title, BooksCount: series.BooksCount},
		}}
		book := mapHardcoverBookNode(&b)
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
 id name books_count
 canonical { id name books_count }
}`
	type seriesMatch struct {
		hardcoverSeries
		Canonical *hardcoverSeries `json:"canonical"`
	}
	var res struct {
		Data struct {
			Named  []seriesMatch `json:"named"`
			Linked []seriesMatch `json:"linked"`
		} `json:"data"`
	}
	if err := p.doQuery(ctx, query, map[string]any{"name": pattern, "known": known, "hasKnown": len(known) != 0}, &res); err != nil {
		return hardcoverSeries{}, err
	}

	byID := func(matches []seriesMatch) map[int]hardcoverSeries {
		series := make(map[int]hardcoverSeries, len(matches))
		for _, match := range matches {
			s := match.hardcoverSeries
			if match.Canonical != nil {
				s = *match.Canonical
			}
			if s.ID != 0 {
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
