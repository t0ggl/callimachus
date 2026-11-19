package requests

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/database"
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
	db  *database.DB
	cfg ServiceConfig
	mu  sync.RWMutex
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

	// Resolve username and role from users table if available
	var dbUsername, dbRole string
	err := s.db.QueryRowContext(ctx, "SELECT username, role FROM users WHERE id = ?", input.UserID).Scan(&dbUsername, &dbRole)
	if err == nil {
		if input.UserRole == "" {
			input.UserRole = dbRole
		}
	} else if input.UserRole == "" {
		input.UserRole = auth.RoleRequester
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
			q := "SELECT COUNT(*) FROM book_requests WHERE user_id = ? AND created_at >= ?"
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
			q := "SELECT COUNT(*) FROM book_requests WHERE user_id = ? AND created_at >= ?"
			if err := s.db.QueryRowContext(ctx, q, input.UserID, cutoff).Scan(&monthlyCount); err != nil {
				return nil, fmt.Errorf("failed to check monthly request quota: %w", err)
			}
			if monthlyCount >= monthlyLimit {
				return nil, fmt.Errorf("%w: monthly limit of %d reached", ErrQuotaExceeded, monthlyLimit)
			}
		}
	}

	now := time.Now().UTC()
	status := StatusSearching
	if input.ManualOnly {
		status = StatusError
	}
	monitored := true
	insertQuery := `
		INSERT INTO book_requests (
			user_id, book_id, title, author, format, status,
			monitored,
			created_at, updated_at, year
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	monitoredInt := 0
	if monitored {
		monitoredInt = 1
	}

	res, err := s.db.ExecContext(ctx, insertQuery,
		input.UserID,
		input.BookID,
		input.Title,
		input.Author,
		input.Format,
		status,
		monitoredInt,
		now,
		now,
		input.Year,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert book request: %w", err)
	}

	requestID, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get request id: %w", err)
	}
	slog.InfoContext(ctx, "book request created", "request_id", requestID, "status", status)

	req, err := s.GetRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}

	if monitored {
		if input.BookID != nil && *input.BookID != "" {
			_ = s.markBookMonitored(ctx, *input.BookID, input.Format)
		}
		s.mu.RLock()
		trigger := s.cfg.SearchTrigger
		s.mu.RUnlock()
		if trigger != nil {
			_ = trigger.TriggerSearch(ctx, req)
		}
	}

	return req, nil
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
	query := "UPDATE book_requests SET status = ?, updated_at = ? WHERE id = ?"
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
	query := "UPDATE book_requests SET status = ?, updated_at = ? WHERE id = ?"
	_, err = s.db.ExecContext(ctx, query, StatusAvailable, now, requestID)
	return err
}

// GetRequest retrieves a single BookRequest by its unique integer ID.
func (s *RequestService) GetRequest(ctx context.Context, requestID int64) (*BookRequest, error) {
	query := `
		SELECT r.id, r.user_id, COALESCE(u.username, ''), r.book_id, r.title, r.author, r.format, r.status,
		       r.monitored, r.created_at, r.updated_at
		FROM book_requests r
		LEFT JOIN users u ON r.user_id = u.id
		WHERE r.id = ?
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
		&req.UpdatedAt,
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
		       r.monitored, r.created_at, r.updated_at
		FROM book_requests r
		LEFT JOIN users u ON r.user_id = u.id
		WHERE 1=1
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
			&req.UpdatedAt,
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

func (s *RequestService) markBookMonitored(ctx context.Context, bookID string, format string) error {
	now := time.Now().UTC()
	_, _ = s.db.ExecContext(ctx, "UPDATE books SET monitored = 1, updated_at = ? WHERE id = ?", now, bookID)

	switch strings.ToLower(format) {
	case FormatEPUB:
		_, _ = s.db.ExecContext(ctx, "UPDATE book_editions SET monitored = 1, updated_at = ? WHERE book_id = ? AND format = 'epub'", now, bookID)
	case FormatM4B:
		_, _ = s.db.ExecContext(ctx, "UPDATE book_editions SET monitored = 1, updated_at = ? WHERE book_id = ? AND format = 'm4b'", now, bookID)
	default:
		_, _ = s.db.ExecContext(ctx, "UPDATE book_editions SET monitored = 1, updated_at = ? WHERE book_id = ?", now, bookID)
	}

	return nil
}
