package metadata

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestSanitizeTitle(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Clean title untouched",
			input:    "City of Iron Spirits",
			expected: "City of Iron Spirits",
		},
		{
			name:     "Dots replaced with spaces",
			input:    "City.of.Iron.Spirits",
			expected: "City of Iron Spirits",
		},
		{
			name:     "Strips year in parens",
			input:    "Bastion of Hope (2018)",
			expected: "Bastion of Hope",
		},
		{
			name:     "Strips scene packaging tags",
			input:    "The Obsidian Citadel RETAIL REPACK v2.0",
			expected: "The Obsidian Citadel",
		},
		{
			name:     "Strips bitrate and audio tags",
			input:    "Tides of Oblivion MP3 320kbps Audiobook",
			expected: "Tides of Oblivion",
		},
		{
			name:     "Strips narrator credit with dot notation",
			input:    "City.of.Iron.Spirits.Read.by.Kate.Reading",
			expected: "City of Iron Spirits",
		},
		{
			name:     "Strips narrator credit with spaces",
			input:    "City of Iron Spirits Read by Simon Vance",
			expected: "City of Iron Spirits",
		},
		{
			name:     "Strips format bundle in brackets",
			input:    "The Silent Orbit eBook Bundle [EPUB-MOBI-AZW3-PDF]",
			expected: "The Silent Orbit",
		},
		{
			name:     "Preserves valid title punctuation like apostrophes and colons",
			input:    "The Alchemist's Secret: Special Edition",
			expected: "The Alchemist's Secret: Special Edition",
		},
		{
			name:     "Preserves foreign characters",
			input:    "L'Étranger: Édition Définitive",
			expected: "L'Étranger: Édition Définitive",
		},
		{
			name:     "Preserves balanced parenthesized series tag",
			input:    "A Drop of Corruption (Shadow of the Leviathan, Book 2)",
			expected: "A Drop of Corruption (Shadow of the Leviathan, Book 2)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeTitle(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeTitle(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeAuthor(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Standard First Last",
			input:    "Alina Starkova",
			expected: "Alina Starkova",
		},
		{
			name:     "Last, First inverted",
			input:    "Starkova, Alina",
			expected: "Alina Starkova",
		},
		{
			name:     "Dot-separated First.Last",
			input:    "Alina.Starkova",
			expected: "Alina Starkova",
		},
		{
			name:     "Initials preserved",
			input:    "J. R. R. Starkova",
			expected: "J. R. R. Starkova",
		},
		{
			name:     "Inverted with initials",
			input:    "Starkova, J. R. R.",
			expected: "J. R. R. Starkova",
		},
		{
			name:     "Co-authors with ampersand",
			input:    "Starkova, Alina & Vance, Aria",
			expected: "Alina Starkova & Aria Vance",
		},
		{
			name:     "Co-authors with 'and'",
			input:    "Alina Starkova and Aria Vance",
			expected: "Alina Starkova & Aria Vance",
		},
		{
			name:     "Co-authors with 'with'",
			input:    "Alina Starkova with Aria Vance",
			expected: "Alina Starkova & Aria Vance",
		},
		{
			name:     "Co-authors with slash",
			input:    "Starkova, Alina / Vance, Aria",
			expected: "Alina Starkova & Aria Vance",
		},
		{
			name:     "Co-authors separated by comma",
			input:    "Alina Starkova, Aria Vance",
			expected: "Alina Starkova & Aria Vance",
		},
		{
			name:     "Foreign author characters",
			input:    "Lindqvist, Søren",
			expected: "Søren Lindqvist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeAuthor(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeAuthor(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractSeries(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedSer   string
		expectedIndex *float64
	}{
		{
			name:          "Book with integer",
			input:         "Book 2",
			expectedSer:   "",
			expectedIndex: floatPtr(2.0),
		},
		{
			name:          "Book with float decimal",
			input:         "Book 2.5",
			expectedSer:   "",
			expectedIndex: floatPtr(2.5),
		},
		{
			name:          "Hash with leading zero",
			input:         "#01",
			expectedSer:   "",
			expectedIndex: floatPtr(1.0),
		},
		{
			name:          "Volume abbreviation",
			input:         "Vol. 3",
			expectedSer:   "",
			expectedIndex: floatPtr(3.0),
		},
		{
			name:          "Full word Volume",
			input:         "Volume 04",
			expectedSer:   "",
			expectedIndex: floatPtr(4.0),
		},
		{
			name:          "Series name with Book float",
			input:         "Synthetic Series, Book 2.5",
			expectedSer:   "Synthetic Series",
			expectedIndex: floatPtr(2.5),
		},
		{
			name:          "Dot-separated Series and Book",
			input:         "Synthetic.Series.Book.05",
			expectedSer:   "Synthetic Series",
			expectedIndex: floatPtr(5.0),
		},
		{
			name:          "Series name with hash number",
			input:         "The Synthetic Chronicles #03",
			expectedSer:   "The Synthetic Chronicles",
			expectedIndex: floatPtr(3.0),
		},
		{
			name:          "Parenthesized series inside title",
			input:         "Bastion of Hope (Synthetic Series #5)",
			expectedSer:   "Synthetic Series",
			expectedIndex: floatPtr(5.0),
		},
		{
			name:          "Bracketed series inside title",
			input:         "Bastion of Hope [Synthetic Series Book 1.5]",
			expectedSer:   "Synthetic Series",
			expectedIndex: floatPtr(1.5),
		},
		{
			name:          "Standalone title with no series",
			input:         "Bastion of Hope",
			expectedSer:   "",
			expectedIndex: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ser, idx := ExtractSeries(tt.input)
			if ser != tt.expectedSer {
				t.Errorf("ExtractSeries(%q) series = %q; want %q", tt.input, ser, tt.expectedSer)
			}
			if (idx == nil) != (tt.expectedIndex == nil) {
				t.Fatalf("ExtractSeries(%q) index nil mismatch: got %v, want %v", tt.input, idx, tt.expectedIndex)
			}
			if idx != nil && *idx != *tt.expectedIndex {
				t.Errorf("ExtractSeries(%q) index = %f; want %f", tt.input, *idx, *tt.expectedIndex)
			}
		})
	}
}

