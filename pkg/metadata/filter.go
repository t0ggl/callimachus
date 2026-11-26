package metadata

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	seriesExcerptKind = `(?:deleted\s+scene|bonus\s+(?:chapter|scene|excerpt|interlude)|promotional\s+(?:excerpt|snippet)|sample\s+chapter|sneak\s+peek)s?`
	collectionCount   = `(?:[2-9]|[1-9]\d+|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve)`
	collectionUnit    = `(?:books?|volumes?|vols?\.?|novels?)`
	collectionMod     = `(?:paperback|hardcover|ebook|digital|trade|deluxe|collector'?s?|special|gift|slipcase|collection|box(?:ed)?)`
)

var (
	seriesAudioAdaptation = regexp.MustCompile(`(?i)\b(?:dramati[sz]ed\s+(?:adaptation|audio(?:book)?|edition)|audio\s+drama|graphic\s*audio)\b`)
	seriesAudioPart       = regexp.MustCompile(`(?i)\bpart\s+\d+\b`)
	seriesExcerptLabel    = regexp.MustCompile(`(?i)(?:^|[:(\[–—-]\s*)(?:an?\s+)?` + seriesExcerptKind + `(?:\s*$|\s*[])\].,:–—-]|\s+(?:from|of|for|in)\b)`)
	seriesExcerptBlurb    = regexp.MustCompile(`(?i)\bis\s+an?\s+` + seriesExcerptKind + `\b`)

	// Multi-book ranges such as "Books 1-4", "Books 1, 2, and 3", "Vols. I-III", or "#1-3".
	// Restrict numeric entries to 1-2 digits so publication years (e.g. "Book 1, 1984") do not match.
	collectionRange = regexp.MustCompile(
		`(?i)\b` + collectionUnit + `\s*#?\s*\d{1,2}(?:\.\d+)?(?:\s*(?:[-–—&+]|\bto\b|\band\b)\s*#?\s*\d{1,2}(?:\.\d+)?|(?:\s*,\s*#?\s*\d{1,2}(?:\.\d+)?)+(?:\s*,?\s*(?:&|\band\b)\s*#?\s*\d{1,2}(?:\.\d+)?)?)\b` +
			`|\b` + collectionUnit + `\s+(?:i|ii|iii|iv|v|vi|vii|viii|ix|x)\s*(?:[-–—&]|\bto\b)\s*(?:i|ii|iii|iv|v|vi|vii|viii|ix|x)\b` +
			`|#\s*\d{1,2}(?:\.\d+)?\s*[-–—]\s*#?\s*\d{1,2}(?:\.\d+)?\b`,
	)

	// Unambiguous box-set, collection-set, slipcase-set, and omnibus markers.
	collectionExplicit = regexp.MustCompile(
		`(?i)\b(?:box(?:ed)?[\s-]*sets?|box(?:ed)?[\s-]+collections?|collection[\s-]+(?:box(?:ed)?[\s-]*)?(?:sets?|packs?|bundles?)|gift[\s-]+sets?|slipcase[\s-]+(?:sets?|collections?)|omnibus(?:es)?)\b`,
	)

	// Counted multi-book sets/collections/bundles (count >= 2) and multi-in-one bind-ups.
	collectionCounted = regexp.MustCompile(
		`(?i)\b` + collectionCount + `[\s-]+` + collectionUnit + `[\s-]+(?:` + collectionMod + `[\s-]+){0,2}(?:sets?|collections?|bundles?|packs?|slipcases?)\b` +
			`|\b(?:collection|set|bundle|pack)\s+of\s+` + collectionCount + `\s+` + collectionUnit + `\b` +
			`|\b(?:[2-9]|[1-9]\d+)[\s-]+(?:` + collectionUnit + `[\s-]+)?in[\s-]+(?:1|one)\b` +
			`|\b(?:two|three|four|five|six|seven|eight|nine|ten|eleven|twelve)[\s-]+` + collectionUnit + `[\s-]+in[\s-]+(?:1|one)\b`,
	)

	// Multi-book series/trilogy/complete collections without matching bare words like "collection" or "trilogy".
	collectionSeriesBundle = regexp.MustCompile(
		`(?i)\b(?:books|novels|volumes|vols\.?|ebooks|audiobooks)[\s-]+(?:collections?|sets?|bundles?|packs?)\b` +
			`|\b(?:book|ebook|audiobook)[\s-]+(?:sets?|bundles?|packs?)\b` +
			`|\b(?:series|trilogy|duology|quartet|quintet|saga)[\s-]+(?:box(?:ed)?[\s-]*sets?|collections?(?:[\s-]+sets?)?|sets?|bundles?|packs?|slipcases?)\b` +
			`|\bcomplete[\s-]+(?:series|trilogy|duology|quartet|quintet|saga|chronicles|collections?|novels|works|box(?:ed)?[\s-]*sets?|sets?|bundles?)\b` +
			`|\bcomplete\s+(?:[a-z0-9'’&-]+\s+){1,3}(?:series|trilogy|duology|quartet|quintet|saga|box(?:ed)?[\s-]*sets?)\b` +
			`|\bcollected[\s-]+(?:novels|works|books)\b`,
	)

	// Bare ascending small-integer ranges used in release names (e.g. "01-03", "1-3", "01–04").
	releaseBookRange    = regexp.MustCompile(`(\d{1,2})\s*[-–—]\s*(\d{1,2})`)
	releaseRangeUnitEnd = regexp.MustCompile(`(?i)^\s*(?:kbps|kb|mb|gb|tb|hz|khz|bit|ch|min|hr|hrs|sec|pages?|pp)\b`)
)

