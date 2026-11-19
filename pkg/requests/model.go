package requests

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Request status constants
const (
	StatusSearching   = "searching"
	StatusDownloading = "downloading"
	StatusAvailable   = "available"
	StatusError       = "error"
	StatusCancelled   = "cancelled"
)

// Request format constants
const (
	FormatEPUB = "epub"
	FormatM4B  = "m4b"
	FormatBoth = "both"
)

// BookRequest represents an Overseerr-style media acquisition request.
type BookRequest struct {
	Stage     string    `json:"stage"`
	Year      int       `json:"year,omitempty"`
	CoverURL  string    `json:"cover_url,omitempty"`
	ID        int64     `json:"id" db:"id"`
	UserID    string    `json:"user_id" db:"user_id"`
	Username  string    `json:"username" db:"username"`
	BookID    *string   `json:"book_id,omitempty" db:"book_id"`
	Title     string    `json:"title" db:"title"`
	Author    string    `json:"author" db:"author"`
	Format    string    `json:"format" db:"format"`
	Status    string    `json:"status" db:"status"`
	Monitored bool      `json:"monitored" db:"monitored"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// CreateRequestInput holds parameters for submitting a book request.
type CreateRequestInput struct {
	Synopsis    string   `json:"synopsis"`
	Series      string   `json:"series"`
	SeriesIndex *float64 `json:"series_index"`
	CoverURL    string   `json:"cover_url"`
	ManualOnly  bool     `json:"manual_only"`
	Year        int      `json:"year"`
	UserID      string   `json:"user_id"`
	BookID      *string  `json:"book_id,omitempty"`
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	Format      string   `json:"format"`
	UserRole    string   `json:"user_role,omitempty"`
}

// RequestFilter defines search, filtering, and pagination parameters.
type RequestFilter struct {
	UserID string `json:"user_id,omitempty"`
	Status string `json:"status,omitempty"`
	Format string `json:"format,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

// Error definitions
var (
	ErrNotFound          = errors.New("request not found")
	ErrForbidden         = errors.New("forbidden")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrQuotaExceeded     = errors.New("request quota exceeded")
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrInvalidStatus     = errors.New("invalid request status")
	ErrInvalidFormat     = errors.New("invalid request format")
	ErrInvalidInput      = errors.New("invalid request input")
)

// validTransitions defines permissible state transitions for book requests.
var validTransitions = map[string]map[string]bool{
	StatusSearching:   {StatusDownloading: true, StatusError: true},
	StatusDownloading: {StatusAvailable: true, StatusSearching: true, StatusError: true},
	StatusError:       {StatusSearching: true, StatusDownloading: true},
	StatusAvailable:   {},
	StatusCancelled:   {},
}

// IsValidStatus returns true if the status string is a recognized request status.
func IsValidStatus(status string) bool {
	switch strings.ToLower(status) {
	case StatusSearching, StatusDownloading, StatusAvailable, StatusError, StatusCancelled:
		return true
	default:
		return false
	}
}

// IsValidFormat returns true if the format string is epub, m4b, or both.
func IsValidFormat(format string) bool {
	switch strings.ToLower(format) {
	case FormatEPUB, FormatM4B, FormatBoth:
		return true
	default:
		return false
	}
}

// CanTransition returns true if the transition from `from` status to `to` status is allowed.
func CanTransition(from, to string) bool {
	allowed, ok := validTransitions[strings.ToLower(from)]
	if !ok {
		return false
	}
	return allowed[strings.ToLower(to)]
}

// ValidateTransition returns nil if the state transition is valid, or ErrInvalidTransition otherwise.
func ValidateTransition(from, to string) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: cannot transition from %q to %q", ErrInvalidTransition, from, to)
	}
	return nil
}
