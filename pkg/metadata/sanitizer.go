// Package metadata provides release string parsing, title and author sanitization,
// series and index extraction, and filesystem character safety normalization.
package metadata

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ParsedRelease holds the structured metadata extracted from a raw scene or P2P release name.
type ParsedRelease struct {
	RawTitle     string   `json:"raw_title"`
	Title        string   `json:"title"`
	Author       string   `json:"author"`
	Series       string   `json:"series"`
	SeriesIndex  *float64 `json:"series_index"`
	Year         int      `json:"year"`
	Format       string   `json:"format"`       // "epub", "m4b", "mp3", "pdf", "azw3"
	BitrateKbps  int      `json:"bitrate_kbps"` // e.g. 64, 128, 320 (or -1 for VBR)
	Language     string   `json:"language"`     // ISO-639-1 code, e.g. "en", "de", "fr"
	Group        string   `json:"group"`        // Scene or p2p group (e.g. "GARGiLE", "OPUS")
	IsUnabridged bool     `json:"is_unabridged"`
}

// Pre-compiled regular expressions for performance (< 100 microseconds per parse).
var (
	// Language tags
	reLanguageTag = regexp.MustCompile(`(?i)(?:^|[\[\(\.\-_])(german|deutsch|french|français|spanish|español|italian|italiano|dutch|nederlands|russian|english|eng|de|fr|es|it|nl|ru|en)(?:$|[\]\)\.\-_])`)

	// Formats
	reFormats = regexp.MustCompile(`(?i)\b(epub|m4b|mp3|pdf|azw3|mobi|flac|aac|cbr|cbz)\b`)

	// Bitrates
	reBitrateKbps = regexp.MustCompile(`(?i)\b(\d+)\s*kbps\b`)
	reBitrateVBR  = regexp.MustCompile(`(?i)\bvbr\b`)
	reBitrateK    = regexp.MustCompile(`(?i)\b(\d+)\s*k\b`)

	// Unabridged / Abridged
	reUnabridged = regexp.MustCompile(`(?i)\bunabridged\b`)
	reAbridged   = regexp.MustCompile(`(?i)\babridged\b`)

	// Years: (2020), [2018], or standalone 4-digit years 1900-2099
	reYearParen      = regexp.MustCompile(`[\(\[]\s*(19\d{2}|20\d{2})\s*[\)\]]`)
	reYearStandalone = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)

	// Scene / P2P groups
	reTrailingDashGroup    = regexp.MustCompile(`-([A-Za-z0-9_]+)$`)
	reTrailingBracketGroup = regexp.MustCompile(`\[([A-Za-z0-9_]+)\]$`)
	reLeadingBracketGroup  = regexp.MustCompile(`^\[([A-Za-z0-9_]+)\]\s*`)

	// Narrator credits
	reNarratorDot   = regexp.MustCompile(`(?i)\bread\.by\.[A-Za-z\.]+\b`)
	reNarratorSpace = regexp.MustCompile(`(?i)\b(?:read|narrated)\s+by\s+[A-Za-z\s\.\-]+`)

	// Packaging and scene tags to strip
	reSceneTags = regexp.MustCompile(`(?i)\b(retail|web-rip|webrip|web|ebook|audiobook|unabridged|abridged|v\d+(?:\.\d+)?|repack|internal|readnfo|bundle|trilogy|complete|proper|scan)\b`)

	// Series patterns
	reParenSeries             = regexp.MustCompile(`(?i)(?:\(([^()]*?(?:book|bk|volume|vol|part|no|#)[^()]*?)(?:\)|$)|\[([^\[\]]*?(?:book|bk|volume|vol|part|no|#)[^\[\]]*?)(?:\]|$))`)
	reTrailingTitleAnnotation = regexp.MustCompile(`(?:\s*(?:\([^()]*\)|\[[^\[\]]*\]|\([^()]*$|\[[^\[\]]*$))+\s*$`)
	reSeriesPattern           = regexp.MustCompile(`(?i)^(.*?)(?:^|[,_\s\-\(\[.]+)(?:book|bk|volume|vol|part|no)\.?[._\s#]*(\d+(?:\.\d+)?)\s*[\)\]]?`)
	reSeriesHash              = regexp.MustCompile(`(?i)^(.*?)\s*#\s*(\d+(?:\.\d+)?)\s*[\)\]]?$`)
	reSeriesNumOnly           = regexp.MustCompile(`(?i)^(?:book|bk|volume|vol|part|no|#)\.?[._\s#]*(\d+(?:\.\d+)?)$`)
	reSeriesTailNum           = regexp.MustCompile(`^(.*?)[,_\s\-]+(\d+(?:\.\d+)?)$`)
	reVolumeInTitle           = regexp.MustCompile(`(?i)\b(?:vol(?:ume)?|book|bk)\.?\s*(\d+(?:\.\d+)?)\b`)
	reTitleSubtitleSeparator  = regexp.MustCompile(`:\s*|\s+[-–—]\s+`)
	seriesTitlePunctReplacer  = strings.NewReplacer(",", " ", ".", " ", ";", " ", ":", " ", "-", " ", "–", " ", "—", " ")
	smartQuoteReplacer        = strings.NewReplacer("\u2019", "'", "\u2018", "'", "\u201c", `"`, "\u201d", `"`)

	// Authors
	reCoAuthorSplit = regexp.MustCompile(`(?i)\s+(?:&|and|with|feat\.?|ft\.?)\s+|;\s*|\s*/\s*`)
	reDotWord       = regexp.MustCompile(`(\b[A-Za-z]{2,})\.([A-Za-z])`)

	// Brackets
	reEmptyBrackets = regexp.MustCompile(`\[\s*\]|\(\s*\)`)
	reBracketNoise  = regexp.MustCompile(`\[[^\]]*?\]`)

	// Illegal filesystem characters
	reIllegalChars  = regexp.MustCompile(`[\\/*?:"<>|]`)
	reMultipleDash  = regexp.MustCompile(`[-–—]{2,}`)
	reMultipleSpace = regexp.MustCompile(`\s+`)
	reVersion       = regexp.MustCompile(`(?i)\bv\d+(?:\.\d+)?\b`)
)

