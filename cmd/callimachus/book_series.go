package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/t0ggl/callimachus/pkg/catalog"
)

func (s *Server) handleAdditionalSeries(w http.ResponseWriter, r *http.Request) {
	if slices.Contains([]string{"cross-site", "same-site"}, r.Header.Get("Sec-Fetch-Site")) {
		http.Error(w, "Cross-origin request forbidden.", http.StatusForbidden)
		return
	}
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentType != "application/json" {
		http.Error(w, "Expected a JSON request.", http.StatusUnsupportedMediaType)
		return
	}
	var req struct {
		SeriesID string   `json:"seriesId"`
		Title    string   `json:"title"`
		Index    *float64 `json:"index"`
		Remove   bool     `json:"remove"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || decoder.Decode(new(any)) != io.EOF ||
		(req.SeriesID == "" && (req.Remove || strings.TrimSpace(req.Title) == "")) {
		http.Error(w, "Choose a series or enter its title.", http.StatusBadRequest)
		return
	}
	if s.db == nil {
		http.Error(w, "Database unavailable.", http.StatusServiceUnavailable)
		return
	}
	ctx, id := r.Context(), r.PathValue("id")
	s.metadataRepairMu.Lock()
	defer s.metadataRepairMu.Unlock()
	err := catalog.New(s.db).EditAdditionalSeries(ctx, id, req.SeriesID, req.Title, req.Index, req.Remove)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "Book or series not found.", http.StatusNotFound)
	case errors.Is(err, catalog.ErrPrimarySeries):
		http.Error(w, "Change the primary series in Fix Metadata.", http.StatusConflict)
	case err != nil:
		http.Error(w, "Unable to save the series membership.", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
