package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 500 {
		http.Error(w, "Enter a title, author, or keywords", 400)
		return
	}
	ctx := r.Context()
	slog.DebugContext(ctx, "metadata discovery search started")
	cfg, err := config.Load(ctx, s.db, s.baseOverrides)
	if err != nil {
		http.Error(w, "Unable to load metadata settings", 500)
		return
	}
	client := s.prowlarrHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	providers := []metadata.Provider{metadata.NewOpenLibraryProvider(metadata.WithOpenLibraryClient(client))}
	if cfg.Settings.HardcoverAPIKey != "" {
		providers = append(providers, metadata.NewHardcoverProvider(cfg.Settings.HardcoverAPIKey, metadata.WithHardcoverClient(client)))
	}
	books := []metadata.BookMetadata{}
	success := false
	for _, p := range providers {
		found, err := p.Search(ctx, query)
		logMetadataResponse(ctx, p.Name(), len(found), err)
		if err == nil {
			success = true
			books = append(found, books...)
		}
	}
	if cfg.Settings.GoogleBooksAPIKey != "" {
		values := url.Values{"q": {query}, "maxResults": {"20"}}
		values.Set("key", cfg.Settings.GoogleBooksAPIKey)
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/books/v1/volumes?"+values.Encode(), nil)
		resp, err := client.Do(req)
		if err != nil {
			logMetadataResponse(ctx, "Google Books", 0, err)
		} else {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				var result struct {
					Items []struct {
						VolumeInfo struct {
							Title         string
							Authors       []string
							PublishedDate string
							Description   string
							Language      string
							ImageLinks    struct{ Thumbnail string }
						}
					}
				}
				err := json.NewDecoder(resp.Body).Decode(&result)
				logMetadataResponse(ctx, "Google Books", len(result.Items), err)
				if err == nil {
					success = true
					for _, item := range result.Items {
						v := item.VolumeInfo
						year := 0
						fmt.Sscanf(v.PublishedDate, "%d", &year)
						books = append(books, metadata.BookMetadata{Title: v.Title, Authors: v.Authors, Year: year, Description: v.Description, Language: v.Language, CoverURL: v.ImageLinks.Thumbnail})
					}
				}
			} else {
				slog.WarnContext(ctx, "metadata provider returned an error status", "provider", "Google Books", "status", resp.StatusCode)
			}
		}
	}
	if !success {
		http.Error(w, "Metadata sources unavailable. Try again shortly.", 502)
		return
	}
	type match struct {
		metadata.BookMetadata
		LibraryBookID string `json:"library_book_id,omitempty"`
	}
	inventory := map[string]string{}
	rows, err := s.db.QueryContext(r.Context(), `SELECT DISTINCT b.id,b.title,a.name FROM books b JOIN authors a ON a.id=b.author_id JOIN book_editions e ON e.book_id=b.id JOIN media_assets m ON m.edition_id=e.id`)
	if err != nil {
		http.Error(w, "Unable to read library inventory", 500)
		return
	}
	for rows.Next() {
		var id, title, author string
		if err := rows.Scan(&id, &title, &author); err != nil {
			rows.Close()
			http.Error(w, "Unable to read inventory", 500)
			return
		}
		inventory[discoveryKey(title, author)] = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "Unable to read inventory", 500)
		return
	}
	out := []match{}
	seen := map[string]bool{}
	for _, b := range books {
		key := strings.ToLower(b.Title + strings.Join(b.Authors, ","))
		if b.Title != "" && len(b.Authors) > 0 && !seen[key] {
			id := ""
			for _, author := range b.Authors {
				if found := inventory[discoveryKey(b.Title, author)]; found != "" {
					id = found
					break
				}
			}
			out = append(out, match{b, id})
			seen[key] = true
		}
	}
	slog.DebugContext(ctx, "metadata discovery search completed", "results", len(out))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func logMetadataResponse(ctx context.Context, provider string, count int, err error) {
	if err != nil {
		slog.WarnContext(ctx, "metadata provider search failed", "provider", provider)
		return
	}
	slog.DebugContext(ctx, "metadata provider search completed", "provider", provider, "results", count)
}

func discoveryKey(title, author string) string {
	return strings.ToLower(strings.TrimSpace(metadata.SanitizeTitle(title))) + "\x00" + strings.ToLower(strings.TrimSpace(metadata.NormalizeAuthor(author)))
}