var knownNonGroups = map[string]bool{
	"epub": true, "m4b": true, "mp3": true, "pdf": true, "azw3": true,
	"mobi": true, "flac": true, "aac": true, "cbr": true, "cbz": true,
	"audiobook": true, "ebook": true, "unabridged": true, "abridged": true,
	"retail": true, "webrip": true, "scan": true, "repack": true,
	"internal": true, "readnfo": true, "vbr": true, "bundle": true,
	"proper": true, "de": true, "fr": true, "es": true, "en": true,
	"it": true, "nl": true, "ru": true,
}

var languageCodeMap = map[string]string{
	"german":     "de",
	"deutsch":    "de",
	"de":         "de",
	"french":     "fr",
	"français":   "fr",
	"fr":         "fr",
	"spanish":    "es",
	"español":    "es",
	"es":         "es",
	"italian":    "it",
	"italiano":   "it",
	"it":         "it",
	"dutch":      "nl",
	"nederlands": "nl",
	"nl":         "nl",
	"russian":    "ru",
	"ru":         "ru",
	"english":    "en",
	"eng":        "en",
	"en":         "en",
}

// ParseRelease parses a raw release name into a structured ParsedRelease.
func ParseRelease(raw string) ParsedRelease {
	s := strings.TrimSpace(raw)
	res := ParsedRelease{
		RawTitle: raw,
		Language: "en",
	}
	if s == "" {
		return res
	}

	// 1. Language extraction
	if mLang := reLanguageTag.FindStringSubmatch(s); len(mLang) > 1 {
		tag := strings.ToLower(mLang[1])
		if code, ok := languageCodeMap[tag]; ok {
			res.Language = code
			// Strip the language tag
			s = strings.TrimSpace(reLanguageTag.ReplaceAllString(s, " "))
		}
	}

	// 2. Scene / P2P group extraction
	// Trailing -GROUP
	if m := reTrailingDashGroup.FindStringSubmatch(s); len(m) > 1 {
		g := m[1]
		gLower := strings.ToLower(g)
		if !knownNonGroups[gLower] && !reBitrateKbps.MatchString(g) && !reYearStandalone.MatchString(g) {
			res.Group = g
			s = strings.TrimSpace(s[:len(s)-len(m[0])])
		}
	}

	// Trailing [GROUP]
	if res.Group == "" {
		if m := reTrailingBracketGroup.FindStringSubmatch(s); len(m) > 1 {
			g := m[1]
			gLower := strings.ToLower(g)
			if !knownNonGroups[gLower] && !reBitrateKbps.MatchString(g) && !reYearStandalone.MatchString(g) {
				res.Group = g
				s = strings.TrimSpace(s[:len(s)-len(m[0])])
			}
		}
	}

	// Leading [GROUP]
	if res.Group == "" {
		if m := reLeadingBracketGroup.FindStringSubmatch(s); len(m) > 1 {
			g := m[1]
			gLower := strings.ToLower(g)
			if !knownNonGroups[gLower] {
				res.Group = g
				s = strings.TrimSpace(s[len(m[0]):])
			}
		}
	}

	// 3. Unabridged / Abridged
	if reUnabridged.MatchString(s) {
		res.IsUnabridged = true
	}

	// 4. Bitrate extraction
	if m := reBitrateKbps.FindStringSubmatch(s); len(m) > 1 {
		if val, err := strconv.Atoi(m[1]); err == nil {
			res.BitrateKbps = val
		}
	} else if reBitrateVBR.MatchString(s) {
		res.BitrateKbps = -1
	} else if m := reBitrateK.FindStringSubmatch(s); len(m) > 1 {
		if val, err := strconv.Atoi(m[1]); err == nil {
			res.BitrateKbps = val
		}
	}

	// 5. Format extraction
	if m := reFormats.FindStringSubmatch(s); len(m) > 1 {
		res.Format = strings.ToLower(m[1])
	}

	// 6. Year extraction
	if m := reYearParen.FindStringSubmatch(s); len(m) > 1 {
		if val, err := strconv.Atoi(m[1]); err == nil {
			res.Year = val
		}
	} else if m := reYearStandalone.FindStringSubmatch(s); len(m) > 1 {
		if val, err := strconv.Atoi(m[1]); err == nil {
			res.Year = val
		}
	}

	// 7. Strip narrator credits
	s = reNarratorDot.ReplaceAllString(s, "")
	s = reNarratorSpace.ReplaceAllString(s, "")

	// 8. Strip bracket bundles (e.g. [EPUB+MOBI], [Audiobook MP3 320kbps])
	sCore := reBracketNoise.ReplaceAllString(s, "")

	// 9. Author, Series, and Title extraction
	if strings.Contains(sCore, " - ") {
		parts := strings.SplitN(sCore, " - ", 3)
		if len(parts) == 2 {
			p0 := strings.TrimSpace(parts[0])
			p1 := strings.TrimSpace(parts[1])

			// If p0 starts with an article, it might be Title with no Author
			p0Lower := strings.ToLower(p0)
			if strings.HasPrefix(p0Lower, "the ") || strings.HasPrefix(p0Lower, "a ") || strings.HasPrefix(p0Lower, "an ") {
				// Title - Subtitle / tag
				res.Title = SanitizeTitle(sCore)
			} else {
				res.Author = NormalizeAuthor(p0)
				// Check if p1 has embedded series: "Synthetic Series, Book 2.5 - Novella Title"
				if strings.Contains(p1, " - ") {
					subParts := strings.SplitN(p1, " - ", 2)
					if ser, idx := ExtractSeries(subParts[0]); idx != nil {
						res.Series = ser
						res.SeriesIndex = idx
						res.Title = SanitizeTitle(subParts[1])
					} else {
						res.Title = SanitizeTitle(p1)
					}
				} else if ser, idx := ExtractSeries(p1); idx != nil && ser != "" {
					res.Series = ser
					res.SeriesIndex = idx
					res.Title = SanitizeTitle(p1)
				} else {
					res.Title = SanitizeTitle(p1)
					if res.SeriesIndex == nil && reParenSeries.MatchString(p1) {
						if _, idx := ExtractSeries(p1); idx != nil {
							res.SeriesIndex = idx
						}
					}
				}
			}
		} else if len(parts) >= 3 {
			res.Author = NormalizeAuthor(parts[0])
			if ser, idx := ExtractSeries(parts[1]); idx != nil {
				res.Series = ser
				res.SeriesIndex = idx
				res.Title = SanitizeTitle(parts[2])
			} else {
				res.Title = SanitizeTitle(parts[1] + " - " + parts[2])
			}
		}
	} else if strings.Contains(sCore, "-") {
		parts := strings.Split(sCore, "-")
		cleanParts := make([]string, 0, len(parts))
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				cleanParts = append(cleanParts, trimmed)
			}
		}

		if len(cleanParts) >= 2 {
			res.Author = NormalizeAuthor(cleanParts[0])

			// Check if cleanParts[1] is a series indicator
			p1Lower := strings.ToLower(cleanParts[1])
			if len(cleanParts) >= 3 && (strings.Contains(p1Lower, "book") || strings.Contains(p1Lower, "vol")) {
				if ser, idx := ExtractSeries(cleanParts[1]); idx != nil {
					res.Series = ser
					res.SeriesIndex = idx
				}
				res.Title = SanitizeTitle(cleanParts[2])
			} else {
				res.Title = SanitizeTitle(cleanParts[1])
			}
		} else if len(cleanParts) == 1 {
			res.Title = SanitizeTitle(cleanParts[0])
		}
	} else {
		// No hyphens separating author and title.
		// If dots are used throughout (scene format, no spaces), first 2 words are author.
		// If spaces are used (regular text like "Synthetic Book Alpha"), it's a standalone title.
		trimmedCore := strings.TrimSpace(sCore)
		hasSceneDots := strings.Contains(trimmedCore, ".") && !strings.Contains(trimmedCore, " ")

		if hasSceneDots {
			clean := cleanDotSeparated(sCore)
			words := strings.Fields(clean)
			if len(words) >= 3 && !isArticle(words[0]) {
				res.Author = NormalizeAuthor(words[0] + " " + words[1])
				res.Title = SanitizeTitle(strings.Join(words[2:], " "))
			} else {
				res.Title = SanitizeTitle(clean)
			}
		} else {
			// Space-separated with no hyphen: title without author
			res.Title = SanitizeTitle(sCore)
		}
	}

	// 10. Extract series index from title if still not extracted (e.g. Vol.5 or Book 2)
	if res.SeriesIndex == nil {
		if m := reVolumeInTitle.FindStringSubmatchIndex(res.Title); len(m) >= 4 {
			idxStr := res.Title[m[2]:m[3]]
			if val, err := strconv.ParseFloat(idxStr, 64); err == nil {
				res.SeriesIndex = &val
				// Strip Vol.X from title if a non-empty title remains
				if stripped := SanitizeTitle(res.Title[:m[0]] + " " + res.Title[m[1]:]); stripped != "" {
					res.Title = stripped
				}
			}
		}
	}

	// Final cleanup on Title and Author
	res.Title = SanitizeTitle(res.Title)
	res.Author = NormalizeAuthor(res.Author)
	if res.Series != "" {
		res.Series = SanitizeTitle(res.Series)
	}

	return res
}

