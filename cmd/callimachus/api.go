package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/config"
)

type APIMediaAsset struct {
	ID            string `json:"id"`
	EditionID     string `json:"editionId"`
	FilePath      string `json:"filePath"`
	FileSizeBytes int64  `json:"fileSizeBytes"`
	Format        string `json:"format"`
}

type APIBookEdition struct {
	ID        string          `json:"id"`
	BookID    string          `json:"bookId"`
	Format    string          `json:"format"`
	ISBN13    string          `json:"isbn13,omitempty"`
	ASIN      string          `json:"asin,omitempty"`
	Publisher string          `json:"publisher,omitempty"`
	Language  string          `json:"language"`
	Assets    []APIMediaAsset `json:"assets"`
}

type APIBook struct {
	ID                 string           `json:"id"`
	Title              string           `json:"title"`
	Subtitle           string           `json:"subtitle,omitempty"`
	AuthorID           string           `json:"authorId"`
	AuthorName         string           `json:"authorName"`
	SeriesID           string           `json:"seriesId,omitempty"`
	SeriesTitle        string           `json:"seriesTitle,omitempty"`
	SeriesIndex        *float64         `json:"seriesIndex,omitempty"`
	SeriesIndexDisplay string           `json:"seriesIndexDisplay,omitempty"`
	ReleaseDate        string           `json:"releaseDate,omitempty"`
	Synopsis           string           `json:"synopsis,omitempty"`
	Formats            []string         `json:"formats"`
	Editions           []APIBookEdition `json:"editions"`
}

type APIAuthor struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SortName  string `json:"sortName"`
	PhotoURL  string `json:"photoUrl,omitempty"`
	Biography string `json:"biography,omitempty"`
	BookCount int    `json:"bookCount"`
}

type APISeries struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	TotalBooks  int    `json:"totalBooks,omitempty"`
	Status      string `json:"status"`
}

type APIConversionJob struct {
	EditionID        string  `json:"editionId"`
	ID               string  `json:"id"`
	BookID           string  `json:"bookId"`
	BookTitle        string  `json:"bookTitle"`
	AuthorName       string  `json:"authorName"`
	SourceFilesCount int     `json:"sourceFilesCount"`
	TargetFormat     string  `json:"targetFormat"`
	Stage            string  `json:"stage"`
	Status           string  `json:"status"`
	ProgressPercent  float64 `json:"progressPercent"`
	BitrateKbps      int     `json:"bitrateKbps"`
	ElapsedSeconds   int     `json:"elapsedSeconds"`
	EtaSeconds       int     `json:"etaSeconds"`
	ErrorMessage     string  `json:"errorMessage,omitempty"`
}

type APIDownloadItem struct {
	EditionID           string  `json:"editionId,omitempty"`
	RequestID           string  `json:"requestId,omitempty"`
	ErrorMessage        string  `json:"errorMessage,omitempty"`
	ID                  string  `json:"id"`
	Title               string  `json:"title"`
	Category            string  `json:"category"`
	ClientType          string  `json:"clientType"`
	Status              string  `json:"status"`
	ProgressPercent     float64 `json:"progressPercent"`
	DownloadSpeedBps    int64   `json:"downloadSpeedBps"`
	TotalSizeBytes      int64   `json:"totalSizeBytes"`
	DownloadedSizeBytes int64   `json:"downloadedSizeBytes"`
	EtaSeconds          int     `json:"etaSeconds"`
}

type APIActivityResponse struct {
	ConversionJobs []APIConversionJob `json:"conversionJobs"`
	Downloads      []APIDownloadItem  `json:"downloads"`
	Logs           []any              `json:"logs"`
}

type APIIndexerConfig struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Priority       int    `json:"priority"`
	Enabled        bool   `json:"enabled"`
	LastSync       string `json:"lastSync"`
	ResponseTimeMs int    `json:"responseTimeMs"`
	Status         string `json:"status"`
}

type APILibrarySettings struct {
	CompletedDownloadRoot string `json:"completedDownloadRoot"`
	RootPath              string `json:"rootPath"`
	StagingPollInterval   string `json:"stagingPollInterval"`
	IndexerSyncInterval   string `json:"indexerSyncInterval"`
}

type APIProwlarrSettings struct {
	URL              string `json:"url"`
	APIKeyConfigured bool   `json:"apiKeyConfigured"`
	SyncInterval     string `json:"syncInterval"`
}

