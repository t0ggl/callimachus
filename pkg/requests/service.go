package requests

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

// ServiceConfig holds configuration for the Overseerr-style book request workflow.
type ServiceConfig struct {
	WeeklyLimit   int
	MonthlyLimit  int
	ExemptRoles   map[string]bool
	SearchTrigger SearchTrigger
}

// DefaultServiceConfig returns default request service configuration.
func DefaultServiceConfig() ServiceConfig {
	return ServiceConfig{
		WeeklyLimit:  5,
		MonthlyLimit: 20,
		ExemptRoles: map[string]bool{
			auth.RoleAdmin:   true,
			auth.RoleCurator: true,
		},
		SearchTrigger: NoopSearchTrigger{},
	}
}

// RequestService manages book acquisition requests, quotas, and state transitions.
type RequestService struct {
	db       *database.DB
	cfg      ServiceConfig
	mu       sync.RWMutex
	submitMu sync.Mutex
}

// NewRequestService creates a new RequestService instance.
func NewRequestService(db *database.DB, cfgs ...ServiceConfig) *RequestService {
	cfg := DefaultServiceConfig()
	if len(cfgs) > 0 {
		c := cfgs[0]
		if c.WeeklyLimit != 0 {
			cfg.WeeklyLimit = c.WeeklyLimit
		}
		if c.MonthlyLimit != 0 {
			cfg.MonthlyLimit = c.MonthlyLimit
		}
		if c.ExemptRoles != nil {
			cfg.ExemptRoles = c.ExemptRoles
		}
		if c.SearchTrigger != nil {
			cfg.SearchTrigger = c.SearchTrigger
		}
	}

	return &RequestService{
		db:  db,
		cfg: cfg,
	}
}

// SetSearchTrigger updates the search trigger hook.
func (s *RequestService) SetSearchTrigger(trigger SearchTrigger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if trigger == nil {
		s.cfg.SearchTrigger = NoopSearchTrigger{}
	} else {
		s.cfg.SearchTrigger = trigger
	}
}

// SetQuotaLimits updates the weekly and monthly limits.
func (s *RequestService) SetQuotaLimits(weekly, monthly int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.WeeklyLimit = weekly
	s.cfg.MonthlyLimit = monthly
}

func (s *RequestService) isRoleExempt(role string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.ExemptRoles[role] || auth.HasRole(role, auth.RoleCurator)
}

// SubmitRequest validates, checks quotas and queues acquisition.
func (s *RequestService) SubmitRequest(ctx context.Context, input CreateRequestInput) (*BookRequest, error) {
	if strings.TrimSpace(input.UserID) == "" {
		return nil, fmt.Errorf("%w: user_id is required", ErrInvalidInput)
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		return nil, fmt.Errorf("%w: title is required", ErrInvalidInput)
	}
	input.Author = strings.TrimSpace(input.Author)
	if input.Author == "" {
		return nil, fmt.Errorf("%w: author is required", ErrInvalidInput)
	}

	input.Format = strings.ToLower(strings.TrimSpace(input.Format))
	if input.Format == "" {
		input.Format = FormatBoth
	}
	if !IsValidFormat(input.Format) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidFormat, input.Format)
	}

	created, err := s.insertRequests(ctx, input)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	trigger := s.cfg.SearchTrigger
	s.mu.RUnlock()
	for _, req := range created {
		slog.InfoContext(ctx, "book request created", "request_id", req.ID, "status", req.Status)
		_ = trigger.TriggerSearch(ctx, req)
	}
	// Keep the existing single-object response for clients opening manual search.
	return created[0], nil
}

