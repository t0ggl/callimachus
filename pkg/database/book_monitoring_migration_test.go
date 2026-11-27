package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBookMonitoringMigrationPreservesSettingsAndAssets(t *testing.T) {
	db, err := Open(Config{Path: filepath.Join(t.TempDir(), "migration.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	files, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := file.Name()
		if name == "0017_book_monitoring_columns.sql" {
			break
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyMigration(ctx, db.DB, name, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO authors(id,name,sort_name) VALUES ('a','Author','Author');
		INSERT INTO books(id,author_id,title,monitored,audiobook_monitored)
		VALUES ('b','a','Book',1,0), ('legacy','a','Legacy',1,NULL),
		       ('cancelled','a','Cancelled',1,NULL), ('rerequested','a','Requested again',1,0);
		INSERT INTO book_editions(id,book_id,format,monitored) VALUES ('e','b','m4b',1);
		INSERT INTO media_assets(id,edition_id,file_path,file_size) VALUES ('m','e','Book.m4b',1);
		INSERT INTO users(id,username,email,role) VALUES ('reader','reader','reader@example.com','user');
		INSERT INTO book_requests(user_id,book_id,title,author,format,status,dismissed) VALUES
		  ('reader','cancelled','Cancelled','Author','epub','cancelled',1),
		  ('reader','rerequested','Requested again','Author','epub','cancelled',1),
		  ('reader','rerequested','Requested again','Author','epub','searching',0);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT ebook_monitored = 1 AND audiobook_monitored = 0 FROM books WHERE id='b'`,
		`SELECT ebook_monitored = 1 AND audiobook_monitored IS NULL FROM books WHERE id='legacy'`,
		`SELECT COUNT(*) = 1 AND MIN(format) = 'epub' FROM missing_monitored_formats WHERE book_id='b'`,
		`SELECT COUNT(*) = 2 FROM missing_monitored_formats WHERE book_id='legacy'`,
		`SELECT ebook_monitoring_excluded=1 AND audiobook_monitoring_excluded=0
		    AND ebook_monitored=0 AND audiobook_monitored=1 FROM books WHERE id='cancelled'`,
		`SELECT ebook_monitoring_excluded=0 AND ebook_monitored=1 FROM books WHERE id='rerequested'`,
		`SELECT COUNT(*) = 0 FROM pragma_table_info('book_editions') WHERE name='monitored'`,
		`SELECT COUNT(*) = 1 FROM media_assets m JOIN book_editions e ON e.id=m.edition_id WHERE m.id='m' AND e.book_id='b'`,
	} {
		var ok bool
		if err := db.QueryRow(query).Scan(&ok); err != nil || !ok {
			t.Errorf("%s: got %v, error %v", query, ok, err)
		}
	}
}