func isArticle(w string) bool {
	l := strings.ToLower(w)
	return l == "the" || l == "a" || l == "an"
}

func cleanDotSeparated(s string) string {
	s = reDotWord.ReplaceAllString(s, "$1 $2")
	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, "_", " ")
	return reMultipleSpace.ReplaceAllString(strings.TrimSpace(s), " ")
}

// SanitizeTitle cleans a book title by stripping scene tags, packaging noise,
// file formats, bitrates, narrator tags, versions, and duplicate whitespace,
// returning a clean, human-readable book title.
func SanitizeTitle(title string) string {
	t := title

	// Fast paths for narrator credits
	tLower := strings.ToLower(t)
	if strings.Contains(tLower, "read") || strings.Contains(tLower, "narrat") {
		t = reNarratorDot.ReplaceAllString(t, "")
		t = reNarratorSpace.ReplaceAllString(t, "")
	}

	// Strip bracket tags like [EPUB+MOBI], [Audiobook MP3 320kbps]
	if strings.Contains(t, "[") {
		t = reBracketNoise.ReplaceAllString(t, "")
	}

	// Strip parenthesized 4-digit years: (2020), (1998)
	if strings.Contains(t, "(") {
		t = reYearParen.ReplaceAllString(t, "")
	}

	// Strip scene packaging noise tags
	if reSceneTags.MatchString(t) {
		t = reSceneTags.ReplaceAllString(t, "")
	}

	// Strip bitrates
	if strings.Contains(tLower, "kbps") || strings.Contains(tLower, "vbr") || strings.Contains(tLower, "k") {
		t = reBitrateKbps.ReplaceAllString(t, "")
		t = reBitrateVBR.ReplaceAllString(t, "")
		t = reBitrateK.ReplaceAllString(t, "")
	}

	// Strip formats
	if reFormats.MatchString(t) {
		t = reFormats.ReplaceAllString(t, "")
	}

	// Strip version numbers like v2.0 or v1
	if strings.ContainsAny(t, "vV") && reVersion.MatchString(t) {
		t = reVersion.ReplaceAllString(t, "")
	}

	// Replace dots and underscores with spaces
	if strings.ContainsAny(t, "._") {
		t = cleanDotSeparated(t)
	}

	// Clean dashes and empty brackets
	if strings.ContainsAny(t, "[]()") {
		t = reEmptyBrackets.ReplaceAllString(t, "")
	}
	if strings.ContainsAny(t, "-–—") {
		t = reMultipleDash.ReplaceAllString(t, " - ")
	}
	if strings.Contains(t, "  ") {
		t = reMultipleSpace.ReplaceAllString(t, " ")
	}

	// Trim edge punctuation and any unmatched opening/closing delimiters without breaking balanced "(Series, Book 2)"
	t = strings.Trim(t, " -:,;_./#")
	t = stripUnclosedTrailingDelimiter(t)
	t = strings.TrimLeft(t, ")]}")
	t = strings.TrimRight(t, "([{")
	for strings.HasSuffix(t, ")") && strings.Count(t, ")") > strings.Count(t, "(") {
		t = strings.TrimRight(strings.TrimSuffix(t, ")"), " -:,;_./#")
	}
	for strings.HasSuffix(t, "]") && strings.Count(t, "]") > strings.Count(t, "[") {
		t = strings.TrimRight(strings.TrimSuffix(t, "]"), " -:,;_./#")
	}
	return strings.Trim(t, " -:,;_./#")
}