type APIQualityProfileSettings struct {
	ConvertEbooks          bool     `json:"convertEbooks"`
	EbookTargetFormat      string   `json:"ebookTargetFormat"`
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	AllowedLanguages       []string `json:"allowedLanguages"`
	AllowedFormats         []string `json:"allowedFormats"`
	PreferSingleFileM4b    bool     `json:"preferSingleFileM4b"`
	MaxFileSizeMb          int      `json:"maxFileSizeMb"`
	MaxEbookSizeBytes      int64    `json:"maxEbookSizeBytes"`
	EbookFormatPreferences []string `json:"ebookFormatPreferences"`
}

type APISettingsResponse struct {
	Metadata       map[string]bool                `json:"metadata"`
	Indexers       []APIIndexerConfig             `json:"indexers"`
	Library        APILibrarySettings             `json:"library"`
	Prowlarr       APIProwlarrSettings            `json:"prowlarr"`
	QualityProfile APIQualityProfileSettings      `json:"qualityProfile"`
	Overrides      map[string]config.OverrideInfo `json:"overrides"`
}

type APISettingsUpdateRequest struct {
	Metadata map[string]string `json:"metadata"`
	Library  *struct {
		CompletedDownloadRoot *string `json:"completedDownloadRoot"`
		RootPath              *string `json:"rootPath"`
		StagingPollInterval   *string `json:"stagingPollInterval"`
		IndexerSyncInterval   *string `json:"indexerSyncInterval"`
	} `json:"library"`
	Prowlarr *struct {
		URL          *string `json:"url"`
		APIKey       *string `json:"apiKey"`
		SyncInterval *string `json:"syncInterval"`
	} `json:"prowlarr"`
	QualityProfile *struct {
		ConvertEbooks          *bool     `json:"convertEbooks"`
		EbookTargetFormat      *string   `json:"ebookTargetFormat"`
		Name                   *string   `json:"name"`
		AllowedLanguages       *[]string `json:"allowedLanguages"`
		AllowedFormats         *[]string `json:"allowedFormats"`
		PreferSingleFileM4b    *bool     `json:"preferSingleFileM4b"`
		MaxFileSizeMb          *int      `json:"maxFileSizeMb"`
		MaxEbookSizeBytes      *int64    `json:"maxEbookSizeBytes"`
		EbookFormatPreferences *[]string `json:"ebookFormatPreferences"`
	} `json:"qualityProfile"`
}

func (s *Server) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/discover", s.handleDiscover)
	mux.Handle("POST /api/library/scan", auth.RequireRole(auth.RoleCurator)(http.HandlerFunc(s.handleScanLibrary)))
	mux.Handle("POST /api/acquisition/failure", auth.RequireRole(auth.RoleCurator)(http.HandlerFunc(s.handleAcquisitionFailure)))
	mux.HandleFunc("GET /api/books", s.handleGetBooks)
	mux.HandleFunc("GET /api/books/", s.handleGetBooks)
	mux.HandleFunc("GET /api/authors", s.handleGetAuthors)
	mux.HandleFunc("GET /api/authors/", s.handleGetAuthors)
	mux.HandleFunc("GET /api/series/books", s.handleSeriesBooks)
	mux.HandleFunc("GET /api/series", s.handleGetSeries)
	mux.HandleFunc("GET /api/series/", s.handleGetSeries)
	mux.HandleFunc("GET /api/activity", s.handleGetActivity)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("POST /api/settings", s.handleRejectSettingsPost)
	mux.HandleFunc("POST /api/settings/prowlarr/sync", s.handleSyncProwlarr)
	mux.HandleFunc("GET /api/events", s.handleEventsSSE)
	mux.Handle("GET /api/releases", auth.RequireRole(auth.RoleCurator)(http.HandlerFunc(s.handleSearchReleases)))
	mux.Handle("POST /api/releases/grab", auth.RequireRole(auth.RoleCurator)(http.HandlerFunc(s.handleGrabRelease)))
}

