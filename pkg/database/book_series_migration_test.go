package database

import (
	"database/sql"
	"testing"
)

func TestBookSeriesMigrationBackfillsPrimary(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"0001_initial_schema.sql", "0018_book_series.sql"} {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if name == "0001_initial_schema.sql" {
			body = append(body, []byte(`
				INSERT INTO authors(id,name,sort_name) VALUES ('a','Author','Author');
				INSERT INTO series(id,title) VALUES ('s','Series'), ('other','Other');
				INSERT INTO books(id,author_id,series_id,series_index,series_index_display,title) VALUES
					('b','a','s',2.5,'Novella 2.5','Book'), ('solo','a',NULL,NULL,NULL,'Standalone');
			`)...)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	var index float64
	var display string
	if err := db.QueryRow(`SELECT series_index, series_index_display FROM book_series
		WHERE book_id = 'b' AND series_id = 's' AND is_primary = 1`).Scan(&index, &display); err != nil || index != 2.5 || display != "Novella 2.5" {
		t.Fatalf("backfill position = %v %q (%v)", index, display, err)
	}
	if _, err := db.Exec(`INSERT INTO book_series(book_id,series_id,is_primary) VALUES ('b','other',1)`); err == nil {
		t.Fatal("allowed two primary series")
	}
	if _, err := db.Exec(`INSERT INTO book_series(book_id,series_id) VALUES ('solo','other')`); err != nil {
		t.Fatalf("standalone additional membership: %v", err)
	}
}
