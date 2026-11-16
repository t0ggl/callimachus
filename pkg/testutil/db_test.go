package testutil

import (
	"context"
	"testing"
)

func TestNewTestDB(t *testing.T) {
	db := NewTestDB(t)
	ctx := context.Background()

	var count int
	err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations;").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query schema_migrations: %v", err)
	}
	if count < 2 {
		t.Errorf("expected at least 2 migrations applied, got %d", count)
	}

	var fk int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys;").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("expected foreign_keys=1, got %d, err: %v", fk, err)
	}
}