func (s *Server) handleGetBooks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if s.db == nil {
		_ = json.NewEncoder(w).Encode([]APIBook{})
		return
	}

	query := `
		SELECT b.id, b.title, COALESCE(b.subtitle, ''), b.author_id, COALESCE(a.name, ''),
		       COALESCE(b.series_id, ''), COALESCE(s.title, ''), b.series_index,
		       COALESCE(b.series_index_display, ''), COALESCE(b.release_date, ''),
		       COALESCE(b.synopsis, '')
		FROM books b
		LEFT JOIN authors a ON b.author_id = a.id
		LEFT JOIN series s ON b.series_id = s.id
		ORDER BY b.title ASC
	`
	rows, err := s.db.QueryContext(r.Context(), query)
	if err != nil {
		log.Printf("[api] get books query error: %v", err)
		_ = json.NewEncoder(w).Encode([]APIBook{})
		return
	}
	defer rows.Close()

	books := make([]APIBook, 0)
	bookIDs := make([]string, 0)
	bookMap := make(map[string]*APIBook)

	for rows.Next() {
		var b APIBook
		var seriesIdx sql.NullFloat64
		if err := rows.Scan(
			&b.ID, &b.Title, &b.Subtitle, &b.AuthorID, &b.AuthorName,
			&b.SeriesID, &b.SeriesTitle, &seriesIdx,
			&b.SeriesIndexDisplay, &b.ReleaseDate, &b.Synopsis,
		); err != nil {
			log.Printf("[api] scan book error: %v", err)
			continue
		}
		if seriesIdx.Valid {
			val := seriesIdx.Float64
			b.SeriesIndex = &val
		}
		b.Formats = make([]string, 0)
		b.Editions = make([]APIBookEdition, 0)
		books = append(books, b)
		bookIDs = append(bookIDs, b.ID)
	}

	for i := range books {
		bookMap[books[i].ID] = &books[i]
	}

	// Query editions if books exist
	if len(bookIDs) > 0 {
		edQuery := `
			SELECT id, book_id, format, COALESCE(isbn13, ''), COALESCE(asin, ''),
			       COALESCE(publisher, ''), language
			FROM book_editions
			ORDER BY format ASC
		`
		edRows, err := s.db.QueryContext(r.Context(), edQuery)
		if err == nil {
			defer edRows.Close()
			for edRows.Next() {
				var ed APIBookEdition
				if err := edRows.Scan(
					&ed.ID, &ed.BookID, &ed.Format, &ed.ISBN13, &ed.ASIN,
					&ed.Publisher, &ed.Language,
				); err == nil {
					ed.Assets = make([]APIMediaAsset, 0)
					if parent, ok := bookMap[ed.BookID]; ok {
						parent.Editions = append(parent.Editions, ed)
						fmtUpper := strings.ToUpper(ed.Format)
						found := false
						for _, f := range parent.Formats {
							if f == fmtUpper {
								found = true
								break
							}
						}
						if !found {
							parent.Formats = append(parent.Formats, fmtUpper)
						}
					}
				}
			}
		}
	}

	_ = json.NewEncoder(w).Encode(books)
}

func (s *Server) handleGetAuthors(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if s.db == nil {
		_ = json.NewEncoder(w).Encode([]APIAuthor{})
		return
	}

	query := `
		SELECT a.id, a.name, a.sort_name, COALESCE(a.photo_url, ''),
		       COALESCE(a.biography, ''), COUNT(b.id)
		FROM authors a
		LEFT JOIN books b ON a.id = b.author_id
		GROUP BY a.id, a.name, a.sort_name, a.photo_url, a.biography
		ORDER BY a.sort_name ASC
	`
	rows, err := s.db.QueryContext(r.Context(), query)
	if err != nil {
		log.Printf("[api] get authors query error: %v", err)
		_ = json.NewEncoder(w).Encode([]APIAuthor{})
		return
	}
	defer rows.Close()

	authors := make([]APIAuthor, 0)
	for rows.Next() {
		var a APIAuthor
		if err := rows.Scan(&a.ID, &a.Name, &a.SortName, &a.PhotoURL, &a.Biography, &a.BookCount); err != nil {
			continue
		}
		authors = append(authors, a)
	}

	_ = json.NewEncoder(w).Encode(authors)
}

func (s *Server) handleGetSeries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if s.db == nil {
		_ = json.NewEncoder(w).Encode([]APISeries{})
		return
	}

	query := `
		SELECT s.id, s.title, COALESCE(s.description, ''), COALESCE(s.total_books, 0), s.status
		FROM series s
		ORDER BY s.title ASC
	`
	rows, err := s.db.QueryContext(r.Context(), query)
	if err != nil {
		log.Printf("[api] get series query error: %v", err)
		_ = json.NewEncoder(w).Encode([]APISeries{})
		return
	}
	defer rows.Close()

	seriesList := make([]APISeries, 0)
	for rows.Next() {
		var s APISeries
		if err := rows.Scan(&s.ID, &s.Title, &s.Description, &s.TotalBooks, &s.Status); err != nil {
			continue
		}
		seriesList = append(seriesList, s)
	}

	_ = json.NewEncoder(w).Encode(seriesList)
}

