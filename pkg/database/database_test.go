package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var (
	testTemplateOnce  sync.Once
	testTemplateBytes []byte
	testTemplateErr   error
)

func getTestTemplateBytes() ([]byte, error) {
	testTemplateOnce.Do(func() {
		tempDir, err := os.MkdirTemp("", "callimachus-db-test-template-*")
		if err != nil {
			testTemplateErr = err
			return
		}
		defer os.RemoveAll(tempDir)

		dbPath := filepath.Join(tempDir, "template.db")
		db, err := Open(Config{
			Path:         dbPath,
			MaxOpenConns: 1,
			MaxIdleConns: 1,
		})
		if err != nil {
			testTemplateErr = err
			return
		}

		ctx := context.Background()
		if err := db.Migrate(ctx); err != nil {
			_ = db.Close()
			testTemplateErr = err
			return
		}

		_, _ = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);")
		_ = db.Close()

		testTemplateBytes, testTemplateErr = os.ReadFile(dbPath)
	})
	return testTemplateBytes, testTemplateErr
}

func openMigratedTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_callimachus.db")

	tpl, err := getTestTemplateBytes()
	if err != nil {
		t.Fatalf("prepare test template failed: %v", err)
	}
	if err := os.WriteFile(dbPath, tpl, 0644); err != nil {
		t.Fatalf("write test db failed: %v", err)
	}

	cfg := Config{
		Path:         dbPath,
		MaxOpenConns: 20,
		MaxIdleConns: 10,
	}

	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db, dbPath
}

func openTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_callimachus.db")

	cfg := Config{
		Path:         dbPath,
		MaxOpenConns: 20,
		MaxIdleConns: 10,
	}

	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db, dbPath
}

func TestDatabasePragmas(t *testing.T) {
	t.Parallel()
	db, _ := openTestDB(t)
	ctx := context.Background()

	// Verify WAL mode
	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("failed to query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode 'wal', got '%s'", journalMode)
	}

	// Verify synchronous = NORMAL (1)
	var synchronous int
	if err := db.QueryRowContext(ctx, "PRAGMA synchronous;").Scan(&synchronous); err != nil {
		t.Fatalf("failed to query synchronous: %v", err)
	}
	if synchronous != 1 {
		t.Errorf("expected synchronous 1 (NORMAL), got %d", synchronous)
	}

	// Verify foreign_keys = ON (1)
	var foreignKeys int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys;").Scan(&foreignKeys); err != nil {
		t.Fatalf("failed to query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("expected foreign_keys 1 (ON), got %d", foreignKeys)
	}

	// Verify busy_timeout = 5000
	var busyTimeout int
	if err := db.QueryRowContext(ctx, "PRAGMA busy_timeout;").Scan(&busyTimeout); err != nil {
		t.Fatalf("failed to query busy_timeout: %v", err)
	}
	if busyTimeout < 5000 {
		t.Errorf("expected busy_timeout >= 5000, got %d", busyTimeout)
	}

	// Verify cache_size = -64000
	var cacheSize int
	if err := db.QueryRowContext(ctx, "PRAGMA cache_size;").Scan(&cacheSize); err != nil {
		t.Fatalf("failed to query cache_size: %v", err)
	}
	if cacheSize != -64000 {
		t.Errorf("expected cache_size -64000, got %d", cacheSize)
	}

	// Verify temp_store = MEMORY (2)
	var tempStore int
	if err := db.QueryRowContext(ctx, "PRAGMA temp_store;").Scan(&tempStore); err != nil {
		t.Fatalf("failed to query temp_store: %v", err)
	}
	if tempStore != 2 {
		t.Errorf("expected temp_store 2 (MEMORY), got %d", tempStore)
	}

	// Verify auto_vacuum = INCREMENTAL (2)
	var autoVacuum int
	if err := db.QueryRowContext(ctx, "PRAGMA auto_vacuum;").Scan(&autoVacuum); err != nil {
		t.Fatalf("failed to query auto_vacuum: %v", err)
	}
	if autoVacuum != 2 {
		t.Errorf("expected auto_vacuum 2 (INCREMENTAL), got %d", autoVacuum)
	}
}