// IsSeriesDerivativeTitle reports explicit adaptation or excerpt labels in
// series listings, including blurbs mistakenly stored as titles.
func IsSeriesDerivativeTitle(title, subtitle string) bool {
	for _, text := range []string{title, subtitle} {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if seriesAudioAdaptation.MatchString(text) || seriesExcerptLabel.MatchString(text) || seriesExcerptBlurb.MatchString(text) {
			return true
		}
	}
	return false
}

// IsCollectionTitle reports titles or subtitles that explicitly represent
// multi-book collections, box sets, or omnibus editions rather than a single book.
func IsCollectionTitle(title, subtitle string) bool {
	for _, text := range []string{title, subtitle} {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if collectionRange.MatchString(text) ||
			collectionExplicit.MatchString(text) ||
			collectionCounted.MatchString(text) ||
			collectionSeriesBundle.MatchString(text) {
			return true
		}
	}
	return false
}

// IsNonSingleBookTitle reports whether a title or subtitle labels a multi-book
// collection/box set/omnibus or a derivative adaptation/excerpt entry.
func IsNonSingleBookTitle(title, subtitle string) bool {
	return IsCollectionTitle(title, subtitle) || IsSeriesDerivativeTitle(title, subtitle)
}

// IsMultiBookRelease reports whether a release title represents a multi-book
// collection, box set, omnibus, or numbered book range (e.g. "01-03", "Books 1-4").
func IsMultiBookRelease(title string) bool {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return false
	}
	normalized := strings.NewReplacer(".", " ", "_", " ").Replace(trimmed)
	if IsCollectionTitle(trimmed, "") || IsCollectionTitle(normalized, "") {
		return true
	}
	return hasReleaseBookRange(trimmed)
}

func isRangeDash(r rune) bool {
	return r == '-' || r == '–' || r == '—'
}

// hasReleaseBookRange detects bare ascending small-integer ranges like "01-03" or "1-4"
// in release titles while avoiding dates (2024-01-03, 01-03-2024), ISBNs, triple-dash
// sequences (1-2-3), and technical units (64-128kbps).
func hasReleaseBookRange(s string) bool {
	matches := releaseBookRange.FindAllStringSubmatchIndex(s, -1)
	for _, m := range matches {
		start, end := m[0], m[1]
		a, errA := strconv.Atoi(s[m[2]:m[3]])
		b, errB := strconv.Atoi(s[m[4]:m[5]])
		if errA != nil || errB != nil || a < 1 || b <= a || b > 50 {
			continue
		}
		if start > 0 {
			prev, prevSize := utf8.DecodeLastRuneInString(s[:start])
			if unicode.IsLetter(prev) || unicode.IsDigit(prev) {
				continue
			}
			// Reject second half of a date or triple-number sequence like "2024-01-03" or "1-2-3".
			if isRangeDash(prev) {
				beforeDash := strings.TrimRight(s[:start-prevSize], " ")
				if r, _ := utf8.DecodeLastRuneInString(beforeDash); unicode.IsDigit(r) {
					continue
				}
			}
		}
		if end < len(s) {
			next, nextSize := utf8.DecodeRuneInString(s[end:])
			if unicode.IsLetter(next) || unicode.IsDigit(next) {
				continue
			}
			// Reject first half of a date or triple-number sequence like "01-03-2024" or "1-2-3".
			if isRangeDash(next) {
				afterDash := strings.TrimLeft(s[end+nextSize:], " ")
				if r, _ := utf8.DecodeRuneInString(afterDash); unicode.IsDigit(r) {
					continue
				}
			}
			if releaseRangeUnitEnd.MatchString(s[end:]) {
				continue
			}
		}
		return true
	}
	return false
}