// stripUnclosedTrailingDelimiter trims a truncated unclosed parenthetical/bracketed
// suffix (e.g. "Abaddon's Gate (The Expanse Book 3" -> "Abaddon's Gate") while
// preserving balanced delimiters like "A Drop of Corruption (Shadow of the Leviathan, Book 2)".
func stripUnclosedTrailingDelimiter(s string) string {
	type openDelim struct {
		pos  int
		want rune
	}
	var stack []openDelim
	for i, r := range s {
		switch r {
		case '(':
			stack = append(stack, openDelim{pos: i, want: ')'})
		case '[':
			stack = append(stack, openDelim{pos: i, want: ']'})
		case '{':
			stack = append(stack, openDelim{pos: i, want: '}'})
		case ')', ']', '}':
			for j := len(stack) - 1; j >= 0; j-- {
				if stack[j].want == r {
					stack = stack[:j]
					break
				}
			}
		}
	}
	if len(stack) == 0 {
		return s
	}
	cut := stack[0].pos
	if prefix := strings.Trim(s[:cut], " -:,;_./#"); prefix != "" {
		return prefix
	}
	// If the unclosed delimiter is at the start of the string, drop only that delimiter.
	return strings.TrimLeft(s[cut+1:], " -:,;_./#([{")
}

// HasUnbalancedDelimiters reports whether s contains unmatched parentheses, brackets, or braces.
func HasUnbalancedDelimiters(s string) bool {
	return unbalancedDelimiterCount(s) > 0
}

func unbalancedDelimiterCount(s string) int {
	var stack []rune
	unmatched := 0
	for _, r := range s {
		switch r {
		case '(':
			stack = append(stack, ')')
		case '[':
			stack = append(stack, ']')
		case '{':
			stack = append(stack, '}')
		case ')', ']', '}':
			found := -1
			for j := len(stack) - 1; j >= 0; j-- {
				if stack[j] == r {
					found = j
					break
				}
			}
			if found >= 0 {
				unmatched += len(stack) - 1 - found
				stack = stack[:found]
			} else {
				unmatched++
			}
		}
	}
	return unmatched + len(stack)
}

