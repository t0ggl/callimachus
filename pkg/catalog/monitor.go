package catalog

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

// Monitor shares one serialized execution path for scheduled and manual work.
// Scan timestamps live in SQLite so restarts preserve the cadence.
type Monitor struct {
	DB       *database.DB
	Discover func(context.Context, string, string, []metadata.BookMetadata) ([]metadata.BookMetadata, error)
	Search   func(context.Context, string) error
	mu       sync.Mutex
}

func entityColumn(kind string) (string, error) {
	switch kind {
	case "authors":
		return "name", nil
	case "series":
		return "title", nil
	}
	return "", fmt.Errorf("invalid entity kind")
}

func (m *Monitor) SetMonitored(ctx context.Context, kind, id string, enabled bool) error {
	if kind == "series" && !enabled {
		return m.SetMonitoring(ctx, kind, id, &enabled, &enabled)
	}
	return m.SetMonitoring(ctx, kind, id, &enabled, nil)
}

// SetMonitoring updates the entity and reconciles its known books in one transaction.
func (m *Monitor) SetMonitoring(ctx context.Context, kind, id string, ebook, audiobook *bool) error {
	if _, err := entityColumn(kind); err != nil {
		return err
	}
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var result sql.Result
	if kind == "series" {
		result, err = tx.ExecContext(ctx, `UPDATE series SET monitored=COALESCE(?, monitored),
			audiobook_monitored=COALESCE(?, audiobook_monitored), discovery_checked_at=NULL WHERE id=?`, ebook, audiobook, id)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE authors SET monitored=?, discovery_checked_at=NULL WHERE id=?`, ebook, id)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}

	if err := reconcileMonitoring(ctx, tx, kind, id, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// reconcileMonitoring only adds missing formats. Disabling an entity stops future
// inheritance while the saved per-book monitoring choices remain in effect.
func reconcileMonitoring(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, kind, id, discoveredBookID string) error {
	if kind == "authors" {
		// Authors watch for either format until one is acquired. Keep the other
		// format available when the user explicitly opted out of just one.
		scope := "books.author_id=authors.id"
		args := []any{id}
		if discoveredBookID != "" {
			// A provider can discover a coauthored book under its lead author.
			scope = "books.id=?"
			args = append(args, discoveredBookID)
		}
		_, err := db.ExecContext(ctx, `UPDATE books
			SET ebook_monitored=NOT ebook_monitoring_excluded,
			    audiobook_monitored=CASE
			        WHEN audiobook_monitoring_excluded THEN 0
			        WHEN ebook_monitoring_excluded OR audiobook_monitored=1 THEN 1
			    END,
			    availability_checked_at=NULL, updated_at=CURRENT_TIMESTAMP
			FROM authors
			WHERE authors.id=? AND authors.monitored=1 AND `+scope+`
			  AND ebook_monitored=0
			  AND (ebook_monitoring_excluded=0 OR (audiobook_monitoring_excluded=0 AND audiobook_monitored IS NOT 1))
			  AND NOT EXISTS (
			      SELECT 1 FROM book_editions e JOIN media_assets a ON a.edition_id=e.id WHERE e.book_id=books.id
			  )`, args...)
		return err
	}

	for _, format := range []struct {
		parent, exclusion string
		audio             bool
	}{
		{"monitored", "ebook_monitoring_excluded", false},
		{"audiobook_monitored", "audiobook_monitoring_excluded", true},
	} {
		assignment := "ebook_monitored=1, audiobook_monitored=COALESCE(books.audiobook_monitored, books.ebook_monitored)"
		missing := "(ebook_monitored=0 OR audiobook_monitored IS NULL)"
		if format.audio {
			assignment = "audiobook_monitored=1"
			missing = "audiobook_monitored IS NOT 1"
		}
		_, err := db.ExecContext(ctx, `UPDATE books
			SET `+assignment+`, availability_checked_at=NULL, updated_at=CURRENT_TIMESTAMP
			WHERE `+missing+` AND `+format.exclusion+`=0
			  AND id IN (
			      SELECT bs.book_id FROM book_series bs JOIN series s ON s.id=bs.series_id
			      WHERE bs.series_id=? AND s.`+format.parent+`=1
			  )
			  AND NOT EXISTS (
			      SELECT 1 FROM book_editions e JOIN media_assets a ON a.edition_id=e.id
			      WHERE e.book_id=books.id AND (e.format IN ('m4b','mp3'))=?
			  )`, id, format.audio)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Monitor) Queue(ctx context.Context, kind, id, action string) error {
	col, err := entityColumn(kind)
	if err != nil {
		return err
	}
	if action != "discover" && action != "downloads" {
		return fmt.Errorf("invalid action")
	}
	var name string
	if err = m.DB.QueryRowContext(ctx, "SELECT "+col+" FROM "+kind+" WHERE id=?", id).Scan(&name); err != nil {
		return err
	}
	_, err = m.DB.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_actions(kind,entity_id,action) VALUES(?,?,?)`, kind, id, action)
	return err
}

