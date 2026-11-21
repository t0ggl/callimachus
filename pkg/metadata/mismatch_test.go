package metadata

import (
	"math"
	"testing"
)

func TestCompareEPUBMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		current        EPUBMetadataSnapshot
		ideal          IdealMetadata
		wantFields     []string
		wantFirstCur   string
		wantFirstIdeal string
	}{
		{
			name:    "exact match",
			current: EPUBMetadataSnapshot{Title: "The Way of Kings", Series: "The Stormlight Archive", SeriesIndex: floatPtr(1)},
			ideal:   IdealMetadata{Title: "The Way of Kings", Series: "The Stormlight Archive", SeriesIndex: floatPtr(1)},
		},
		{
			name:    "case whitespace punctuation smart quotes and ampersand ignored",
			current: EPUBMetadataSnapshot{Title: "  ender\u2019s  game & shadow! ", Series: "The Ender’s  Saga", SeriesIndex: floatPtr(1.0)},
			ideal:   IdealMetadata{Title: "Ender's Game and Shadow", Series: "Ender's Saga", SeriesIndex: floatPtr(1)},
		},
		{
			name:    "leading and trailing articles normalized",
			current: EPUBMetadataSnapshot{Title: "Hobbit, The", Series: "Middle-Earth", SeriesIndex: floatPtr(0.5)},
			ideal:   IdealMetadata{Title: "The Hobbit", Series: "The Middle Earth", SeriesIndex: floatPtr(0.5)},
		},
		{
			name:    "US and UK spelling variants normalized",
			current: EPUBMetadataSnapshot{Title: "The Colour of Magic", Series: "Discworld", SeriesIndex: floatPtr(1)},
			ideal:   IdealMetadata{Title: "The Color of Magic", Series: "Discworld", SeriesIndex: floatPtr(1)},
		},
		{
			name:    "single-sided subtitle noise ignored",
			current: EPUBMetadataSnapshot{Title: "The Way of Kings: Book One of the Stormlight Archive"},
			ideal:   IdealMetadata{Title: "The Way of Kings"},
		},
		{
			name:    "separate EPUB subtitle matches combined ideal title",
			current: EPUBMetadataSnapshot{Title: "The Way of Kings", Subtitle: "Book One"},
			ideal:   IdealMetadata{Title: "The Way of Kings: Book One"},
		},
		{
			name:           "different subtitles on both sides is significant mismatch",
			current:        EPUBMetadataSnapshot{Title: "Star Wars: Heir to the Empire"},
			ideal:          IdealMetadata{Title: "Star Wars: Dark Force Rising"},
			wantFields:     []string{"title"},
			wantFirstCur:   "Star Wars: Heir to the Empire",
			wantFirstIdeal: "Star Wars: Dark Force Rising",
		},
		{
			name:           "scene noise in EPUB title is significant mismatch",
			current:        EPUBMetadataSnapshot{Title: "01 - The Final Empire [Retail EPUB]"},
			ideal:          IdealMetadata{Title: "The Final Empire"},
			wantFields:     []string{"title"},
			wantFirstCur:   "01 - The Final Empire [Retail EPUB]",
			wantFirstIdeal: "The Final Empire",
		},
		{
			name:           "completely different title is flagged",
			current:        EPUBMetadataSnapshot{Title: "Dune Messiah"},
			ideal:          IdealMetadata{Title: "Dune"},
			wantFields:     []string{"title"},
			wantFirstCur:   "Dune Messiah",
			wantFirstIdeal: "Dune",
		},
		{
			name:           "missing EPUB title when ideal title exists is flagged",
			current:        EPUBMetadataSnapshot{Title: ""},
			ideal:          IdealMetadata{Title: "Dune"},
			wantFields:     []string{"title"},
			wantFirstCur:   "",
			wantFirstIdeal: "Dune",
		},
		{
			name:    "missing ideal metadata never causes false positive",
			current: EPUBMetadataSnapshot{Title: "Anything", Series: "Some Series", SeriesIndex: floatPtr(3)},
			ideal:   IdealMetadata{},
		},
		{
			name:    "standalone ideal book ignores EPUB series tag",
			current: EPUBMetadataSnapshot{Title: "Project Hail Mary", Series: "Standalone Collection", SeriesIndex: floatPtr(1)},
			ideal:   IdealMetadata{Title: "Project Hail Mary"},
		},
		{
			name:    "ideal series without index ignores EPUB series index",
			current: EPUBMetadataSnapshot{Title: "Dune", Series: "Dune Chronicles", SeriesIndex: floatPtr(1)},
			ideal:   IdealMetadata{Title: "Dune", Series: "Dune Chronicles"},
		},
		{
			name:           "missing series and index in EPUB when ideal has series",
			current:        EPUBMetadataSnapshot{Title: "The Final Empire"},
			ideal:          IdealMetadata{Title: "The Final Empire", Series: "Mistborn", SeriesIndex: floatPtr(1)},
			wantFields:     []string{"series", "seriesIndex"},
			wantFirstCur:   "",
			wantFirstIdeal: "Mistborn",
		},
		{
			name:           "series name mismatch",
			current:        EPUBMetadataSnapshot{Title: "The Final Empire", Series: "Mistborn Trilogy", SeriesIndex: floatPtr(1)},
			ideal:          IdealMetadata{Title: "The Final Empire", Series: "Mistborn", SeriesIndex: floatPtr(1)},
			wantFields:     []string{"series"},
			wantFirstCur:   "Mistborn Trilogy",
			wantFirstIdeal: "Mistborn",
		},
		{
			name:           "float series index mismatch",
			current:        EPUBMetadataSnapshot{Title: "Edgedancer", Series: "The Stormlight Archive", SeriesIndex: floatPtr(2)},
			ideal:          IdealMetadata{Title: "Edgedancer", Series: "The Stormlight Archive", SeriesIndex: floatPtr(2.5)},
			wantFields:     []string{"seriesIndex"},
			wantFirstCur:   "2",
			wantFirstIdeal: "2.5",
		},
		{
			name:    "float series index exact match with fractional value",
			current: EPUBMetadataSnapshot{Title: "Edgedancer", Series: "The Stormlight Archive", SeriesIndex: floatPtr(2.5000001)},
			ideal:   IdealMetadata{Title: "Edgedancer", Series: "The Stormlight Archive", SeriesIndex: floatPtr(2.5)},
		},
		{
			name:    "NaN ideal series index treated as missing without false positive",
			current: EPUBMetadataSnapshot{Title: "Dune", Series: "Dune Chronicles"},
			ideal:   IdealMetadata{Title: "Dune", Series: "Dune Chronicles", SeriesIndex: floatPtr(math.NaN())},
		},
		{
			name:           "all three fields mismatched",
			current:        EPUBMetadataSnapshot{Title: "Wrong Book", Series: "Wrong Series", SeriesIndex: floatPtr(9)},
			ideal:          IdealMetadata{Title: "The Final Empire", Series: "Mistborn", SeriesIndex: floatPtr(1)},
			wantFields:     []string{"title", "series", "seriesIndex"},
			wantFirstCur:   "Wrong Book",
			wantFirstIdeal: "The Final Empire",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CompareEPUBMetadata(tc.current, tc.ideal)
			if len(got) != len(tc.wantFields) {
				t.Fatalf("CompareEPUBMetadata() = %+v, want %d mismatches (%v)", got, len(tc.wantFields), tc.wantFields)
			}
			for i, wantField := range tc.wantFields {
				if got[i].Field != wantField {
					t.Errorf("mismatch[%d].Field = %q, want %q", i, got[i].Field, wantField)
				}
			}
			if len(got) > 0 && (got[0].Current != tc.wantFirstCur || got[0].Ideal != tc.wantFirstIdeal) {
				t.Errorf("mismatch[0] = (%q -> %q), want (%q -> %q)", got[0].Current, got[0].Ideal, tc.wantFirstCur, tc.wantFirstIdeal)
			}
		})
	}
}
