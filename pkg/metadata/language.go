package metadata

import (
	"slices"
	"strings"
)

// LanguageAll disables language filtering and includes results in every language.
const LanguageAll = "all"

// languageAliases maps ISO 639-1, ISO 639-2/B, ISO 639-2/T, and common English/native
// language names to canonical ISO 639-1 codes.
var languageAliases = map[string]string{
	// English
	"en": "en", "eng": "en", "english": "en",
	// Spanish
	"es": "es", "spa": "es", "spanish": "es", "español": "es", "espanol": "es", "castilian": "es",
	// French
	"fr": "fr", "fra": "fr", "fre": "fr", "french": "fr", "français": "fr", "francais": "fr",
	// German
	"de": "de", "deu": "de", "ger": "de", "german": "de", "deutsch": "de",
	// Portuguese
	"pt": "pt", "por": "pt", "portuguese": "pt", "português": "pt", "portugues": "pt",
	// Italian
	"it": "it", "ita": "it", "italian": "it", "italiano": "it",
	// Dutch
	"nl": "nl", "nld": "nl", "dut": "nl", "dutch": "nl", "nederlands": "nl", "flemish": "nl",
	// Russian
	"ru": "ru", "rus": "ru", "russian": "ru", "русский": "ru",
	// Japanese
	"ja": "ja", "jpn": "ja", "japanese": "ja", "日本語": "ja",
	// Chinese
	"zh": "zh", "zho": "zh", "chi": "zh", "chinese": "zh", "mandarin": "zh", "cantonese": "zh", "中文": "zh",
	// Korean
	"ko": "ko", "kor": "ko", "korean": "ko", "한국어": "ko",
	// Polish
	"pl": "pl", "pol": "pl", "polish": "pl", "polski": "pl",
	// Swedish
	"sv": "sv", "swe": "sv", "swedish": "sv", "svenska": "sv",
	// Norwegian
	"no": "no", "nor": "no", "nb": "no", "nob": "no", "nn": "no", "nno": "no",
	"norwegian": "no", "norsk": "no", "bokmal": "no", "bokmål": "no", "nynorsk": "no",
	// Danish
	"da": "da", "dan": "da", "danish": "da", "dansk": "da",
	// Finnish
	"fi": "fi", "fin": "fi", "finnish": "fi", "suomi": "fi",
	// Arabic
	"ar": "ar", "ara": "ar", "arabic": "ar", "العربية": "ar",
	// Hindi
	"hi": "hi", "hin": "hi", "hindi": "hi", "हिन्दी": "hi",
	// Turkish
	"tr": "tr", "tur": "tr", "turkish": "tr", "türkçe": "tr", "turkce": "tr",
	// Czech
	"cs": "cs", "ces": "cs", "cze": "cs", "czech": "cs", "čeština": "cs", "cestina": "cs",
	// Hungarian
	"hu": "hu", "hun": "hu", "hungarian": "hu", "magyar": "hu",
	// Romanian
	"ro": "ro", "ron": "ro", "rum": "ro", "romanian": "ro", "română": "ro", "romana": "ro",
	// Greek
	"el": "el", "ell": "el", "gre": "el", "greek": "el", "ελληνικά": "el",
	// Hebrew
	"he": "he", "heb": "he", "iw": "he", "hebrew": "he", "עברית": "he",
	// Ukrainian
	"uk": "uk", "ukr": "uk", "ukrainian": "uk", "українська": "uk",
	// Indonesian
	"id": "id", "ind": "id", "in": "id", "indonesian": "id", "bahasa indonesia": "id",
	// Vietnamese
	"vi": "vi", "vie": "vi", "vietnamese": "vi", "tiếng việt": "vi",
	// Thai
	"th": "th", "tha": "th", "thai": "th", "ไทย": "th",
	// Catalan
	"ca": "ca", "cat": "ca", "catalan": "ca", "català": "ca",
	// Bulgarian
	"bg": "bg", "bul": "bg", "bulgarian": "bg",
	// Croatian
	"hr": "hr", "hrv": "hr", "croatian": "hr", "hrvatski": "hr",
	// Slovak
	"sk": "sk", "slk": "sk", "slo": "sk", "slovak": "sk",
	// Slovenian
	"sl": "sl", "slv": "sl", "slovenian": "sl", "slovene": "sl",
	// Serbian
	"sr": "sr", "srp": "sr", "serbian": "sr",
	// Persian
	"fa": "fa", "fas": "fa", "per": "fa", "persian": "fa", "farsi": "fa",
	// Latin
	"la": "la", "lat": "la", "latin": "la",
	// Irish
	"ga": "ga", "gle": "ga", "irish": "ga",
	// Welsh
	"cy": "cy", "cym": "cy", "wel": "cy", "welsh": "cy",
	// Icelandic
	"is": "is", "isl": "is", "ice": "is", "icelandic": "is",
	// Estonian
	"et": "et", "est": "et", "estonian": "et",
	// Latvian
	"lv": "lv", "lav": "lv", "latvian": "lv",
	// Lithuanian
	"lt": "lt", "lit": "lt", "lithuanian": "lt",
	// Malay
	"ms": "ms", "msa": "ms", "may": "ms", "malay": "ms",
	// Tagalog / Filipino
	"tl": "tl", "tgl": "tl", "fil": "tl", "tagalog": "tl", "filipino": "tl",
}

