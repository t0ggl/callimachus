package catalog

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/t0ggl/callimachus/pkg/epub"
	"github.com/t0ggl/callimachus/pkg/metadata"
)

var (
	reYearInString = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)

	// Use explicit MIME types so recognition does not depend on the host MIME database.
	supportedMediaExtensions = map[string]string{
		".epub": "application/epub+zip",
		".m4b":  "audio/mp4",
		".mp3":  "audio/mpeg",
		".pdf":  "application/pdf",
		".azw3": "application/vnd.amazon.mobi8-ebook",
		".mobi": "application/x-mobipocket-ebook",
		".cbr":  "application/vnd.comicbook-rar",
		".cbz":  "application/vnd.comicbook+zip",
	}

	supportedCoverExtensions = map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
	}
)

// ScannedMediaFile represents an individual media file discovered on disk.
type ScannedMediaFile struct {
	Path     string `json:"path"`
	Format   string `json:"format"` // e.g. "epub", "m4b", "mp3", "pdf", "azw3"
	FileSize int64  `json:"file_size"`
	MIMEType string `json:"mime_type"`
}

// ScannedBook represents a reconciled book with its media assets and companion sidecars.
type ScannedBook struct {
	Title       string             `json:"title"`
	Subtitle    string             `json:"subtitle,omitempty"`
	Author      string             `json:"author"`
	Series      string             `json:"series,omitempty"`
	SeriesIndex *float64           `json:"series_index,omitempty"`
	Year        int                `json:"year,omitempty"`
	Description string             `json:"description,omitempty"`
	Publisher   string             `json:"publisher,omitempty"`
	ISBN13      string             `json:"isbn13,omitempty"`
	Language    string             `json:"language,omitempty"`
	Directory   string             `json:"directory"`
	Basename    string             `json:"basename"`
	MediaFiles  []ScannedMediaFile `json:"media_files"`
	SidecarPath string             `json:"sidecar_path,omitempty"`
	CoverPath   string             `json:"cover_path,omitempty"`
	OPFMetadata *epub.Metadata     `json:"opf_metadata,omitempty"`
}

// ScanReport summarizes the complete scan of a library directory tree.
type ScanReport struct {
	LibraryRoot string         `json:"library_root"`
	Books       []*ScannedBook `json:"books"`
	TotalFiles  int            `json:"total_files"`
	MediaCount  int            `json:"media_count"`
}

type dirGroup struct {
	mediaFiles   map[string][]ScannedMediaFile // basename -> files
	sidecarFiles map[string]string             // basename -> path
	coverFiles   map[string]string             // basename -> path
	genericOPF   string                        // e.g. metadata.opf
	genericCover string                        // e.g. cover.jpg
}