// IsMalformedTitle reports whether a book title is empty, has unbalanced delimiters,
// or ends with dangling separator punctuation.
func IsMalformedTitle(title string) bool {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return true
	}
	if HasUnbalancedDelimiters(trimmed) {
		return true
	}
	switch trimmed[len(trimmed)-1] {
	case ':', '-', ',', ';', '(', '[', '{':
		return true
	}
	return strings.HasSuffix(trimmed, "–") || strings.HasSuffix(trimmed, "—")
}

// StripTitleAnnotation removes balanced or truncated trailing parenthetical/bracketed
// annotations (e.g. "(The Expanse Book 3)" or "(The Expanse Book 3") unless doing so
// would leave an empty title.
func StripTitleAnnotation(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return ""
	}
	stripped := strings.TrimSpace(reTrailingTitleAnnotation.ReplaceAllString(trimmed, ""))
	if stripped == "" {
		return trimmed
	}
	return stripped
}

// stripTrailingNamedSeriesAnnotation removes a trailing parenthetical/bracketed series
// tag that names a series and index (e.g. "(The Expanse Book 3)") while preserving
// bare part indicators like "(Part 1)" / "(Part 2)".
func stripTrailingNamedSeriesAnnotation(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return ""
	}
	for {
		loc := reTrailingTitleAnnotation.FindStringIndex(trimmed)
		if loc == nil {
			break
		}
		suffix := trimmed[loc[0]:loc[1]]
		ser, idx := ExtractSeries(suffix)
		if ser == "" || idx == nil {
			break
		}
		prefix := strings.TrimSpace(trimmed[:loc[0]])
		if prefix == "" {
			break
		}
		trimmed = prefix
	}
	return trimmed
}

var (
	reASCIIWord = regexp.MustCompile(`[a-z]+`)

	ukToUSWords = map[string]string{
		"grey": "gray", "greys": "grays", "greyish": "grayish",
		"centre": "center", "centres": "centers", "centred": "centered", "centring": "centering",
		"theatre": "theater", "theatres": "theaters",
		"spectre": "specter", "spectres": "specters",
		"sceptre": "scepter", "sceptres": "scepters",
		"sombre": "somber", "sabre": "saber", "sabres": "sabers",
		"fibre": "fiber", "fibres": "fibers",
		"calibre": "caliber", "lustre": "luster", "meagre": "meager",
		"sepulchre": "sepulcher", "sepulchres": "sepulchers",
		"manoeuvre": "maneuver", "manoeuvres": "maneuvers", "manoeuvred": "maneuvered",
		"defence": "defense", "defences": "defenses",
		"offence": "offense", "offences": "offenses",
		"pretence": "pretense", "pretences": "pretenses",
		"judgement": "judgment", "judgements": "judgments",
		"acknowledgement": "acknowledgment", "acknowledgements": "acknowledgments",
		"artefact": "artifact", "artefacts": "artifacts",
		"catalogue": "catalog", "catalogues": "catalogs", "catalogued": "cataloged",
		"dialogue": "dialog", "dialogues": "dialogs",
		"prologue": "prolog", "epilogue": "epilog", "monologue": "monolog", "analogue": "analog",
		"travelled": "traveled", "traveller": "traveler", "travellers": "travelers", "travelling": "traveling",
		"cancelled": "canceled", "cancelling": "canceling",
		"labelled": "labeled", "labelling": "labeling",
		"marvellous": "marvelous",
		"counsellor": "counselor", "counsellors": "counselors",
		"jeweller": "jeweler", "jewellers": "jewelers",
		"plough": "plow", "ploughed": "plowed", "ploughing": "plowing",
		"draught": "draft", "draughts": "drafts",
		"aeon": "eon", "aeons": "eons",
		"mediaeval": "medieval", "encyclopaedia": "encyclopedia",
		"fulfil": "fulfill", "fulfilment": "fulfillment",
		"skilful": "skillful", "wilful": "willful", "enthral": "enthrall",
		"realise": "realize", "realised": "realized", "realises": "realizes", "realising": "realizing",
		"recognise": "recognize", "recognised": "recognized", "recognises": "recognizes", "recognising": "recognizing",
		"organise": "organize", "organised": "organized", "organises": "organizes", "organising": "organizing",
		"criticise": "criticize", "criticised": "criticized", "criticises": "criticizes",
		"apologise": "apologize", "apologised": "apologized",
		"memorise": "memorize", "memorised": "memorized",
		"symbolise": "symbolize", "symbolised": "symbolized",
		"civilise": "civilize", "civilised": "civilized",
		"modernise": "modernize", "modernised": "modernized",
	}

	ukOurRoots    = []string{"honour", "colour", "favour", "armour", "rumour", "valour", "harbour", "splendour", "saviour", "endeavour", "behaviour", "neighbour", "flavour", "humour", "labour", "odour", "parlour", "rancour", "rigour", "clamour", "candour", "fervour", "glamour", "vigour", "tumour", "ardour", "demeanour", "enamour", "succour", "vapour"}
	ukOurPrefixes = []string{"", "dis", "un", "mis", "re", "non", "in"}
	ukOurSuffixes = []string{"", "s", "ed", "ing", "er", "ers", "able", "ably", "ite", "ites", "less", "ful", "hood", "y", "ily", "ist", "ists"}
)