func TestMigrationAndTables(t *testing.T) {
	t.Parallel()
	db, _ := openTestDB(t)
	ctx := context.Background()

	// Apply migration
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	// Verify all 15 core domain entity tables exist
	expectedTables := []string{
		"authors",
		"narrators",
		"series",
		"books",
		"book_editions",
		"edition_narrators",
		"media_assets",
		"transcode_queue",
		"indexers",
		"download_clients",
		"download_queue",
		"blocklist",
		"users",
		"user_requests",
		"book_requests",
		"settings",
		"schema_migrations",
	}

	for _, table := range expectedTables {
		var name string
		query := "SELECT name FROM sqlite_master WHERE type='table' AND name=?;"
		err := db.QueryRowContext(ctx, query, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s was not created: %v", table, err)
		}
	}

	// Verify all expected indexes exist
	expectedIndexes := []string{
		"idx_authors_name",
		"idx_authors_sort_name",
		"idx_narrators_name",
		"idx_series_title",
		"idx_books_author_id",
		"idx_books_series_id",
		"idx_books_series_order",
		"idx_books_monitored",
		"idx_editions_book_id",
		"idx_editions_format",
		"idx_editions_isbn13",
		"idx_editions_asin",
		"idx_edition_narrators_narrator",
		"idx_media_assets_edition",
		"idx_media_assets_path",
		"idx_transcode_status_priority",
		"idx_download_queue_client_task",
		"idx_download_queue_status",
		"idx_blocklist_hash",
		"idx_users_username",
		"idx_users_oidc_sub",
		"idx_user_requests_status",
		"idx_user_requests_user",
	}

	for _, idx := range expectedIndexes {
		var name string
		query := "SELECT name FROM sqlite_master WHERE type='index' AND name=?;"
		err := db.QueryRowContext(ctx, query, idx).Scan(&name)
		if err != nil {
			t.Errorf("index %s was not created: %v", idx, err)
		}
	}
}

func TestMigrationIdempotency(t *testing.T) {
	t.Parallel()
	db, _ := openTestDB(t)
	ctx := context.Background()

	// Run migration first time
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate failed: %v", err)
	}

	// Run migration second time - must be idempotent and succeed
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate call failed: %v", err)
	}

	// Verify schema_migrations has exactly 1 entry for 0001_initial_schema.sql
	var count int
	err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version='0001_initial_schema.sql';").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query schema_migrations: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 migration recorded, got %d", count)
	}
}

func TestSeriesIndexFloat64Precision(t *testing.T) {
	t.Parallel()
	db, _ := openMigratedTestDB(t)
	ctx := context.Background()

	// Insert author
	_, err := db.ExecContext(ctx, `
		INSERT INTO authors (id, name, sort_name)
		VALUES ('auth-1', 'Brandon Sanderson', 'Sanderson, Brandon');
	`)
	if err != nil {
		t.Fatalf("failed to insert author: %v", err)
	}

	// Insert series
	_, err = db.ExecContext(ctx, `
		INSERT INTO series (id, title)
		VALUES ('ser-1', 'The Stormlight Archive');
	`)
	if err != nil {
		t.Fatalf("failed to insert series: %v", err)
	}

	// Insert books with float64 series_index (e.g. 0.5, 1.0, 1.5, 2.0, 2.5)
	testIndices := []struct {
		id      string
		title   string
		idx     float64
		display string
	}{
		{"book-0", "Prequel", 0.5, "#0.5"},
		{"book-1", "The Way of Kings", 1.0, "#1"},
		{"book-1.5", "Edgedancer", 1.5, "#1.5"},
		{"book-2", "Words of Radiance", 2.0, "#2"},
		{"book-2.5", "Dawnshard", 2.5, "#2.5"},
	}

	for _, b := range testIndices {
		_, err := db.ExecContext(ctx, `
			INSERT INTO books (id, author_id, series_id, series_index, series_index_display, title)
			VALUES (?, 'auth-1', 'ser-1', ?, ?, ?);
		`, b.id, b.idx, b.display, b.title)
		if err != nil {
			t.Fatalf("failed to insert book %s: %v", b.title, err)
		}
	}

	// Query books ordered by series_index ASC
	rows, err := db.QueryContext(ctx, `
		SELECT id, series_index, series_index_display
		FROM books
		WHERE series_id = 'ser-1'
		ORDER BY series_index ASC;
	`)
	if err != nil {
		t.Fatalf("failed to query books: %v", err)
	}
	defer rows.Close()

	var retrieved []struct {
		id      string
		idx     float64
		display string
	}

	for rows.Next() {
		var id, display string
		var idx float64
		if err := rows.Scan(&id, &idx, &display); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		retrieved = append(retrieved, struct {
			id      string
			idx     float64
			display string
		}{id, idx, display})
	}

	if len(retrieved) != len(testIndices) {
		t.Fatalf("expected %d books, got %d", len(testIndices), len(retrieved))
	}

	for i, expected := range testIndices {
		actual := retrieved[i]
		if actual.id != expected.id {
			t.Errorf("row %d: expected id %s, got %s", i, expected.id, actual.id)
		}
		if actual.idx != expected.idx {
			t.Errorf("row %d: expected series_index %f, got %f", i, expected.idx, actual.idx)
		}
		if actual.display != expected.display {
			t.Errorf("row %d: expected display %s, got %s", i, expected.display, actual.display)
		}
	}
}

