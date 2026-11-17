package main

import (
	"context"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestMaintenanceStartupTicksAndCancellation(t *testing.T) {
	db := testutil.NewTestDB(t)
	insert := func(id string) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO download_queue(id,title,protocol,download_client_task_id,status,created_at,updated_at) VALUES(?,'Book','torrent','task','completed',datetime('now','-10 days'),datetime('now','-10 days'))`, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	assertGone := func() {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM download_queue`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("remaining %d: %v", n, err)
		}
	}
	s := &Server{db: db, maintenance: config.MaintenanceConfig{LogRetentionDays: 7}}
	insert("startup")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() { defer close(done); s.maintainLogs(ctx, ticks) }()
	// Each tick rendezvous proves the preceding pass completed.
	ticks <- time.Now()
	ticks <- time.Now()
	assertGone()
	insert("daily")
	ticks <- time.Now()
	ticks <- time.Now()
	assertGone()
	cancel()
	<-done
}