func normalizeUSUKWord(w string) string {
	if us, ok := ukToUSWords[w]; ok {
		return us
	}
	if len(w) >= 6 && strings.Contains(w, "our") {
		for _, pre := range ukOurPrefixes {
			if pre != "" && !strings.HasPrefix(w, pre) {
				continue
			}
			rest := w[len(pre):]
			for _, root := range ukOurRoots {
				if !strings.HasPrefix(rest, root) {
					continue
				}
				tail := rest[len(root):]
				for _, suf := range ukOurSuffixes {
					if tail == suf {
						return pre + strings.Replace(root, "our", "or", 1) + suf
					}
				}
			}
		}
	}
	return w
}

// NormalizeTitleKey returns a normalized comparison key for matching book titles across
// casing, whitespace, scene noise, smart quotes, trailing named series annotations,
// and common US/UK spelling variations.
func NormalizeTitleKey(title string) string {
	normalized := smartQuoteReplacer.Replace(stripTrailingNamedSeriesAnnotation(title))
	cleaned := strings.ToLower(strings.Join(strings.Fields(SanitizeTitle(normalized)), " "))
	if cleaned == "" {
		return ""
	}
	return reASCIIWord.ReplaceAllStringFunc(cleaned, normalizeUSUKWord)
}

// SeriesTitleKey returns a punctuation-insensitive normalized key for comparing
// series titles and book titles within a series.
func SeriesTitleKey(title string) string {
	key := NormalizeTitleKey(title)
	if key == "" {
		return ""
	}
	key = seriesTitlePunctReplacer.Replace(key)
	fields := strings.Fields(key)
	for i, f := range fields {
		if f == "vol" {
			fields[i] = "volume"
		}
	}
	return strings.Join(fields, " ")
}

func isSeriesPrefixSegment(prefix, seriesTitle string) bool {
	normPrefix := SeriesTitleKey(prefix)
	if normPrefix == "" {
		return false
	}
	if seriesTitle != "" && normPrefix == SeriesTitleKey(seriesTitle) {
		return true
	}
	if ser, idx := ExtractSeries(prefix); idx != nil {
		if ser == "" {
			return true
		}
		if seriesTitle != "" && SeriesTitleKey(ser) == SeriesTitleKey(seriesTitle) {
			return true
		}
	}
	return false
}

// CanonicalSeriesBookTitleKey returns the normalized core title key for a book inside a
// series, stripping trailing balanced/truncated parenthetical annotations and leading
// series/volume prefixes (e.g. "Series, Book 3: Title").
func CanonicalSeriesBookTitleKey(title, seriesTitle string) string {
	clean := StripTitleAnnotation(title)
	if sep := reTitleSubtitleSeparator.FindStringIndex(clean); sep != nil {
		mainPart := strings.TrimSpace(clean[:sep[0]])
		subPart := strings.TrimSpace(clean[sep[1]:])
		if subPart != "" && isSeriesPrefixSegment(mainPart, seriesTitle) {
			clean = subPart
		}
	}
	return SeriesTitleKey(clean)
}

// HasSeriesTitleAnnotation reports whether title carries a trailing parenthetical/bracketed
// series annotation or a leading series/volume prefix.
func HasSeriesTitleAnnotation(title, seriesTitle string) bool {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return false
	}
	if stripped := StripTitleAnnotation(trimmed); stripped != trimmed {
		suffix := strings.TrimSpace(trimmed[len(stripped):])
		if _, idx := ExtractSeries(suffix); idx != nil {
			return true
		}
		if seriesTitle != "" && SeriesTitleKey(suffix) == SeriesTitleKey(seriesTitle) {
			return true
		}
		if HasUnbalancedDelimiters(suffix) {
			return true
		}
	}
	if sep := reTitleSubtitleSeparator.FindStringIndex(trimmed); sep != nil {
		mainPart := strings.TrimSpace(trimmed[:sep[0]])
		subPart := strings.TrimSpace(trimmed[sep[1]:])
		if subPart != "" && isSeriesPrefixSegment(mainPart, seriesTitle) {
			return true
		}
	}
	return false
}

// PreferBookTitle reports whether candidate is a strictly cleaner, better-formed title
// than current for the same book (preferring well-formed punctuation and titles without
// redundant series annotations while preserving current on ties).
func PreferBookTitle(candidate, current, seriesTitle string) bool {
	cand := strings.TrimSpace(candidate)
	cur := strings.TrimSpace(current)
	if cand == "" {
		return false
	}
	if cur == "" {
		return true
	}
	candMalformed := IsMalformedTitle(cand)
	curMalformed := IsMalformedTitle(cur)
	if candMalformed != curMalformed {
		return !candMalformed
	}
	if candMalformed && curMalformed {
		candUnbal := unbalancedDelimiterCount(cand)
		curUnbal := unbalancedDelimiterCount(cur)
		if candUnbal != curUnbal {
			return candUnbal < curUnbal
		}
	}
	candAnnotated := HasSeriesTitleAnnotation(cand, seriesTitle)
	curAnnotated := HasSeriesTitleAnnotation(cur, seriesTitle)
	if candAnnotated != curAnnotated {
		return !candAnnotated
	}
	if candAnnotated && curAnnotated {
		// When both carry annotations, prefer the cleaner/shorter annotation form if one is
		// missing closing punctuation or otherwise noisier.
		candUnbal := unbalancedDelimiterCount(cand)
		curUnbal := unbalancedDelimiterCount(cur)
		if candUnbal != curUnbal {
			return candUnbal < curUnbal
		}
	}
	return false
}

