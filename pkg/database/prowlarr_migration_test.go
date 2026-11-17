package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestProwlarrMigrationPreservesQueue(t *testing.T) {
	t.Parallel()
	db, err := Open(Config{Path: filepath.Join(t.TempDir(), "migration.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_initial_schema.sql", "0002_book_requests.sql", "0003_remove_audio_bitrate_settings.sql"} {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyMigration(ctx, db.DB, name, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO download_clients (id,name,type,host,port,password) VALUES ('old','Old','qbittorrent','localhost',8080,'secret'); INSERT INTO download_queue (id,client_id,download_client_task_id,title,protocol,staging_path) VALUES ('q','old','hash','Book','torrent','/completed/Book')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db.DB); err != nil {
		t.Fatal(err)
	}
	var title, path string
	if err := db.QueryRow(`SELECT title,staging_path FROM download_queue WHERE id='q'`).Scan(&title, &path); err != nil || title != "Book" || path != "/completed/Book" {
		t.Fatalf("lost queue: %s %s %v", title, path, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='download_clients'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("client credentials retained: %d %v", count, err)
	}
	if _, err := db.Exec(`INSERT INTO download_queue (id,download_client_task_id,title,protocol) VALUES ('new','guid','New Book','usenet')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db.DB); err != nil {
		t.Fatal(err)
	}
}
