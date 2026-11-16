package testutil

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/t0ggl/callimachus/pkg/database"
)

var (
	dbTemplateOnce  sync.Once
	dbTemplateBytes []byte
	dbTemplateErr   error
)

func getDBTemplateBytes() ([]byte, error) {
	dbTemplateOnce.Do(func() {
		tempDir, err := os.MkdirTemp("", "callimachus-testdb-template-*")
		if err != nil {
			dbTemplateErr = err
			return
		}
		defer os.RemoveAll(tempDir)

		dbPath := filepath.Join(tempDir, "template.db")
		db, err := database.Open(database.Config{
			Path:         dbPath,
			MaxOpenConns: 1,
			MaxIdleConns: 1,
		})
		if err != nil {
			dbTemplateErr = err
			return
		}

		ctx := context.Background()
		if err := db.Migrate(ctx); err != nil {
			_ = db.Close()
			dbTemplateErr = err
			return
		}

		// Ensure WAL is completely checkpointed into the main database file
		_, _ = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);")
		_ = db.Close()

		dbTemplateBytes, dbTemplateErr = os.ReadFile(dbPath)
	})
	return dbTemplateBytes, dbTemplateErr
}

// PrepareTestDBFile populates destPath with the pre-migrated schema snapshot.
// It allows tests to bypass executing DDL migrations repeatedly.
func PrepareTestDBFile(t testing.TB, destPath string) {
	t.Helper()
	tpl, err := getDBTemplateBytes()
	if err != nil {
		t.Fatalf("testutil: prepare template database failed: %v", err)
	}
	if err := os.WriteFile(destPath, tpl, 0644); err != nil {
		t.Fatalf("testutil: write test db file failed: %v", err)
	}
}

// NewTestDB creates a fresh, isolated, fully-migrated test database in t.TempDir().
func NewTestDB(t testing.TB) *database.DB {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	PrepareTestDBFile(t, dbPath)

	db, err := database.Open(database.Config{
		Path:         dbPath,
		MaxOpenConns: 10,
		MaxIdleConns: 5,
	})
	if err != nil {
		t.Fatalf("testutil: open test db failed: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}