// DistinctTitlePartIndices reports whether two titles carry explicit, conflicting
// volume/part numbers (e.g. "The Long Tale (Part 1)" vs "The Long Tale (Part 2)").
func DistinctTitlePartIndices(a, b string) bool {
	_, aIdx := ExtractSeriesFromTitle(a)
	_, bIdx := ExtractSeriesFromTitle(b)
	if aIdx == nil || bIdx == nil {
		return false
	}
	diff := *aIdx - *bIdx
	if diff < 0 {
		diff = -diff
	}
	return diff > 1e-6
}

// DiscoveryKey returns a normalized comparison key for matching a book by title and author.
func DiscoveryKey(title, author string) string {
	author = NormalizeAuthor(author)
	title = StripDiscoveryAuthorPrefix(title, author)
	return NormalizeTitleKey(title) + "\x00" + strings.ToLower(author)
}

// StripDiscoveryAuthorPrefix removes a leading credited name from a comparison title.
// Single-word names require an explicit separator to avoid removing ordinary title words.
func StripDiscoveryAuthorPrefix(title string, authors ...string) string {
	trimmed := strings.TrimSpace(title)
	for _, credit := range authors {
		for _, author := range strings.Split(NormalizeAuthor(credit), " & ") {
			author = strings.Join(strings.Fields(author), " ")
			if author == "" {
				continue
			}
			for i, r := range trimmed {
				if i == 0 || (!unicode.IsSpace(r) && r != ':') {
					continue
				}
				prefix := strings.Join(strings.Fields(trimmed[:i]), " ")
				matches := strings.EqualFold(prefix, author)
				if last, first, ok := strings.Cut(prefix, ","); ok {
					matches = strings.EqualFold(strings.TrimSpace(first)+" "+strings.TrimSpace(last), author)
				}
				if !matches {
					continue
				}
				rest := strings.TrimSpace(trimmed[i:])
				remainder := strings.TrimLeftFunc(rest, func(r rune) bool {
					return r == ':' || r == '-' || r == '–' || r == '—' || unicode.IsSpace(r)
				})
				if (remainder != rest || strings.Contains(author, " ")) && strings.TrimSpace(SanitizeTitle(remainder)) != "" {
					return remainder
				}
			}
		}
	}
	return title
}

// NormalizeAuthor normalizes an author string, converting "Last, First" into "First Last",
// separating multiple co-authors (with " & "), cleaning dot-separated names ("First.Last" -> "First Last"),
// while preserving initials and multi-part surnames.
func NormalizeAuthor(author string) string {
	a := strings.TrimSpace(author)
	if a == "" {
		return ""
	}

	// Normalize dot-separated words like Alina.Starkova -> Alina Starkova
	// while preserving initials like J. R. R. or J.R.R.
	a = reDotWord.ReplaceAllString(a, "$1 $2")
	a = strings.ReplaceAll(a, "_", " ")
	a = reMultipleSpace.ReplaceAllString(a, " ")

	// Check for multiple co-authors separated by &, and, with, ;, or /
	delims := reCoAuthorSplit.Split(a, -1)
	if len(delims) > 1 {
		normParts := make([]string, 0, len(delims))
		for _, part := range delims {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				normParts = append(normParts, normalizeSingleAuthor(trimmed))
			}
		}
		if len(normParts) > 0 {
			return strings.Join(normParts, " & ")
		}
	}

	// Check if a single string has a comma separating two full authors:
	// e.g. "Author One, Author Two"
	if strings.Contains(a, ",") {
		parts := strings.Split(a, ",")
		if len(parts) == 2 {
			w0 := strings.Fields(parts[0])
			w1 := strings.Fields(parts[1])
			if len(w0) >= 2 && len(w1) >= 2 {
				// Two distinct authors
				return normalizeSingleAuthor(parts[0]) + " & " + normalizeSingleAuthor(parts[1])
			}
		}
	}

	return normalizeSingleAuthor(a)
}

func normalizeSingleAuthor(s string) string {
	s = strings.Trim(s, " -:,_()[]{}#")
	if s == "" {
		return ""
	}

	// Check for "Last, First Middle" format
	if strings.Contains(s, ",") {
		parts := strings.SplitN(s, ",", 2)
		last := strings.TrimSpace(parts[0])
		first := strings.TrimSpace(parts[1])
		if last != "" && first != "" {
			return strings.TrimSpace(first + " " + last)
		}
	}

	return reMultipleSpace.ReplaceAllString(s, " ")
}

