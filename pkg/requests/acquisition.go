package requests

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/t0ggl/callimachus/internal/uuid"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

var errTransferFailed = errors.New("download dispatch failed")

const exclusionPenalty = -1000

// Evaluation captures the quality-profile evaluation and ordered format
// preferences for a candidate release.
type Evaluation struct {
	Format           string
	EbookPreferences []string
	AudioPreferences []string
	Exclusions       map[string]string
}

type Candidate struct {
	Key       string                   `json:"key"`
	Release   indexers.ProwlarrRelease `json:"release"`
	Format    string                   `json:"format"`
	Score     int                      `json:"score"`
	Breakdown map[string]int           `json:"breakdown"`
	Penalty   int                      `json:"penalty"`
}

func (c Candidate) excludedCriteria() []string {
	var out []string
	for k, v := range c.Breakdown {
		if v < 0 {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// IsExcluded reports whether any hard-gate criterion marked this candidate down.
func (c Candidate) IsExcluded() bool {
	return len(c.excludedCriteria()) > 0
}

func formatPreferenceScore(index int) int {
	return max(5, 30-index*5)
}

// ScoreCandidate starts at zero and records each positive signal and each
// individual exclusion criterion in Breakdown.
func ScoreCandidate(r indexers.ProwlarrRelease, year int, requestFormat string, eval Evaluation) Candidate {
	c := Candidate{
		Key:       fmt.Sprintf("%d:%s", r.IndexerID, r.GUID),
		Release:   r,
		Format:    strings.ToLower(strings.TrimPrefix(strings.TrimSpace(eval.Format), ".")),
		Breakdown: map[string]int{},
	}
	title := strings.ToLower(r.Title)

	audioPrefs := eval.AudioPreferences
	if len(audioPrefs) == 0 {
		audioPrefs = []string{"m4b", "mp3"}
	}

	// Fallback format detection from title when caller did not pre-detect it.
	if c.Format == "" {
		for _, f := range eval.EbookPreferences {
			lf := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "."))
			if lf != "" && strings.Contains(title, lf) {
				c.Format = lf
				break
			}
		}
	}
	if c.Format == "" {
		for _, f := range audioPrefs {
			lf := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "."))
			if lf != "" && strings.Contains(title, lf) {
				c.Format = lf
				break
			}
		}
	}

	// Format criterion: either excluded (-1000) or scored by settings preference order.
	_, formatExcluded := eval.Exclusions["format"]
	if formatExcluded || !formatMatchesRequest(requestFormat, c.Format) {
		c.Breakdown["format"] = exclusionPenalty
	} else if c.Format != "" {
		prefs := eval.EbookPreferences
		if c.Format == "m4b" || c.Format == "mp3" {
			prefs = audioPrefs
		}
		for i, f := range prefs {
			if strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), ".")) == c.Format {
				c.Breakdown["format"] = formatPreferenceScore(i)
				break
			}
		}
	}

	// Record each non-format exclusion criterion individually.
	for criterion := range eval.Exclusions {
		if criterion == "format" {
			continue
		}
		c.Breakdown[criterion] = exclusionPenalty
	}
	if _, already := c.Breakdown["multi-book"]; !already && metadata.IsMultiBookRelease(r.Title) {
		c.Breakdown["multi-book"] = exclusionPenalty
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
	Search   func(context.Context, string, string) ([]indexers.ProwlarrRelease, error)
	Grab     func(context.Context, indexers.ProwlarrRelease) error
	Evaluate func(context.Context, indexers.ProwlarrRelease) (Evaluation, error)
	mu       sync.Mutex
	activeMu sync.Mutex
	activeID int64
	cancel   context.CancelFunc
}

