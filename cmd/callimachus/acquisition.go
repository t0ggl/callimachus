package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/requests"
)

func (s *Server) newAcquisition() *requests.Acquisition {
	client := func(ctx context.Context) (*indexers.ProwlarrClient, error) {
		r, _ := http.NewRequestWithContext(ctx, "GET", "/", nil)
		return s.prowlarrClient(r)
	}
	return &requests.Acquisition{DB: s.db,
		Search: func(ctx context.Context, q, format string) ([]indexers.ProwlarrRelease, error) {
			c, err := client(ctx)
			if err != nil {
				return nil, err
			}
			categories := indexers.DefaultCategories
			switch format {
			case requests.FormatEPUB:
				categories = []int{indexers.CategoryBooks, indexers.CategoryBooksEBook}
			case requests.FormatM4B:
				categories = []int{indexers.CategoryAudioAudiobook}
			}
			return c.Search(ctx, q, categories)
		},
		Grab: func(ctx context.Context, r indexers.ProwlarrRelease) error {
			c, err := client(ctx)
			if err != nil {
				return err
			}
			return c.Grab(ctx, r)
		},
		Evaluate: func(ctx context.Context, r indexers.ProwlarrRelease, requestFormat string) (requests.Evaluation, error) {
			cfg, err := config.Load(ctx, s.db, s.baseOverrides)
			if err != nil {
				return requests.Evaluation{}, err
			}
			p := cfg.Settings.ToQualityProfile()
			candidate := downloaders.ReleaseCandidate{
				Title:      r.Title,
				Size:       r.Size,
				Categories: r.CategoryIDs(),
			}
			switch requestFormat {
			case requests.FormatM4B:
				candidate.IsAudio = true
				p.MaxEbookSizeBytes = 0
			case requests.FormatEPUB:
				p.MaxAudiobookSizeMB = 0
			}
			detectedFormat := downloaders.DetectReleaseFormat(candidate)
			issues := p.EvaluateReleaseIssues(candidate)
			exclusions := make(map[string]string, len(issues))
			for _, issue := range issues {
				exclusions[issue.Criterion] = issue.Reason
			}
			return requests.Evaluation{
				Format:           detectedFormat,
				EbookPreferences: p.AllowedEbookPreferences(),
				AudioPreferences: []string{"m4b", "mp3"},
				Exclusions:       exclusions,
			}, nil
		},
	}
}

// Download clients can report terminal failures through an authenticated webhook.
// Completed media is still imported and marked completed by the staging monitor.
func (s *Server) handleAcquisitionFailure(w http.ResponseWriter, r *http.Request) {
	var event struct {
		QueueID string `json:"queue_id"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&event); err != nil || event.QueueID == "" {
		http.Error(w, "queue_id is required", 400)
		return
	}
	result, err := s.db.ExecContext(r.Context(), `UPDATE download_queue SET status='failed',error_message=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND status IN ('queued','downloading','dispatching') AND request_id IS NOT NULL`, event.Error, event.QueueID)
	if err != nil {
		http.Error(w, "Unable to record failure", 500)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		http.Error(w, "Active acquisition not found", 404)
		return
	}
	slog.WarnContext(r.Context(), "download client reported acquisition failure")
	w.WriteHeader(http.StatusAccepted)
}
