package main

import (
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestIntakeEnrichmentUsesAudnexusAndProviderArtwork(t *testing.T) {
	db := testutil.NewTestDB(t)
	if _, err := db.Exec("INSERT INTO settings(key,value) VALUES ('openlibrary_enabled','false')"); err != nil {
		t.Fatal(err)
	}
	const coverURL = "https://art.example/cover.jpg"
	mock := testutil.NewMockTransportForTest(t)
	mock.RegisterString("https://api.audnexus.com/books/B012345678", http.StatusOK, `{
		"asin":"B012345678","title":"Canonical Title","summary":"Canonical synopsis",
		"releaseDate":"2021-09-01","authors":[{"name":"Canonical Author"}],
		"narrators":[{"name":"First Reader"},{"name":"Second Reader"}],
		"series":[{"name":"Example Series","position":"2.5"}],"coverUrl":"`+coverURL+`"
	}`, nil)
	mock.RegisterResponse(coverURL, http.StatusOK, encodeCoverJPEG(t, 300, 450, color.RGBA{R: 100, A: 255}),
		http.Header{"Content-Type": {"image/jpeg"}})
	s := &Server{db: db, prowlarrHTTPClient: &http.Client{Transport: mock}}

	book, art := s.enrichIntake(t.Context(), catalog.BookDetails{
		Title: "Filename Title", ASIN: "B012345678", Format: "m4b",
	})
	if book.Title != "Canonical Title" || book.Author != "Canonical Author" || book.Year != 2021 ||
		book.Series != "Example Series" || book.SeriesIndex == nil || *book.SeriesIndex != 2.5 ||
		strings.Join(book.Narrators, ", ") != "First Reader, Second Reader" || book.Description != "Canonical synopsis" {
		t.Fatalf("matched intake metadata = %+v", book)
	}
	cover, err := catalog.ValidateAndNormalizeCover(art)
	if err != nil || cover.Width != 300 || cover.Height != 450 {
		t.Fatalf("provider cover invalid: %+v (%v)", cover, err)
	}
}

func TestIntakeSettingsCanBeUpdatedAndDisabled(t *testing.T) {
	s := &Server{db: testutil.NewTestDB(t), intakeWake: make(chan struct{}, 1)}
	put := func(body string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleUpdateSettings(w, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)))
		if w.Code != want {
			t.Fatalf("PUT %s returned %d: %s", body, w.Code, w.Body.String())
		}
	}
	put(`{"library":{"intakeRoot":"/intake","intakePollInterval":"15s"}}`, http.StatusOK)
	select {
	case <-s.intakeWake:
	default:
		t.Fatal("saving settings did not wake the scanner")
	}
	put(`{"library":{"intakePollInterval":"0s"}}`, http.StatusBadRequest)
	put(`{"library":{"intakeRoot":""}}`, http.StatusOK)

	resp := s.buildSettingsResponse(t.Context(), nil)
	if resp.Library.IntakeRoot != "" || resp.Library.IntakePollInterval != "15s" ||
		resp.Overrides["intake_root"].IsOverridden || resp.Overrides["intake_poll_interval"].IsOverridden {
		t.Fatalf("unexpected saved intake settings: %+v", resp.Library)
	}
}
