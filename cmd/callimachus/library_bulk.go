package main

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/t0ggl/callimachus/pkg/auth"
)

const maxBulkLibraryItems = 500

type bulkLibraryTarget struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

type libraryBulkRequest struct {
	Action      string              `json:"action"`
	Items       []bulkLibraryTarget `json:"items"`
	DeleteFiles bool                `json:"deleteFiles,omitempty"`
}

type bulkLibraryItemResult struct {
	Kind              string `json:"kind"`
	ID                string `json:"id"`
	Title             string `json:"title,omitempty"`
	OK                bool   `json:"ok"`
	Error             string `json:"error,omitempty"`
	Monitored         *bool  `json:"monitored,omitempty"`
	AffectedBookCount int    `json:"affectedBookCount,omitempty"`
	DeletedFiles      int    `json:"deletedFiles,omitempty"`
}

type libraryBulkResponse struct {
	Action    string                  `json:"action"`
	Total     int                     `json:"total"`
	Succeeded int                     `json:"succeeded"`
	Failed    int                     `json:"failed"`
	Results   []bulkLibraryItemResult `json:"results"`
}

func (s *Server) handleLibraryBulk(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "Database unavailable", http.StatusInternalServerError)
		return
	}

	var req libraryBulkRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "Invalid bulk request", http.StatusBadRequest)
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "Invalid bulk request", http.StatusBadRequest)
		return
	}

	req.Action = strings.TrimSpace(req.Action)
	switch req.Action {
	case "monitor", "unmonitor", "discover", "refresh", "delete":
	default:
		http.Error(w, "Invalid bulk action", http.StatusBadRequest)
		return
	}

	if req.Action == "delete" {
		user, ok := auth.UserFromContext(r.Context())
		if !ok || user == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if !auth.HasRole(user.Role, auth.RoleAdmin) {
			http.Error(w, "Administrator privileges are required to delete books.", http.StatusForbidden)
			return
		}
	}

	if req.Action == "refresh" && s.refreshBookMetadata == nil {
		http.Error(w, "Metadata refresh is not available.", http.StatusNotImplemented)
		return
	}

	if len(req.Items) == 0 {
		http.Error(w, "Select at least one item.", http.StatusBadRequest)
		return
	}
	if len(req.Items) > maxBulkLibraryItems {
		http.Error(w, fmt.Sprintf("Cannot process more than %d items at once.", maxBulkLibraryItems), http.StatusBadRequest)
		return
	}

	deduped := make([]bulkLibraryTarget, 0, len(req.Items))
	seen := make(map[string]bool, len(req.Items))
	for _, item := range req.Items {
		kind := strings.TrimSpace(item.Kind)
		if kind != "book" && kind != "series" {
			http.Error(w, "Each item kind must be book or series.", http.StatusBadRequest)
			return
		}
		id := strings.TrimSpace(item.ID)
		key := kind + ":" + id
		if id != "" && seen[key] {
			continue
		}
		if id != "" {
			seen[key] = true
		}
		deduped = append(deduped, bulkLibraryTarget{
			Kind:  kind,
			ID:    id,
			Title: strings.TrimSpace(item.Title),
		})
	}

	monitor := s.catalogMonitor
	if monitor == nil {
		monitor = s.newCatalogMonitor()
	}

	ctx := r.Context()
	resp := libraryBulkResponse{
		Action:  req.Action,
		Total:   len(deduped),
		Results: make([]bulkLibraryItemResult, 0, len(deduped)),
	}
	wakeMonitor := false

	for _, item := range deduped {
		var result bulkLibraryItemResult
		switch req.Action {
		case "monitor", "unmonitor":
			enabled := req.Action == "monitor"
			result = s.applyBulkMonitor(ctx, monitor, item, enabled)
			if result.OK && (enabled || item.Kind == "series") {
				wakeMonitor = true
			}
		case "discover":
			result = s.applyBulkDiscover(ctx, monitor, item)
			if result.OK {
				wakeMonitor = true
			}
		case "refresh":
			result = s.applyBulkRefresh(ctx, item)
		case "delete":
			result = s.applyBulkDelete(ctx, item, req.DeleteFiles)
		}

		if result.OK {
			resp.Succeeded++
		} else {
			resp.Failed++
		}
		resp.Results = append(resp.Results, result)
	}

	if wakeMonitor {
		s.wakeMonitor()
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) applyBulkMonitor(ctx context.Context, monitor interface {
	SetMonitored(context.Context, string, string, bool) error
}, item bulkLibraryTarget, enabled bool) bulkLibraryItemResult {
	res := bulkLibraryItemResult{
		Kind:  item.Kind,
		ID:    item.ID,
		Title: cmp.Or(item.Title, item.ID),
	}
	if item.ID == "" || strings.Contains(item.ID, "/") {
		res.Error = fmt.Sprintf("Invalid %s ID.", item.Kind)
		return res
	}

	if item.Kind == "book" {
		var title string
		err := s.db.QueryRowContext(ctx, `SELECT title FROM books WHERE id = ?`, item.ID).Scan(&title)
		if errors.Is(err, sql.ErrNoRows) {
			res.Error = "Book not found."
			return res
		}
		if err != nil {
			res.Error = "Unable to load book."
			return res
		}
		res.Title = cmp.Or(title, res.Title)

		updateRes, err := s.db.ExecContext(ctx, `
			UPDATE books
			SET monitored = ?,
			    availability_checked_at = CASE WHEN ? THEN NULL ELSE availability_checked_at END,
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`, enabled, enabled, item.ID)
		if err != nil {
			res.Error = "Unable to update book monitoring."
			return res
		}
		if n, err := updateRes.RowsAffected(); err == nil && n == 0 {
			res.Error = "Book not found."
			return res
		}
		val := enabled
		res.OK = true
		res.Monitored = &val
		return res
	}

	var title string
	err := s.db.QueryRowContext(ctx, `SELECT title FROM series WHERE id = ?`, item.ID).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		res.Error = "Series not found."
		return res
	}
	if err != nil {
		res.Error = "Unable to load series."
		return res
	}
	res.Title = cmp.Or(title, res.Title)

	if err := monitor.SetMonitored(ctx, "series", item.ID, enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			res.Error = "Series not found."
		} else {
			res.Error = "Unable to update series monitoring."
		}
		return res
	}

	var bookCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM books WHERE series_id = ?`, item.ID).Scan(&bookCount); err == nil {
		res.AffectedBookCount = bookCount
	}
	val := enabled
	res.OK = true
	res.Monitored = &val
	return res
}

func (s *Server) applyBulkDiscover(ctx context.Context, monitor interface {
	Queue(context.Context, string, string, string) error
}, item bulkLibraryTarget) bulkLibraryItemResult {
	res := bulkLibraryItemResult{
		Kind:  item.Kind,
		ID:    item.ID,
		Title: cmp.Or(item.Title, item.ID),
	}
	if item.Kind != "series" {
		res.Error = "New release checks apply to series only."
		return res
	}
	if item.ID == "" || strings.Contains(item.ID, "/") {
		res.Error = "Invalid series ID."
		return res
	}

	var title string
	err := s.db.QueryRowContext(ctx, `SELECT title FROM series WHERE id = ?`, item.ID).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		res.Error = "Series not found."
		return res
	}
	if err != nil {
		res.Error = "Unable to load series."
		return res
	}
	res.Title = cmp.Or(title, res.Title)

	if err := monitor.Queue(ctx, "series", item.ID, "discover"); err != nil {
		res.Error = "Unable to queue new release check."
		return res
	}
	res.OK = true
	return res
}

func (s *Server) applyBulkRefresh(ctx context.Context, item bulkLibraryTarget) bulkLibraryItemResult {
	res := bulkLibraryItemResult{
		Kind:  item.Kind,
		ID:    item.ID,
		Title: cmp.Or(item.Title, item.ID),
	}
	if item.ID == "" || strings.Contains(item.ID, "/") {
		res.Error = fmt.Sprintf("Invalid %s ID.", item.Kind)
		return res
	}

	if item.Kind == "book" {
		var title string
		err := s.db.QueryRowContext(ctx, `SELECT title FROM books WHERE id = ?`, item.ID).Scan(&title)
		if errors.Is(err, sql.ErrNoRows) {
			res.Error = "Book not found."
			return res
		}
		if err != nil {
			res.Error = "Unable to load book."
			return res
		}
		res.Title = cmp.Or(title, res.Title)

		if err := s.refreshBookMetadata(ctx, item.ID); err != nil {
			res.Error = err.Error()
			return res
		}
		res.OK = true
		res.AffectedBookCount = 1
		return res
	}

	var title string
	err := s.db.QueryRowContext(ctx, `SELECT title FROM series WHERE id = ?`, item.ID).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		res.Error = "Series not found."
		return res
	}
	if err != nil {
		res.Error = "Unable to load series."
		return res
	}
	res.Title = cmp.Or(title, res.Title)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title FROM books
		WHERE series_id = ?
		ORDER BY series_index IS NULL, series_index, title, id`, item.ID)
	if err != nil {
		res.Error = "Unable to load series books."
		return res
	}
	type bookRef struct {
		id, title string
	}
	var books []bookRef
	for rows.Next() {
		var b bookRef
		if err := rows.Scan(&b.id, &b.title); err != nil {
			rows.Close()
			res.Error = "Unable to load series books."
			return res
		}
		books = append(books, b)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		res.Error = "Unable to load series books."
		return res
	}
	if len(books) == 0 {
		res.Error = "Series has no books in library to refresh."
		return res
	}

	refreshed := 0
	var failures []string
	for _, b := range books {
		if err := s.refreshBookMetadata(ctx, b.id); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %s", b.title, err.Error()))
		} else {
			refreshed++
		}
	}
	res.AffectedBookCount = refreshed
	if len(failures) > 0 {
		res.Error = fmt.Sprintf("%d of %d books failed to refresh (%s).", len(failures), len(books), strings.Join(failures, "; "))
		return res
	}
	res.OK = true
	return res
}

func (s *Server) applyBulkDelete(ctx context.Context, item bulkLibraryTarget, deleteFiles bool) bulkLibraryItemResult {
	res := bulkLibraryItemResult{
		Kind:  item.Kind,
		ID:    item.ID,
		Title: cmp.Or(item.Title, item.ID),
	}
	if item.Kind != "book" {
		res.Error = "Bulk delete is only available for books."
		return res
	}
	if item.ID == "" || strings.Contains(item.ID, "/") {
		res.Error = "Invalid book ID."
		return res
	}

	var title string
	if err := s.db.QueryRowContext(ctx, `SELECT title FROM books WHERE id = ?`, item.ID).Scan(&title); err == nil && title != "" {
		res.Title = title
	}

	deletedFiles, _, errMsg := s.deleteBook(ctx, item.ID, deleteFiles)
	if errMsg != "" {
		res.Error = errMsg
		return res
	}
	res.OK = true
	res.DeletedFiles = deletedFiles
	return res
}
