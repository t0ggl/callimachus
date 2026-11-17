package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestAcquisitionMigrationPreservesRequests(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys=ON;CREATE TABLE schema_migrations(version TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	files, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name() >= "0005" {
			continue
		}
		data, err := migrationsFS.ReadFile("migrations/" + f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err = applyMigration(ctx, db, f.Name(), string(data)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO users(id,username,email) VALUES('r','reader','r@example.test');
 INSERT INTO book_requests(id,user_id,title,author,status,rejection_reason) VALUES(1,'r','Waiting','Author','approved',NULL),(2,'r','Declined','Author','rejected','old review');
 INSERT INTO user_requests(id,user_id,requested_title,requested_author,status) VALUES('legacy','r','Legacy','Author','available');`); err != nil {
		t.Fatal(err)
	}
	data, _ := migrationsFS.ReadFile("migrations/0005_candidates.sql")
	if err = applyMigration(ctx, db, "0005_candidates.sql", string(data)); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT title,status,completed_at IS NOT NULL FROM book_requests ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	expected := []struct {
		title, status string
		completed     bool
	}{{"Waiting", "searching", false}, {"Declined", "error", false}, {"Legacy", "available", true}}
	for _, want := range expected {
		if !rows.Next() {
			t.Fatal("request lost")
		}
		var title, status string
		var completed bool
		if err := rows.Scan(&title, &status, &completed); err != nil {
			t.Fatal(err)
		}
		if title != want.title || status != want.status || completed != want.completed {
			t.Fatalf("%s %s %v", title, status, completed)
		}
	}
	rows.Close()
	if _, err = db.Exec(`SELECT rejection_reason FROM book_requests`); err == nil {
		t.Fatal("review fields retained")
	}
	if _, err = db.Exec(`SELECT * FROM user_requests`); err == nil {
		t.Fatal("legacy table retained")
	}
}

func TestFailurePenaltyMigrationUpgradesExistingRecords(t *testing.T) {
	db, _ := openMigratedTestDB(t)
	if _, err := db.Exec(`INSERT INTO failed_releases(release_key,penalty) VALUES('existing',-50)`); err != nil {
		t.Fatal(err)
	}
	data, err := migrationsFS.ReadFile("migrations/0006_failure_penalty.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	var penalty int
	if err = db.QueryRow(`SELECT penalty FROM failed_releases WHERE release_key='existing'`).Scan(&penalty); err != nil || penalty != -100 {
		t.Fatalf("penalty %d: %v", penalty, err)
	}
}