func TestStripIllegalFileChars(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Replaces colons with spaced dashes",
			input:    "Synthetic Title: Awakening",
			expected: "Synthetic Title - Awakening",
		},
		{
			name:     "Replaces slashes with dashes",
			input:    "AC/DC Volume\\One",
			expected: "AC-DC Volume-One",
		},
		{
			name:     "Removes wildcards, quotes, angles and replaces pipe with dash",
			input:    `"What?" *Dangerous* <Book> | Edition`,
			expected: "What Dangerous Book - Edition",
		},
		{
			name:     "Cleans multiple spaces and dashes",
			input:    "Title  --   Subtitle",
			expected: "Title - Subtitle",
		},
		{
			name:     "Trims leading/trailing periods and spaces",
			input:    "  .Dangerous File Name.  ",
			expected: "Dangerous File Name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripIllegalFileChars(tt.input)
			if got != tt.expected {
				t.Errorf("StripIllegalFileChars(%q) = %q; want %q", tt.input, got, tt.expected)
			}
			// Verify SanitizeFilename behaves identically
			if fnGot := SanitizeFilename(tt.input); fnGot != got {
				t.Errorf("SanitizeFilename(%q) = %q != StripIllegalFileChars %q", tt.input, fnGot, got)
			}
		})
	}
}

func TestParseReleaseEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantAuthor  string
		wantTitle   string
		wantSeries  string
		wantIndex   *float64
		wantYear    int
		wantFormat  string
		wantBitrate int
		wantLang    string
		wantGroup   string
		wantUnabr   bool
	}{
		{
			name:        "Missing author (standalone title with year and tags)",
			raw:         "Synthetic Book Alpha (2021) [EPUB] [GRP]",
			wantAuthor:  "",
			wantTitle:   "Synthetic Book Alpha",
			wantYear:    2021,
			wantFormat:  "epub",
			wantGroup:   "GRP",
			wantLang:    "en",
			wantBitrate: 0,
		},
		{
			name:        "Multiple authors with ampersand and audio format",
			raw:         "Author One & Author Two - Great Journey (2020) [M4B]",
			wantAuthor:  "Author One & Author Two",
			wantTitle:   "Great Journey",
			wantYear:    2020,
			wantFormat:  "m4b",
			wantLang:    "en",
			wantBitrate: 0,
		},
		{
			name:        "Multiple authors in Last, First inverted format",
			raw:         "Alpha, Author & Beta, Second - Great Journey (2020) [M4B]",
			wantAuthor:  "Author Alpha & Second Beta",
			wantTitle:   "Great Journey",
			wantYear:    2020,
			wantFormat:  "m4b",
			wantLang:    "en",
			wantBitrate: 0,
		},
		{
			name:        "Complex series numbering with float 2.5",
			raw:         "Synthetic Author - Synthetic Series Book 2.5 - Novella Title (2022) [EPUB]",
			wantAuthor:  "Synthetic Author",
			wantTitle:   "Novella Title",
			wantSeries:  "Synthetic Series",
			wantIndex:   floatPtr(2.5),
			wantYear:    2022,
			wantFormat:  "epub",
			wantLang:    "en",
		},
		{
			name:        "Complex series numbering with float 1.5 and hash",
			raw:         "Synthetic Author - Synthetic Series #01 - Novel Title (2020) [EPUB]",
			wantAuthor:  "Synthetic Author",
			wantTitle:   "Novel Title",
			wantSeries:  "Synthetic Series",
			wantIndex:   floatPtr(1.0),
			wantYear:    2020,
			wantFormat:  "epub",
			wantLang:    "en",
		},
		{
			name:        "Foreign author characters and title accents",
			raw:         "Søren Kierkegaard - Enten-Eller (2020) [EPUB]",
			wantAuthor:  "Søren Kierkegaard",
			wantTitle:   "Enten-Eller",
			wantYear:    2020,
			wantFormat:  "epub",
			wantLang:    "en",
		},
		{
			name:        "Language detection from German tag",
			raw:         "[German] Alina Starkova - City of Iron Spirits (2022) [EPUB] [TWB]",
			wantAuthor:  "Alina Starkova",
			wantTitle:   "City of Iron Spirits",
			wantYear:    2022,
			wantFormat:  "epub",
			wantLang:    "de",
			wantGroup:   "TWB",
		},
		{
			name:        "Language detection from French tag in parens",
			raw:         "Alina Starkova - City of Iron Spirits (FR) (2022) [EPUB] -KOPiE",
			wantAuthor:  "Alina Starkova",
			wantTitle:   "City of Iron Spirits",
			wantYear:    2022,
			wantFormat:  "epub",
			wantLang:    "fr",
			wantGroup:   "KOPiE",
		},
		{
			name:        "Leading bracket group with unabridged VBR audio",
			raw:         "[AUDIO] Caelum Voss - Sovereign Stars (2021) [Audiobook MP3 VBR] Unabridged",
			wantAuthor:  "Caelum Voss",
			wantTitle:   "Sovereign Stars",
			wantYear:    2021,
			wantFormat:  "mp3",
			wantBitrate: -1,
			wantGroup:   "AUDIO",
			wantUnabr:   true,
			wantLang:    "en",
		},
		{
			name:        "Hyphenated scene release with Book 05 series",
			raw:         "Alina.Starkova-Synthetic.Series.Book.05-Bastion.of.Hope-(2018)-PDF-SWAG",
			wantAuthor:  "Alina Starkova",
			wantTitle:   "Bastion of Hope",
			wantSeries:  "Synthetic Series",
			wantIndex:   floatPtr(5.0),
			wantYear:    2018,
			wantFormat:  "pdf",
			wantGroup:   "SWAG",
			wantLang:    "en",
		},
		{
			name:        "Dot separated scene release with Vol.5 in title",
			raw:         "Alina.Starkova.Bastion.of.Hope.Vol.5.(2018).PDF-SWAG",
			wantAuthor:  "Alina Starkova",
			wantTitle:   "Bastion of Hope",
			wantIndex:   floatPtr(5.0),
			wantYear:    2018,
			wantFormat:  "pdf",
			wantGroup:   "SWAG",
			wantLang:    "en",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRelease(tt.raw)
			if got.RawTitle != tt.raw {
				t.Errorf("RawTitle = %q; want %q", got.RawTitle, tt.raw)
			}
			if got.Author != tt.wantAuthor {
				t.Errorf("Author = %q; want %q", got.Author, tt.wantAuthor)
			}
			if got.Title != tt.wantTitle {
				t.Errorf("Title = %q; want %q", got.Title, tt.wantTitle)
			}
			if tt.wantSeries != "" && got.Series != tt.wantSeries {
				t.Errorf("Series = %q; want %q", got.Series, tt.wantSeries)
			}
			if (got.SeriesIndex == nil) != (tt.wantIndex == nil) {
				t.Fatalf("SeriesIndex nil mismatch: got %v, want %v", got.SeriesIndex, tt.wantIndex)
			}
			if got.SeriesIndex != nil && *got.SeriesIndex != *tt.wantIndex {
				t.Errorf("SeriesIndex = %f; want %f", *got.SeriesIndex, *tt.wantIndex)
			}
			if got.Year != tt.wantYear {
				t.Errorf("Year = %d; want %d", got.Year, tt.wantYear)
			}
			if got.Format != tt.wantFormat {
				t.Errorf("Format = %q; want %q", got.Format, tt.wantFormat)
			}
			if got.BitrateKbps != tt.wantBitrate {
				t.Errorf("BitrateKbps = %d; want %d", got.BitrateKbps, tt.wantBitrate)
			}
			if got.Language != tt.wantLang {
				t.Errorf("Language = %q; want %q", got.Language, tt.wantLang)
			}
			if tt.wantGroup != "" && got.Group != tt.wantGroup {
				t.Errorf("Group = %q; want %q", got.Group, tt.wantGroup)
			}
			if got.IsUnabridged != tt.wantUnabr {
				t.Errorf("IsUnabridged = %v; want %v", got.IsUnabridged, tt.wantUnabr)
			}
		})
	}
}

