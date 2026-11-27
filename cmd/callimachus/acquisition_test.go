package main

import (
	"context"
	"testing"

	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/requests"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestAcquisitionEvaluate_UnknownFormatAudiobookUsesAudiobookSizeLimit(t *testing.T) {
	db := testutil.NewTestDB(t)
	s := &Server{db: db}
	acq := s.newAcquisition()
	ctx := context.Background()

	const mb = int64(1024 * 1024)

	t.Run("audiobook request without categories allows 305MB unknown format", func(t *testing.T) {
		eval, err := acq.Evaluate(ctx, indexers.ProwlarrRelease{
			Title: "Dune - Frank Herbert (Unabridged)",
			Size:  305 * mb,
		}, requests.FormatM4B)
		if err != nil {
			t.Fatal(err)
		}
		if eval.Format != "" {
			t.Fatalf("expected unknown format, got %q", eval.Format)
		}
		if reason, excluded := eval.Exclusions["size"]; excluded {
			t.Fatalf("305MB unknown-format audiobook should not be excluded for size: %s", reason)
		}
	})

	t.Run("audio category allows 305MB unknown format even on both request", func(t *testing.T) {
		eval, err := acq.Evaluate(ctx, indexers.ProwlarrRelease{
			Title:      "Dune - Frank Herbert (Unabridged)",
			Size:       305 * mb,
			Categories: []indexers.ProwlarrCategory{{ID: 3030, Name: "Audio/Audiobook"}},
		}, requests.FormatBoth)
		if err != nil {
			t.Fatal(err)
		}
		if reason, excluded := eval.Exclusions["size"]; excluded {
			t.Fatalf("305MB audio-category release should not be excluded for size: %s", reason)
		}
	})

	t.Run("audiobook request rejects unknown format exceeding 1536MB", func(t *testing.T) {
		eval, err := acq.Evaluate(ctx, indexers.ProwlarrRelease{
			Title: "Dune - Frank Herbert (Unabridged)",
			Size:  1600 * mb,
		}, requests.FormatM4B)
		if err != nil {
			t.Fatal(err)
		}
		if _, excluded := eval.Exclusions["size"]; !excluded {
			t.Fatalf("1600MB audiobook should be excluded for size, got %+v", eval.Exclusions)
		}
	})

	t.Run("audiobook request never applies ebook size limit", func(t *testing.T) {
		eval, err := acq.Evaluate(ctx, indexers.ProwlarrRelease{
			Title: "Dune - Frank Herbert.epub",
			Size:  305 * mb,
		}, requests.FormatM4B)
		if err != nil {
			t.Fatal(err)
		}
		if reason, excluded := eval.Exclusions["size"]; excluded {
			t.Fatalf("audiobook request should never apply ebook size limit: %s", reason)
		}
	})

	t.Run("ebook request rejects 305MB unknown format", func(t *testing.T) {
		eval, err := acq.Evaluate(ctx, indexers.ProwlarrRelease{
			Title: "Dune - Frank Herbert",
			Size:  305 * mb,
		}, requests.FormatEPUB)
		if err != nil {
			t.Fatal(err)
		}
		if _, excluded := eval.Exclusions["size"]; !excluded {
			t.Fatalf("305MB unknown-format ebook should be excluded for size, got %+v", eval.Exclusions)
		}
	})
}
