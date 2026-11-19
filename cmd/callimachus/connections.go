package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/config"
)

// Connection checks only read configuration; neither persistence nor indexer sync runs here.
func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	reply := func(status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
	}
	service := r.PathValue("service")
	var draft struct {
		URL    *string `json:"url"`
		APIKey string  `json:"apiKey"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&draft); err != nil {
		if !(errors.Is(err, io.EOF) && service == "openlibrary") {
			reply(400, "Invalid connection test request.")
			return
		}
	}
	cfg, err := config.Load(r.Context(), s.db, s.baseOverrides)
	if err != nil {
		reply(500, "Unable to load configuration. Check server configuration.")
		return
	}
	value := func(key, stored, entered string, supplied bool) string {
		if cfg.Overrides[key].IsOverridden || !supplied {
			return stored
		}
		return strings.TrimSpace(entered)
	}
	endpoint, method, body, key := "", http.MethodGet, "", ""
	switch service {
	case "prowlarr":
		base := cfg.Settings.ProwlarrURL
		if draft.URL != nil {
			base = value("prowlarr_url", base, *draft.URL, true)
		}
		u, parseErr := url.Parse(base)
		if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			reply(400, "Enter a valid HTTP or HTTPS Prowlarr URL without credentials or query parameters.")
			return
		}
		key = value("prowlarr_api_key", cfg.Settings.ProwlarrAPIKey, draft.APIKey, strings.TrimSpace(draft.APIKey) != "")
		endpoint = strings.TrimRight(base, "/") + "/api/v1/system/status"
	case "google-books":
		key = value("google_books_api_key", cfg.Settings.GoogleBooksAPIKey, draft.APIKey, strings.TrimSpace(draft.APIKey) != "")
		endpoint = "https://www.googleapis.com/books/v1/volumes?q=isbn:9780140328721&maxResults=1"
		if key != "" {
			endpoint += "&key=" + url.QueryEscape(key)
		}
	case "hardcover":
		key = value("hardcover_api_key", cfg.Settings.HardcoverAPIKey, draft.APIKey, strings.TrimSpace(draft.APIKey) != "")
		endpoint, method, body = "https://api.hardcover.app/v1/graphql", http.MethodPost, `{"query":"query { me { id } }"}`
	case "openlibrary":
		endpoint = "https://openlibrary.org/search.json?q=isbn:9780140328721&limit=1"
	default:
		reply(404, "Unknown connection service.")
		return
	}
	if service != "google-books" && service != "openlibrary" && key == "" {
		reply(400, "Enter an API key before testing the connection.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(body))
	if err != nil {
		reply(400, "Invalid connection configuration. Check the URL and API key.")
		return
	}
	if service == "prowlarr" {
		req.Header.Set("X-Api-Key", key)
	}
	if service == "hardcover" {
		if !strings.HasPrefix(strings.ToLower(key), "bearer ") {
			key = "Bearer " + key
		}
		req.Header.Set("Authorization", key)
		req.Header.Set("Content-Type", "application/json")
	}
	client := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if s.prowlarrHTTPClient != nil {
		client.Transport = s.prowlarrHTTPClient.Transport
	}
	resp, err := client.Do(req)
	// Never expose upstream bodies or transport errors: they can contain API keys.
	if err != nil {
		reply(502, "Unable to reach service. Check the URL, network access and TLS configuration, then retry.")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		reply(502, "Authentication failed. Check the API key and service permissions.")
		return
	}
	if resp.StatusCode == 429 {
		reply(502, "Service rate limit reached. Check your quota and retry later.")
		return
	}
	if resp.StatusCode != 200 {
		reply(502, "Service rejected the request. Check the endpoint and API configuration.")
		return
	}
	var result struct {
		Version string            `json:"version"`
		Kind    string            `json:"kind"`
		Docs    []json.RawMessage `json:"docs"`
		Errors  []json.RawMessage `json:"errors"`
		Data    struct {
			Me []struct {
				ID int `json:"id"`
			} `json:"me"`
		} `json:"data"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result)
	valid := err == nil && len(result.Errors) == 0
	switch service {
	case "prowlarr":
		valid = valid && result.Version != ""
	case "google-books":
		valid = valid && result.Kind == "books#volumes"
	case "hardcover":
		valid = valid && len(result.Data.Me) > 0 && result.Data.Me[0].ID > 0
	case "openlibrary":
		valid = valid && result.Docs != nil
	}
	if !valid {
		reply(502, "Service returned an unexpected response. Check the endpoint, API key and service permissions.")
		return
	}
	reply(200, "Connection successful.")
}
