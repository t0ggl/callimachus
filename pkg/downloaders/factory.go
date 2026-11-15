package downloaders

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/t0ggl/callimachus/pkg/database"
)

// NewClientFromConfig creates a concrete DownloadClient based on a database.DownloadClient record.
func NewClientFromConfig(cfg *database.DownloadClient, httpClient *http.Client) (DownloadClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("download client config cannot be nil")
	}

	scheme := "http"
	if cfg.UseSSL {
		scheme = "https"
	}
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = "localhost"
	}
	baseURL := fmt.Sprintf("%s://%s:%d", scheme, host, cfg.Port)

	username := ""
	if cfg.Username != nil {
		username = *cfg.Username
	}
	password := ""
	if cfg.Password != nil {
		password = *cfg.Password
	}
	apiToken := ""
	if cfg.APIToken != nil {
		apiToken = *cfg.APIToken
	}

	switch strings.ToLower(cfg.Type) {
	case TypeQBittorrent:
		return NewQBittorrentClient(QBittorrentConfig{
			BaseURL:    baseURL,
			Username:   username,
			Password:   password,
			HTTPClient: httpClient,
		}), nil
	case TypeTransmission:
		return NewTransmissionClient(TransmissionConfig{
			BaseURL:    baseURL,
			Username:   username,
			Password:   password,
			HTTPClient: httpClient,
		}), nil
	case TypeSABnzbd:
		return NewSABnzbdClient(SABnzbdConfig{
			BaseURL:    baseURL,
			APIKey:     apiToken,
			HTTPClient: httpClient,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported download client type: %s", cfg.Type)
	}
}
