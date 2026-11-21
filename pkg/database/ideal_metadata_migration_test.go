package database

import (
	"context"
	"testing"
)

func TestIdealMetadataMigration(t *testing.T) {
	t.Parallel()
	db, _ := openTestDB(t)
	ctx := context.Background()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate failed: %v", err)
	}

	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='book_ideal_metadata'`).Scan(&name); err != nil {
		t.Fatalf("book_ideal_metadata table missing: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO authors (id, name, sort_name) VALUES ('a1', 'Frank Herbert', 'Herbert, Frank')`); err != nil {
		t.Fatalf("insert author: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO books (id, author_id, title) VALUES ('b1', 'a1', 'Dune')`); err != nil {
		t.Fatalf("insert book: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO book_ideal_metadata (book_id, title, series, series_index, source)
		VALUES ('b1', 'Dune', 'Dune Chronicles', 1.5, 'match')`); err != nil {
		t.Fatalf("insert book_ideal_metadata: %v", err)
	}

	// Deleting the parent book must cascade-delete its ideal metadata snapshot.
	if _, err := db.ExecContext(ctx, `DELETE FROM books WHERE id = 'b1'`); err != nil {
		t.Fatalf("delete book: %v", err)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM book_ideal_metadata WHERE book_id = 'b1'`).Scan(&remaining); err != nil {
		t.Fatalf("count remaining ideal metadata: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected cascade delete of book_ideal_metadata, found %d rows", remaining)
	}
}
