package downloaders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/t0ggl/callimachus/pkg/metadata"
)

// DBTX specifies the standard database execution and querying interface.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// BlocklistEntry represents a recorded release or info-hash blocklist entry.
type BlocklistEntry struct {
	ID        int64     `json:"id"`
	InfoHash  string    `json:"info_hash"`
	Title     string    `json:"title"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// BlocklistManager manages SQLite blocklist records.
type BlocklistManager struct {
	db DBTX
}

// NewBlocklistManager constructs a new BlocklistManager.
func NewBlocklistManager(db DBTX) *BlocklistManager {
	return &BlocklistManager{db: db}
}

var (
	reHash40 = regexp.MustCompile(`(?i)\b([0-9a-f]{40})\b`)
	reBtih   = regexp.MustCompile(`(?i)btih:([0-9a-f]{40})`)
)

// NormalizeInfoHash strips whitespace and converts a 40-character hex torrent hash to lowercase.
func NormalizeInfoHash(h string) string {
	clean := strings.TrimSpace(h)
	if clean == "" {
		return ""
	}
	if m := reBtih.FindStringSubmatch(clean); len(m) > 1 {
		return strings.ToLower(m[1])
	}
	if m := reHash40.FindStringSubmatch(clean); len(m) > 1 {
		return strings.ToLower(m[1])
	}
	if len(clean) == 40 && isHex(clean) {
		return strings.ToLower(clean)
	}
	return ""
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

// Add normalizes the infoHash and records the release in the blocklist table.
// It is idempotent for duplicate hashes and titles.
func (m *BlocklistManager) Add(ctx context.Context, infoHash, title, reason string) error {
	normHash := NormalizeInfoHash(infoHash)
	cleanTitle := strings.TrimSpace(title)
	cleanReason := strings.TrimSpace(reason)
	if cleanReason == "" {
		cleanReason = "blocklisted"
	}

	if normHash != "" {
		const query = `
INSERT INTO blocklist (info_hash, title, reason, created_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(info_hash) DO UPDATE SET
    title = excluded.title,
    reason = excluded.reason;`
		if _, err := m.db.ExecContext(ctx, query, normHash, cleanTitle, cleanReason); err != nil {
			return fmt.Errorf("failed to add blocklist entry: %w", err)
		}
	} else {
		// When info_hash is empty, check for an existing entry with the same title
		var existingRowID int64
		err := m.db.QueryRowContext(ctx, `SELECT rowid FROM blocklist WHERE (info_hash IS NULL OR info_hash = '') AND LOWER(TRIM(title)) = LOWER(TRIM(?)) LIMIT 1;`, cleanTitle).Scan(&existingRowID)
		if err == nil {
			_, err = m.db.ExecContext(ctx, `UPDATE blocklist SET reason = ? WHERE rowid = ?;`, cleanReason, existingRowID)
			return err
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("failed to check existing title blocklist: %w", err)
		}

		const query = `
INSERT INTO blocklist (info_hash, title, reason, created_at)
VALUES (NULL, ?, ?, CURRENT_TIMESTAMP);`
		if _, err := m.db.ExecContext(ctx, query, cleanTitle, cleanReason); err != nil {
			return fmt.Errorf("failed to insert blocklist title: %w", err)
		}
	}

	// Populate id column if schema uses id TEXT PRIMARY KEY and it defaulted to NULL
	_, _ = m.db.ExecContext(ctx, `UPDATE blocklist SET id = CAST(rowid AS TEXT) WHERE id IS NULL;`)
	return nil
}

// IsBlocklisted checks if an infoHash (case-insensitive) or title is present in the blocklist.
func (m *BlocklistManager) IsBlocklisted(ctx context.Context, infoHash, title string) (bool, string, error) {
	normHash := NormalizeInfoHash(infoHash)
	if normHash != "" {
		var reason string
		err := m.db.QueryRowContext(ctx, `SELECT reason FROM blocklist WHERE LOWER(info_hash) = LOWER(?) LIMIT 1;`, normHash).Scan(&reason)
		if err == nil {
			return true, reason, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return false, "", fmt.Errorf("query blocklist by hash failed: %w", err)
		}
	}

	cleanTitle := strings.TrimSpace(title)
	if cleanTitle != "" {
		// 1. Direct case-insensitive match on title
		var reason string
		err := m.db.QueryRowContext(ctx, `SELECT reason FROM blocklist WHERE LOWER(TRIM(title)) = LOWER(TRIM(?)) LIMIT 1;`, cleanTitle).Scan(&reason)
		if err == nil {
			return true, reason, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return false, "", fmt.Errorf("query blocklist by title failed: %w", err)
		}

		// 2. Check candidate title matches
		sanitizedInput := metadata.SanitizeTitle(cleanTitle)
		parsedRelease := metadata.ParseRelease(cleanTitle)
		parsedInput := parsedRelease.Title

		candidateQueries := make(map[string]bool)
		for _, q := range []string{
			cleanTitle,
			sanitizedInput,
			parsedInput,
			parsedRelease.Author + " " + parsedRelease.Title,
			parsedRelease.Author + " - " + parsedRelease.Title,
		} {
			tTrim := strings.TrimSpace(q)
			if tTrim != "" && tTrim != "-" {
				candidateQueries[strings.ToLower(tTrim)] = true
			}
		}

		for cq := range candidateQueries {
			err = m.db.QueryRowContext(ctx, `SELECT reason FROM blocklist WHERE LOWER(TRIM(title)) = ? LIMIT 1;`, cq).Scan(&reason)
			if err == nil {
				return true, reason, nil
			}
		}

		// 3. Fallback: normalize against stored blocklist titles
		normCandidates := make([]string, 0, len(candidateQueries))
		for cq := range candidateQueries {
			if n := normalizeTitleForMatch(cq); n != "" {
				normCandidates = append(normCandidates, n)
			}
		}

		rows, err := m.db.QueryContext(ctx, `SELECT title, reason FROM blocklist WHERE title IS NOT NULL AND title != '';`)
		if err != nil {
			return false, "", fmt.Errorf("query blocklist titles failed: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var rowTitle, rowReason string
			if err := rows.Scan(&rowTitle, &rowReason); err != nil {
				return false, "", err
			}
			rowClean := strings.TrimSpace(rowTitle)
			rowParsed := metadata.ParseRelease(rowClean)
			rowCandidates := []string{
				normalizeTitleForMatch(rowClean),
				normalizeTitleForMatch(metadata.SanitizeTitle(rowClean)),
				normalizeTitleForMatch(rowParsed.Title),
				normalizeTitleForMatch(rowParsed.Author + " " + rowParsed.Title),
				normalizeTitleForMatch(rowParsed.Author + " - " + rowParsed.Title),
			}

			for _, rc := range rowCandidates {
				if rc == "" {
					continue
				}
				for _, nc := range normCandidates {
					if rc == nc {
						return true, rowReason, nil
					}
				}
			}
		}
		if err := rows.Err(); err != nil {
			return false, "", err
		}
	}

	return false, "", nil
}

// Remove deletes a blocklist entry by its ID.
func (m *BlocklistManager) Remove(ctx context.Context, id int64) error {
	const query = `DELETE FROM blocklist WHERE rowid = ? OR id = ? OR id = CAST(? AS TEXT);`
	if _, err := m.db.ExecContext(ctx, query, id, id, id); err != nil {
		return fmt.Errorf("failed to remove blocklist entry %d: %w", id, err)
	}
	return nil
}

// List returns blocklist entries with pagination support.
func (m *BlocklistManager) List(ctx context.Context, limit, offset int) ([]BlocklistEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	const query = `
SELECT rowid, COALESCE(info_hash, ''), title, reason, created_at
FROM blocklist
ORDER BY created_at DESC, rowid DESC
LIMIT ? OFFSET ?;`

	rows, err := m.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query blocklist: %w", err)
	}
	defer rows.Close()

	var entries []BlocklistEntry
	for rows.Next() {
		var entry BlocklistEntry
		var rawCreatedAt any
		if err := rows.Scan(&entry.ID, &entry.InfoHash, &entry.Title, &entry.Reason, &rawCreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan blocklist row: %w", err)
		}
		entry.CreatedAt = parseDBTime(rawCreatedAt)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading blocklist rows: %w", err)
	}

	if entries == nil {
		entries = []BlocklistEntry{}
	}
	return entries, nil
}

var (
	reYearNoise  = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	reMediaNoise = regexp.MustCompile(`(?i)\b(epub|m4b|mp3|pdf|azw3|mobi|flac|aac|cbr|cbz|retail|webrip|ebook|audiobook|unabridged|abridged)\b`)
)

func normalizeTitleForMatch(s string) string {
	s = strings.ToLower(s)
	s = reYearNoise.ReplaceAllString(s, " ")
	s = reMediaNoise.ReplaceAllString(s, " ")
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func parseDBTime(val any) time.Time {
	switch v := val.(type) {
	case time.Time:
		return v
	case string:
		return parseTimeString(v)
	case []byte:
		return parseTimeString(string(v))
	default:
		return time.Time{}
	}
}

func parseTimeString(s string) time.Time {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}
	trimmed := strings.TrimSpace(s)
	for _, l := range layouts {
		if t, err := time.Parse(l, trimmed); err == nil {
			return t
		}
	}
	return time.Time{}
}
