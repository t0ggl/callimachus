package converter

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// AudioExtensions contains supported audio file extensions in lowercase.
var AudioExtensions = map[string]bool{
	".mp3":  true,
	".m4a":  true,
	".m4b":  true,
	".aac":  true,
	".flac": true,
	".ogg":  true,
	".oga":  true,
	".opus": true,
	".wav":  true,
	".wma":  true,
}

// IsAudioFile returns true if the file path has a recognized audio extension.
func IsAudioFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return AudioExtensions[ext]
}

// IsPlaylistFile returns true if the file has an .m3u or .m3u8 extension.
func IsPlaylistFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".m3u" || ext == ".m3u8"
}

// NaturalLess reports whether s1 should sort before s2 in natural numeric order.
// For example: "track2.mp3" < "track10.mp3", "01 - Track.mp3" < "02 - Track.mp3".
func NaturalLess(s1, s2 string) bool {
	chunks1 := splitChunks(s1)
	chunks2 := splitChunks(s2)

	minLen := len(chunks1)
	if len(chunks2) < minLen {
		minLen = len(chunks2)
	}

	for i := 0; i < minLen; i++ {
		c1 := chunks1[i]
		c2 := chunks2[i]

		isDigit1 := unicode.IsDigit(rune(c1[0]))
		isDigit2 := unicode.IsDigit(rune(c2[0]))

		if isDigit1 && isDigit2 {
			// Compare as numeric values
			trim1 := strings.TrimLeft(c1, "0")
			trim2 := strings.TrimLeft(c2, "0")

			if len(trim1) != len(trim2) {
				return len(trim1) < len(trim2)
			}
			if trim1 != trim2 {
				return trim1 < trim2
			}
			// Same numeric value, prefer fewer leading zeros
			if len(c1) != len(c2) {
				return len(c1) < len(c2)
			}
		} else {
			// Compare non-digits case-insensitively
			lower1 := strings.ToLower(c1)
			lower2 := strings.ToLower(c2)
			if lower1 != lower2 {
				return lower1 < lower2
			}
			// Tie-breaker on original case
			if c1 != c2 {
				return c1 < c2
			}
		}
	}

	return len(chunks1) < len(chunks2)
}

func splitChunks(s string) []string {
	if s == "" {
		return nil
	}
	var chunks []string
	var current strings.Builder
	var isDigitChunk bool

	for i, r := range s {
		isDigit := unicode.IsDigit(r)
		if i == 0 {
			isDigitChunk = isDigit
			current.WriteRune(r)
			continue
		}
		if isDigit == isDigitChunk {
			current.WriteRune(r)
		} else {
			chunks = append(chunks, current.String())
			current.Reset()
			isDigitChunk = isDigit
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

// SortFilesNaturally sorts a slice of file paths in natural numeric order in place.
func SortFilesNaturally(files []string) {
	sort.Slice(files, func(i, j int) bool {
		baseI := filepath.Base(files[i])
		baseJ := filepath.Base(files[j])
		if baseI != baseJ {
			return NaturalLess(baseI, baseJ)
		}
		return NaturalLess(files[i], files[j])
	})
}

// ParseM3U parses an .m3u or .m3u8 playlist file and returns resolved file paths.
func ParseM3U(playlistPath string) ([]string, error) {
	f, err := os.Open(playlistPath)
	if err != nil {
		return nil, fmt.Errorf("open playlist %s: %w", playlistPath, err)
	}
	defer f.Close()

	baseDir := filepath.Dir(playlistPath)
	var audioFiles []string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		filePath := line
		if !filepath.IsAbs(filePath) {
			filePath = filepath.Join(baseDir, filePath)
		}

		if _, err := os.Stat(filePath); err == nil {
			audioFiles = append(audioFiles, filepath.Clean(filePath))
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read playlist %s: %w", playlistPath, err)
	}

	return audioFiles, nil
}

// ResolveAudioFiles resolves, filters, and orders audio files from a staging directory or file.
// If an M3U playlist is present, it uses the playlist ordering.
// Otherwise, it performs natural numeric sorting on all discovered audio files.
func ResolveAudioFiles(sourcePath string) ([]string, error) {
	if strings.TrimSpace(sourcePath) == "" {
		return nil, errors.New("sourcePath cannot be empty")
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("stat source path %s: %w", sourcePath, err)
	}

	// Single file
	if !info.IsDir() {
		if IsPlaylistFile(sourcePath) {
			files, err := ParseM3U(sourcePath)
			if err != nil {
				return nil, err
			}
			if len(files) == 0 {
				return nil, fmt.Errorf("playlist %s contained no valid audio files", sourcePath)
			}
			return files, nil
		}
		if IsAudioFile(sourcePath) {
			return []string{filepath.Clean(sourcePath)}, nil
		}
		return nil, fmt.Errorf("file %s is neither an audio file nor a supported playlist", sourcePath)
	}

	// Directory: check for .m3u / .m3u8 playlists first
	var playlists []string
	var candidateAudioFiles []string

	err = filepath.WalkDir(sourcePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip hidden directories
			if strings.HasPrefix(d.Name(), ".") && path != sourcePath {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip hidden files
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}

		if IsPlaylistFile(path) {
			playlists = append(playlists, path)
		} else if IsAudioFile(path) {
			candidateAudioFiles = append(candidateAudioFiles, filepath.Clean(path))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk directory %s: %w", sourcePath, err)
	}

	// If playlist(s) found, attempt to parse the first one that yields existing audio files
	if len(playlists) > 0 {
		SortFilesNaturally(playlists)
		for _, pl := range playlists {
			files, err := ParseM3U(pl)
			if err == nil && len(files) > 0 {
				return files, nil
			}
		}
	}

	if len(candidateAudioFiles) == 0 {
		return nil, fmt.Errorf("no audio files found in %s", sourcePath)
	}

	SortFilesNaturally(candidateAudioFiles)
	return candidateAudioFiles, nil
}
