package downloaders

import (
	"context"
	"errors"
)

// Standard categories configured in download clients for Callimachus.
const (
	CategoryEBooks     = "callimachus-ebooks"
	CategoryAudiobooks = "callimachus-audiobooks"
)

// Supported download client driver types.
const (
	TypeQBittorrent  = "qbittorrent"
	TypeTransmission = "transmission"
	TypeSABnzbd      = "sabnzbd"
)

// Common errors returned by download client drivers.
var (
	// ErrTaskNotFound indicates the requested download task does not exist on the client.
	ErrTaskNotFound = errors.New("download task not found")

	// ErrUnsupportedOperation is returned when a client does not support an operation (e.g. NZB on torrent client).
	ErrUnsupportedOperation = errors.New("unsupported operation for download client")

	// ErrAuthenticationFailed indicates invalid credentials or failed login handshake.
	ErrAuthenticationFailed = errors.New("download client authentication failed")
)

// TaskStatus represents the normalized real-time status of a download task.
type TaskStatus struct {
	TaskID          string  `json:"task_id"`
	Name            string  `json:"name"`
	Category        string  `json:"category"`
	Progress        float64 `json:"progress"` // Normalized percentage: 0.0 to 100.0
	DownloadRate    int64   `json:"download_rate"`
	TotalBytes      int64   `json:"total_bytes"`
	DownloadedBytes int64   `json:"downloaded_bytes"`
	EtaSeconds      int64   `json:"eta_seconds"`
	State           string  `json:"state"`
	SavePath        string  `json:"save_path"`
	ContentPath     string  `json:"content_path"`
	IsCompleted     bool    `json:"is_completed"`
}

// DownloadClient defines the unified interface for interacting with torrent and Usenet download clients.
type DownloadClient interface {
	// Type returns the client driver identifier (e.g. "qbittorrent", "transmission", "sabnzbd").
	Type() string

	// AddTorrent enqueues a torrent via .torrent URL or magnet URI with a target category.
	AddTorrent(ctx context.Context, urlOrMagnet string, category string) (string, error)

	// AddNZB enqueues an NZB via URL with a target category.
	AddNZB(ctx context.Context, nzbURL string, category string) (string, error)

	// GetTaskStatus retrieves the normalized real-time status of a task by its taskID (hash or nzo_id).
	GetTaskStatus(ctx context.Context, taskID string) (*TaskStatus, error)

	// RemoveTask removes the task from the download client, optionally deleting payload files on disk.
	RemoveTask(ctx context.Context, taskID string, deleteFiles bool) error
}
