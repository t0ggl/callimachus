package requests

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/indexers"
)

var errTransferFailed = errors.New("download dispatch failed")

type Candidate struct {
	Key       string                   `json:"key"`
	Release   indexers.ProwlarrRelease `json:"release"`
	Format    string                   `json:"format"`
	Score     int                      `json:"score"`
	Breakdown map[string]int           `json:"breakdown"`
	Penalty   int                      `json:"penalty"`
}

// ScoreCandidate starts at zero; quality exclusions remain hard gates.
func ScoreCandidate(r indexers.ProwlarrRelease, year int, formats []string) Candidate {
	c := Candidate{Key: fmt.Sprintf("%d:%s", r.IndexerID, r.GUID), Release: r, Breakdown: map[string]int{}}
	title := strings.ToLower(r.Title)
	for i, f := range formats {
		if strings.Contains(title, strings.ToLower(f)) {
			c.Format = strings.ToLower(f)
			c.Breakdown["format"] = max(20, 30-i*5)
			break
		}
	}
	if r.Seeders >= 50 {
		c.Breakdown["seeders"] = 20
	} else if r.Seeders > 0 {
		c.Breakdown["seeders"] = 10
	}
	if r.Verified {
		c.Breakdown["verified"] = 15
	}
	if year > 0 && strings.Contains(title, fmt.Sprint(year)) {
		c.Breakdown["year"] = 10
	}
	for _, v := range c.Breakdown {
		c.Score += v
	}
	return c
}

// Acquisition serializes source refresh, dispatch and reconciliation. Durable state
// makes restart recovery independent of HTTP request lifetimes.
// ponytail: one lock serializes all acquisitions; use per-request locks if source latency limits throughput.
type Acquisition struct {
	DB       *database.DB
	Search   func(context.Context, string) ([]indexers.ProwlarrRelease, error)
	Grab     func(context.Context, indexers.ProwlarrRelease) error
	Evaluate func(context.Context, indexers.ProwlarrRelease) (bool, []string, error)
	mu       sync.Mutex
}

func (a *Acquisition) Candidates(ctx context.Context, id int64) ([]Candidate, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT c.release_key,c.release_json,c.score_json,c.score,c.format,COALESCE(f.penalty,0) FROM acquisition_candidates c LEFT JOIN failed_releases f USING(release_key) WHERE request_id=? ORDER BY c.score+COALESCE(f.penalty,0) DESC,c.release_key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Candidate{}
	for rows.Next() {
		var c Candidate
		var release, score string
		if err := rows.Scan(&c.Key, &release, &score, &c.Score, &c.Format, &c.Penalty); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(release), &c.Release); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(score), &c.Breakdown); err != nil {
			return nil, err
		}
		c.Score += c.Penalty
		out = append(out, c)
	}
	return out, rows.Err()
}