func (s *Server) handleGetActivity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	resp, err := s.activityResponse(r.Context())
	if err != nil {
		http.Error(w, "Unable to load activity", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) activityResponse(ctx context.Context) (APIActivityResponse, error) {
	resp := APIActivityResponse{
		ConversionJobs: make([]APIConversionJob, 0),
		Downloads:      make([]APIDownloadItem, 0),
		Logs:           make([]any, 0),
	}

	if s.db == nil {
		return resp, nil
	}

	// Query conversion queue
	tqQuery := `
		SELECT t.id, t.edition_id, COALESCE(be.book_id, ''), COALESCE(b.title, ''), COALESCE(a.name, ''),
		       t.status, t.progress_percent, t.current_stage, COALESCE(t.error_message, ''), t.target_path
		FROM conversion_queue t
		LEFT JOIN book_editions be ON t.edition_id = be.id
		LEFT JOIN books b ON be.book_id = b.id
		LEFT JOIN authors a ON b.author_id = a.id
		WHERE t.status != 'cancelled'
		ORDER BY t.created_at DESC, t.id DESC
	`
	rows, err := s.db.QueryContext(ctx, tqQuery)
	if err != nil {
		return resp, err
	}
	{
		defer rows.Close()
		for rows.Next() {
			var job APIConversionJob
			var targetPath string
			if err := rows.Scan(
				&job.ID, &job.EditionID, &job.BookID, &job.BookTitle, &job.AuthorName,
				&job.Status, &job.ProgressPercent, &job.Stage, &job.ErrorMessage, &targetPath,
			); err != nil {
				return resp, err
			} else {
				if job.Status == "pending" {
					job.Status = "queued"
				}
				// Old persisted stages remain readable after the terminology change.
				if job.Stage == "encoding" {
					job.Stage = "converting"
				}
				if job.Stage == "probing" {
					job.Stage = "preparing"
				}
				job.TargetFormat = strings.ToUpper(strings.TrimPrefix(filepath.Ext(targetPath), "."))
				resp.ConversionJobs = append(resp.ConversionJobs, job)
			}
		}
		if err := rows.Err(); err != nil {
			return resp, err
		}
		if err := rows.Close(); err != nil {
			return resp, err
		}
	}

	// Query download queue
	dqQuery := `
		SELECT q.id, q.title, q.status, q.progress_percent, q.size_bytes,
        COALESCE(q.edition_id, ''), COALESCE((SELECT request_id FROM acquisition_candidates WHERE queue_id=q.id LIMIT 1), ''),
        COALESCE(q.error_message, ''), COALESCE(e.format, '')
        FROM download_queue q LEFT JOIN book_editions e ON e.id=q.edition_id
        ORDER BY q.created_at DESC, q.id DESC
	`
	rows, err = s.db.QueryContext(ctx, dqQuery)
	if err != nil {
		return resp, err
	}
	{
		defer rows.Close()
		for rows.Next() {
			var item APIDownloadItem
			var format string
			item.Category = "callimachus-audiobooks"
			item.ClientType = "Prowlarr"
			if err := rows.Scan(
				&item.ID, &item.Title, &item.Status, &item.ProgressPercent, &item.TotalSizeBytes, &item.EditionID, &item.RequestID, &item.ErrorMessage, &format,
			); err != nil {
				return resp, err
			} else {
				if format != "" && !strings.EqualFold(format, "m4b") && !strings.EqualFold(format, "mp3") {
					item.Category = "callimachus-ebooks"
				}
				if item.Status == "failed" {
					item.Status = "error"
				}
				if item.Status == "dispatching" || item.Status == "staged" {
					item.Status = "queued"
				}
				resp.Downloads = append(resp.Downloads, item)
			}
		}
	}

	if err := rows.Err(); err != nil {
		return resp, err
	}
	return resp, nil
}

func (s *Server) buildSettingsResponse(ctx context.Context, resolved *config.ResolvedSettings) APISettingsResponse {
	var resp APISettingsResponse
	resp.Indexers = make([]APIIndexerConfig, 0)

	if resolved == nil {
		var err error
		resolved, err = config.Load(ctx, s.db, s.baseOverrides)
		if err != nil {
			log.Printf("[api] warning: failed to load resolved config: %v", err)
			resolved = &config.ResolvedSettings{Overrides: map[string]config.OverrideInfo{}}
		}
	}

	cfg := resolved.Settings
	resp.Overrides = resolved.Overrides

	// Library & Storage
	resp.Library = APILibrarySettings{
		RootPath:              cfg.LibraryRoot,
		CompletedDownloadRoot: cfg.CompletedDownloadRoot,
		StagingPollInterval:   cfg.StagingPollInterval.String(),
		IndexerSyncInterval:   cfg.IndexerSyncInterval.String(),
	}

	resp.Metadata = map[string]bool{config.KeyGoogleBooksAPIKey: cfg.GoogleBooksAPIKey != "", config.KeyHardcoverAPIKey: cfg.HardcoverAPIKey != ""}
	// Prowlarr Integration (never expose raw API key)
	resp.Prowlarr = APIProwlarrSettings{
		URL:              cfg.ProwlarrURL,
		APIKeyConfigured: cfg.ProwlarrAPIKey != "",
		SyncInterval:     cfg.ProwlarrSyncInterval.String(),
	}

	// Quality Profile
	resp.QualityProfile = APIQualityProfileSettings{
		ID:            "default",
		ConvertEbooks: cfg.ConvertEbooks, EbookTargetFormat: cfg.EbookTargetFormat,
		Name:                   cfg.QualityProfileName,
		AllowedLanguages:       cfg.AllowedLanguages,
		AllowedFormats:         cfg.AllowedFormats,
		PreferSingleFileM4b:    cfg.PreferSingleFileM4B,
		MaxFileSizeMb:          cfg.MaxFileSizeMB,
		MaxEbookSizeBytes:      cfg.MaxEbookSizeBytes,
		EbookFormatPreferences: cfg.EbookFormatPreferences,
	}

	if s.db != nil {
		// Query indexers
		if rows, err := s.db.QueryContext(ctx, `SELECT id, name, protocol, priority, enabled, COALESCE(last_sync_at, '') FROM indexers ORDER BY priority ASC`); err == nil {
			defer rows.Close()
			for rows.Next() {
				var idx APIIndexerConfig
				var en int
				var lastSync string
				if err := rows.Scan(&idx.ID, &idx.Name, &idx.Type, &idx.Priority, &en, &lastSync); err == nil {
					idx.Enabled = en == 1
					idx.LastSync = lastSync
					idx.Status = "online"
					resp.Indexers = append(resp.Indexers, idx)
				}
			}
		}

	}

	return resp
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	resp := s.buildSettingsResponse(r.Context(), nil)
	_ = json.NewEncoder(w).Encode(resp)
}

func hasForbiddenAuthSettingKeys(raw map[string]json.RawMessage) bool {
	for k := range raw {
		normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(strings.TrimSpace(k)))
		switch normalized {
		case "auth", "authsettings",
			"oidc", "oidcenabled", "oidcissuerurl", "oidcclientid", "oidcclientsecret",
			"oidcredirecturl", "oidcscopes", "oidcadmingroups", "oidccuratorgroups", "oidcdefaultrole",
			"adminpassword", "localauthenabled", "sessiontimeouthours":
			return true
		}
	}
	return false
}

