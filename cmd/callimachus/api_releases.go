package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/indexers"
)

func (s *Server) prowlarrClient(r *http.Request) (*indexers.ProwlarrClient, error) {
	cfg, err := config.Load(r.Context(), s.db, s.baseOverrides)
	if err != nil {
		return nil, err
	}
	return indexers.NewProwlarrClient(cfg.Settings.ProwlarrURL, cfg.Settings.ProwlarrAPIKey, s.prowlarrHTTPClient), nil
}

func (s *Server) handleSearchReleases(w http.ResponseWriter, r *http.Request) {
	client, err := s.prowlarrClient(r)
	if err != nil || client.BaseURL == "" || client.APIKey == "" {
		http.Error(w, "Configure the Prowlarr URL and API key in Settings", http.StatusServiceUnavailable)
		return
	}
	releases, err := client.Search(r.Context(), r.URL.Query().Get("query"), indexers.DefaultCategories)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(releases)
}

func (s *Server) handleGrabRelease(w http.ResponseWriter, r *http.Request) {
	var release indexers.ProwlarrRelease
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&release); err != nil || release.IndexerID <= 0 || strings.TrimSpace(release.GUID) == "" || strings.TrimSpace(release.Title) == "" {
		http.Error(w, "Select a search result with a GUID, indexer ID, and title", http.StatusBadRequest)
		return
	}
	client, err := s.prowlarrClient(r)
	if err != nil || client.BaseURL == "" || client.APIKey == "" {
		http.Error(w, "Configure the Prowlarr URL and API key in Settings", http.StatusServiceUnavailable)
		return
	}
	resolved, err := config.Load(r.Context(), s.db, s.baseOverrides)
	if err != nil {
		http.Error(w, "Unable to resolve quality profile", http.StatusInternalServerError)
		return
	}
	profile := resolved.Settings.ToQualityProfile()
	if ok, reason := profile.EvaluateRelease(downloaders.ReleaseCandidate{Title: release.Title, Size: release.Size}); !ok {
		http.Error(w, reason, http.StatusBadRequest)
		return
	}
	// Persist before dispatch so a database failure cannot lose a successful grab.
	id := uuid.NewString()
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO download_queue (id, download_client_task_id, title, protocol, size_bytes, status) VALUES (?, ?, ?, ?, ?, 'dispatching')`, id, release.GUID, release.Title, release.Protocol, release.Size)
	if err != nil {
		http.Error(w, "Unable to queue release", http.StatusInternalServerError)
		return
	}
	if err := client.Grab(r.Context(), release); err != nil {
		_, _ = s.db.ExecContext(r.Context(), `UPDATE download_queue SET status = 'failed', error_message = ? WHERE id = ?`, err.Error(), id)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE download_queue SET status = 'queued' WHERE id = ?`, id); err != nil {
		http.Error(w, "Release dispatched but queue update failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
