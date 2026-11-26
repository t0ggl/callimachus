package downloaders

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/t0ggl/callimachus/pkg/converter"
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
	ConvertEbooks          bool
	EbookTargetFormat      string
	AllowedLanguages       []string
	AllowedFormats         []string
	PreferSingleM4B        bool
	MaxAudiobookSizeMB     int
	MaxEbookSizeBytes      int64 `json:"maxEbookSizeBytes"`
	RejectPatterns         []*regexp.Regexp
	EbookFormatPreferences []string
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
		AllowedLanguages:       DefaultAllowedLanguages,
		AllowedFormats:         DefaultAllowedFormats,
		PreferSingleM4B:        true,
		MaxEbookSizeBytes:      100 * 1024 * 1024,
		MaxAudiobookSizeMB:     1536,
		EbookFormatPreferences: []string{"epub", "azw3", "mobi", "pdf", "cbr", "cbz"},
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

// EvaluationIssue describes a single quality profile criterion that rejected a release.
type EvaluationIssue struct {
	Criterion string
	Reason    string
}

// DetectReleaseFormat returns the normalized, lowercased format for a release
// from its explicit Format field, parsed release metadata, or filename extension.
func DetectReleaseFormat(release ReleaseCandidate) string {
	format := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(release.Format), "."))
	if format == "" && release.Title != "" {
		parsed := metadata.ParseRelease(release.Title)
		if parsed.Format != "" {
			format = strings.ToLower(parsed.Format)
		} else if mExt := reFileExtension.FindStringSubmatch(release.Title); len(mExt) > 1 {
			format = strings.ToLower(mExt[1])
		}
	}
	return format
}

