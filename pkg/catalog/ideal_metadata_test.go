package catalog

import (
	"context"
	"testing"

	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestSaveIdealMetadataPrecedence(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	cat := New(db)

	author, err := cat.GetOrCreateAuthor(ctx, "Brandon Sanderson")
	if err != nil {
		t.Fatalf("GetOrCreateAuthor: %v", err)
	}
	book, err := cat.GetOrCreateBook(ctx, &database.Book{
		AuthorID: author.ID,
		Title:    "The Final Empire",
	})
	if err != nil {
		t.Fatalf("GetOrCreateBook: %v", err)
	}

	idx1 := 1.0
	idx25 := 2.5

	// 1. Scan writes initial ideal metadata.
	if err := cat.SaveIdealMetadata(ctx, book.ID, "Final Empire", "Mistborn", &idx1, IdealSourceScan); err != nil {
		t.Fatalf("SaveIdealMetadata scan: %v", err)
	}

	// 2. Match overrides scan and updates title/series/index.
	if err := cat.SaveIdealMetadata(ctx, book.ID, "The Final Empire", "Mistborn", &idx25, IdealSourceMatch); err != nil {
		t.Fatalf("SaveIdealMetadata match: %v", err)
	}

	// 3. A subsequent weaker scan must NOT overwrite matched ideal metadata.
	if err := cat.SaveIdealMetadata(ctx, book.ID, "Wrong Scan Title", "Wrong Series", &idx1, IdealSourceScan); err != nil {
		t.Fatalf("SaveIdealMetadata weaker scan: %v", err)
	}

	var title, source string
	var series *string
	var seriesIndex *float64
	if err := db.QueryRowContext(ctx, `SELECT title, series, series_index, source FROM book_ideal_metadata WHERE book_id = ?`, book.ID).
		Scan(&title, &series, &seriesIndex, &source); err != nil {
		t.Fatalf("query ideal metadata: %v", err)
	}
	if title != "The Final Empire" || series == nil || *series != "Mistborn" || seriesIndex == nil || *seriesIndex != 2.5 || source != IdealSourceMatch {
		t.Fatalf("unexpected ideal metadata after weaker scan: title=%q series=%v idx=%v source=%q", title, series, seriesIndex, source)
	}

	// 4. Manual repair overrides match and can clear series for a standalone book.
	if err := cat.SaveIdealMetadata(ctx, book.ID, "Mistborn: The Final Empire", "", nil, IdealSourceManualRepair); err != nil {
		t.Fatalf("SaveIdealMetadata manual_repair: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT title, series, series_index, source FROM book_ideal_metadata WHERE book_id = ?`, book.ID).
		Scan(&title, &series, &seriesIndex, &source); err != nil {
		t.Fatalf("query ideal metadata after manual repair: %v", err)
	}
	if title != "Mistborn: The Final Empire" || series != nil || seriesIndex != nil || source != IdealSourceManualRepair {
		t.Fatalf("unexpected ideal metadata after manual repair: title=%q series=%v idx=%v source=%q", title, series, seriesIndex, source)
	}
}