func (s *RequestService) insertRequests(ctx context.Context, input CreateRequestInput) ([]*BookRequest, error) {
	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	var username, dbRole string
	err := s.db.QueryRowContext(ctx, "SELECT username, role FROM users WHERE id = ?", input.UserID).Scan(&username, &dbRole)
	if err != nil {
		return nil, err
	}
	if input.UserRole == "" {
		input.UserRole = dbRole
	}

	// Check quotas for non-exempt users
	if !s.isRoleExempt(input.UserRole) {
		s.mu.RLock()
		weeklyLimit := s.cfg.WeeklyLimit
		monthlyLimit := s.cfg.MonthlyLimit
		s.mu.RUnlock()

		now := time.Now().UTC()
		if weeklyLimit > 0 {
			var weeklyCount int
			cutoff := now.Add(-7 * 24 * time.Hour)
			q := "SELECT COUNT(*) FROM book_requests WHERE user_id = ? AND counts_toward_quota = 1 AND created_at >= ?"
			if err := s.db.QueryRowContext(ctx, q, input.UserID, cutoff).Scan(&weeklyCount); err != nil {
				return nil, fmt.Errorf("failed to check weekly request quota: %w", err)
			}
			if weeklyCount >= weeklyLimit {
				return nil, fmt.Errorf("%w: weekly limit of %d reached", ErrQuotaExceeded, weeklyLimit)
			}
		}

		if monthlyLimit > 0 {
			var monthlyCount int
			cutoff := now.Add(-30 * 24 * time.Hour)
			q := "SELECT COUNT(*) FROM book_requests WHERE user_id = ? AND counts_toward_quota = 1 AND created_at >= ?"
			if err := s.db.QueryRowContext(ctx, q, input.UserID, cutoff).Scan(&monthlyCount); err != nil {
				return nil, fmt.Errorf("failed to check monthly request quota: %w", err)
			}
			if monthlyCount >= monthlyLimit {
				return nil, fmt.Errorf("%w: monthly limit of %d reached", ErrQuotaExceeded, monthlyLimit)
			}
		}
	}

	formats := []string{input.Format}
	if input.Format == FormatBoth {
		formats = []string{FormatEPUB, FormatM4B}
	}
	providedBookID := ""
	if input.BookID != nil {
		providedBookID = *input.BookID
	}
	needed, err := s.missingActiveFormats(ctx, providedBookID, input.Title, input.Author, formats)
	if err != nil {
		return nil, err
	}
	if len(needed) == 0 {
		return nil, fmt.Errorf("%w: active request already exists for %q (%s)", ErrDuplicateRequest, input.Title, input.Format)
	}

	if input.BookID == nil || *input.BookID == "" {
		c := catalog.New(s.db)
		author, err := c.GetOrCreateAuthor(ctx, input.Author)
		if err != nil {
			return nil, err
		}
		book := &database.Book{AuthorID: author.ID, Title: input.Title, Synopsis: &input.Synopsis, SeriesIndex: input.SeriesIndex, Monitored: !input.ManualOnly}
		if input.Year > 0 {
			date := fmt.Sprintf("%04d", input.Year)
			book.ReleaseDate = &date
		}
		if strings.TrimSpace(input.Series) != "" {
			series, err := c.GetOrCreateSeries(ctx, input.Series, author.ID, input.HardcoverSeriesID)
			if err != nil {
				return nil, err
			}
			if series != nil {
				book.SeriesID = &series.ID
			}
		}
		book, err = c.GetOrCreateBook(ctx, book)
		if err != nil {
			return nil, err
		}
		input.BookID = &book.ID
		needed, err = s.missingActiveFormats(ctx, *input.BookID, input.Title, input.Author, formats)
		if err != nil {
			return nil, err
		}
		if len(needed) == 0 {
			return nil, fmt.Errorf("%w: active request already exists for %q (%s)", ErrDuplicateRequest, input.Title, input.Format)
		}
	}
	if cover, err := url.Parse(input.CoverURL); err != nil || cover.Host == "" || (cover.Scheme != "https" && cover.Scheme != "http") || cover.User != nil {
		input.CoverURL = ""
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	status := StatusSearching
	if input.ManualOnly {
		status = StatusError
	}
	var created []*BookRequest
	for i, format := range needed {
		res, err := tx.ExecContext(ctx, `INSERT INTO book_requests
			(user_id, book_id, title, author, format, status, created_at, updated_at, year, counts_toward_quota, cover_url)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			input.UserID, input.BookID, input.Title, input.Author, format, status, now, now, input.Year, i == 0, input.CoverURL)
		if err != nil {
			return nil, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		created = append(created, &BookRequest{ID: id, UserID: input.UserID, Username: username, BookID: input.BookID,
			Title: input.Title, Author: input.Author, Format: format, Status: status, Monitored: !input.ManualOnly,
			CreatedAt: now, UpdatedAt: now, Year: input.Year, CoverURL: input.CoverURL, Stage: "queued"})
		if !input.ManualOnly {
			if _, err := tx.ExecContext(ctx, "UPDATE book_editions SET monitored=1, updated_at=? WHERE book_id=? AND format=?", now, input.BookID, format); err != nil {
				return nil, err
			}
		}
	}
	if input.ManualOnly {
		if _, err := tx.ExecContext(ctx, "UPDATE books SET synopsis=COALESCE(NULLIF(synopsis,''), NULLIF(?,'')), updated_at=? WHERE id=?", input.Synopsis, now, input.BookID); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, "UPDATE books SET monitored=1, synopsis=COALESCE(NULLIF(synopsis,''), NULLIF(?,'')), updated_at=? WHERE id=?", input.Synopsis, now, input.BookID); err != nil {
			return nil, err
		}
	}
	return created, tx.Commit()
}

func (s *RequestService) missingActiveFormats(ctx context.Context, bookID, title, author string, requested []string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.format, COALESCE(r.book_id, ''), r.title, r.author, COALESCE(b.title, ''), COALESCE(a.name, '')
		FROM book_requests r
		LEFT JOIN books b ON b.id = r.book_id
		LEFT JOIN authors a ON a.id = b.author_id
		WHERE r.dismissed = 0 AND r.status IN (?, ?, ?)
	`, StatusSearching, StatusDownloading, StatusAvailable)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing requests: %w", err)
	}
	defer rows.Close()

	targetKey := metadata.DiscoveryKey(title, author)
	active := make(map[string]bool)
	for rows.Next() {
		var rFormat, rBookID, rTitle, rAuthor, bTitle, aName string
		if err := rows.Scan(&rFormat, &rBookID, &rTitle, &rAuthor, &bTitle, &aName); err != nil {
			return nil, fmt.Errorf("failed to scan existing request: %w", err)
		}
		matched := (bookID != "" && rBookID == bookID) ||
			metadata.DiscoveryKey(rTitle, rAuthor) == targetKey ||
			(bTitle != "" && aName != "" && metadata.DiscoveryKey(bTitle, aName) == targetKey)
		if !matched {
			continue
		}
		switch strings.ToLower(rFormat) {
		case FormatBoth:
			active[FormatEPUB] = true
			active[FormatM4B] = true
		case FormatEPUB, FormatM4B:
			active[strings.ToLower(rFormat)] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate existing requests: %w", err)
	}

	var needed []string
	for _, f := range requested {
		if !active[f] {
			needed = append(needed, f)
		}
	}
	return needed, nil
}

// MarkDownloading transitions a searching request to downloading status.
func (s *RequestService) MarkDownloading(ctx context.Context, requestID int64) error {
	current, err := s.GetRequest(ctx, requestID)
	if err != nil {
		return err
	}

	if err := ValidateTransition(current.Status, StatusDownloading); err != nil {
		return err
	}

	now := time.Now().UTC()
	query := "UPDATE book_requests SET status = ?, updated_at = ? WHERE id = ? AND dismissed = 0"
	_, err = s.db.ExecContext(ctx, query, StatusDownloading, now, requestID)
	return err
}

// MarkAvailable transitions a downloading request to available status.
func (s *RequestService) MarkAvailable(ctx context.Context, requestID int64) error {
	current, err := s.GetRequest(ctx, requestID)
	if err != nil {
		return err
	}

	if err := ValidateTransition(current.Status, StatusAvailable); err != nil {
		return err
	}

	now := time.Now().UTC()
	query := "UPDATE book_requests SET status = ?, updated_at = ? WHERE id = ? AND dismissed = 0"
	_, err = s.db.ExecContext(ctx, query, StatusAvailable, now, requestID)
	return err
}

// GetRequest retrieves a single BookRequest by its unique integer ID.
func (s *RequestService) GetRequest(ctx context.Context, requestID int64) (*BookRequest, error) {
	query := `
		SELECT r.id, r.user_id, COALESCE(u.username, ''), r.book_id, r.title, r.author, r.format, r.status,
		       r.monitored, r.created_at, r.updated_at, r.year, r.cover_url,
		       CASE WHEN EXISTS (SELECT 1 FROM download_queue q WHERE q.request_id=r.id AND q.status IN ('importing','converting')) THEN 'importing'
		            WHEN r.status = 'available' THEN 'available'
		            WHEN r.status = 'downloading' THEN 'downloading'
		            ELSE COALESCE((SELECT stage FROM request_events WHERE request_id=r.id AND stage!='' ORDER BY id DESC LIMIT 1), 'queued') END
		FROM book_requests r
		LEFT JOIN users u ON r.user_id = u.id
		WHERE r.id = ? AND r.dismissed = 0
	`

	var (
		req        BookRequest
		bookID     sql.NullString
		monitoredI int
	)

	err := s.db.QueryRowContext(ctx, query, requestID).Scan(
		&req.ID,
		&req.UserID,
		&req.Username,
		&bookID,
		&req.Title,
		&req.Author,
		&req.Format,
		&req.Status,
		&monitoredI,
		&req.CreatedAt,
		&req.UpdatedAt, &req.Year, &req.CoverURL, &req.Stage,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get request %d: %w", requestID, err)
	}

	if bookID.Valid {
		req.BookID = &bookID.String
	}
	req.Monitored = monitoredI == 1
	req.CreatedAt = req.CreatedAt.UTC()
	req.UpdatedAt = req.UpdatedAt.UTC()

	return &req, nil
}

// ListRequests returns a list of BookRequests matching the filter criteria.
func (s *RequestService) ListRequests(ctx context.Context, filter RequestFilter) ([]BookRequest, error) {
	query := `
		SELECT r.id, r.user_id, COALESCE(u.username, ''), r.book_id, r.title, r.author, r.format, r.status,
		       r.monitored, r.created_at, r.updated_at, r.year, r.cover_url,
		       CASE WHEN EXISTS (SELECT 1 FROM download_queue q WHERE q.request_id=r.id AND q.status IN ('importing','converting')) THEN 'importing'
		            WHEN r.status = 'available' THEN 'available'
		            WHEN r.status = 'downloading' THEN 'downloading'
		            ELSE COALESCE((SELECT stage FROM request_events WHERE request_id=r.id AND stage!='' ORDER BY id DESC LIMIT 1), 'queued') END
		FROM book_requests r
		LEFT JOIN users u ON r.user_id = u.id
		WHERE r.dismissed = 0
	`
	var args []any

	if filter.UserID != "" {
		query += " AND r.user_id = ?"
		args = append(args, filter.UserID)
	}
	if filter.Status != "" {
		query += " AND r.status = ?"
		args = append(args, strings.ToLower(filter.Status))
	}
	if filter.Format != "" {
		query += " AND r.format = ?"
		args = append(args, strings.ToLower(filter.Format))
	}

	query += " ORDER BY r.created_at DESC, r.id DESC"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	if filter.Offset > 0 && filter.Limit <= 0 {
		query += " LIMIT -1"
	}
	if filter.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query requests: %w", err)
	}
	defer rows.Close()

	results := make([]BookRequest, 0)
	for rows.Next() {
		var (
			req        BookRequest
			bookID     sql.NullString
			monitoredI int
		)

		err := rows.Scan(
			&req.ID,
			&req.UserID,
			&req.Username,
			&bookID,
			&req.Title,
			&req.Author,
			&req.Format,
			&req.Status,
			&monitoredI,
			&req.CreatedAt,
			&req.UpdatedAt, &req.Year, &req.CoverURL, &req.Stage,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan book request row: %w", err)
		}

		if bookID.Valid {
			req.BookID = &bookID.String
		}
		req.Monitored = monitoredI == 1
		req.CreatedAt = req.CreatedAt.UTC()
		req.UpdatedAt = req.UpdatedAt.UTC()

		results = append(results, req)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return results, nil
}