// ScanLibrary scans a library directory tree, identifies media files, companion OPF sidecars,
// and cover images, and reconciles them into structured domain objects.
// Zero symlinks are permitted: any symlink encountered immediately returns ErrSymlinkForbidden.
func ScanLibrary(libraryRoot string) (*ScanReport, error) {
	if strings.TrimSpace(libraryRoot) == "" {
		return nil, ErrInvalidLibraryRoot
	}

	cleanRoot, err := filepath.Abs(libraryRoot)
	if err != nil {
		return nil, err
	}
	if err := checkPathNoSymlinks(cleanRoot); err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("stat library root: %w", err)
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("library root must be a directory")
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: library root is a symlink", ErrSymlinkForbidden)
	}

	groups := make(map[string]*dirGroup)
	totalFiles := 0
	mediaCount := 0

	err = filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Hard failure if any symlink is encountered anywhere in the tree
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink detected at %s", ErrSymlinkForbidden, path)
		}

		if d.IsDir() {
			if path != cleanRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}

		totalFiles++
		ext := strings.ToLower(filepath.Ext(path))
		dir := filepath.Dir(path)
		filename := d.Name()
		baseWithoutExt := strings.TrimSuffix(filename, filepath.Ext(filename))

		group, ok := groups[dir]
		if !ok {
			group = &dirGroup{
				mediaFiles:   make(map[string][]ScannedMediaFile),
				sidecarFiles: make(map[string]string),
				coverFiles:   make(map[string]string),
			}
			groups[dir] = group
		}

		if mimeType, isMedia := supportedMediaExtensions[ext]; isMedia {
			info, err := d.Info()
			var size int64
			if err == nil {
				size = info.Size()
			}
			group.mediaFiles[baseWithoutExt] = append(group.mediaFiles[baseWithoutExt], ScannedMediaFile{
				Path:     path,
				Format:   strings.TrimPrefix(ext, "."),
				MIMEType: mimeType,
				FileSize: size,
			})
			mediaCount++
		} else if ext == ".opf" {
			group.sidecarFiles[baseWithoutExt] = path
			if strings.EqualFold(filename, "metadata.opf") && group.genericOPF == "" {
				group.genericOPF = path
			}
		} else if supportedCoverExtensions[ext] {
			group.coverFiles[baseWithoutExt] = path
			lowerName := strings.ToLower(filename)
			if (strings.HasPrefix(lowerName, "cover.") || strings.HasPrefix(lowerName, "folder.")) && group.genericCover == "" {
				group.genericCover = path
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	var books []*ScannedBook

	// Sort directory keys for deterministic results
	dirKeys := make([]string, 0, len(groups))
	for k := range groups {
		dirKeys = append(dirKeys, k)
	}
	sort.Strings(dirKeys)

	for _, dir := range dirKeys {
		group := groups[dir]
		if len(group.mediaFiles) == 0 {
			continue
		}

		// Sort basenames within directory
		baseKeys := make([]string, 0, len(group.mediaFiles))
		for b := range group.mediaFiles {
			baseKeys = append(baseKeys, b)
		}
		sort.Strings(baseKeys)

		for _, base := range baseKeys {
			media := group.mediaFiles[base]

			// Resolve companion OPF
			var sidecarPath string
			if opf, exists := group.sidecarFiles[base]; exists {
				sidecarPath = opf
			} else if len(group.mediaFiles) == 1 && group.genericOPF != "" {
				sidecarPath = group.genericOPF
			}

			// Resolve companion Cover
			var coverPath string
			if cov, exists := group.coverFiles[base]; exists {
				coverPath = cov
			} else if len(group.mediaFiles) == 1 && group.genericCover != "" {
				coverPath = group.genericCover
			}

			sb := reconcileBook(cleanRoot, dir, base, media, sidecarPath, coverPath)
			books = append(books, sb)
		}
	}

	return &ScanReport{
		LibraryRoot: cleanRoot,
		Books:       books,
		TotalFiles:  totalFiles,
		MediaCount:  mediaCount,
	}, nil
}

// Scan is a convenience alias for ScanLibrary that returns only the discovered books.
func Scan(libraryRoot string) ([]*ScannedBook, error) {
	report, err := ScanLibrary(libraryRoot)
	if err != nil {
		return nil, err
	}
	return report.Books, nil
}

func reconcileBook(root, dir, base string, media []ScannedMediaFile, sidecarPath, coverPath string) *ScannedBook {
	sb := &ScannedBook{
		Directory:   dir,
		Basename:    base,
		MediaFiles:  media,
		SidecarPath: sidecarPath,
		CoverPath:   coverPath,
	}

	var rawTitle string

	// 1. Try parsing companion OPF if available
	if sidecarPath != "" {
		if data, err := os.ReadFile(sidecarPath); err == nil {
			if meta, err := epub.ParseOPF(data); err == nil {
				sb.OPFMetadata = &meta
				if meta.Title != "" {
					rawTitle = meta.Title
					sb.Title = meta.Title
				}
				if meta.Subtitle != "" {
					sb.Subtitle = strings.TrimSpace(meta.Subtitle)
				}
				if len(meta.Authors) > 0 {
					sb.Author = meta.Authors[0]
				}
				sb.Series = meta.Series
				sb.SeriesIndex = meta.SeriesIndex
				if meta.Description != "" {
					sb.Description = metadata.CleanDescription(meta.Description)
				}
				if meta.Publisher != "" {
					sb.Publisher = strings.TrimSpace(meta.Publisher)
				}
				if meta.ISBN13 != "" {
					sb.ISBN13 = strings.TrimSpace(meta.ISBN13)
				}
				if meta.Language != "" {
					sb.Language = strings.TrimSpace(meta.Language)
				}
				if meta.PublishDate != "" {
					if m := reYearInString.FindString(meta.PublishDate); m != "" {
						if y, err := strconv.Atoi(m); err == nil {
							sb.Year = y
						}
					}
				}
			}
		}
	}

	// 2. Canonical Directory Structure Analysis relative to libraryRoot
	var titleDir string
	rel, err := filepath.Rel(root, dir)
	if err == nil && rel != "." && rel != "" {
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) >= 1 && parts[0] != "" {
			if sb.Author == "" {
				sb.Author = metadata.NormalizeAuthor(parts[0])
			}
		}

		authorName := sb.Author
		if authorName == "" && len(parts) >= 1 {
			authorName = parts[0]
		}

		parsedTitle := ""
		parsedYear := 0

		if authorName != "" && strings.Contains(base, " - ") {
			// Check if base ends with author name (canonical format {Title} ({Year}) - {Author})
			idx := strings.LastIndex(base, " - ")
			candidateTitlePart := strings.TrimSpace(base[:idx])
			candidateAuthorPart := strings.TrimSpace(base[idx+3:])

			if strings.EqualFold(candidateAuthorPart, authorName) || strings.EqualFold(metadata.NormalizeAuthor(candidateAuthorPart), metadata.NormalizeAuthor(authorName)) {
				if sb.Author == "" {
					sb.Author = metadata.NormalizeAuthor(candidateAuthorPart)
				}
				if ym := reYearInString.FindString(candidateTitlePart); ym != "" {
					if y, err := strconv.Atoi(ym); err == nil {
						parsedYear = y
					}
					candidateTitlePart = strings.TrimSpace(reYearInString.ReplaceAllString(candidateTitlePart, ""))
					candidateTitlePart = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(candidateTitlePart, "()"), "[]"))
				}
				parsedTitle = candidateTitlePart
			}
		}

		if sb.Title == "" && parsedTitle != "" {
			sb.Title = parsedTitle
		}
		if sb.Year <= 0 && parsedYear > 0 {
			sb.Year = parsedYear
		}

		// Resolve Series and Title Directory based on path depth
		if len(parts) == 3 {
			// [Author, Series, TitleDir]
			titleDir = parts[2]
			if sb.Series == "" {
				sb.Series = parts[1]
			}
			if sb.Year <= 0 {
				if ym := reYearInString.FindString(parts[2]); ym != "" {
					if y, err := strconv.Atoi(ym); err == nil {
						sb.Year = y
					}
				}
			}
			if sb.Title == "" {
				tDir := parts[2]
				if ym := reYearInString.FindString(tDir); ym != "" {
					tDir = strings.TrimSpace(reYearInString.ReplaceAllString(tDir, ""))
					tDir = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(tDir, "()"), "[]"))
				}
				sb.Title = tDir
			}
		} else if len(parts) == 2 {
			// [Author, Folder]
			folder := parts[1]
			if parsedTitle != "" && (strings.EqualFold(folder, parsedTitle) || strings.HasPrefix(strings.ToLower(folder), strings.ToLower(parsedTitle))) {
				// Standalone title folder
				if sb.Year <= 0 {
					if ym := reYearInString.FindString(folder); ym != "" {
						if y, err := strconv.Atoi(ym); err == nil {
							sb.Year = y
						}
					}
				}
			} else {
				// Flat series folder: [Author, Series]
				if sb.Series == "" {
					sb.Series = folder
				}
			}
		}
	}

	// 3. Inspect EPUB media file when core or descriptive metadata is still missing
	var epubTitle string
	if sb.Title == "" || sb.Author == "" || sb.Series == "" || sb.SeriesIndex == nil ||
		sb.Description == "" || sb.Subtitle == "" || sb.Publisher == "" || sb.ISBN13 == "" || sb.Language == "" {
		for _, m := range media {
			if m.Format == "epub" {
				if ep, err := epub.Open(m.Path); err == nil {
					meta := ep.Metadata()
					epubTitle = meta.Title
					if sb.Title == "" && meta.Title != "" {
						sb.Title = meta.Title
					}
					if sb.Subtitle == "" && strings.TrimSpace(meta.Subtitle) != "" {
						sb.Subtitle = strings.TrimSpace(meta.Subtitle)
					}
					if sb.Author == "" && len(meta.Authors) > 0 {
						sb.Author = meta.Authors[0]
					}
					if sb.Series == "" && meta.Series != "" {
						sb.Series = meta.Series
					}
					if sb.SeriesIndex == nil && meta.SeriesIndex != nil &&
						(meta.Series == "" || sb.Series == "" || strings.EqualFold(meta.Series, sb.Series)) {
						idx := *meta.SeriesIndex
						sb.SeriesIndex = &idx
					}
					if sb.Description == "" && meta.Description != "" {
						sb.Description = metadata.CleanDescription(meta.Description)
					}
					if sb.Publisher == "" && strings.TrimSpace(meta.Publisher) != "" {
						sb.Publisher = strings.TrimSpace(meta.Publisher)
					}
					if sb.ISBN13 == "" && strings.TrimSpace(meta.ISBN13) != "" {
						sb.ISBN13 = strings.TrimSpace(meta.ISBN13)
					}
					if sb.Language == "" && strings.TrimSpace(meta.Language) != "" {
						sb.Language = strings.TrimSpace(meta.Language)
					}
					if sb.Year <= 0 && meta.PublishDate != "" {
						if ym := reYearInString.FindString(meta.PublishDate); ym != "" {
							if y, err := strconv.Atoi(ym); err == nil {
								sb.Year = y
							}
						}
					}
				}
				break
			}
		}
	}

	// 4. Release parser fallback for non-canonical releases (e.g. Author - Title)
	if sb.Title == "" || sb.Author == "" {
		parsed := metadata.ParseRelease(base)
		if sb.Title == "" && parsed.Title != "" {
			sb.Title = parsed.Title
		}
		if sb.Author == "" && parsed.Author != "" {
			sb.Author = parsed.Author
		}
		if sb.Series == "" && parsed.Series != "" {
			sb.Series = parsed.Series
		}
		if sb.SeriesIndex == nil && parsed.SeriesIndex != nil {
			sb.SeriesIndex = parsed.SeriesIndex
		}
		if sb.Year <= 0 && parsed.Year > 0 {
			sb.Year = parsed.Year
		}
	}

	// Extract parenthesized series or explicit volume/book index even when title and author exist.
	if sb.Series == "" || sb.SeriesIndex == nil {
		for _, candidate := range []string{rawTitle, sb.Title, base, titleDir, epubTitle} {
			if candidate == "" {
				continue
			}
			ser, idx := metadata.ExtractSeriesFromTitle(candidate)
			if sb.Series == "" && ser != "" {
				sb.Series = ser
			}
			if sb.SeriesIndex == nil && idx != nil && (ser == "" || sb.Series == "" || strings.EqualFold(ser, sb.Series)) {
				v := *idx
				sb.SeriesIndex = &v
			}
			if sb.Series != "" && sb.SeriesIndex != nil {
				break
			}
		}
	}

	// 5. Final year fallback from base string if still missing
	if sb.Year <= 0 {
		if ym := reYearInString.FindString(base); ym != "" {
			if y, err := strconv.Atoi(ym); err == nil {
				sb.Year = y
			}
		}
	}

	sb.Title = metadata.SanitizeTitle(sb.Title)
	sb.Author = metadata.NormalizeAuthor(sb.Author)

	return sb
}