func TestForeignKeyCascades(t *testing.T) {
	t.Parallel()
	db, _ := openMigratedTestDB(t)
	ctx := context.Background()

	// 1. Insert Author -> Book -> BookEdition -> MediaAsset & TranscodeQueue & EditionNarrator
	_, err := db.ExecContext(ctx, `
		INSERT INTO authors (id, name, sort_name) VALUES ('auth-fk', 'Andy Weir', 'Weir, Andy');
		INSERT INTO series (id, title) VALUES ('ser-fk', 'Standalone Works');
		INSERT INTO books (id, author_id, series_id, title) VALUES ('book-fk', 'auth-fk', 'ser-fk', 'Project Hail Mary');
		INSERT INTO book_editions (id, book_id, format) VALUES ('ed-audio', 'book-fk', 'm4b');
		INSERT INTO narrators (id, name, sort_name) VALUES ('narr-fk', 'Ray Porter', 'Porter, Ray');
		INSERT INTO edition_narrators (edition_id, narrator_id, role) VALUES ('ed-audio', 'narr-fk', 'primary');
		INSERT INTO media_assets (id, edition_id, file_path, file_size) VALUES ('asset-1', 'ed-audio', '/audiobooks/phm.m4b', 500000000);
		INSERT INTO transcode_queue (id, edition_id, source_path, target_path) VALUES ('tq-1', 'ed-audio', '/staging/phm', '/audiobooks/phm.m4b');
	`)
	if err != nil {
		t.Fatalf("initial setup insert failed: %v", err)
	}

	// Verify all exist
	var count int
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM media_assets WHERE id = 'asset-1';").Scan(&count)
	if count != 1 {
		t.Fatalf("expected media asset to exist")
	}

	// 2. Delete the Book: should cascade delete BookEdition, MediaAsset, TranscodeQueue, EditionNarrator
	_, err = db.ExecContext(ctx, "DELETE FROM books WHERE id = 'book-fk';")
	if err != nil {
		t.Fatalf("failed to delete book: %v", err)
	}

	// Check book is gone
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM books WHERE id = 'book-fk';").Scan(&count)
	if count != 0 {
		t.Errorf("book was not deleted")
	}

	// Check edition was cascade deleted
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM book_editions WHERE id = 'ed-audio';").Scan(&count)
	if count != 0 {
		t.Errorf("book edition was not cascade deleted")
	}

	// Check media asset was cascade deleted
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM media_assets WHERE id = 'asset-1';").Scan(&count)
	if count != 0 {
		t.Errorf("media asset was not cascade deleted")
	}

	// Check transcode queue was cascade deleted
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM transcode_queue WHERE id = 'tq-1';").Scan(&count)
	if count != 0 {
		t.Errorf("transcode queue was not cascade deleted")
	}

	// Check edition narrator was cascade deleted
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM edition_narrators WHERE edition_id = 'ed-audio';").Scan(&count)
	if count != 0 {
		t.Errorf("edition narrator was not cascade deleted")
	}

	// Narrator entity itself must NOT be deleted (narrators are first-class entities)
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM narrators WHERE id = 'narr-fk';").Scan(&count)
	if count != 1 {
		t.Errorf("narrator was unexpectedly deleted")
	}

	// 3. Test SET NULL behavior: delete series -> books.series_id becomes NULL
	_, err = db.ExecContext(ctx, `
		INSERT INTO books (id, author_id, series_id, title) VALUES ('book-setnull', 'auth-fk', 'ser-fk', 'The Martian');
	`)
	if err != nil {
		t.Fatalf("failed to insert book for set null test: %v", err)
	}

	_, err = db.ExecContext(ctx, "DELETE FROM series WHERE id = 'ser-fk';")
	if err != nil {
		t.Fatalf("failed to delete series: %v", err)
	}

	var seriesID sql.NullString
	err = db.QueryRowContext(ctx, "SELECT series_id FROM books WHERE id = 'book-setnull';").Scan(&seriesID)
	if err != nil {
		t.Fatalf("failed to query series_id: %v", err)
	}
	if seriesID.Valid {
		t.Errorf("expected series_id to be NULL after series deletion, got %s", seriesID.String)
	}

	// 4. Test User -> UserRequest cascade
	_, err = db.ExecContext(ctx, `
		INSERT INTO users (id, username, email) VALUES ('user-fk', 'jdoe', 'jdoe@example.com');
		INSERT INTO user_requests (id, user_id, requested_title, requested_author)
		VALUES ('req-fk', 'user-fk', 'Artemis', 'Andy Weir');
	`)
	if err != nil {
		t.Fatalf("failed to insert user and request: %v", err)
	}

	_, err = db.ExecContext(ctx, "DELETE FROM users WHERE id = 'user-fk';")
	if err != nil {
		t.Fatalf("failed to delete user: %v", err)
	}

	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM user_requests WHERE id = 'req-fk';").Scan(&count)
	if count != 0 {
		t.Errorf("user request was not cascade deleted on user deletion")
	}
}

