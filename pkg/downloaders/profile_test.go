package downloaders

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

func TestQualityProfile_EvaluateRelease_Languages(t *testing.T) {
	t.Parallel()

	defaultProfile := DefaultQualityProfile()

	tests := []struct {
		name         string
		profile      QualityProfile
		candidate    ReleaseCandidate
		wantAccepted bool
		reasonSubstr string
	}{
		{
			name:    "Standard English EPUB accepted",
			profile: defaultProfile,
			candidate: ReleaseCandidate{
				Title:    "Synthetic Author - Synthetic Title (2024)",
				Format:   "epub",
				Language: "en",
			},
			wantAccepted: true,
		},
		{
			name:    "Rejection of [FR] tagged title",
			profile: defaultProfile,
			candidate: ReleaseCandidate{
				Title:  "Synthetic Author - Synthetic Book [FR]",
				Format: "epub",
			},
			wantAccepted: false,
			reasonSubstr: "language",
		},
		{
			name:    "Rejection of GERMAN tagged title",
			profile: defaultProfile,
			candidate: ReleaseCandidate{
				Title:       "Synthetic Author - Synthetic Novel GERMAN",
				Format:      "m4b",
				BitrateKbps: 128,
			},
			wantAccepted: false,
			reasonSubstr: "language",
		},
		{
			name:    "Rejection of SPANISH tagged title",
			profile: defaultProfile,
			candidate: ReleaseCandidate{
				Title:  "Synthetic.Author.Synthetic.Tale.SPANISH.epub",
				Format: "epub",
			},
			wantAccepted: false,
			reasonSubstr: "language",
		},
		{
			name:    "Rejection of ITALIAN tagged title",
			profile: defaultProfile,
			candidate: ReleaseCandidate{
				Title:  "Synthetic Author - Synthetic Book (ITALIAN)",
				Format: "azw3",
			},
			wantAccepted: false,
			reasonSubstr: "language",
		},
		{
			name:    "Rejection of RUSSIAN tagged title",
			profile: defaultProfile,
			candidate: ReleaseCandidate{
				Title:  "Synthetic.Author.Synthetic.Story.RUSSIAN.pdf",
				Format: "pdf",
			},
			wantAccepted: false,
			reasonSubstr: "language",
		},
		{
			name:    "Rejection of explicit foreign Language field",
			profile: defaultProfile,
			candidate: ReleaseCandidate{
				Title:    "Synthetic Author - Synthetic Story",
				Format:   "epub",
				Language: "fr",
			},
			wantAccepted: false,
			reasonSubstr: "language",
		},
		{
			name: "Acceptance when foreign language is explicitly allowed",
			profile: QualityProfile{
				AllowedLanguages: []string{"fr", "french"},
				AllowedFormats:   []string{"epub", "m4b"},
			},
			candidate: ReleaseCandidate{
				Title:  "Synthetic Author - Synthetic Book [FR]",
				Format: "epub",
			},
			wantAccepted: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			accepted, reason := tc.profile.EvaluateRelease(tc.candidate)
			if accepted != tc.wantAccepted {
				t.Fatalf("EvaluateRelease() accepted = %v, want %v; reason: %q", accepted, tc.wantAccepted, reason)
			}
			if !tc.wantAccepted && tc.reasonSubstr != "" {
				if !strings.Contains(strings.ToLower(reason), strings.ToLower(tc.reasonSubstr)) {
					t.Errorf("reason %q does not contain expected substring %q", reason, tc.reasonSubstr)
				}
			}
		})
	}
}

