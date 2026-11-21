package metadata_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/metadata"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestNormalizeLanguage(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"   ", ""},
		{"en", "en"},
		{"EN", "en"},
		{"eng", "en"},
		{"en-US", "en"},
		{"en_GB", "en"},
		{"English", "en"},
		{"/languages/eng", "en"},
		{"es", "es"},
		{"spa", "es"},
		{"es-MX", "es"},
		{"es-419", "es"},
		{"Spanish", "es"},
		{"Español", "es"},
		{"fr", "fr"},
		{"fra", "fr"},
		{"fre", "fr"},
		{"fr-CA", "fr"},
		{"French", "fr"},
		{"Français", "fr"},
		{"de", "de"},
		{"deu", "de"},
		{"ger", "de"},
		{"de-AT", "de"},
		{"German", "de"},
		{"Deutsch", "de"},
		{"pt", "pt"},
		{"por", "pt"},
		{"pt-BR", "pt"},
		{"Portuguese", "pt"},
		{"it", "it"},
		{"ita", "it"},
		{"Italian", "it"},
		{"ja", "ja"},
		{"jpn", "ja"},
		{"ja-JP", "ja"},
		{"Japanese", "ja"},
		{"zh", "zh"},
		{"zho", "zh"},
		{"chi", "zh"},
		{"zh-CN", "zh"},
		{"zh-Hans-CN", "zh"},
		{"Chinese", "zh"},
		{"nl", "nl"},
		{"nld", "nl"},
		{"dut", "nl"},
		{"nb-NO", "no"},
		{"nn", "no"},
		{"und", ""},
		{"un", ""},
		{"unknown", ""},
		{"zxx", ""},
		{"mul", ""},
		{"/languages/und", ""},
		{"eu", "eu"},
		{"eu-ES", "eu"},
		{"123", ""},
		{"not-a-valid-language-tag", ""},
	}
	for _, tc := range tests {
		if got := metadata.NormalizeLanguage(tc.input); got != tc.want {
			t.Errorf("NormalizeLanguage(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNormalizeLanguageFilterAndDefaultProfileLanguage(t *testing.T) {
	if got := metadata.NormalizeLanguageFilter("all"); got != metadata.LanguageAll {
		t.Errorf("NormalizeLanguageFilter(all) = %q, want %q", got, metadata.LanguageAll)
	}
	if got := metadata.NormalizeLanguageFilter("*"); got != metadata.LanguageAll {
		t.Errorf("NormalizeLanguageFilter(*) = %q, want %q", got, metadata.LanguageAll)
	}
	if got := metadata.NormalizeLanguageFilter("fr-CA"); got != "fr" {
		t.Errorf("NormalizeLanguageFilter(fr-CA) = %q, want fr", got)
	}
	if got := metadata.NormalizeLanguageFilter("invalid-value"); got != "" {
		t.Errorf("NormalizeLanguageFilter(invalid-value) = %q, want empty", got)
	}

	profileCases := []struct {
		allowed []string
		want    string
	}{
		{nil, "en"},
		{[]string{}, "en"},
		{[]string{"", "und"}, "en"},
		{[]string{"en-US", "fr"}, "en"},
		{[]string{"fre", "eng"}, "fr"},
		{[]string{"Deutsch"}, "de"},
		{[]string{"all"}, metadata.LanguageAll},
	}
	for _, tc := range profileCases {
		if got := metadata.DefaultProfileLanguage(tc.allowed); got != tc.want {
			t.Errorf("DefaultProfileLanguage(%v) = %q, want %q", tc.allowed, got, tc.want)
		}
	}
}

func TestMatchLanguageFilter(t *testing.T) {
	untagged := metadata.BookMetadata{Title: "Untagged"}
	english := metadata.BookMetadata{Title: "English", Language: "en-US"}
	french := metadata.BookMetadata{Title: "French", Language: "fre"}
	multi := metadata.BookMetadata{Title: "Multi", Language: "ger", Languages: []string{"ger", "fre", "eng"}}

	// Specific language keeps exact/equivalent matches + untagged and excludes other languages.
	if ok, lang, explicit := metadata.MatchLanguageFilter(untagged, "en"); !ok || lang != "" || explicit {
		t.Errorf("untagged for en: got (%v, %q, %v), want (true, \"\", false)", ok, lang, explicit)
	}
	if ok, lang, explicit := metadata.MatchLanguageFilter(english, "en"); !ok || lang != "en" || !explicit {
		t.Errorf("english for en: got (%v, %q, %v), want (true, \"en\", true)", ok, lang, explicit)
	}
	if ok, _, _ := metadata.MatchLanguageFilter(french, "en"); ok {
		t.Error("french should not match en")
	}
	if ok, lang, explicit := metadata.MatchLanguageFilter(multi, "en"); !ok || lang != "en" || !explicit {
		t.Errorf("multi for en: got (%v, %q, %v), want (true, \"en\", true)", ok, lang, explicit)
	}
	if ok, lang, explicit := metadata.MatchLanguageFilter(multi, "fr"); !ok || lang != "fr" || !explicit {
		t.Errorf("multi for fr: got (%v, %q, %v), want (true, \"fr\", true)", ok, lang, explicit)
	}
	if ok, _, _ := metadata.MatchLanguageFilter(multi, "es"); ok {
		t.Error("multi should not match es")
	}

	// All languages keeps everything and normalizes primary language.
	if ok, lang, explicit := metadata.MatchLanguageFilter(french, metadata.LanguageAll); !ok || lang != "fr" || !explicit {
		t.Errorf("french for all: got (%v, %q, %v), want (true, \"fr\", true)", ok, lang, explicit)
	}
	if ok, lang, explicit := metadata.MatchLanguageFilter(untagged, metadata.LanguageAll); !ok || lang != "" || explicit {
		t.Errorf("untagged for all: got (%v, %q, %v), want (true, \"\", false)", ok, lang, explicit)
	}
}

func TestProvidersPreserveMixedLanguages(t *testing.T) {
	t.Run("OpenLibrary search preserves multiple languages", func(t *testing.T) {
		mt := testutil.NewMockTransportForTest(t)
		mt.RegisterString("/search.json", http.StatusOK, `{
			"docs": [
				{"key": "/works/OL1W", "title": "Dune", "author_name": ["Frank Herbert"], "language": ["ger", "eng", "fre"]},
				{"key": "/works/OL2W", "title": "Untagged", "author_name": ["Someone"]}
			]
		}`, nil)
		ol := metadata.NewOpenLibraryProvider(metadata.WithOpenLibraryClient(mt.Client()))
		results, err := ol.Search(context.Background(), "Dune")
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("len(results) = %d, want 2", len(results))
		}
		if got := metadata.BookLanguages(results[0]); !slices.Equal(got, []string{"de", "en", "fr"}) {
			t.Errorf("BookLanguages(results[0]) = %v, want [de en fr]", got)
		}
		if got := metadata.BookLanguages(results[1]); len(got) != 0 {
			t.Errorf("BookLanguages(results[1]) = %v, want empty", got)
		}
	})

	t.Run("Hardcover search maps default_cover_edition and edition languages", func(t *testing.T) {
		mt := testutil.NewMockTransportForTest(t)
		mt.RegisterHandler("/v1/graphql", func(r *http.Request) (*http.Response, error) {
			var input struct{ Query string }
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			var body string
			switch {
			case strings.Contains(input.Query, "query SearchBooks"):
				body = `{"data":{"search":{"ids":[1,2,3]}}}`
			case strings.Contains(input.Query, "query SearchDetails"):
				if !strings.Contains(input.Query, "default_cover_edition") {
					t.Error("SearchDetails query should request default_cover_edition language")
				}
				body = `{"data":{"books":[
					{"id":1,"title":"English Book","default_cover_edition":{"language":{"code2":"en","code3":"eng"},"publisher":{"name":"Ace"}}},
					{"id":2,"title":"German Book","editions":[{"isbn_13":"9783453317178","language":{"code3":"deu"}}]},
					{"id":3,"title":"Untagged Book"}
				]}}`
			default:
				t.Fatalf("unexpected query: %s", input.Query)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		hc := metadata.NewHardcoverProvider("mock", metadata.WithHardcoverClient(mt.Client()))
		results, err := hc.Search(context.Background(), "Dune")
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("len(results) = %d, want 3", len(results))
		}
		if results[0].Language != "en" || results[0].Publisher != "Ace" {
			t.Errorf("results[0] = %+v, want language=en publisher=Ace", results[0])
		}
		if results[1].Language != "de" {
			t.Errorf("results[1].Language = %q, want de", results[1].Language)
		}
		if results[2].Language != "" {
			t.Errorf("results[2].Language = %q, want empty", results[2].Language)
		}
	})
}