func TestParseRelease900Releases(t *testing.T) {
	root := testutil.RepoRoot()
	fixturePath := filepath.Join(root, "testdata/release_names.txt")
	file, err := os.Open(fixturePath)
	if err != nil {
		t.Fatalf("Failed to open %s: %v", fixturePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	panicked := false

	defer func() {
		if r := recover(); r != nil {
			panicked = true
			t.Fatalf("Parser panicked on release line %d: %v", count, r)
		}
	}()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		count++

		p := ParseRelease(line)

		// Verify that none panic, and essential fields are extracted
		if p.RawTitle != line {
			t.Errorf("Line %d: RawTitle = %q; want %q", count, p.RawTitle, line)
		}
		if p.Title == "" {
			t.Errorf("Line %d: Extracted empty Title for %q", count, line)
		}
		if p.Author == "" {
			t.Errorf("Line %d: Extracted empty Author for %q", count, line)
		}
		if p.Year <= 0 {
			t.Errorf("Line %d: Year was not extracted (%d) for %q", count, p.Year, line)
		}
		if p.Format == "" {
			t.Errorf("Line %d: Format was not extracted for %q", count, line)
		}
		if p.Group == "" {
			t.Errorf("Line %d: Group was not extracted for %q", count, line)
		}
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("Scanner error: %v", err)
	}

	if count != 900 {
		t.Errorf("Expected 900 release names tested, but found %d", count)
	}
	if panicked {
		t.Errorf("Parser panicked during test")
	}
	t.Logf("Successfully parsed all %d release strings without panic or missing critical fields", count)
}

func BenchmarkParseRelease(b *testing.B) {
	root := testutil.RepoRoot()
	fixturePath := filepath.Join(root, "testdata/release_names.txt")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		b.Fatalf("Failed to read fixture: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	numLines := len(lines)
	if numLines == 0 {
		b.Fatalf("No lines found in fixture")
	}

	b.ResetTimer()
	start := time.Now()

	for i := 0; i < b.N; i++ {
		line := lines[i%numLines]
		_ = ParseRelease(line)
	}

	elapsed := time.Since(start)
	avgPerOp := elapsed / time.Duration(b.N)

	// Threshold: 100 microseconds per operation
	threshold := 100 * time.Microsecond
	if avgPerOp > threshold {
		b.Errorf("Average parse time %v exceeded 100 microseconds threshold", avgPerOp)
	}
}

func floatPtr(f float64) *float64 {
	return &f
}

func TestParseRelease_EbookFormats(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"AZW3", "MOBI", "CBR", "CBZ"} {
		for _, token := range []string{format, strings.ToLower(format)} {
			parsed := ParseRelease("Synthetic Author - Synthetic Book (2024) " + token)
			if parsed.Format != strings.ToLower(format) || parsed.Title != "Synthetic Book" || parsed.Group != "" {
				t.Errorf("format %s: unexpected release: %+v", token, parsed)
			}
		}
	}
}

func TestDiscoveryKeyAuthorPrefix(t *testing.T) {
	for _, tc := range []struct {
		title, author, want string
	}{
		{"Scott Reintgens Bloodsworn", "Scott Reintgens", "Bloodsworn"},
		{"SCOTT  REINTGENS - Bloodsworn", "Scott Reintgens", "Bloodsworn"},
		{"Reintgens, Scott — Bloodsworn", "Scott Reintgens", "Bloodsworn"},
		{"Scott Reintgens: Bloodsworn", "Reintgens, Scott", "Bloodsworn"},
		{"Scott Reintgens Bloodsworn", "Other Writer & Scott Reintgens", "Bloodsworn"},
		{"Scott Reintgens Bloodsworn", "Scott Smith", "Scott Reintgens Bloodsworn"},
		{"Scott Reintgens Bloodsworn", "", "Scott Reintgens Bloodsworn"},
		{"Scott Reintgensson Bloodsworn", "Scott Reintgens", "Scott Reintgensson Bloodsworn"},
		{"Scott Reintgens's Bloodsworn", "Scott Reintgens", "Scott Reintgens's Bloodsworn"},
		{"Scott Reintgens", "Scott Reintgens", "Scott Reintgens"},
		{"Scott Reintgens -", "Scott Reintgens", "Scott Reintgens"},
		{"Scott Reintgens (2020)", "Scott Reintgens", "Scott Reintgens"},
		{"Scott Reintgens Bloodsworn", "Scott", "Scott Reintgens Bloodsworn"},
		{"Homer - Odyssey", "Homer", "Odyssey"},
	} {
		t.Run(tc.title+" by "+tc.author, func(t *testing.T) {
			want := strings.ToLower(tc.want) + "\x00" + strings.ToLower(NormalizeAuthor(tc.author))
			if got := DiscoveryKey(tc.title, tc.author); got != want {
				t.Errorf("DiscoveryKey = %q, want %q", got, want)
			}
		})
	}
}

func TestExtractSeriesFromTitle(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSeries string
		wantIndex  *float64
	}{
		{
			name:      "Volume abbreviation in title (vol 1)",
			input:     "Lord of Mysteries vol 1",
			wantIndex: floatPtr(1),
		},
		{
			name:      "Volume with dot and capital (Vol. 2)",
			input:     "Lord of Mysteries Vol. 2",
			wantIndex: floatPtr(2),
		},
		{
			name:      "Full word Volume in title",
			input:     "Lord of Mysteries Volume 3",
			wantIndex: floatPtr(3),
		},
		{
			name:      "Book N in title",
			input:     "Chronicle Book 4",
			wantIndex: floatPtr(4),
		},
		{
			name:      "Bk abbreviation in title",
			input:     "Chronicle Bk. 2.5",
			wantIndex: floatPtr(2.5),
		},
		{
			name:       "Parenthesized series and book",
			input:      "The Tainted Cup (Shadow of the Leviathan, Book 1)",
			wantSeries: "Shadow of the Leviathan",
			wantIndex:  floatPtr(1),
		},
		{
			name:       "Bracketed series and hash",
			input:      "A Drop of Corruption [Shadow of the Leviathan #2]",
			wantSeries: "Shadow of the Leviathan",
			wantIndex:  floatPtr(2),
		},
		{
			name:      "Parenthesized index only",
			input:     "Novella (#2.5)",
			wantIndex: floatPtr(2.5),
		},
		{
			name:      "Numeric title Fahrenheit 451 must not false match",
			input:     "Fahrenheit 451",
			wantIndex: nil,
		},
		{
			name:      "Numeric title Catch-22 must not false match",
			input:     "Catch-22",
			wantIndex: nil,
		},
		{
			name:      "Word ending in no must not false match",
			input:     "Piano 1",
			wantIndex: nil,
		},
		{
			name:      "Word ending in book must not false match",
			input:     "Yearbook 2024",
			wantIndex: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ser, idx := ExtractSeriesFromTitle(tc.input)
			if ser != tc.wantSeries {
				t.Errorf("ExtractSeriesFromTitle(%q) series = %q; want %q", tc.input, ser, tc.wantSeries)
			}
			if (idx == nil) != (tc.wantIndex == nil) {
				t.Fatalf("ExtractSeriesFromTitle(%q) index nil mismatch: got %v, want %v", tc.input, idx, tc.wantIndex)
			}
			if idx != nil && *idx != *tc.wantIndex {
				t.Errorf("ExtractSeriesFromTitle(%q) index = %v; want %v", tc.input, *idx, *tc.wantIndex)
			}
		})
	}
}