func TestQualityProfile_EvaluateRelease_Formats(t *testing.T) {
	t.Parallel()

	profile := QualityProfile{
		AllowedLanguages: []string{"en"},
		AllowedFormats:   []string{"epub", "m4b", "azw3", "pdf"},
	}

	tests := []struct {
		name         string
		candidate    ReleaseCandidate
		wantAccepted bool
		reasonSubstr string
	}{
		{
			name: "Valid EPUB format accepted",
			candidate: ReleaseCandidate{
				Title:  "Synthetic Author - Synthetic Book One",
				Format: "epub",
			},
			wantAccepted: true,
		},
		{
			name: "Valid M4B audiobook format accepted",
			candidate: ReleaseCandidate{
				Title:       "Synthetic Author - Synthetic Book Two",
				Format:      "m4b",
				BitrateKbps: 96,
			},
			wantAccepted: true,
		},
		{
			name: "Format inferred from title extension",
			candidate: ReleaseCandidate{
				Title: "Synthetic.Author.Synthetic.Book.Three.azw3",
			},
			wantAccepted: true,
		},
		{
			name: "Unwanted .exe format rejected",
			candidate: ReleaseCandidate{
				Title:  "Synthetic Author - Synthetic Book.exe",
				Format: "exe",
			},
			wantAccepted: false,
			reasonSubstr: "format",
		},
		{
			name: "Unwanted .txt format rejected",
			candidate: ReleaseCandidate{
				Title:  "Synthetic Author - Synthetic Notes.txt",
				Format: "txt",
			},
			wantAccepted: false,
			reasonSubstr: "format",
		},
		{
			name: "Unknown / unsupported format rejected",
			candidate: ReleaseCandidate{
				Title:  "Synthetic Author - Synthetic Disk Image.iso",
				Format: "iso",
			},
			wantAccepted: false,
			reasonSubstr: "format",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			accepted, reason := profile.EvaluateRelease(tc.candidate)
			if accepted != tc.wantAccepted {
				t.Fatalf("EvaluateRelease() accepted = %v, want %v; reason: %q", accepted, tc.wantAccepted, reason)
			}
			if !tc.wantAccepted && tc.reasonSubstr != "" {
				if !strings.Contains(strings.ToLower(reason), strings.ToLower(tc.reasonSubstr)) {
					t.Errorf("reason %q does not contain expected substring %q", reason, tc.reasonSubstr)
				}
			}
		})
	}
}

func TestQualityProfile_EvaluateRelease_AudioBitrate(t *testing.T) {
	t.Parallel()

	profile := QualityProfile{
		AllowedLanguages: []string{"en"},
		AllowedFormats:   []string{"epub", "m4b", "mp3"},
	}

	tests := []struct {
		name         string
		candidate    ReleaseCandidate
		wantAccepted bool
		reasonSubstr string
	}{
		{
			name: "Low bitrate M4B accepted for passthrough",
			candidate: ReleaseCandidate{
				Title:       "Synthetic Author - Low Quality Audio",
				Format:      "m4b",
				BitrateKbps: 32,
			},
			wantAccepted: true,
		},
		{
			name: "Low bitrate MP3 accepted for conversion",
			candidate: ReleaseCandidate{
				Title:       "Synthetic Author - Subpar Audio",
				Format:      "mp3",
				BitrateKbps: 48,
			},
			wantAccepted: true,
		},
		{
			name: "Bitrate exactly at threshold accepted (64kbps == 64kbps)",
			candidate: ReleaseCandidate{
				Title:       "Synthetic Author - Threshold Audio",
				Format:      "m4b",
				BitrateKbps: 64,
			},
			wantAccepted: true,
		},
		{
			name: "High bitrate accepted (128kbps > 64kbps)",
			candidate: ReleaseCandidate{
				Title:       "Synthetic Author - Crisp Audio",
				Format:      "m4b",
				BitrateKbps: 128,
			},
			wantAccepted: true,
		},
		{
			name: "VBR bitrate (-1) accepted",
			candidate: ReleaseCandidate{
				Title:       "Synthetic Author - Variable Audio",
				Format:      "mp3",
				BitrateKbps: -1,
			},
			wantAccepted: true,
		},
		{
			name: "EBook release is unaffected by audio bitrate requirement",
			candidate: ReleaseCandidate{
				Title:       "Synthetic Author - Synthetic Novel",
				Format:      "epub",
				BitrateKbps: 0,
			},
			wantAccepted: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			accepted, reason := profile.EvaluateRelease(tc.candidate)
			if accepted != tc.wantAccepted {
				t.Fatalf("EvaluateRelease() accepted = %v, want %v; reason: %q", accepted, tc.wantAccepted, reason)
			}
			if !tc.wantAccepted && tc.reasonSubstr != "" {
				if !strings.Contains(strings.ToLower(reason), strings.ToLower(tc.reasonSubstr)) {
					t.Errorf("reason %q does not contain expected substring %q", reason, tc.reasonSubstr)
				}
			}
		})
	}
}