func (m *Monitor) Tick(ctx context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	slog.DebugContext(ctx, "catalog monitoring check started")
	now = now.UTC()
	var failures []string
	for _, kind := range []string{"authors", "series"} {
		col, _ := entityColumn(kind)
		audio := "monitored"
		if kind == "series" {
			audio = "audiobook_monitored"
		}
		rows, err := m.DB.QueryContext(ctx, "SELECT id,"+col+",monitored,"+audio+",COALESCE(discovery_checked_at,''),EXISTS(SELECT 1 FROM catalog_actions WHERE kind=? AND entity_id="+kind+".id AND action='discover'),EXISTS(SELECT 1 FROM catalog_actions WHERE kind=? AND entity_id="+kind+".id AND action='downloads') FROM "+kind+" WHERE (monitored=1 OR "+audio+"=1) OR EXISTS(SELECT 1 FROM catalog_actions WHERE kind=? AND entity_id="+kind+".id)", kind, kind, kind)
		if err != nil {
			return err
		}
		type entity struct {
			id, name, last                        string
			ebook, audiobook, discover, downloads bool
		}
		var entities []entity
		for rows.Next() {
			var e entity
			if err = rows.Scan(&e.id, &e.name, &e.ebook, &e.audiobook, &e.last, &e.discover, &e.downloads); err != nil {
				rows.Close()
				return err
			}
			entities = append(entities, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range entities {
			last, _ := time.Parse(time.RFC3339, e.last)
			// Clamp month ends rather than letting AddDate skip February.
			next := last.AddDate(0, 1, -last.Day()+1)
			day := min(last.Day(), next.AddDate(0, 1, -1).Day())
			next = next.AddDate(0, 0, day-1)
			if e.discover || ((e.ebook || e.audiobook) && (last.IsZero() || !now.Before(next))) {
				slog.DebugContext(ctx, "catalog discovery check", "entity_kind", kind, "entity_id", e.id)
				err := m.discover(ctx, kind, e.id, e.name)
				if err != nil {
					failures = append(failures, err.Error())
					continue
				}
				if _, err = m.DB.ExecContext(ctx, "UPDATE "+kind+" SET discovery_checked_at=? WHERE id=?", now.Format(time.RFC3339), e.id); err != nil {
					return err
				}
				if _, err = m.DB.ExecContext(ctx, `DELETE FROM catalog_actions WHERE kind=? AND entity_id=? AND action='discover'`, kind, e.id); err != nil {
					return err
				}
			}
			if e.downloads {
				column := "author_id"
				if kind == "series" {
					column = "series_id"
				}
				if err = m.checkBooks(ctx, now, column, e.id, true); err != nil {
					failures = append(failures, err.Error())
					continue
				}
				if _, err = m.DB.ExecContext(ctx, `DELETE FROM catalog_actions WHERE kind=? AND entity_id=? AND action='downloads'`, kind, e.id); err != nil {
					return err
				}
			}
		}
	}
	if err := m.checkBooks(ctx, now, "", "", false); err != nil {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		return fmt.Errorf("catalog monitor: %s", strings.Join(failures, "; "))
	}
	slog.DebugContext(ctx, "catalog monitoring check completed")
	return nil
}

func (m *Monitor) discover(ctx context.Context, kind, id, name string) error {
	c := New(m.DB)
	var known []metadata.BookMetadata
	if kind == "series" {
		var err error
		known, err = c.SeriesEvidence(ctx, id)
		if errors.Is(err, ErrAmbiguousSeries) {
			// A mixed legacy row needs explicit correction before monitoring can infer its identity.
			return nil
		}
		if err != nil {
			return err
		}
	}
	found, err := m.Discover(ctx, kind, name, known)
	if err != nil {
		return err
	}
	if err := m.persist(ctx, kind, id, name, known, found); err != nil {
		return err
	}
	return reconcileMonitoring(ctx, m.DB, kind, id, "")
}

func (m *Monitor) persist(ctx context.Context, kind, id, name string, known, found []metadata.BookMetadata) error {
	c := New(m.DB)
	var authors []string
	var providerID int
	for _, b := range known {
		authors = append(authors, b.Authors...)
		if b.HardcoverSeriesID > 0 {
			providerID = b.HardcoverSeriesID
		}
	}
	// Establish a single provider ID before writing any results. Mixed responses
	// from an unpinned lookup give no safe way to choose a series.
	if kind == "series" && providerID == 0 {
		for _, b := range found {
			if b.HardcoverSeriesID == 0 || !strings.EqualFold(b.Series, name) || !metadata.SharesAuthor(b.Authors, authors) {
				continue
			}
			if providerID != 0 && providerID != b.HardcoverSeriesID {
				return nil
			}
			providerID = b.HardcoverSeriesID
		}
	}
	pinned := len(known) == 1 && known[0].HardcoverSeriesID > 0
	if kind == "series" && providerID > 0 && !pinned {
		result, err := m.DB.ExecContext(ctx, `UPDATE series SET hardcover_id = ? WHERE id = ?
   AND (hardcover_id IS NULL OR hardcover_id = ?) AND NOT EXISTS
   (SELECT 1 FROM series WHERE hardcover_id = ? AND id <> ?)`, providerID, id, providerID, providerID, id)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
	}
	if kind == "series" {
		found = deduplicateDiscoveredSeriesBooks(found, name)
	}
	for _, b := range found {
		if b.Title == "" || len(b.Authors) == 0 || b.Authors[0] == "" ||
			metadata.IsNonSingleBookTitle(b.Title, b.Subtitle) {
			continue
		}
		if kind == "series" {
			if !strings.EqualFold(b.Series, name) || (providerID > 0 && b.HardcoverSeriesID != providerID) ||
				(!pinned && !metadata.SharesAuthor(b.Authors, authors)) {
				continue
			}
		} else if !metadata.SharesAuthor(b.Authors, []string{name}) {
			continue
		}
		author, err := c.GetOrCreateAuthor(ctx, b.Authors[0])
		if err != nil {
			return err
		}
		primary := metadata.SeriesMembership{Title: b.Series, Index: b.SeriesIndex, HardcoverID: b.HardcoverSeriesID}
		if b.ContextualSeries {
			primary = metadata.SeriesMembership{}
			if b.PrimarySeries != nil {
				primary = *b.PrimarySeries
			}
		}
		book := &database.Book{AuthorID: author.ID, Title: b.Title, SeriesIndex: primary.Index, Synopsis: &b.Description}
		if kind == "series" && !b.ContextualSeries {
			book.SeriesID = &id
		} else {
			series, err := c.GetOrCreateSeries(ctx, primary.Title, author.ID, primary.HardcoverID)
			if err != nil {
				return err
			}
			if series != nil {
				book.SeriesID = &series.ID
			}
		}
		if releaseDate := metadata.NormalizeReleaseDate(b.PublishDate, b.Year); releaseDate != "" {
			book.ReleaseDate = &releaseDate
		}
		var saved *database.Book
		if kind == "series" && (book.SeriesID == nil || *book.SeriesID != id) {
			if existing, hasMedia, findErr := c.FindBookInSeries(ctx, id, name, b.Title, b.SeriesIndex); findErr == nil {
				saved, err = c.mergeExistingBook(ctx, existing, hasMedia, book, metadata.SanitizeTitle(b.Title), name)
				if err != nil {
					return err
				}
			} else if !errors.Is(findErr, sql.ErrNoRows) {
				return findErr
			}
		}
		if saved == nil {
			saved, err = c.GetOrCreateBook(ctx, book)
			if err != nil {
				return err
			}
		}
		if book.SeriesID != nil {
			if err := c.SaveObservedSeries(ctx, saved.ID, *book.SeriesID, primary.Index); err != nil {
				return err
			}
		}
		if kind == "series" {
			if err := c.SaveObservedSeries(ctx, saved.ID, id, b.SeriesIndex); err != nil {
				return err
			}
		}
		if err = c.SaveSeriesMemberships(ctx, saved.ID, author.ID, b.SeriesMemberships, IdealSourceMatch); err != nil {
			return err
		}
		if saved.SeriesID == nil || book.SeriesID == nil || *saved.SeriesID != *book.SeriesID {
			primary.Title, primary.Index = "", nil
		}
		if err = c.SaveIdealMetadata(ctx, saved.ID, saved.Title, primary.Title, primary.Index, IdealSourceMatch); err != nil {
			return err
		}
		// Announcement dates can move. Preserve monitoring while updating a known date without dropping precision for the same period.
		if book.ReleaseDate != nil {
			existing := ""
			if saved.ReleaseDate != nil {
				existing = *saved.ReleaseDate
			}
			if merged := metadata.MergeReleaseDate(existing, *book.ReleaseDate); merged != "" && merged != existing {
				saved.ReleaseDate = &merged
				if err = c.UpsertBook(ctx, saved); err != nil {
					return err
				}
			}
		}
		for _, format := range []string{"epub", "m4b"} {
			if _, err = c.GetOrCreateEdition(ctx, &database.BookEdition{BookID: saved.ID, Format: format, Language: b.Language, PublicationDate: saved.ReleaseDate}); err != nil {
				return err
			}
		}
		if kind == "authors" && saved.AuthorID != id {
			if err := reconcileMonitoring(ctx, m.DB, kind, id, saved.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func deduplicateDiscoveredSeriesBooks(found []metadata.BookMetadata, seriesTitle string) []metadata.BookMetadata {
	if len(found) <= 1 {
		return found
	}
	kept := make([]metadata.BookMetadata, 0, len(found))
	for _, item := range found {
		if item.SeriesIndex == nil {
			if idx := effectiveSeriesIndex(nil, item.Title, seriesTitle); idx != nil {
				v := *idx
				item.SeriesIndex = &v
			}
		}
		itemKey := metadata.CanonicalSeriesBookTitleKey(item.Title, seriesTitle)
		matchIdx := -1
		for i := range kept {
			if metadata.DistinctTitlePartIndices(kept[i].Title, item.Title) {
				continue
			}
			if item.HardcoverSeriesID > 0 && kept[i].HardcoverSeriesID > 0 && item.HardcoverSeriesID != kept[i].HardcoverSeriesID {
				continue
			}
			if len(item.Authors) > 0 && len(kept[i].Authors) > 0 && !metadata.SharesAuthor(item.Authors, kept[i].Authors) {
				continue
			}
			sameIdx := sameSeriesIndex(kept[i].SeriesIndex, item.SeriesIndex)
			conflictIdx := kept[i].SeriesIndex != nil && item.SeriesIndex != nil && !sameIdx
			sameTitle := itemKey != "" && metadata.CanonicalSeriesBookTitleKey(kept[i].Title, seriesTitle) == itemKey
			if sameIdx || (sameTitle && !conflictIdx) {
				matchIdx = i
				break
			}
		}
		if matchIdx < 0 {
			kept = append(kept, item)
			continue
		}
		dst := &kept[matchIdx]
		if metadata.PreferBookTitle(item.Title, dst.Title, seriesTitle) {
			dst.Title = item.Title
		}
		if dst.SeriesIndex == nil && item.SeriesIndex != nil {
			v := *item.SeriesIndex
			dst.SeriesIndex = &v
		}
		dst.Subtitle = cmp.Or(dst.Subtitle, item.Subtitle)
		dst.Description = cmp.Or(dst.Description, item.Description)
		dst.CoverURL = cmp.Or(dst.CoverURL, item.CoverURL)
		dst.Language = cmp.Or(dst.Language, item.Language)
		dst.PublishDate = cmp.Or(metadata.MergeReleaseDate(dst.PublishDate, metadata.NormalizeReleaseDate(item.PublishDate, item.Year)), dst.PublishDate)
		if dst.Year == 0 {
			dst.Year = item.Year
		}
		if dst.HardcoverSeriesID == 0 {
			dst.HardcoverSeriesID = item.HardcoverSeriesID
		}
		if dst.PrimarySeries == nil && item.PrimarySeries != nil {
			p := *item.PrimarySeries
			dst.PrimarySeries = &p
		}
		for _, membership := range item.SeriesMemberships {
			dst.AddSeriesMembership(membership)
		}
	}
	return kept
}

type checkBookCandidate struct {
	id          string
	authorID    string
	title       string
	subtitle    string
	releaseDate string
	seriesID    string
	seriesTitle string
	seriesIndex *float64
	formats     map[string]bool
}

func (m *Monitor) checkBooks(ctx context.Context, now time.Time, column, id string, force bool) error {
	query := `SELECT b.id, b.author_id, b.title, COALESCE(b.subtitle,''), COALESCE(b.release_date,''),
		       COALESCE(b.series_id,''), COALESCE(s.title,''), b.series_index, f.format
		FROM books b
		JOIN missing_monitored_formats f ON f.book_id=b.id
		LEFT JOIN series s ON s.id=b.series_id
		WHERE NOT EXISTS(SELECT 1 FROM hidden_library_review review WHERE review.book_id=b.id AND review.kept=0)`
	var args []any
	if column != "" {
		if column == "series_id" {
			query += " AND b.id IN (SELECT book_id FROM book_series WHERE series_id=?)"
		} else {
			query += " AND b.author_id=?"
		}
		args = append(args, id)
	}
	if !force {
		query += ` AND (b.availability_checked_at IS NULL OR b.availability_checked_at<=?)`
		args = append(args, now.Add(-24*time.Hour).Format(time.RFC3339))
	}
	query += ` ORDER BY b.rowid, f.format`
	rows, err := m.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	today := now.UTC().Format(time.DateOnly)
	byID := make(map[string]*checkBookCandidate)
	var ordered []*checkBookCandidate
	for rows.Next() {
		var bookID, authorID, title, subtitle, releaseDate, seriesID, seriesTitle, format string
		var seriesIndex *float64
		if err = rows.Scan(&bookID, &authorID, &title, &subtitle, &releaseDate, &seriesID, &seriesTitle, &seriesIndex, &format); err != nil {
			rows.Close()
			return err
		}
		if metadata.IsNonSingleBookTitle(title, subtitle) || metadata.IsFutureRelease(releaseDate, 0, today) {
			continue
		}
		cand := byID[bookID]
		if cand == nil {
			cand = &checkBookCandidate{
				id:          bookID,
				authorID:    authorID,
				title:       title,
				subtitle:    subtitle,
				releaseDate: releaseDate,
				seriesID:    seriesID,
				seriesTitle: seriesTitle,
				seriesIndex: effectiveSeriesIndex(seriesIndex, title, seriesTitle),
				formats:     map[string]bool{},
			}
			byID[bookID] = cand
			ordered = append(ordered, cand)
		}
		cand.formats[format] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	ids, err := m.deduplicateCheckBookCandidates(ctx, ordered)
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "catalog availability check", "books", len(ids), "forced", force)
	for _, id := range ids {
		if err = m.Search(ctx, id); err != nil {
			return err
		}
		if _, err = m.DB.ExecContext(ctx, `UPDATE books SET availability_checked_at=? WHERE id=?`, now.Format(time.RFC3339), id); err != nil {
			return err
		}
	}
	return nil
}

type checkBookSeriesSlot struct {
	seriesID    string
	seriesTitle string
	seriesIndex *float64
}

// deduplicateCheckBookCandidates prevents queueing duplicate downloads when
// malformed/duplicate rows exist for the same series slot or normalized title.
func (m *Monitor) deduplicateCheckBookCandidates(ctx context.Context, candidates []*checkBookCandidate) ([]string, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	slotsByBook := make(map[string][]checkBookSeriesSlot, len(candidates))
	for _, c := range candidates {
		rows, err := m.DB.QueryContext(ctx, `
			SELECT bs.series_id, s.title, bs.series_index
			FROM book_series bs
			JOIN series s ON s.id = bs.series_id
			WHERE bs.book_id = ?`, c.id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var sl checkBookSeriesSlot
			if err := rows.Scan(&sl.seriesID, &sl.seriesTitle, &sl.seriesIndex); err != nil {
				rows.Close()
				return nil, err
			}
			sl.seriesIndex = effectiveSeriesIndex(sl.seriesIndex, c.title, sl.seriesTitle)
			slotsByBook[c.id] = append(slotsByBook[c.id], sl)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	// Step 1: Drop any candidate whose missing formats are already satisfied (owned or actively requested)
	// by another catalog row for the same series slot or normalized title.
	eligible := make([]*checkBookCandidate, 0, len(candidates))
	for _, c := range candidates {
		satisfied, err := m.coveredBySiblingBook(ctx, c, slotsByBook[c.id])
		if err != nil {
			return nil, err
		}
		if !satisfied {
			eligible = append(eligible, c)
		}
	}

	// Step 2: Among remaining monitored candidates in this pass, keep one representative per
	// (series, index) or normalized title, preferring the cleanest/best title.
	type keptEntry struct {
		cand  *checkBookCandidate
		slots []checkBookSeriesSlot
	}
	var kept []keptEntry
	for _, c := range eligible {
		cSlots := slotsByBook[c.id]
		matchIdx := -1
		matchSeries := c.seriesTitle
		for i, k := range kept {
			if metadata.DistinctTitlePartIndices(k.cand.title, c.title) {
				continue
			}
			matched := false
			for _, cs := range cSlots {
				for _, ks := range k.slots {
					if cs.seriesID != ks.seriesID {
						continue
					}
					if sameSeriesIndex(cs.seriesIndex, ks.seriesIndex) {
						matched = true
						matchSeries = cs.seriesTitle
						break
					}
					ck := metadata.CanonicalSeriesBookTitleKey(c.title, cs.seriesTitle)
					kk := metadata.CanonicalSeriesBookTitleKey(k.cand.title, ks.seriesTitle)
					conflict := cs.seriesIndex != nil && ks.seriesIndex != nil && !sameSeriesIndex(cs.seriesIndex, ks.seriesIndex)
					if ck != "" && ck == kk && !conflict {
						matched = true
						matchSeries = cs.seriesTitle
						break
					}
				}
				if matched {
					break
				}
			}
			if !matched && c.authorID == k.cand.authorID {
				series := cmp.Or(c.seriesTitle, k.cand.seriesTitle)
				ck := metadata.CanonicalSeriesBookTitleKey(c.title, series)
				kk := metadata.CanonicalSeriesBookTitleKey(k.cand.title, series)
				conflict := c.seriesID != "" && c.seriesID == k.cand.seriesID && c.seriesIndex != nil && k.cand.seriesIndex != nil && !sameSeriesIndex(c.seriesIndex, k.cand.seriesIndex)
				if ck != "" && ck == kk && !conflict {
					matched = true
					matchSeries = series
				}
			}
			if matched {
				matchIdx = i
				break
			}
		}
		if matchIdx < 0 {
			kept = append(kept, keptEntry{cand: c, slots: cSlots})
			continue
		}
		if metadata.PreferBookTitle(c.title, kept[matchIdx].cand.title, matchSeries) {
			kept[matchIdx] = keptEntry{cand: c, slots: cSlots}
		}
	}

	ids := make([]string, 0, len(kept))
	for _, k := range kept {
		ids = append(ids, k.cand.id)
	}
	return ids, nil
}

func (m *Monitor) coveredBySiblingBook(ctx context.Context, cand *checkBookCandidate, slots []checkBookSeriesSlot) (bool, error) {
	// Check whether every missing monitored format on cand is already owned or actively requested on a sibling book.
	covered := make(map[string]bool, len(cand.formats))
	markFormats := func(siblingID string) error {
		rows, err := m.DB.QueryContext(ctx, `
			SELECT CASE WHEN e.format IN ('m4b','mp3') THEN 'm4b' ELSE 'epub' END
			FROM book_editions e JOIN media_assets a ON a.edition_id = e.id
			WHERE e.book_id = ?
			UNION
			SELECT CASE WHEN r.format = 'both' THEN 'both' ELSE r.format END
			FROM book_requests r
			WHERE r.book_id = ? AND r.dismissed = 0 AND r.status IN ('searching','downloading','available')`, siblingID, siblingID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var f string
			if err := rows.Scan(&f); err != nil {
				return err
			}
			if f == "both" {
				covered["epub"] = true
				covered["m4b"] = true
			} else {
				covered[f] = true
			}
		}
		return rows.Err()
	}

	allCovered := func() bool {
		for f := range cand.formats {
			if !covered[f] {
				return false
			}
		}
		return len(cand.formats) > 0
	}

	// Check siblings in the same series.
	for _, sl := range slots {
		rows, err := m.DB.QueryContext(ctx, `
			SELECT b.id, b.title, bs.series_index
			FROM books b
			JOIN book_series bs ON bs.book_id = b.id
			WHERE bs.series_id = ? AND b.id <> ?`, sl.seriesID, cand.id)
		if err != nil {
			return false, err
		}
		candKey := metadata.CanonicalSeriesBookTitleKey(cand.title, sl.seriesTitle)
		var matchedIDs []string
		for rows.Next() {
			var sibID, sibTitle string
			var sibIdx *float64
			if err := rows.Scan(&sibID, &sibTitle, &sibIdx); err != nil {
				rows.Close()
				return false, err
			}
			if metadata.DistinctTitlePartIndices(cand.title, sibTitle) {
				continue
			}
			sibIdx = effectiveSeriesIndex(sibIdx, sibTitle, sl.seriesTitle)
			sameIdx := sameSeriesIndex(sl.seriesIndex, sibIdx)
			conflict := sl.seriesIndex != nil && sibIdx != nil && !sameIdx
			sameTitle := candKey != "" && metadata.CanonicalSeriesBookTitleKey(sibTitle, sl.seriesTitle) == candKey
			if sameIdx || (sameTitle && !conflict) {
				matchedIDs = append(matchedIDs, sibID)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		rows.Close()
		for _, sibID := range matchedIDs {
			if err := markFormats(sibID); err != nil {
				return false, err
			}
			if allCovered() {
				return true, nil
			}
		}
	}

	// Check siblings by the same author with the same normalized title.
	candKey := metadata.CanonicalSeriesBookTitleKey(cand.title, cand.seriesTitle)
	if candKey != "" {
		rows, err := m.DB.QueryContext(ctx, `
			SELECT b.id, b.title, COALESCE(b.series_id, ''), COALESCE(s.title, ''), b.series_index
			FROM books b
			LEFT JOIN series s ON s.id = b.series_id
			WHERE b.author_id = ? AND b.id <> ?`, cand.authorID, cand.id)
		if err != nil {
			return false, err
		}
		var matchedIDs []string
		for rows.Next() {
			var sibID, sibTitle, sibSeriesID, sibSeriesTitle string
			var sibIdx *float64
			if err := rows.Scan(&sibID, &sibTitle, &sibSeriesID, &sibSeriesTitle, &sibIdx); err != nil {
				rows.Close()
				return false, err
			}
			if metadata.DistinctTitlePartIndices(cand.title, sibTitle) {
				continue
			}
			series := cmp.Or(cand.seriesTitle, sibSeriesTitle)
			if metadata.CanonicalSeriesBookTitleKey(sibTitle, series) != candKey {
				continue
			}
			if cand.seriesID != "" && sibSeriesID == cand.seriesID && cand.seriesIndex != nil && sibIdx != nil && !sameSeriesIndex(cand.seriesIndex, sibIdx) {
				continue
			}
			matchedIDs = append(matchedIDs, sibID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		rows.Close()
		for _, sibID := range matchedIDs {
			if err := markFormats(sibID); err != nil {
				return false, err
			}
			if allCovered() {
				return true, nil
			}
		}
	}

	return allCovered(), nil
}