// AllowedEbookPreferences returns the user's preferred ebook formats in priority order,
// filtered to formats allowed by the profile.
func (p QualityProfile) AllowedEbookPreferences() []string {
	allowedSet := make(map[string]bool, len(p.AllowedFormats))
	for _, af := range p.AllowedFormats {
		if norm := NormalizeFormat(af); norm != "" {
			allowedSet[norm] = true
		}
	}
	seen := make(map[string]bool, len(p.EbookFormatPreferences))
	out := make([]string, 0, len(p.EbookFormatPreferences))
	for _, pref := range p.EbookFormatPreferences {
		norm := NormalizeFormat(pref)
		if norm == "" || seen[norm] {
			continue
		}
		if len(allowedSet) > 0 && !allowedSet[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	return out
}

// EvaluateReleaseIssues evaluates a candidate release against all quality profile
// criteria and returns every criterion that caused a rejection.
func (p QualityProfile) EvaluateReleaseIssues(release ReleaseCandidate) []EvaluationIssue {
	var issues []EvaluationIssue

	// 1. Check RejectPatterns
	for _, pattern := range p.RejectPatterns {
		if pattern != nil && (pattern.MatchString(release.Title) || pattern.MatchString(release.Format)) {
			issues = append(issues, EvaluationIssue{
				Criterion: "pattern",
				Reason:    fmt.Sprintf("title matches reject pattern %q", pattern.String()),
			})
			break
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
		if clean == "" {
			continue
		}
		allowedLangSet[clean] = true
		if code := metadata.NormalizeLanguage(clean); code != "" {
			allowedLangSet[code] = true
		} else if code, ok := languageCodeMap[clean]; ok {
			allowedLangSet[code] = true
		}
	}

	langReason := ""
	if release.Language != "" {
		clean := strings.ToLower(strings.TrimSpace(release.Language))
		detectedLang := clean
		if code := metadata.NormalizeLanguage(clean); code != "" {
			detectedLang = code
		} else if code, ok := languageCodeMap[clean]; ok {
			detectedLang = code
		}
		if !allowedLangSet[detectedLang] && !allowedLangSet[clean] {
			langReason = fmt.Sprintf("language %q not allowed by quality profile", release.Language)
		}
	}

	// Check language parsed or tagged in title
	if langReason == "" && release.Title != "" {
		parsed := metadata.ParseRelease(release.Title)
		if parsed.Language != "" && parsed.Language != "en" {
			pCode := parsed.Language
			if !allowedLangSet[pCode] {
				langReason = fmt.Sprintf("language %q not allowed by quality profile", pCode)
			}
		}

		if langReason == "" {
			matches := reTitleLanguageTags.FindAllStringSubmatch(release.Title, -1)
			for _, m := range matches {
				if len(m) > 1 {
					tag := strings.ToLower(m[1])
					code, ok := languageCodeMap[tag]
					if !ok {
						code = tag
					}
					if !allowedLangSet[code] && !allowedLangSet[tag] {
						langReason = fmt.Sprintf("language %q detected in title not allowed by quality profile", tag)
						break
					}
				}
			}
		}
	}
	if langReason != "" {
		issues = append(issues, EvaluationIssue{Criterion: "language", Reason: langReason})
	}

	// 3. Evaluate Format
	format := DetectReleaseFormat(release)
	formatReason := ""

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
			formatReason = fmt.Sprintf("format %q not allowed by quality profile", format)
		}
	}

	if formatReason == "" && len(p.AllowedFormats) > 0 && format != "m4b" && format != "mp3" {
		if format == "" {
			formatReason = "release format could not be determined"
		} else {
			allowed := false
			for _, af := range p.AllowedFormats {
				if strings.EqualFold(strings.TrimPrefix(af, "."), format) {
					allowed = true
					break
				}
			}
			if !allowed {
				formatReason = fmt.Sprintf("format %q not allowed by quality profile", format)
			}
		}
	}
	if formatReason != "" {
		issues = append(issues, EvaluationIssue{Criterion: "format", Reason: formatReason})
	}

	// Audio can be identified by format, an explicit flag, or a Torznab audio category.
	isAudio := release.IsAudio || converter.IsAudioFile("release."+format)
	for _, category := range release.Categories {
		if category >= CategoryAudioRoot && category < CategoryAudioRoot+1000 {
			isAudio = true
		}
	}
	if !isAudio && p.MaxEbookSizeBytes > 0 && release.Size > p.MaxEbookSizeBytes {
		issues = append(issues, EvaluationIssue{
			Criterion: "size",
			Reason:    fmt.Sprintf("eBook release size %d bytes exceeds maximum %d bytes", release.Size, p.MaxEbookSizeBytes),
		})
	}

	// 4. Evaluate audiobook size independently of the ebook limit
	if isAudio && p.MaxAudiobookSizeMB > 0 && release.Size > 0 {
		maxBytes := int64(p.MaxAudiobookSizeMB) * 1024 * 1024
		if release.Size > maxBytes {
			issues = append(issues, EvaluationIssue{
				Criterion: "size",
				Reason:    fmt.Sprintf("audiobook release size %d bytes exceeds maximum %d MB", release.Size, p.MaxAudiobookSizeMB),
			})
		}
	}

	// 5. Reject multi-book collections, box sets, omnibuses, and series ranges
	if release.Title != "" && metadata.IsMultiBookRelease(release.Title) {
		issues = append(issues, EvaluationIssue{
			Criterion: "multi-book",
			Reason:    "release appears to contain multiple books",
		})
	}

	return issues
}

// EvaluateRelease evaluates a candidate release against the quality profile.
// It returns (accepted bool, reason string).
func (p QualityProfile) EvaluateRelease(release ReleaseCandidate) (bool, string) {
	if issues := p.EvaluateReleaseIssues(release); len(issues) > 0 {
		return false, issues[0].Reason
	}
	return true, ""
}

// ScoreRelease ranks ebook preferences and always prefers M4B over MP3.
// Rejected releases score -1; MP3 and unranked formats score zero.
func (p QualityProfile) ScoreRelease(release ReleaseCandidate) int {
	if accepted, _ := p.EvaluateRelease(release); !accepted {
		return -1
	}
	format := NormalizeFormat(release.Format)
	if format == "" {
		format = NormalizeFormat(metadata.ParseRelease(release.Title).Format)
		if format == "" {
			format = SanitizeExt(release.Title)
		}
	}
	if format == "m4b" {
		return 1
	}
	if release.IsAudio || format == "mp3" {
		return 0
	}
	for i, preferred := range p.EbookFormatPreferences {
		if format == NormalizeFormat(preferred) {
			return len(p.EbookFormatPreferences) - i
		}
	}
	return 0
}

// RankReleases filters unacceptable releases and stably orders acquisition
// candidates by format preference, preserving discovery order for equal scores.
func (p QualityProfile) RankReleases(releases []ReleaseCandidate) []ReleaseCandidate {
	type scored struct {
		release ReleaseCandidate
		score   int
	}
	candidates := make([]scored, 0, len(releases))
	for _, release := range releases {
		if score := p.ScoreRelease(release); score >= 0 {
			candidates = append(candidates, scored{release, score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	ranked := make([]ReleaseCandidate, len(candidates))
	for i, candidate := range candidates {
		ranked[i] = candidate.release
	}
	return ranked
}

// EvaluateRelease package-level wrapper for profile.EvaluateRelease.
func EvaluateRelease(profile QualityProfile, release ReleaseCandidate) (bool, string) {
	return profile.EvaluateRelease(release)
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