func (a *Acquisition) refresh(ctx context.Context, id int64) error {
	var title, author, format string
	var year int
	if err := a.DB.QueryRowContext(ctx, `SELECT title,author,format,year FROM book_requests WHERE id=?`, id).Scan(&title, &author, &format, &year); err != nil {
		return err
	}
	releases, err := a.Search(ctx, title+" "+author)
	if err != nil {
		return err
	}
	candidates := []Candidate{}
	for _, r := range releases {
		if r.GUID == "" || r.IndexerID <= 0 {
			continue
		}
		ok, formats, err := a.Evaluate(ctx, r)
		if err != nil {
			return err
		}
		c := ScoreCandidate(r, year, formats)
		if !ok || (format != "both" && ((format == "m4b" && c.Format != "m4b" && c.Format != "mp3") || (format == "epub" && (c.Format == "m4b" || c.Format == "mp3")))) {
			c.Breakdown["quality"] = -1000
			c.Score -= 1000
		}
		candidates = append(candidates, c)
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Keep active queue references even if a source no longer returns that release.
	if _, err = tx.ExecContext(ctx, `DELETE FROM acquisition_candidates WHERE request_id=? AND queue_id IS NULL`, id); err != nil {
		return err
	}
	for _, c := range candidates {
		r := c.Release
		data, _ := json.Marshal(r)
		scores, _ := json.Marshal(c.Breakdown)
		if _, err = tx.ExecContext(ctx, `INSERT INTO acquisition_candidates(request_id,release_key,release_json,score_json,score,format) VALUES(?,?,?,?,?,?) ON CONFLICT(request_id,release_key) DO UPDATE SET release_json=excluded.release_json,score_json=excluded.score_json,score=excluded.score,format=excluded.format`, id, c.Key, string(data), string(scores), c.Score, c.Format); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE book_requests SET searched_at=CURRENT_TIMESTAMP WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *Acquisition) Refresh(ctx context.Context, id int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refresh(ctx, id)
}

func (a *Acquisition) fail(ctx context.Context, key string) error {
	_, err := a.DB.ExecContext(ctx, `INSERT INTO failed_releases(release_key,penalty) VALUES(?,-100) ON CONFLICT(release_key) DO UPDATE SET penalty=-100`, key)
	return err
}

func (a *Acquisition) dispatch(ctx context.Context, id int64, c Candidate) error {
	ok, _, err := a.Evaluate(ctx, c.Release)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("release excluded by quality profile")
	}
	qid := uuid.NewString()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO download_queue(id,download_client_task_id,title,protocol,size_bytes,status,edition_id) VALUES(?,?,?,?,?,'dispatching',(SELECT e.id FROM book_editions e JOIN book_requests r ON r.book_id=e.book_id WHERE r.id=? ORDER BY (e.format=?) DESC LIMIT 1))`, qid, c.Release.GUID, c.Release.Title, c.Release.Protocol, c.Release.Size, id, c.Format); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE acquisition_candidates SET queue_id=? WHERE request_id=? AND release_key=?`, qid, id, c.Key); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE book_requests SET status='downloading',attempts=attempts+1,updated_at=CURRENT_TIMESTAMP WHERE id=?`, id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = a.Grab(ctx, c.Release); err != nil {
		if _, e := a.DB.ExecContext(ctx, `UPDATE download_queue SET status='failed',error_message=? WHERE id=?`, err.Error(), qid); e != nil {
			return e
		}
		return fmt.Errorf("%w: %v", errTransferFailed, err)
	}
	_, err = a.DB.ExecContext(ctx, `UPDATE download_queue SET status='queued' WHERE id=?`, qid)
	return err
}

func (a *Acquisition) Download(ctx context.Context, id int64, key string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var status string
	if err := a.DB.QueryRowContext(ctx, `SELECT status FROM book_requests WHERE id=?`, id).Scan(&status); err != nil {
		return err
	}
	if status == StatusDownloading {
		return fmt.Errorf("a download is already active")
	}
	candidates, err := a.Candidates(ctx, id)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if c.Key == key {
			err = a.dispatch(ctx, id, c)
			if errors.Is(err, errTransferFailed) {
				if e := a.fail(ctx, key); e != nil {
					return e
				}
			}
			return err
		}
	}
	return ErrNotFound
}

func (a *Acquisition) process(ctx context.Context, id int64) error {
	var attempts int
	var searched *time.Time
	var status string
	if err := a.DB.QueryRowContext(ctx, `SELECT attempts,searched_at,status FROM book_requests WHERE id=?`, id).Scan(&attempts, &searched, &status); err != nil {
		return err
	}
	candidates, err := a.Candidates(ctx, id)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		var state string
		err := a.DB.QueryRowContext(ctx, `SELECT q.status FROM download_queue q JOIN acquisition_candidates c ON c.queue_id=q.id WHERE c.request_id=? AND c.release_key=?`, id, c.Key).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		switch state {
		case "queued", "downloading", "dispatching":
			return nil
		case "completed":
			_, err = a.DB.ExecContext(ctx, `UPDATE book_requests SET status='available',completed_at=COALESCE(completed_at,CURRENT_TIMESTAMP),book_id=COALESCE(book_id,(SELECT e.book_id FROM acquisition_candidates c JOIN download_queue q ON q.id=c.queue_id JOIN book_editions e ON e.id=q.edition_id WHERE c.request_id=book_requests.id AND q.status='completed' LIMIT 1)),updated_at=CURRENT_TIMESTAMP WHERE id=?`, id)
			return err
		case "failed", "error":
			if err = a.fail(ctx, c.Key); err != nil {
				return err
			}
		}
	}
	if status == StatusAvailable {
		return nil
	}
	if attempts >= 3 {
		_, err = a.DB.ExecContext(ctx, `UPDATE book_requests SET status='error' WHERE id=?`, id)
		return err
	}
	if searched == nil || time.Since(*searched) >= 24*time.Hour {
		if err = a.refresh(ctx, id); err != nil {
			return err
		}
	}
	candidates, err = a.Candidates(ctx, id)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if attempts >= 3 {
			break
		}
		if c.Score <= 0 || c.Penalty < 0 {
			continue
		}
		attempts++
		if err = a.dispatch(ctx, id, c); err == nil {
			return nil
		}
		if !errors.Is(err, errTransferFailed) {
			return err
		}
		if e := a.fail(ctx, c.Key); e != nil {
			return e
		}
	}
	status = StatusSearching
	if attempts >= 3 {
		status = StatusError
	}
	_, err = a.DB.ExecContext(ctx, `UPDATE book_requests SET status=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, status, id)
	return err
}

func (a *Acquisition) Purge(ctx context.Context) error {
	_, err := a.DB.ExecContext(ctx, `DELETE FROM acquisition_candidates WHERE request_id IN (SELECT id FROM book_requests WHERE completed_at IS NOT NULL AND julianday(completed_at)<=julianday('now','-30 days'))`)
	return err
}

func (a *Acquisition) Tick(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.Purge(ctx); err != nil {
		return err
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT id FROM book_requests WHERE status IN ('searching','downloading')`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		if err := a.process(ctx, id); err != nil && first == nil {
			first = err
		}
	}
	return first
}
