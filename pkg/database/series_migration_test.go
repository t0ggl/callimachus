package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestSeriesIdentityMigrationPreservesMixedLegacyCatalog(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	files, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	const migration = "0014_series_identity.sql"
	for _, f := range files {
		if f.Name() >= migration {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err := applyMigration(ctx, db, f.Name(), string(body)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`
  INSERT INTO authors(id,name,sort_name) VALUES('a','Steven Erikson','Erikson'),('b','Grace Monroe','Monroe');
  INSERT INTO series(id,title,monitored) VALUES('witness','The Witness',1);
  INSERT INTO catalog_actions(kind,entity_id,action) VALUES('series','witness','discover');
  INSERT INTO books(id,author_id,title,series_id) VALUES
   ('first','a','The God Is Not Willing','witness'),('second','b','A Different Witness','witness');
  INSERT INTO book_editions(id,book_id,format) VALUES('ebook','first','epub');
  INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES('asset','ebook','/books/book.epub',1);
 `); err != nil {
		t.Fatal(err)
	}
	body, err := migrationsFS.ReadFile("migrations/" + migration)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, db, migration, string(body)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) = 1 FROM series WHERE id='witness' AND monitored=1 AND author_id IS NULL AND hardcover_id IS NULL`,
		`SELECT COUNT(*) = 2 FROM books WHERE series_id='witness'`,
		`SELECT COUNT(*) = 1 FROM media_assets m JOIN book_editions e ON e.id=m.edition_id WHERE m.id='asset' AND e.book_id='first'`,
		`SELECT COUNT(*) = 1 FROM catalog_actions WHERE entity_id='witness' AND action='discover'`,
	} {
		var preserved bool
		if err := db.QueryRow(query).Scan(&preserved); err != nil || !preserved {
			t.Fatalf("legacy data changed for %s: %v", query, err)
		}
	}
}