func (s *Server) handleRejectSettingsPost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var raw map[string]json.RawMessage
	if len(body) > 0 && json.Unmarshal(body, &raw) == nil && hasForbiddenAuthSettingKeys(raw) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "authentication and OIDC settings are configured exclusively via environment variables and cannot be modified via HTTP",
		})
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "POST /api/settings is not supported; use PUT /api/settings for non-authentication settings",
	})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if s.db == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "database unavailable"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to read request body"})
		return
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON request body"})
		return
	}
	if hasForbiddenAuthSettingKeys(raw) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "authentication and OIDC settings are configured exclusively via environment variables and cannot be modified via HTTP",
		})
		return
	}

	var req APISettingsUpdateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON request body"})
		return
	}

	updates := make(map[string]string)
	for key, value := range req.Metadata {
		if key == config.KeyGoogleBooksAPIKey || key == config.KeyHardcoverAPIKey {
			updates[key] = value
		}
	}

	if req.Library != nil {
		if req.Library.CompletedDownloadRoot != nil {
			updates[config.KeyCompletedDownloadRoot] = *req.Library.CompletedDownloadRoot
		}
		if req.Library.RootPath != nil {
			updates[config.KeyLibraryRoot] = *req.Library.RootPath
		}
		if req.Library.StagingPollInterval != nil {
			updates[config.KeyStagingPollInterval] = *req.Library.StagingPollInterval
		}
		if req.Library.IndexerSyncInterval != nil {
			updates[config.KeyIndexerSyncInterval] = *req.Library.IndexerSyncInterval
		}
	}

	if req.Prowlarr != nil {
		if req.Prowlarr.URL != nil {
			updates[config.KeyProwlarrURL] = *req.Prowlarr.URL
		}
		if req.Prowlarr.APIKey != nil && strings.TrimSpace(*req.Prowlarr.APIKey) != "" {
			updates[config.KeyProwlarrAPIKey] = *req.Prowlarr.APIKey
		}
		if req.Prowlarr.SyncInterval != nil {
			updates[config.KeyProwlarrSyncInterval] = *req.Prowlarr.SyncInterval
		}
	}

	if req.QualityProfile != nil {
		if v := req.QualityProfile.ConvertEbooks; v != nil {
			updates[config.KeyConvertEbooks] = strconv.FormatBool(*v)
		}
		if v := req.QualityProfile.EbookTargetFormat; v != nil {
			updates[config.KeyEbookTargetFormat] = *v
		}
		if req.QualityProfile.Name != nil {
			updates[config.KeyQualityProfileName] = *req.QualityProfile.Name
		}
		if req.QualityProfile.AllowedLanguages != nil {
			updates[config.KeyAllowedLanguages] = strings.Join(*req.QualityProfile.AllowedLanguages, ",")
		}
		if req.QualityProfile.AllowedFormats != nil {
			updates[config.KeyAllowedFormats] = strings.Join(*req.QualityProfile.AllowedFormats, ",")
		}
		if req.QualityProfile.EbookFormatPreferences != nil {
			encoded, _ := json.Marshal(*req.QualityProfile.EbookFormatPreferences)
			updates[config.KeyEbookFormatPreferences] = string(encoded)
		}
		if size := req.QualityProfile.MaxEbookSizeBytes; size != nil {
			if _, _, overridden := config.LookupEnvForSetting(config.KeyMaxEbookSizeMB); !overridden {
				if *size < 0 || *size%(1024*1024) != 0 {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "maxEbookSizeBytes must be a non-negative whole number of MB (1048576 bytes)"})
					return
				}
				updates[config.KeyMaxEbookSizeMB] = strconv.FormatInt(*size/(1024*1024), 10)
			}
		}
		if req.QualityProfile.MaxFileSizeMb != nil {
			updates[config.KeyMaxFileSizeMB] = strconv.Itoa(*req.QualityProfile.MaxFileSizeMb)
		}
	}

	resolved, err := config.SaveNonOverridden(r.Context(), s.db, updates, s.baseOverrides)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if s.stagingMon != nil {
		s.stagingMon.SetCompletedRoot(resolved.Settings.CompletedDownloadRoot)
	}
	resp := s.buildSettingsResponse(r.Context(), resolved)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleSyncProwlarr(w http.ResponseWriter, r *http.Request) {
	client, err := s.prowlarrClient(r)
	if err != nil || client.BaseURL == "" || client.APIKey == "" {
		http.Error(w, "Configure the Prowlarr URL and API key in Settings", http.StatusServiceUnavailable)
		return
	}
	if _, err := client.SyncIndexers(r.Context(), s.db); err != nil {
		http.Error(w, "Prowlarr indexer sync failed", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	if user, ok := auth.UserFromContext(r.Context()); !ok || user == nil {
		authenticated := false
		if s.sessionMgr != nil {
			if token := s.sessionMgr.GetSessionToken(r); token != "" {
				if _, sOK := s.sessionMgr.GetSession(token); sOK {
					authenticated = true
				}
			}
		}
		if !authenticated {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Send initial connect frame
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			activity, loadErr := s.activityResponse(r.Context())
			if loadErr != nil {
				return
			}
			snapshot, marshalErr := json.Marshal(activity)
			if marshalErr != nil {
				return
			}
			// nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter -- SSE JSON payload, json.Marshal escapes HTML and embedded newlines; never rendered as HTML.
			_, err := fmt.Fprintf(w, "event: activity_snapshot\ndata: %s\n\n", snapshot)
			if err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
