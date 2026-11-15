package downloaders

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/t0ggl/callimachus/pkg/metadata"
)

// Standard Torznab category constants for audiobooks and books.
const (
	CategoryAudioRoot      = 3000
	CategoryAudioAudiobook = 3030
	CategoryBooksRoot      = 7000
	CategoryBooksEBook     = 7020
)

// DefaultAllowedLanguages defines the standard languages permitted for ingestion.
var DefaultAllowedLanguages = []string{"en", "eng", "english"}

// DefaultAllowedFormats defines the standard eBook and audiobook formats permitted.
var DefaultAllowedFormats = []string{"epub", "azw3", "pdf", "m4b", "mp3"}

// QualityProfile defines rules for filtering discovered media releases.
type QualityProfile struct {
	AllowedLanguages    []string
	AllowedFormats      []string
	MinAudioBitrateKbps int
	PreferSingleM4B     bool
	RejectPatterns      []*regexp.Regexp
}

// ReleaseCandidate represents a candidate release evaluated for acquisition.
type ReleaseCandidate struct {
	Title       string `json:"title"`
	InfoHash    string `json:"info_hash"`
	Format      string `json:"format"`
	BitrateKbps int    `json:"bitrate_kbps"`
	Language    string `json:"language"`
	IsAudio     bool   `json:"is_audio"`
	Categories  []int  `json:"categories,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// DefaultQualityProfile returns a QualityProfile initialized with standard defaults.
func DefaultQualityProfile() QualityProfile {
	return QualityProfile{
		AllowedLanguages:    DefaultAllowedLanguages,
		AllowedFormats:      DefaultAllowedFormats,
		MinAudioBitrateKbps: 64,
		PreferSingleM4B:     true,
	}
}

var languageCodeMap = map[string]string{
	"german":     "de",
	"deutsch":    "de",
	"ger":        "de",
	"de":         "de",
	"french":     "fr",
	"français":   "fr",
	"francais":   "fr",
	"fre":        "fr",
	"fra":        "fr",
	"fr":         "fr",
	"spanish":    "es",
	"español":    "es",
	"espanol":    "es",
	"spa":        "es",
	"es":         "es",
	"italian":    "it",
	"italiano":   "it",
	"ita":        "it",
	"it":         "it",
	"dutch":      "nl",
	"nederlands": "nl",
	"dut":        "nl",
	"nl":         "nl",
	"russian":    "ru",
	"rus":        "ru",
	"ru":         "ru",
	"portuguese": "pt",
	"português":  "pt",
	"portugues":  "pt",
	"por":        "pt",
	"pt":         "pt",
	"japanese":   "ja",
	"jpn":        "ja",
	"ja":         "ja",
	"chinese":    "zh",
	"chi":        "zh",
	"zho":        "zh",
	"zh":         "zh",
	"polish":     "pl",
	"pol":        "pl",
	"pl":         "pl",
	"swedish":    "sv",
	"swe":        "sv",
	"sv":         "sv",
	"english":    "en",
	"eng":        "en",
	"en":         "en",
}

// Regular expressions to detect explicit foreign language tags embedded in release titles.
var (
	reTitleLanguageTags = regexp.MustCompile(`(?i)(?:^|[\[\(\.\-_\s])(german|deutsch|french|français|francais|spanish|español|espanol|italian|italiano|dutch|nederlands|russian|de|fr|es|it|nl|ru|ger|fre|fra|spa|ita|dut|rus|english|eng|en)(?:$|[\]\)\.\-_\s])`)
	reFileExtension     = regexp.MustCompile(`(?i)\.([a-z0-9]{2,5})$`)
)

var unsafeFormats = map[string]bool{
	"exe": true, "bat": true, "cmd": true, "sh": true,
	"msi": true, "scr": true, "com": true, "vbs": true,
	"apk": true, "jar": true, "bin": true, "txt": true,
}

// EvaluateRelease evaluates a candidate release against the quality profile.
// It returns (accepted bool, reason string).
func (p QualityProfile) EvaluateRelease(release ReleaseCandidate) (bool, string) {
	// 1. Check RejectPatterns
	for _, pattern := range p.RejectPatterns {
		if pattern != nil && (pattern.MatchString(release.Title) || pattern.MatchString(release.Format)) {
			return false, fmt.Sprintf("title matches reject pattern %q", pattern.String())
		}
	}

	// 2. Evaluate Language
	allowedLangs := p.AllowedLanguages
	if len(allowedLangs) == 0 {
		allowedLangs = DefaultAllowedLanguages
	}
	allowedLangSet := make(map[string]bool, len(allowedLangs))
	for _, l := range allowedLangs {
		clean := strings.ToLower(strings.TrimSpace(l))
		allowedLangSet[clean] = true
		if code, ok := languageCodeMap[clean]; ok {
			allowedLangSet[code] = true
		}
	}

	detectedLang := ""
	if release.Language != "" {
		clean := strings.ToLower(strings.TrimSpace(release.Language))
		if code, ok := languageCodeMap[clean]; ok {
			detectedLang = code
		} else {
			detectedLang = clean
		}
		if !allowedLangSet[detectedLang] && !allowedLangSet[clean] {
			return false, fmt.Sprintf("language %q not allowed by quality profile", release.Language)
		}
	}

	// Check language parsed or tagged in title
	if release.Title != "" {
		parsed := metadata.ParseRelease(release.Title)
		if parsed.Language != "" && parsed.Language != "en" {
			pCode := parsed.Language
			if !allowedLangSet[pCode] {
				return false, fmt.Sprintf("language %q not allowed by quality profile", pCode)
			}
		}

		// Also check all explicit language tags in title
		matches := reTitleLanguageTags.FindAllStringSubmatch(release.Title, -1)
		for _, m := range matches {
			if len(m) > 1 {
				tag := strings.ToLower(m[1])
				code, ok := languageCodeMap[tag]
				if !ok {
					code = tag
				}
				if !allowedLangSet[code] && !allowedLangSet[tag] {
					return false, fmt.Sprintf("language %q detected in title not allowed by quality profile", tag)
				}
			}
		}
	}

	// 3. Evaluate Format
	format := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(release.Format), "."))
	if format == "" && release.Title != "" {
		parsed := metadata.ParseRelease(release.Title)
		if parsed.Format != "" {
			format = strings.ToLower(parsed.Format)
		} else if mExt := reFileExtension.FindStringSubmatch(release.Title); len(mExt) > 1 {
			format = strings.ToLower(mExt[1])
		}
	}

	// Reject known unsafe / non-book formats by default
	if unsafeFormats[format] {
		allowed := false
		for _, af := range p.AllowedFormats {
			if strings.EqualFold(strings.TrimPrefix(af, "."), format) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false, fmt.Sprintf("format %q not allowed by quality profile", format)
		}
	}

	if len(p.AllowedFormats) > 0 {
		if format == "" {
			return false, "release format could not be determined"
		}
		allowed := false
		for _, af := range p.AllowedFormats {
			if strings.EqualFold(strings.TrimPrefix(af, "."), format) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false, fmt.Sprintf("format %q not allowed by quality profile", format)
		}
	}

	// 4. Evaluate Audio Bitrate
	bitrate := release.BitrateKbps
	if bitrate == 0 && release.Title != "" {
		parsed := metadata.ParseRelease(release.Title)
		bitrate = parsed.BitrateKbps
	}

	if isAudioRelease(release, format) && bitrate > 0 && p.MinAudioBitrateKbps > 0 {
		if bitrate < p.MinAudioBitrateKbps {
			return false, fmt.Sprintf("audio bitrate %dkbps below minimum %dkbps", bitrate, p.MinAudioBitrateKbps)
		}
	}

	return true, ""
}

// EvaluateRelease package-level wrapper for profile.EvaluateRelease.
func EvaluateRelease(profile QualityProfile, release ReleaseCandidate) (bool, string) {
	return profile.EvaluateRelease(release)
}

func isAudioRelease(release ReleaseCandidate, format string) bool {
	if release.IsAudio {
		return true
	}
	switch format {
	case "m4b", "mp3", "flac", "aac", "ogg", "opus", "m4a", "wma", "wav":
		return true
	}
	for _, c := range release.Categories {
		if c == CategoryAudioRoot || c == CategoryAudioAudiobook {
			return true
		}
	}
	return false
}

// ShouldDownload coordinates blocklist and quality profile evaluations.
// Returns (accepted bool, reason string, err error).
func ShouldDownload(ctx context.Context, profile QualityProfile, blocklist *BlocklistManager, release ReleaseCandidate) (bool, string, error) {
	if blocklist != nil {
		isBlocked, reason, err := blocklist.IsBlocklisted(ctx, release.InfoHash, release.Title)
		if err != nil {
			return false, "", fmt.Errorf("blocklist check failed: %w", err)
		}
		if isBlocked {
			if reason == "" {
				reason = "blocklisted"
			}
			return false, fmt.Sprintf("release is blocklisted: %s", reason), nil
		}
	}

	accepted, reason := profile.EvaluateRelease(release)
	if !accepted {
		return false, reason, nil
	}

	return true, "", nil
}

// NormalizeFormat returns a clean, lowercased extension without leading dot.
func NormalizeFormat(ext string) string {
	ext = strings.TrimSpace(strings.ToLower(ext))
	return strings.TrimPrefix(ext, ".")
}

// SanitizeExt extracts the extension from a file path or title.
func SanitizeExt(filename string) string {
	return NormalizeFormat(filepath.Ext(filename))
}
