package database_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestPruneLogs(t *testing.T) {
	db := testutil.NewTestDB(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO authors(id,name,sort_name) VALUES('a','Author','Author')`)
	exec(`INSERT INTO books(id,author_id,title) VALUES('b','a','Book')`)
	exec(`INSERT INTO book_editions(id,book_id,format) VALUES('e','b','M4B')`)
	for _, table := range []string{"conversion_queue", "download_queue"} {
		for _, days := range []int{10, 29, 31, 60} {
			for _, status := range []string{"pending", "processing", "queued", "completed", "failed", "cancelled"} {
				id := fmt.Sprintf("%s-%d-%s", table, days, status)
				cols, vals := "edition_id,source_path,target_path", "'e','source','target'"
				if table == "download_queue" {
					cols, vals = "title,protocol,download_client_task_id", "'Book','torrent','task'"
				}
				exec(`INSERT INTO `+table+`(id,`+cols+`,status,created_at,updated_at) VALUES(?,`+vals+`,?,datetime('now',?),datetime('now',?))`, id, status, fmt.Sprintf("-%d days", days), fmt.Sprintf("-%d days", days))
			}
		}
		// Old jobs that only just finished are not historical yet.
		exec(`UPDATE `+table+` SET updated_at=CURRENT_TIMESTAMP WHERE id=?`, fmt.Sprintf("%s-60-completed", table))
	}
	// History referenced by an unfinished acquisition is still operational data.
	exec(`INSERT INTO users(id,username,email) VALUES('u','user','user@example.test')`)
	exec(`INSERT INTO book_requests(id,user_id,title,author,status) VALUES(1,'u','Book','Author','downloading'),(2,'u','Book','Author','available')`)
	exec(`INSERT INTO acquisition_candidates(request_id,release_key,release_json,score_json,score,format,queue_id) VALUES(1,'r1','{}','{}',1,'M4B','download_queue-31-completed'),(2,'r2','{}','{}',1,'M4B','download_queue-60-failed')`)
	// A read transaction must remain usable while cleanup commits in WAL mode.
	reader, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var before int
	if err := reader.QueryRow(`SELECT count(*) FROM download_queue`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := db.PruneLogs(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	var snapshot int
	if err := reader.QueryRow(`SELECT count(*) FROM download_queue`).Scan(&snapshot); err != nil || snapshot != before {
		t.Fatalf("reader: %d %v", snapshot, err)
	}
	for _, table := range []string{"conversion_queue", "download_queue"} {
		for _, days := range []int{10, 29, 31, 60} {
			for _, status := range []string{"pending", "processing", "queued", "completed", "failed", "cancelled"} {
				id := fmt.Sprintf("%s-%d-%s", table, days, status)
				want := 1
				if days > 30 && (status == "completed" || status == "failed" || status == "cancelled") {
					want = 0
				}
				if days == 60 && status == "completed" || id == "download_queue-31-completed" {
					want = 1
				}
				var got int
				if err := db.QueryRow(`SELECT count(*) FROM `+table+` WHERE id=?`, id).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("%s: %d want %d", id, got, want)
				}
			}
		}
	}
	var candidates int
	if err := db.QueryRow(`SELECT count(*) FROM acquisition_candidates`).Scan(&candidates); err != nil || candidates != 1 {
		t.Fatalf("candidates: %d %v", candidates, err)
	}
	if err := db.PruneLogs(context.Background(), 0); err == nil {
		t.Fatal("accepted zero retention")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.PruneLogs(ctx, 30); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestPruneLogsMultipleBatches(t *testing.T) {
	db := testutil.NewTestDB(t)
	_, err := db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1001) INSERT INTO download_queue(id,title,protocol,download_client_task_id,status,created_at,updated_at) SELECT printf('job-%d',x),'Book','torrent','task','completed',datetime('now','-60 days'),datetime('now','-60 days') FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PruneLogs(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM download_queue`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("remaining %d: %v", n, err)
	}
}

func TestPruneLogsRollsBackRelatedCandidates(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, q := range []string{
		`INSERT INTO users(id,username,email) VALUES('u','user','user@example.test')`,
		`INSERT INTO book_requests(id,user_id,title,author,status) VALUES(1,'u','Book','Author','available')`,
		`INSERT INTO download_queue(id,title,protocol,download_client_task_id,status,created_at,updated_at) VALUES('job','Book','torrent','task','completed',datetime('now','-60 days'),datetime('now','-60 days'))`,
		`INSERT INTO acquisition_candidates(request_id,release_key,release_json,score_json,score,format,queue_id) VALUES(1,'release','{}','{}',1,'M4B','job')`,
		`CREATE TRIGGER prevent_delete BEFORE DELETE ON download_queue BEGIN SELECT RAISE(ABORT,'fixture failure'); END`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.PruneLogs(context.Background(), 30); err == nil {
		t.Fatal("expected delete failure")
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM acquisition_candidates`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("candidate deletion not rolled back: %d %v", n, err)
	}
}

func TestRetentionTimestampIndexes(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, table := range []string{"conversion_queue", "download_queue"} {
		rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT id FROM `+table+` WHERE status IN ('completed','failed','cancelled') AND updated_at < ? AND created_at < ? LIMIT 500`, "2026-01-01", "2026-01-01")
		if err != nil {
			t.Fatal(err)
		}
		indexed := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "retention (status=? AND updated_at<?)") {
				indexed = true
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !indexed {
			t.Errorf("%s does not use timestamp retention index", table)
		}
	}
}