func extractParenSeries(s string) string {
	m := reParenSeries.FindStringSubmatch(s)
	if len(m) < 3 {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

// ExtractSeries extracts a series name and its numeric index (e.g. 1.0, 2.5) from a string.
// Handles formats like "Book 2.5", "#01", "Vol. 3", "Synthetic Series Book 05", "Series, Book 2", etc.
func ExtractSeries(raw string) (series string, index *float64) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}

	// Check for parenthesized series: e.g. "Title (Series Name #2)" or truncated "Title (Series Name Book 3"
	if inner := extractParenSeries(s); inner != "" {
		ser, idx := ExtractSeries(inner)
		if idx != nil {
			return ser, idx
		}
	}

	// Pattern 1: Standalone series index only: "Book 2.5", "#01", "Vol. 3", "Volume 03"
	if m := reSeriesNumOnly.FindStringSubmatch(s); len(m) > 1 {
		if val, err := strconv.ParseFloat(m[1], 64); err == nil {
			return "", &val
		}
	}

	// Pattern 2: "Synthetic Series, Book 2.5" or "Synthetic.Series.Book.05"
	if m := reSeriesPattern.FindStringSubmatch(s); len(m) > 2 {
		ser := cleanSeriesName(m[1])
		if val, err := strconv.ParseFloat(m[2], 64); err == nil {
			return ser, &val
		}
	}

	// Pattern 3: "The Dresden Files #01"
	if m := reSeriesHash.FindStringSubmatch(s); len(m) > 2 {
		ser := cleanSeriesName(m[1])
		if val, err := strconv.ParseFloat(m[2], 64); err == nil {
			return ser, &val
		}
	}

	// Pattern 4: "Synthetic Series 02"
	if m := reSeriesTailNum.FindStringSubmatch(s); len(m) > 2 {
		ser := cleanSeriesName(m[1])
		if val, err := strconv.ParseFloat(m[2], 64); err == nil {
			// Ensure ser is not empty
			if ser != "" {
				return ser, &val
			}
		}
	}

	return "", nil
}

// ExtractSeriesFromTitle conservatively extracts a parenthesized series name and/or
// an explicit volume/book index from a book title or filename without misinterpreting
// numeric titles like "Fahrenheit 451" or "Catch-22".
func ExtractSeriesFromTitle(raw string) (series string, index *float64) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if inner := extractParenSeries(s); inner != "" {
		ser, idx := ExtractSeries(inner)
		if idx != nil {
			return ser, idx
		}
	}
	if m := reSeriesNumOnly.FindStringSubmatch(s); len(m) > 1 {
		if val, err := strconv.ParseFloat(m[1], 64); err == nil {
			return "", &val
		}
	}
	if m := reVolumeInTitle.FindStringSubmatch(s); len(m) > 1 {
		if val, err := strconv.ParseFloat(m[1], 64); err == nil {
			return "", &val
		}
	}
	return "", nil
}

func cleanSeriesName(s string) string {
	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = reMultipleSpace.ReplaceAllString(s, " ")
	return strings.Trim(s, " -:,_./()[]{}#")
}

// StripIllegalFileChars strips or replaces characters that are illegal in file and directory
// names across Linux, macOS, and Windows (/ \ : * ? " < > |).
// Colons and pipes are replaced with " - ", slashes replaced with "-", and invalid characters removed.
func StripIllegalFileChars(s string) string {
	// Replace colons and pipes with " - "
	s = strings.ReplaceAll(s, ":", " - ")
	s = strings.ReplaceAll(s, "|", " - ")
	// Replace slashes with "-"
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	// Remove invalid chars
	s = reIllegalChars.ReplaceAllString(s, "")
	// Clean duplicate dashes and spaces
	s = reMultipleDash.ReplaceAllString(s, " - ")
	s = reMultipleSpace.ReplaceAllString(s, " ")
	// Trim edge dots, spaces, dashes (Windows disallows trailing dots and spaces)
	return strings.Trim(s, " .-\t\r\n")
}

// SanitizeFilename produces a safe filesystem name for a library file or directory.
func SanitizeFilename(name string) string {
	return StripIllegalFileChars(name)
}

// FormatBitrate returns a formatted string for bitrates, e.g. "320kbps" or "VBR".
func FormatBitrate(bitrateKbps int) string {
	if bitrateKbps < 0 {
		return "VBR"
	}
	if bitrateKbps == 0 {
		return ""
	}
	return fmt.Sprintf("%dkbps", bitrateKbps)
}

var (
	reHTMLBlockBreak = regexp.MustCompile(`(?i)<\s*(?:br\s*/?|/\s*(?:p|div|li|h[1-6]|blockquote))\s*>`)
	reHTMLTag        = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
)

// CleanDescription normalizes a book description/synopsis into clean readable plain text.
// Plain text without HTML markup is preserved with normalized line endings and trimmed whitespace.
func CleanDescription(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if strings.ContainsRune(s, '<') && reHTMLTag.MatchString(s) {
		s = reHTMLBlockBreak.ReplaceAllString(s, "\n")
		s = reHTMLTag.ReplaceAllString(s, "")
		s = html.UnescapeString(s)
	}
	lines := strings.Split(s, "\n")
	cleaned := make([]string, 0, len(lines))
	blankCount := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(cleaned) == 0 {
				continue
			}
			blankCount++
			if blankCount == 1 {
				cleaned = append(cleaned, "")
			}
			continue
		}
		blankCount = 0
		cleaned = append(cleaned, trimmed)
	}
	for len(cleaned) > 0 && cleaned[len(cleaned)-1] == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}
	return strings.Join(cleaned, "\n")
}