func TestQualityProfile_EvaluateRelease_RejectPatterns(t *testing.T) {
	t.Parallel()

	profile := QualityProfile{
		AllowedLanguages: []string{"en"},
		AllowedFormats:   []string{"epub", "m4b"},
		RejectPatterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\b(sample|preview)\b`),
			regexp.MustCompile(`(?i)\bpassword\b`),
		},
	}

	tests := []struct {
		name         string
		candidate    ReleaseCandidate
		wantAccepted bool
	}{
		{
			name: "Clean release passes",
			candidate: ReleaseCandidate{
				Title:  "Synthetic Author - Complete Book",
				Format: "epub",
			},
			wantAccepted: true,
		},
		{
			name: "Sample keyword matches reject pattern",
			candidate: ReleaseCandidate{
				Title:  "Synthetic Author - Book Sample",
				Format: "epub",
			},
			wantAccepted: false,
		},
		{
			name: "Password keyword matches reject pattern",
			candidate: ReleaseCandidate{
				Title:  "Synthetic Author - Book With Password",
				Format: "epub",
			},
			wantAccepted: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			accepted, _ := profile.EvaluateRelease(tc.candidate)
			if accepted != tc.wantAccepted {
				t.Fatalf("EvaluateRelease() accepted = %v, want %v", accepted, tc.wantAccepted)
			}
		})
	}
}

func TestPackageEvaluateRelease(t *testing.T) {
	t.Parallel()

	profile := DefaultQualityProfile()
	candidate := ReleaseCandidate{
		Title:  "Synthetic Novel Alpha",
		Format: "epub",
	}

	accepted, reason := EvaluateRelease(profile, candidate)
	if !accepted {
		t.Fatalf("expected accepted, got reason: %s", reason)
	}
}

func TestQualityProfile_EvaluateRelease_MaxAudiobookSizeMB(t *testing.T) {
	t.Parallel()

	profile := QualityProfile{
		AllowedLanguages:   []string{"en"},
		AllowedFormats:     []string{"epub", "m4b"},
		MaxAudiobookSizeMB: 500,
	}

	okCandidate := ReleaseCandidate{
		Title:  "Synthetic Novel Alpha",
		Format: "m4b",
		Size:   400 * 1024 * 1024,
	}
	if accepted, reason := profile.EvaluateRelease(okCandidate); !accepted {
		t.Fatalf("expected 400MB candidate accepted, got: %s", reason)
	}

	tooBigCandidate := ReleaseCandidate{
		Title:  "Synthetic Novel Beta",
		Format: "m4b",
		Size:   600 * 1024 * 1024,
	}
	if accepted, _ := profile.EvaluateRelease(tooBigCandidate); accepted {
		t.Fatalf("expected 600MB candidate rejected by 500MB limit")
	}
}

func TestQualityProfile_EbookSize(t *testing.T) {
	const mb = 1024 * 1024
	profile := DefaultQualityProfile()
	profile.AllowedFormats = nil
	for _, tc := range []struct {
		name      string
		candidate ReleaseCandidate
		accepted  bool
	}{
		{"below", ReleaseCandidate{Format: "epub", Size: 100*mb - 1}, true},
		{"boundary", ReleaseCandidate{Format: "pdf", Size: 100 * mb}, true},
		{"oversized", ReleaseCandidate{Format: ".EPUB", Size: 100*mb + 1}, false},
		{"inferred ebook", ReleaseCandidate{Title: "Book.azw3", Size: 101 * mb}, false},
		{"unknown format archive", ReleaseCandidate{Title: "Book.zip", Size: 101 * mb}, false},
		{"unknown size", ReleaseCandidate{Format: "epub"}, true},
		{"m4b", ReleaseCandidate{Format: "m4b", Size: 500 * mb}, true},
		{"wma", ReleaseCandidate{Format: "wma", Size: 500 * mb}, true},
		{"inferred mp3", ReleaseCandidate{Title: "Book.mp3", Size: 500 * mb}, true},
		{"audio flag", ReleaseCandidate{IsAudio: true, Size: 500 * mb}, true},
		{"audio category", ReleaseCandidate{Categories: []int{3030}, Size: 500 * mb}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accepted, reason, err := ShouldDownload(context.Background(), profile, nil, tc.candidate)
			if err != nil || accepted != tc.accepted {
				t.Fatalf("accepted=%v reason=%s err=%v", accepted, reason, err)
			}
			if ranked := profile.RankReleases([]ReleaseCandidate{tc.candidate}); (len(ranked) == 1) != tc.accepted {
				t.Fatalf("ranking did not enforce size filter: %+v", ranked)
			}
		})
	}
	profile.MaxEbookSizeBytes = 200 * mb
	if ok, _ := profile.EvaluateRelease(ReleaseCandidate{Format: "epub", Size: 150 * mb}); !ok {
		t.Fatal("custom limit ignored")
	}
	profile.MaxEbookSizeBytes = 0
	if ok, _ := profile.EvaluateRelease(ReleaseCandidate{Format: "epub", Size: 500 * mb}); !ok {
		t.Fatal("zero should disable ebook limit")
	}
	profile.MaxAudiobookSizeMB = 300
	if ok, _ := profile.EvaluateRelease(ReleaseCandidate{Format: "m4b", Size: 500 * mb}); ok {
		t.Fatal("audio must use audiobook size limit")
	}
}

func TestQualityProfile_DefaultAudiobookSize(t *testing.T) {
	profile := DefaultQualityProfile()
	const limit = int64(1536 * 1024 * 1024)
	for _, size := range []int64{limit - 1, limit, limit + 1} {
		accepted, reason := profile.EvaluateRelease(ReleaseCandidate{Format: "m4b", Size: size})
		if accepted != (size <= limit) {
			t.Fatalf("size %d: accepted=%v, reason=%s", size, accepted, reason)
		}
	}
}

func TestEbookFormatPreferenceScoring(t *testing.T) {
	profile := DefaultQualityProfile()
	profile.AllowedFormats = []string{"epub", "pdf", "mobi", "m4b"}
	profile.EbookFormatPreferences = []string{"PDF", "EPUB", "MOBI"}
	releases := []ReleaseCandidate{
		{Title: "Book.mobi"},
		{Format: ".EPUB"},
		{Format: "pdf"},
		{Format: "m4b", IsAudio: true},
		{Format: "pdf", Language: "de"},
		{Format: "exe"},
	}
	ranked := profile.RankReleases(releases)
	if len(ranked) != 4 || ranked[0].Format != "pdf" || ranked[1].Format != ".EPUB" || ranked[2].Title != "Book.mobi" || !ranked[3].IsAudio {
		t.Fatalf("unexpected ranking: %+v", ranked)
	}
	if profile.ScoreRelease(releases[2]) <= profile.ScoreRelease(releases[1]) || profile.ScoreRelease(releases[1]) <= profile.ScoreRelease(releases[0]) {
		t.Fatal("higher-ranked ebook formats must have strictly higher scores")
	}
	profile.EbookFormatPreferences = []string{"EPUB", "PDF", "MOBI"}
	if got := profile.RankReleases(releases); got[0].Format != ".EPUB" {
		t.Fatalf("reordering did not change precedence: %+v", got)
	}
	profile.AllowedFormats = []string{"m4b"}
	if profile.ScoreRelease(releases[1]) != -1 {
		t.Fatal("preference must not bypass allowed formats")
	}
}

func TestEbookRankingStableTiesAndEmptyPreferences(t *testing.T) {
	profile := DefaultQualityProfile()
	profile.EbookFormatPreferences = nil
	releases := []ReleaseCandidate{{Title: "First", Format: "epub"}, {Title: "Second", Format: "pdf"}}
	for _, release := range releases {
		if got := profile.ScoreRelease(release); got != 0 {
			t.Fatalf("empty preferences scored %d", got)
		}
	}
	ranked := profile.RankReleases(releases)
	if len(ranked) != 2 || ranked[0].Title != "First" || ranked[1].Title != "Second" {
		t.Fatalf("ties reordered: %+v", ranked)
	}
}

func TestAudiobookFormatsAlwaysAcceptedAndM4BPreferred(t *testing.T) {
	profile := DefaultQualityProfile()
	profile.AllowedFormats = []string{"epub"}
	profile.PreferSingleM4B = false
	releases := []ReleaseCandidate{{Title: "Book.mp3", IsAudio: true}, {Format: "M4B", IsAudio: true}}
	ranked := profile.RankReleases(releases)
	if len(ranked) != 2 || ranked[0].Format != "M4B" {
		t.Fatalf("audio must remain enabled and prefer M4B: %+v", ranked)
	}
}

func TestQualityProfile_IndependentMediaSizeLimits(t *testing.T) {
	const mb = 1024 * 1024
	for _, limits := range []struct {
		name             string
		audioMB, ebookMB int
	}{
		{"audio smaller", 100, 300},
		{"ebook smaller", 300, 100},
		{"audio disabled", 0, 100},
		{"ebook disabled", 100, 0},
	} {
		t.Run(limits.name, func(t *testing.T) {
			profile := DefaultQualityProfile()
			profile.AllowedFormats = nil
			profile.MaxAudiobookSizeMB = limits.audioMB
			profile.MaxEbookSizeBytes = int64(limits.ebookMB) * mb
			for _, candidate := range []ReleaseCandidate{
				{Format: "epub"}, {Format: "pdf"}, {Title: "Book.azw3"},
				{Format: "m4b"}, {Title: "Book.mp3"}, {IsAudio: true},
				{Categories: []int{3030}},
			} {
				isAudio := candidate.Format == "m4b" || candidate.Title == "Book.mp3" || candidate.IsAudio || len(candidate.Categories) > 0
				limit := limits.ebookMB
				if isAudio {
					limit = limits.audioMB
				}
				for _, size := range []int64{0, 100 * mb, 100*mb + 1, 200 * mb, 300 * mb, 300*mb + 1} {
					candidate.Size = size
					want := limit == 0 || size <= int64(limit)*mb
					accepted, reason, err := ShouldDownload(context.Background(), profile, nil, candidate)
					if err != nil || accepted != want {
						t.Fatalf("candidate=%+v limit=%d: accepted=%v reason=%s err=%v", candidate, limit, accepted, reason, err)
					}
					if ranked := profile.RankReleases([]ReleaseCandidate{candidate}); (len(ranked) == 1) != want {
						t.Fatalf("ranking ignored media size limit: %+v", candidate)
					}
				}
			}
		})
	}
}

func TestQualityProfile_EvaluateReleaseIssues_ReportsAllCriteria(t *testing.T) {
	t.Parallel()
	profile := DefaultQualityProfile()
	profile.AllowedFormats = []string{"epub", "m4b"}
	profile.EbookFormatPreferences = []string{"EPUB", "AZW3", "MOBI", "PDF"}
	profile.MaxEbookSizeBytes = 10 * 1024 * 1024
	profile.RejectPatterns = []*regexp.Regexp{regexp.MustCompile(`(?i)\bsample\b`)}

	issues := profile.EvaluateReleaseIssues(ReleaseCandidate{
		Title: "Philip.Pullman-His.Dark.Materials.01-03.FRENCH.sample.azw3",
		Size:  20 * 1024 * 1024,
	})
	got := map[string]bool{}
	for _, issue := range issues {
		got[issue.Criterion] = true
	}
	for _, want := range []string{"pattern", "language", "format", "size", "multi-book"} {
		if !got[want] {
			t.Errorf("missing expected criterion %q in issues: %+v", want, issues)
		}
	}

	prefs := profile.AllowedEbookPreferences()
	if len(prefs) != 1 || prefs[0] != "epub" {
		t.Fatalf("AllowedEbookPreferences() = %v, want [epub]", prefs)
	}
}
