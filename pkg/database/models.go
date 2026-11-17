package database

import (
	"time"
)

// Author represents the primary author or creator profile.
type Author struct {
	ID        string    `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	SortName  string    `json:"sort_name" db:"sort_name"`
	Aliases   string    `json:"aliases" db:"aliases"` // JSON array of alternate names
	PhotoURL  *string   `json:"photo_url,omitempty" db:"photo_url"`
	Biography *string   `json:"biography,omitempty" db:"biography"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// Narrator represents an audiobook narrator / voice actor.
type Narrator struct {
	ID        string    `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	SortName  string    `json:"sort_name" db:"sort_name"`
	Aliases   string    `json:"aliases" db:"aliases"` // JSON array of aliases
	PhotoURL  *string   `json:"photo_url,omitempty" db:"photo_url"`
	Biography *string   `json:"biography,omitempty" db:"biography"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// Series represents a named book series.
type Series struct {
	ID          string    `json:"id" db:"id"`
	Title       string    `json:"title" db:"title"`
	Description *string   `json:"description,omitempty" db:"description"`
	TotalBooks  *int      `json:"total_books,omitempty" db:"total_books"`
	Status      string    `json:"status" db:"status"` // 'ongoing', 'completed', 'hiatus'
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Book represents an abstract intellectual work.
type Book struct {
	ID                 string    `json:"id" db:"id"`
	AuthorID           string    `json:"author_id" db:"author_id"`
	SeriesID           *string   `json:"series_id,omitempty" db:"series_id"`
	SeriesIndex        *float64  `json:"series_index,omitempty" db:"series_index"`
	SeriesIndexDisplay *string   `json:"series_index_display,omitempty" db:"series_index_display"`
	Title              string    `json:"title" db:"title"`
	Subtitle           *string   `json:"subtitle,omitempty" db:"subtitle"`
	ReleaseDate        *string   `json:"release_date,omitempty" db:"release_date"`
	Synopsis           *string   `json:"synopsis,omitempty" db:"synopsis"`
	Genres             string    `json:"genres" db:"genres"` // JSON array of genres
	Monitored          bool      `json:"monitored" db:"monitored"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time `json:"updated_at" db:"updated_at"`
}

// BookEdition represents a published edition of a book work.
type BookEdition struct {
	ID              string    `json:"id" db:"id"`
	BookID          string    `json:"book_id" db:"book_id"`
	Format          string    `json:"format" db:"format"` // 'epub', 'm4b', 'pdf', 'azw3'
	ISBN13          *string   `json:"isbn13,omitempty" db:"isbn13"`
	ASIN            *string   `json:"asin,omitempty" db:"asin"`
	HardcoverID     *string   `json:"hardcover_id,omitempty" db:"hardcover_id"`
	OpenLibraryID   *string   `json:"openlibrary_id,omitempty" db:"openlibrary_id"`
	GoodreadsID     *string   `json:"goodreads_id,omitempty" db:"goodreads_id"`
	Publisher       *string   `json:"publisher,omitempty" db:"publisher"`
	PublicationDate *string   `json:"publication_date,omitempty" db:"publication_date"`
	Language        string    `json:"language" db:"language"`
	Monitored       bool      `json:"monitored" db:"monitored"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// EditionNarrator maps narrators to specific book editions.
type EditionNarrator struct {
	EditionID  string `json:"edition_id" db:"edition_id"`
	NarratorID string `json:"narrator_id" db:"narrator_id"`
	Role       string `json:"role" db:"role"` // 'primary', 'co-narrator', 'full_cast'
}

// MediaAsset represents a physical media file stored on disk.
type MediaAsset struct {
	ID              string    `json:"id" db:"id"`
	EditionID       string    `json:"edition_id" db:"edition_id"`
	FilePath        string    `json:"file_path" db:"file_path"`
	FileSize        int64     `json:"file_size" db:"file_size"`
	Checksum        *string   `json:"checksum,omitempty" db:"checksum"`
	AudioCodec      *string   `json:"audio_codec,omitempty" db:"audio_codec"`
	Bitrate         *int      `json:"bitrate,omitempty" db:"bitrate"`
	SampleRate      *int      `json:"sample_rate,omitempty" db:"sample_rate"`
	Channels        *int      `json:"channels,omitempty" db:"channels"`
	DurationSeconds *float64  `json:"duration_seconds,omitempty" db:"duration_seconds"`
	Chapters        string    `json:"chapters" db:"chapters"` // JSON array of chapters
	IsVerified      bool      `json:"is_verified" db:"is_verified"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// TranscodeQueueItem represents an entry in the decoupled transcode queue.
type TranscodeQueueItem struct {
	ID              string     `json:"id" db:"id"`
	EditionID       string     `json:"edition_id" db:"edition_id"`
	SourcePath      string     `json:"source_path" db:"source_path"`
	TargetPath      string     `json:"target_path" db:"target_path"`
	Status          string     `json:"status" db:"status"` // 'pending', 'processing', 'completed', 'failed', 'cancelled'
	ProgressPercent float64    `json:"progress_percent" db:"progress_percent"`
	CurrentStage    string     `json:"current_stage" db:"current_stage"`
	ErrorMessage    *string    `json:"error_message,omitempty" db:"error_message"`
	WorkerID        *string    `json:"worker_id,omitempty" db:"worker_id"`
	Attempts        int        `json:"attempts" db:"attempts"`
	MaxAttempts     int        `json:"max_attempts" db:"max_attempts"`
	Priority        int        `json:"priority" db:"priority"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`
	StartedAt       *time.Time `json:"started_at,omitempty" db:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty" db:"completed_at"`
}

// TranscodeQueue alias for TranscodeQueueItem.
type TranscodeQueue = TranscodeQueueItem

// Indexer represents a Prowlarr Torznab / Newznab metadata & download source.
type Indexer struct {
	ID         string     `json:"id" db:"id"`
	Name       string     `json:"name" db:"name"`
	Protocol   string     `json:"protocol" db:"protocol"` // 'torznab', 'newznab'
	URL        string     `json:"url" db:"url"`
	APIKey     string     `json:"api_key" db:"api_key"`
	Categories string     `json:"categories" db:"categories"` // JSON array of category IDs
	Enabled    bool       `json:"enabled" db:"enabled"`
	Priority   int        `json:"priority" db:"priority"`
	LastSyncAt *time.Time `json:"last_sync_at,omitempty" db:"last_sync_at"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at" db:"updated_at"`
}

// DownloadQueueItem represents an active or ingested client download task.
type DownloadQueueItem struct {
	ID                   string    `json:"id" db:"id"`
	ClientID             string    `json:"client_id" db:"client_id"`
	EditionID            *string   `json:"edition_id,omitempty" db:"edition_id"`
	DownloadClientTaskID string    `json:"download_client_task_id" db:"download_client_task_id"`
	Title                string    `json:"title" db:"title"`
	Protocol             string    `json:"protocol" db:"protocol"` // 'torrent', 'usenet'
	InfoHash             *string   `json:"info_hash,omitempty" db:"info_hash"`
	Status               string    `json:"status" db:"status"` // 'queued', 'downloading', 'completed', 'staged', 'importing', 'failed'
	ProgressPercent      float64   `json:"progress_percent" db:"progress_percent"`
	SizeBytes            int64     `json:"size_bytes" db:"size_bytes"`
	StagingPath          *string   `json:"staging_path,omitempty" db:"staging_path"`
	ErrorMessage         *string   `json:"error_message,omitempty" db:"error_message"`
	CreatedAt            time.Time `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time `json:"updated_at" db:"updated_at"`
}

// DownloadQueue alias for DownloadQueueItem.
type DownloadQueue = DownloadQueueItem

// BlocklistItem represents a blacklisted info-hash or rejected release.
type BlocklistItem struct {
	ID           string    `json:"id" db:"id"`
	InfoHash     *string   `json:"info_hash,omitempty" db:"info_hash"`
	Title        string    `json:"title" db:"title"`
	IndexerName  *string   `json:"indexer_name,omitempty" db:"indexer_name"`
	Reason       string    `json:"reason" db:"reason"`
	SourceFormat *string   `json:"source_format,omitempty" db:"source_format"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// Blocklist alias for BlocklistItem.
type Blocklist = BlocklistItem

// User represents an authenticated user account (OIDC or local admin).
type User struct {
	ID           string     `json:"id" db:"id"`
	Username     string     `json:"username" db:"username"`
	Email        string     `json:"email" db:"email"`
	Role         string     `json:"role" db:"role"`               // 'admin', 'curator', 'requester', 'guest'
	AuthSource   string     `json:"auth_source" db:"auth_source"` // 'oidc', 'local'
	OIDCSub      *string    `json:"oidc_sub,omitempty" db:"oidc_sub"`
	PasswordHash *string    `json:"password_hash,omitempty" db:"password_hash"`
	AvatarURL    *string    `json:"avatar_url,omitempty" db:"avatar_url"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty" db:"last_login_at"`
}

// UserRequest represents an Overseerr-style media acquisition request.
type UserRequest struct {
	ID              string     `json:"id" db:"id"`
	UserID          string     `json:"user_id" db:"user_id"`
	BookID          *string    `json:"book_id,omitempty" db:"book_id"`
	RequestedTitle  string     `json:"requested_title" db:"requested_title"`
	RequestedAuthor string     `json:"requested_author" db:"requested_author"`
	Format          string     `json:"format" db:"format"` // 'epub', 'm4b', 'both'
	Status          string     `json:"status" db:"status"` // 'pending', 'approved', 'rejected', 'downloading', 'available'
	RejectionReason *string    `json:"rejection_reason,omitempty" db:"rejection_reason"`
	RequestedAt     time.Time  `json:"requested_at" db:"requested_at"`
	ProcessedAt     *time.Time `json:"processed_at,omitempty" db:"processed_at"`
	ProcessedBy     *string    `json:"processed_by,omitempty" db:"processed_by"`
}

// Setting represents a persistent application key-value configuration.
type Setting struct {
	Key       string    `json:"key" db:"key"`
	Value     string    `json:"value" db:"value"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}