// run serializes acquisition operations while letting dismissal interrupt active I/O.
func (a *Acquisition) run(ctx context.Context, id int64, work func(context.Context, int64) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	a.activeMu.Lock()
	a.activeID, a.cancel = id, cancel
	a.activeMu.Unlock()
	defer func() {
		a.activeMu.Lock()
		a.cancel = nil
		a.activeMu.Unlock()
		cancel()
	}()
	err := work(ctx, id)
	if err != nil && ctx.Err() == nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrNotFound) {
		// External errors can contain URLs or credentials. Persist a safe summary.
		_, logErr := a.DB.ExecContext(ctx, `INSERT INTO request_events(request_id,kind,message)
			SELECT id,'error','Acquisition could not continue. Check indexer or download settings.'
			FROM book_requests WHERE id=? AND dismissed=0 AND COALESCE((
				SELECT kind FROM request_events WHERE request_id=book_requests.id ORDER BY id DESC LIMIT 1
			),'') NOT IN ('error','search_error')`, id)
		if logErr != nil {
			return errors.Join(err, logErr)
		}
	}
	return err
}

func (a *Acquisition) Stop(id int64) {
	a.activeMu.Lock()
	defer a.activeMu.Unlock()
	if a.activeID == id && a.cancel != nil {
		a.cancel()
	}
}