func TestNormalizeTitleKey(t *testing.T) {
	equalPairs := [][2]string{
		{"Honoured Enemy", "Honored Enemy"},
		{"Murder In LaMut", "Murder in LaMut"},
		{"The Colour of Magic", "The Color of Magic"},
		{"Grey Sister", "Gray Sister"},
		{"Centre of the Storm", "Center of the Storm"},
		{"Armour of Light", "Armor of Light"},
		{"Dishonoured Crown", "Dishonored Crown"},
	}
	for _, pair := range equalPairs {
		a := NormalizeTitleKey(pair[0])
		b := NormalizeTitleKey(pair[1])
		if a == "" || a != b {
			t.Errorf("NormalizeTitleKey(%q)=%q != NormalizeTitleKey(%q)=%q", pair[0], a, pair[1], b)
		}
	}

	distinctPairs := [][2]string{
		{"Four Kingdoms", "For Kingdoms"},
		{"The Dark Hour", "The Dark Hor"},
		{"Grand Tour", "Grand Tor"},
		{"Your Shadow", "Yor Shadow"},
	}
	for _, pair := range distinctPairs {
		a := NormalizeTitleKey(pair[0])
		b := NormalizeTitleKey(pair[1])
		if a == b {
			t.Errorf("NormalizeTitleKey false collision: %q and %q both normalized to %q", pair[0], pair[1], a)
		}
	}
}

func TestCleanDescription(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain text preserved",
			in:   "  A sweeping desert epic.  ",
			want: "A sweeping desert epic.",
		},
		{
			name: "html paragraphs and breaks cleaned",
			in:   "<p>First paragraph with <i>italics</i> &amp; entities.</p><p>Second line.<br/>Third line.</p>",
			want: "First paragraph with italics & entities.\nSecond line.\nThird line.",
		},
		{
			name: "excessive blank lines collapsed",
			in:   "Para one.\r\n\r\n\r\n\r\nPara two.\n\n",
			want: "Para one.\n\nPara two.",
		},
		{
			name: "whitespace only returns empty",
			in:   "   \n\t  ",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanDescription(tc.in); got != tc.want {
				t.Errorf("CleanDescription(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