var unknownLanguageTokens = map[string]bool{
	"und": true, "un": true, "unknown": true, "unspecified": true,
	"none": true, "n/a": true, "zxx": true, "qaa": true, "mul": true,
}

func isASCIILetters(s string, minLen, maxLen int) bool {
	if len(s) < minLen || len(s) > maxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}

func isValidBCP47Subtag(sub string) bool {
	if isASCIILetters(sub, 2, 2) || isASCIILetters(sub, 4, 4) {
		return true
	}
	if len(sub) == 3 {
		for i := 0; i < 3; i++ {
			if sub[i] < '0' || sub[i] > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// NormalizeLanguage converts ISO 639-1, ISO 639-2/B/T, BCP-47 locale tags, OpenLibrary
// "/languages/..." keys, and language names into a canonical lowercase language code.
// Empty or unknown/undetermined values return "".
func NormalizeLanguage(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "/languages/")
	s = strings.TrimSpace(s)
	if s == "" || unknownLanguageTokens[s] {
		return ""
	}
	if code, ok := languageAliases[s]; ok {
		return code
	}

	// Split BCP-47 / POSIX locale tags (e.g. "en-US", "en_GB", "es-419", "zh-Hans-CN").
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
	if len(parts) == 0 || len(parts) > 3 {
		return ""
	}
	primary := parts[0]
	for _, sub := range parts[1:] {
		if !isValidBCP47Subtag(sub) {
			return ""
		}
	}
	if unknownLanguageTokens[primary] {
		return ""
	}
	if code, ok := languageAliases[primary]; ok {
		return code
	}
	if isASCIILetters(primary, 2, 3) {
		return primary
	}
	return ""
}

// NormalizeLanguageFilter normalizes a user or profile language filter value.
// "all" and "*" normalize to LanguageAll; other values use NormalizeLanguage.
func NormalizeLanguageFilter(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == LanguageAll || s == "*" {
		return LanguageAll
	}
	return NormalizeLanguage(s)
}

// DefaultProfileLanguage returns the primary canonical language from the quality
// profile's allowed languages list, defaulting to "en" when unset or empty.
func DefaultProfileLanguage(allowedLanguages []string) string {
	for _, raw := range allowedLanguages {
		if code := NormalizeLanguageFilter(raw); code != "" {
			return code
		}
	}
	return "en"
}

// BookLanguages returns deduplicated canonical language codes associated with a
// BookMetadata record, preserving discovery order.
func BookLanguages(book BookMetadata) []string {
	var out []string
	add := func(raw string) {
		code := NormalizeLanguage(raw)
		if code != "" && !slices.Contains(out, code) {
			out = append(out, code)
		}
	}
	add(book.Language)
	for _, raw := range book.Languages {
		add(raw)
	}
	return out
}

// MatchLanguageFilter checks whether a metadata candidate should be shown for the
// given normalized language filter. Untagged books (no known language) always match.
// When a multi-language record matches the selected filter, resolvedLang is set to
// the matched canonical language code.
func MatchLanguageFilter(book BookMetadata, filter string) (matched bool, resolvedLang string, explicitMatch bool) {
	langs := BookLanguages(book)
	if filter == "" || filter == LanguageAll {
		if len(langs) > 0 {
			return true, langs[0], true
		}
		return true, "", false
	}
	if len(langs) == 0 {
		return true, "", false
	}
	if slices.Contains(langs, filter) {
		return true, filter, true
	}
	return false, "", false
}