func (a *Acquisition) logEvent(ctx context.Context, id int64, kind, stage, message string) error {
	_, err := a.DB.ExecContext(ctx, `INSERT INTO request_events(request_id,kind,stage,message)
		SELECT id,?,?,? FROM book_requests WHERE id=? AND dismissed=0`, kind, stage, message, id)
	return err
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

func formatMatchesRequest(requestFormat, releaseFormat string) bool {
	if releaseFormat == "" {
		return true
	}
	audio := releaseFormat == "m4b" || releaseFormat == "mp3"
	switch requestFormat {
	case FormatEPUB:
		return !audio
	case FormatM4B:
		return audio
	default:
		return true
	}
}

func (a *Acquisition) refresh(ctx context.Context, id int64) error {
	var title, author, format string
	var year int
	if err := a.DB.QueryRowContext(ctx, `SELECT title,author,format,year FROM book_requests WHERE id=? AND dismissed=0`, id).Scan(&title, &author, &format, &year); err != nil {
		return err
	}
	slog.DebugContext(ctx, "acquisition search started", "request_id", id)
	if err := a.logEvent(ctx, id, "search_started", "searching", "Querying enabled Prowlarr indexers"); err != nil {
		return err
	}
	releases, err := a.Search(ctx, title+" "+author, format)
	if err != nil {
		slog.WarnContext(ctx, "acquisition search failed", "request_id", id)
		return errors.Join(err, a.logEvent(ctx, id, "search_error", "searching", "Indexer search failed. Automatic searches retry in one minute."))
	}
	if err := a.logEvent(ctx, id, "indexers_queried", "evaluating", fmt.Sprintf("Prowlarr returned %d releases", len(releases))); err != nil {
		return err
	}
	candidates := []Candidate{}
	invalid := 0
	for _, r := range releases {
		if r.GUID == "" || r.IndexerID <= 0 {
			invalid++
			continue
		}
		eval, err := a.Evaluate(ctx, r)
		if err != nil {
			return err
		}
		c := ScoreCandidate(r, year, format, eval)
		slog.DebugContext(ctx, "acquisition candidate scored", "request_id", id, "indexer_id", r.IndexerID, "score", c.Score)
		candidates = append(candidates, c)
	}
	if invalid > 0 {
		if err := a.logEvent(ctx, id, "candidate_rejected", "", fmt.Sprintf("%d releases lacked an indexer or release identifier", invalid)); err != nil {
			return err
		}
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
		kind, message := "candidate_scored", fmt.Sprintf("%s (indexer %d) scored %d", r.Title, r.IndexerID, c.Score)
		if excluded := c.excludedCriteria(); len(excluded) > 0 {
			kind, message = "candidate_rejected", fmt.Sprintf("%s (indexer %d) excluded (%s)", r.Title, r.IndexerID, strings.Join(excluded, ", "))
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO request_events(request_id,kind,message)
			SELECT id,?,? FROM book_requests WHERE id=? AND dismissed=0`, kind, message, id); err != nil {
			return err
		}
		data, _ := json.Marshal(r)
		scores, _ := json.Marshal(c.Breakdown)
		if _, err = tx.ExecContext(ctx, `INSERT INTO acquisition_candidates(request_id,release_key,release_json,score_json,score,format) VALUES(?,?,?,?,?,?) ON CONFLICT(request_id,release_key) DO UPDATE SET release_json=excluded.release_json,score_json=excluded.score_json,score=excluded.score,format=excluded.format`, id, c.Key, string(data), string(scores), c.Score, c.Format); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE book_requests SET searched_at=CURRENT_TIMESTAMP WHERE id=? AND dismissed=0`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	slog.InfoContext(ctx, "acquisition search completed", "request_id", id, "releases", len(releases), "candidates", len(candidates))
	return nil
}

func (a *Acquisition) Refresh(ctx context.Context, id int64) error {
	return a.run(ctx, id, a.refresh)
}

func (a *Acquisition) fail(ctx context.Context, key string) error {
	_, err := a.DB.ExecContext(ctx, `INSERT INTO failed_releases(release_key,penalty) VALUES(?,-100) ON CONFLICT(release_key) DO UPDATE SET penalty=-100`, key)
	return err
}

func (a *Acquisition) dispatch(ctx context.Context, id int64, c Candidate) error {
	eval, err := a.Evaluate(ctx, c.Release)
	if err != nil {
		return err
	}
	if len(eval.Exclusions) > 0 {
		reasons := make([]string, 0, len(eval.Exclusions))
		for k := range eval.Exclusions {
			reasons = append(reasons, k)
		}
		slices.Sort(reasons)
		return fmt.Errorf("release excluded (%s)", strings.Join(reasons, ", "))
	}
	qid := uuid.NewString()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE book_requests SET status='downloading',attempts=attempts+1,completed_at=NULL,updated_at=CURRENT_TIMESTAMP WHERE id=? AND dismissed=0`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO download_queue(id,request_id,download_client_task_id,title,protocol,size_bytes,status,edition_id)
		VALUES(?,?,?,?,?,?,'dispatching',(SELECT e.id FROM book_editions e JOIN book_requests r ON r.book_id=e.book_id WHERE r.id=? AND e.format=? LIMIT 1))`,
		qid, id, c.Release.GUID, c.Release.Title, c.Release.Protocol, c.Release.Size, id, c.Format); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE acquisition_candidates SET queue_id=? WHERE request_id=? AND release_key=?`, qid, id, c.Key); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	slog.InfoContext(ctx, "requesting Prowlarr grab", "request_id", id, "queue_id", qid, "indexer_id", c.Release.IndexerID)
	if err = a.Grab(ctx, c.Release); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.WarnContext(ctx, "Prowlarr grab failed", "request_id", id, "queue_id", qid, "indexer_id", c.Release.IndexerID)
		if _, e := a.DB.ExecContext(ctx, `UPDATE download_queue SET status='failed',error_message=? WHERE id=? AND status='dispatching'`, "Prowlarr could not grab this release", qid); e != nil {
			return e
		}
		return fmt.Errorf("%w: %v", errTransferFailed, err)
	}
	_, err = a.DB.ExecContext(ctx, `UPDATE download_queue SET status='queued' WHERE id=? AND status='dispatching'`, qid)
	if err == nil {
		slog.InfoContext(ctx, "acquisition dispatched", "request_id", id, "queue_id", qid)
	}
	if err != nil {
		return err
	}
	return a.logEvent(ctx, id, "release_grabbed", "downloading", "Grabbed "+c.Release.Title)
}

func (a *Acquisition) Download(ctx context.Context, id int64, key string) error {
	return a.run(ctx, id, func(ctx context.Context, id int64) error {
		return a.download(ctx, id, key)
	})
}

func (a *Acquisition) download(ctx context.Context, id int64, key string) error {
	var status string
	if err := a.DB.QueryRowContext(ctx, `SELECT status FROM book_requests WHERE id=? AND dismissed=0`, id).Scan(&status); err != nil {
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
			if excluded := c.excludedCriteria(); len(excluded) > 0 {
				return fmt.Errorf("release excluded (%s)", strings.Join(excluded, ", "))
			}
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
	var shouldSearch bool
	if err := a.DB.QueryRowContext(ctx, `SELECT attempts,searched_at,status,
		user_id!='catalog-monitor' OR (
			SELECT COUNT(*) FROM missing_monitored_formats f
			WHERE f.book_id=book_requests.book_id AND book_requests.format IN (f.format, 'both')
		) = CASE format WHEN 'both' THEN 2 ELSE 1 END
		FROM book_requests WHERE id=? AND dismissed=0`, id).Scan(&attempts, &searched, &status, &shouldSearch); err != nil {
		return err
	}
	// Reconcile the current attempt by its queue link. Older completed downloads
	// can coexist with a manual upgrade, so candidate rank cannot identify it.
	var state, key string
	err := a.DB.QueryRowContext(ctx, `SELECT q.status, COALESCE(c.release_key,'')
		FROM download_queue q LEFT JOIN acquisition_candidates c ON c.queue_id=q.id
		WHERE q.request_id=? ORDER BY q.created_at DESC,q.rowid DESC LIMIT 1`, id).Scan(&state, &key)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	switch state {
	case "queued", "downloading", "dispatching", "importing", "converting", "staged":
		return nil
	case "completed":
		_, err = a.DB.ExecContext(ctx, `UPDATE book_requests SET status='available',
			completed_at=CURRENT_TIMESTAMP, book_id=COALESCE(book_id,(SELECT e.book_id FROM download_queue q
				JOIN book_editions e ON e.id=q.edition_id WHERE q.request_id=book_requests.id AND q.status='completed'
				ORDER BY q.created_at DESC,q.rowid DESC LIMIT 1)),updated_at=CURRENT_TIMESTAMP WHERE id=? AND dismissed=0`, id)
		if err != nil {
			return err
		}
		_, err = a.DB.ExecContext(ctx, `UPDATE books
			SET ebook_monitored=CASE WHEN r.format='m4b' AND books.audiobook_monitored IS NOT NULL THEN books.ebook_monitored ELSE 0 END,
			    audiobook_monitored=CASE WHEN r.format='epub' THEN COALESCE(books.audiobook_monitored, 0) ELSE 0 END,
			    updated_at=CURRENT_TIMESTAMP
			FROM book_requests r WHERE r.id=? AND books.id=r.book_id AND (books.ebook_monitored=1 OR books.audiobook_monitored=1)`, id)
		return err
	case "failed", "error":
		if key != "" {
			if err := a.fail(ctx, key); err != nil {
				return err
			}
		}
	}
	if !shouldSearch && (state == "failed" || state == "error") {
		_, err = a.DB.ExecContext(ctx, `UPDATE book_requests SET status='error',updated_at=CURRENT_TIMESTAMP
			WHERE id=? AND dismissed=0`, id)
		return err
	}
	if status == StatusAvailable || !shouldSearch {
		return nil
	}
	if attempts >= 3 {
		_, err = a.DB.ExecContext(ctx, `UPDATE book_requests SET status='error' WHERE id=? AND dismissed=0`, id)
		return err
	}
	if searched == nil || time.Since(*searched) >= 24*time.Hour {
		var backingOff bool
		if err := a.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM request_events
			WHERE request_id=? AND kind='search_error' AND occurred_at > datetime('now','-1 minute'))`, id).Scan(&backingOff); err != nil {
			return err
		}
		if backingOff {
			return nil
		}
		if err = a.refresh(ctx, id); err != nil {
			return err
		}
	}
	candidates, err := a.Candidates(ctx, id)
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
		if attempts > 1 {
			if err := a.logEvent(ctx, id, "retry", "evaluating", fmt.Sprintf("Trying release %d of 3", attempts)); err != nil {
				return err
			}
		}
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
	_, err = a.DB.ExecContext(ctx, `UPDATE book_requests SET status=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND dismissed=0 AND status != ?`, status, id, status)
	return err
}

func (a *Acquisition) Purge(ctx context.Context) error {
	_, err := a.DB.ExecContext(ctx, `DELETE FROM acquisition_candidates WHERE request_id IN (SELECT id FROM book_requests WHERE completed_at IS NOT NULL AND julianday(completed_at)<=julianday('now','-30 days'))`)
	return err
}

func (a *Acquisition) Tick(ctx context.Context) error {
	if err := a.Purge(ctx); err != nil {
		return err
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT id FROM book_requests WHERE dismissed=0 AND status IN ('searching','downloading')`)
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
		if err := a.run(ctx, id, a.process); err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, context.Canceled) {
			slog.WarnContext(ctx, "book request processing failed", "request_id", id)
			if first == nil {
				first = err
			}
		}
	}
	return first
}