func TestConcurrentReadsDuringActiveWriteTransaction(t *testing.T) {
	t.Parallel()
	db, _ := openMigratedTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Seed some initial data
	for i := 0; i < 20; i++ {
		_, err := db.ExecContext(ctx, fmt.Sprintf(
			"INSERT INTO settings (key, value) VALUES ('key_%d', 'val_%d');", i, i,
		))
		if err != nil {
			t.Fatalf("failed seeding settings: %v", err)
		}
	}

	// Start an active write transaction holding an EXCLUSIVE write lock in SQLite WAL
	writeTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("failed to begin write transaction: %v", err)
	}

	_, err = writeTx.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES ('write_tx_key', 'in_progress');")
	if err != nil {
		_ = writeTx.Rollback()
		t.Fatalf("failed writing in transaction: %v", err)
	}

	// While writeTx is OPEN and uncommitted, spawn multiple concurrent reader goroutines
	const numReaders = 8
	const readsPerGoroutine = 25
	var wg sync.WaitGroup
	errCh := make(chan error, numReaders*readsPerGoroutine)

	readersReady := make(chan struct{})

	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			<-readersReady

			for j := 0; j < readsPerGoroutine; j++ {
				var count int
				// In SQLite WAL mode, readers must see snapshot of committed state
				// without being blocked by active writeTx!
				err := db.QueryRowContext(ctx, "SELECT count(*) FROM settings;").Scan(&count)
				if err != nil {
					errCh <- fmt.Errorf("reader %d (iter %d) failed: %w", readerID, j, err)
					return
				}
				// Reader must see 20 committed rows (not the uncommitted 21st row)
				if count != 20 {
					errCh <- fmt.Errorf("reader %d saw uncommitted row count: %d", readerID, count)
					return
				}
			}
		}(r)
	}

	// Trigger concurrent readers
	close(readersReady)

	// Wait for all readers to complete their reads while write transaction is active
	wg.Wait()

	// Commit write transaction
	if err := writeTx.Commit(); err != nil {
		t.Fatalf("failed to commit write transaction: %v", err)
	}
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent read error: %v", err)
	}

	// Verify post-commit state sees 21 rows
	var finalCount int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM settings;").Scan(&finalCount); err != nil {
		t.Fatalf("failed reading final count: %v", err)
	}
	if finalCount != 21 {
		t.Errorf("expected 21 settings after commit, got %d", finalCount)
	}
}
