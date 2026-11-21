package metadata

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const seriesIndexEpsilon = 1e-6

var (
	reSubtitleSep = regexp.MustCompile(`:\s*|\s+[-–—]\s+`)
	reTrailingArt = regexp.MustCompile(`(?i),\s*(?:the|a|an)\s*$`)
)

// IdealMetadata captures the matched canonical fields compared against an EPUB.
// Only title, series name, and series index are eligible for significant mismatch detection.
type IdealMetadata struct {
	Title       string   `json:"title"`
	Subtitle    string   `json:"subtitle,omitempty"`
	Series      string   `json:"series,omitempty"`
	SeriesIndex *float64 `json:"seriesIndex,omitempty"`
}

// EPUBMetadataSnapshot captures the relevant fields read from an EPUB file.
type EPUBMetadataSnapshot struct {
	Title       string   `json:"title"`
	Subtitle    string   `json:"subtitle,omitempty"`
	Series      string   `json:"series,omitempty"`
	SeriesIndex *float64 `json:"seriesIndex,omitempty"`
}

// MetadataFieldMismatch describes one significant discrepancy between an EPUB and its ideal metadata.
type MetadataFieldMismatch struct {
	Field   string `json:"field"`
	Current string `json:"current"`
	Ideal   string `json:"ideal"`
}

// CompareEPUBMetadata compares an EPUB's current metadata against the matched ideal metadata
// and returns significant mismatches across title, series name, and series index only.
// Missing ideal fields never trigger false positives, and trivial formatting differences
// (case, whitespace, punctuation, leading/trailing articles, US/UK spellings, or a single-sided subtitle)
// are normalized away.
func CompareEPUBMetadata(current EPUBMetadataSnapshot, ideal IdealMetadata) []MetadataFieldMismatch {
	var mismatches []MetadataFieldMismatch

	idealTitle := strings.TrimSpace(ideal.Title)
	currentTitle := strings.TrimSpace(current.Title)
	if idealTitle != "" && !titlesEquivalent(currentTitle, strings.TrimSpace(current.Subtitle), idealTitle, strings.TrimSpace(ideal.Subtitle)) {
		mismatches = append(mismatches, MetadataFieldMismatch{
			Field:   "title",
			Current: currentTitle,
			Ideal:   idealTitle,
		})
	}

	idealSeries := strings.TrimSpace(ideal.Series)
	currentSeries := strings.TrimSpace(current.Series)
	if idealSeries != "" {
		if !seriesNamesEquivalent(currentSeries, idealSeries) {
			mismatches = append(mismatches, MetadataFieldMismatch{
				Field:   "series",
				Current: currentSeries,
				Ideal:   idealSeries,
			})
		}

		idealIdx := finiteFloatPtr(ideal.SeriesIndex)
		currentIdx := finiteFloatPtr(current.SeriesIndex)
		if idealIdx != nil {
			if currentIdx == nil || math.Abs(*currentIdx-*idealIdx) > seriesIndexEpsilon {
				mismatches = append(mismatches, MetadataFieldMismatch{
					Field:   "seriesIndex",
					Current: formatSeriesIndex(currentIdx),
					Ideal:   formatSeriesIndex(idealIdx),
				})
			}
		}
	}

	return mismatches
}

func finiteFloatPtr(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return nil
	}
	return v
}

func formatSeriesIndex(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func titlesEquivalent(currentTitle, currentSubtitle, idealTitle, idealSubtitle string) bool {
	curNorm := normalizeComparableText(currentTitle)
	idealNorm := normalizeComparableText(idealTitle)
	if idealNorm == "" {
		return true
	}
	if curNorm == "" {
		return false
	}
	if curNorm == idealNorm {
		return true
	}

	// Check combined title + subtitle combinations when one side folds subtitle into dc:title.
	if currentSubtitle != "" && normalizeComparableText(currentTitle+" "+currentSubtitle) == idealNorm {
		return true
	}
	if idealSubtitle != "" && curNorm == normalizeComparableText(idealTitle+" "+idealSubtitle) {
		return true
	}

	// Split inline subtitles ("Title: Subtitle" / "Title - Subtitle").
	// Only treat subtitle differences as trivial when at most one side has a subtitle,
	// so distinct books sharing a prefix ("Star Wars: Heir" vs "Star Wars: Dark Force") still mismatch.
	curMain, curInlineSub := splitComparableSubtitle(currentTitle)
	idealMain, idealInlineSub := splitComparableSubtitle(idealTitle)

	curHasSub := curInlineSub != "" || currentSubtitle != ""
	idealHasSub := idealInlineSub != "" || idealSubtitle != ""

	curMainNorm := normalizeComparableText(curMain)
	idealMainNorm := normalizeComparableText(idealMain)

	if curMainNorm != "" && curMainNorm == idealMainNorm {
		if !curHasSub || !idealHasSub {
			return true
		}
		curSub := curInlineSub
		if curSub == "" {
			curSub = currentSubtitle
		}
		idealSub := idealInlineSub
		if idealSub == "" {
			idealSub = idealSubtitle
		}
		if normalizeComparableText(curSub) == normalizeComparableText(idealSub) {
			return true
		}
	}

	return false
}

func seriesNamesEquivalent(currentSeries, idealSeries string) bool {
	idealNorm := normalizeComparableText(idealSeries)
	if idealNorm == "" {
		return true
	}
	curNorm := normalizeComparableText(currentSeries)
	if curNorm == "" {
		return false
	}
	return curNorm == idealNorm
}

func splitComparableSubtitle(s string) (string, string) {
	loc := reSubtitleSep.FindStringIndex(s)
	if loc == nil {
		return s, ""
	}
	main := strings.TrimSpace(s[:loc[0]])
	sub := strings.TrimSpace(s[loc[1]:])
	if main == "" || sub == "" {
		return s, ""
	}
	return main, sub
}

// normalizeComparableText folds case, typographic quotes/dashes, punctuation, whitespace,
// ampersands, leading/trailing English articles, and common US/UK spellings while preserving
// substantive words and numbers (including scene/edition junk so dirty EPUB titles are caught).
func normalizeComparableText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.NewReplacer(
		"\u2018", "'",
		"\u2019", "'",
		"\u02bc", "'",
		"`", "'",
		"\u00b4", "'",
		"\u201c", "\"",
		"\u201d", "\"",
		"\u2013", "-",
		"\u2014", "-",
		"&", " and ",
	).Replace(s)

	s = strings.ToLower(s)
	// Handle Calibre/library sort form like "Hobbit, The" -> "Hobbit"
	if stripped := strings.TrimSpace(reTrailingArt.ReplaceAllString(s, "")); stripped != "" {
		s = stripped
	}

	var b strings.Builder
	b.Grow(len(s))
	lastSpace := true
	for _, r := range s {
		switch {
		case r == '\'':
			// Drop apostrophes inside words so "Ender's" matches "Enders".
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastSpace = false
		default:
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	cleaned := strings.TrimSpace(b.String())
	if cleaned == "" {
		return ""
	}

	// Strip a single leading article when substantive words follow.
	for _, prefix := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(cleaned, prefix) && len(cleaned) > len(prefix) {
			cleaned = cleaned[len(prefix):]
			break
		}
	}

	return reASCIIWord.ReplaceAllStringFunc(cleaned, normalizeUSUKWord)
}
