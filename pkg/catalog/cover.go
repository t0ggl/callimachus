package catalog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/webp"

	"github.com/t0ggl/callimachus/pkg/epub"
)

const (
	maxCoverBytes = 32 << 20

	// MaxUploadedCoverBytes is the maximum allowed raw byte size for user-uploaded or chosen covers.
	MaxUploadedCoverBytes = 10 << 20 // 10 MiB
	// MinCoverDimension is the minimum allowed width or height in pixels for book covers.
	MinCoverDimension = 200
	// MaxCoverDimension is the maximum allowed width or height in pixels for book covers.
	MaxCoverDimension = 4096
	// MinCoverAspectRatio is the narrowest allowed width/height ratio (1:2 tall portrait).
	MinCoverAspectRatio = 0.5
	// MaxCoverAspectRatio is the widest allowed width/height ratio (5:4 slightly wide, including 1:1 audiobooks).
	MaxCoverAspectRatio = 1.25
)

// CoverValidationError reports a user-facing validation problem with a cover image.
type CoverValidationError struct {
	Message string
}

func (e *CoverValidationError) Error() string {
	return e.Message
}

// ValidatedCover holds normalized JPEG bytes and dimensions for an accepted cover image.
type ValidatedCover struct {
	JPEG   []byte
	Width  int
	Height int
	Format string
}

// ValidateCoverDimensions checks whether pixel dimensions satisfy book cover
// minimum/maximum bounds and aspect ratio limits.
func ValidateCoverDimensions(width, height int) error {
	if width < MinCoverDimension || height < MinCoverDimension {
		return &CoverValidationError{
			Message: fmt.Sprintf("Cover image is too small (%d×%d px). Minimum dimensions are %d×%d px.", width, height, MinCoverDimension, MinCoverDimension),
		}
	}
	if width > MaxCoverDimension || height > MaxCoverDimension {
		return &CoverValidationError{
			Message: fmt.Sprintf("Cover image is too large (%d×%d px). Maximum dimensions are %d×%d px.", width, height, MaxCoverDimension, MaxCoverDimension),
		}
	}
	ratio := float64(width) / float64(height)
	if ratio < MinCoverAspectRatio || ratio > MaxCoverAspectRatio {
		return &CoverValidationError{
			Message: fmt.Sprintf("Cover aspect ratio (%.2f:1) is outside the supported book cover range (%.2f:1 to %.2f:1, portrait to square).", ratio, MinCoverAspectRatio, MaxCoverAspectRatio),
		}
	}
	return nil
}

// IsValidCoverDimensions reports whether width and height satisfy book cover bounds.
func IsValidCoverDimensions(width, height int) bool {
	return ValidateCoverDimensions(width, height) == nil
}

// ValidateAndNormalizeCover validates raw cover image bytes (size, JPEG/PNG/WebP format,
// pixel dimensions, and book-cover aspect ratio) and returns normalized JPEG bytes
// suitable for companion cover art and EPUB embedding.
func ValidateAndNormalizeCover(data []byte) (*ValidatedCover, error) {
	if len(data) == 0 {
		return nil, &CoverValidationError{Message: "Cover image file is empty."}
	}
	if len(data) > MaxUploadedCoverBytes {
		return nil, &CoverValidationError{Message: "Cover image must be 10 MB or smaller."}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png" && format != "webp") {
		return nil, &CoverValidationError{Message: "Cover image must be a valid JPEG, PNG, or WebP file."}
	}
	if err := ValidateCoverDimensions(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, &CoverValidationError{Message: "Cover image is corrupted or unreadable. Choose a valid JPEG, PNG, or WebP file."}
	}
	var out []byte
	if format == "jpeg" {
		out = bytes.Clone(data)
	} else {
		dst := image.NewRGBA(img.Bounds())
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Over)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90}); err != nil {
			return nil, fmt.Errorf("encode cover jpeg: %w", err)
		}
		out = buf.Bytes()
	}
	return &ValidatedCover{
		JPEG:   out,
		Width:  cfg.Width,
		Height: cfg.Height,
		Format: format,
	}, nil
}

// coverJPEG validates dimensions before allocating pixels and normalizes artwork.
func coverJPEG(data []byte) ([]byte, error) {
	if len(data) > maxCoverBytes {
		return nil, errors.New("cover too large")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 24_000_000 {
		return nil, errors.New("cover dimensions too large")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if format == "jpeg" {
		return data, nil
	}
	// Composite transparent artwork on white rather than JPEG's implicit black.
	dst := image.NewRGBA(img.Bounds())
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Over)
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90})
	return buf.Bytes(), err
}

func validCover(path string) bool {
	if err := checkPathNoSymlinks(path); err != nil {
		return false
	}
	data, err := readCover(path)
	if err != nil {
		return false
	}
	_, err = coverJPEG(data)
	return err == nil
}

func readCover(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxCoverBytes+1))
}

// ensureCover is best effort: bad or absent artwork never blocks media ingestion.
// It writes only a validated JPEG, and never modifies the source book.
func ensureCover(source, target string, supplied []byte) bool {
	if validCover(target) {
		return true
	}
	if checkPathNoSymlinks(target) != nil {
		return false
	}
	data, err := coverJPEG(supplied)
	if err != nil {
		raw, extractErr := embeddedCover(source)
		if extractErr != nil {
			return false
		}
		data, err = coverJPEG(raw)
		if err != nil {
			return false
		}
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".cover-*.jpg")
	if err != nil {
		return false
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return false
	}
	if os.Chmod(f.Name(), 0644) != nil {
		return false
	}
	return os.Rename(f.Name(), target) == nil
}

func embeddedCover(source string) ([]byte, error) {
	switch strings.ToLower(filepath.Ext(source)) {
	case ".epub":
		ep, err := epub.OpenNative(source)
		if err != nil {
			return nil, err
		}
		f, err := os.CreateTemp("", "callimachus-cover-*")
		if err != nil {
			return nil, err
		}
		name := f.Name()
		f.Close()
		defer os.Remove(name)
		if err := ep.ExtractCover(name); err != nil {
			return nil, err
		}
		return readCover(name)
	case ".azw3", ".mobi", ".pdf", ".cbz", ".cbr":
		f, err := os.CreateTemp("", "callimachus-cover-*")
		if err != nil {
			return nil, err
		}
		name := f.Name()
		f.Close()
		defer os.Remove(name)
		if err := epub.ExtractCoverWithCalibre(source, name); err != nil {
			return nil, err
		}
		return readCover(name)

	case ".mp3":
		return id3Cover(source)
	case ".m4b", ".m4a", ".mp4":
		f, err := os.Open(source)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		return mp4Cover(f, 0, info.Size(), 0, false)
	}
	return nil, errors.New("no supported embedded cover")
}

// Walk only metadata atoms; audio payloads are skipped without reading them.
func mp4Cover(r io.ReaderAt, start, end int64, depth int, inCover bool) ([]byte, error) {
	if depth > 8 {
		return nil, errors.New("MP4 metadata nesting too deep")
	}
	for pos := start; pos+8 <= end; {
		var h [16]byte
		if _, err := r.ReadAt(h[:8], pos); err != nil {
			return nil, err
		}
		size, header := int64(binary.BigEndian.Uint32(h[:4])), int64(8)
		if size == 1 {
			if end-pos < 16 {
				return nil, io.ErrUnexpectedEOF
			}
			if _, err := r.ReadAt(h[8:], pos+8); err != nil {
				return nil, err
			}
			n := binary.BigEndian.Uint64(h[8:])
			if n > uint64(end-pos) {
				return nil, io.ErrUnexpectedEOF
			}
			size = int64(n)
			header = 16
		} else if size == 0 {
			size = end - pos
		}
		if size < header || size > end-pos {
			return nil, errors.New("invalid MP4 atom size")
		}
		kind := string(h[4:8])
		body := pos + header
		limit := pos + size
		switch kind {
		case "moov", "udta", "meta", "ilst", "covr":
			if kind == "meta" {
				body += 4
			}
			if body > limit {
				return nil, io.ErrUnexpectedEOF
			}
			data, err := mp4Cover(r, body, limit, depth+1, kind == "covr")
			if err != nil {
				return nil, err
			}
			if len(data) > 0 {
				return data, nil
			}
		case "data":
			if inCover && limit-body > 8 && limit-body-8 <= maxCoverBytes {
				data := make([]byte, limit-body-8)
				if _, err := r.ReadAt(data, body+8); err != nil {
					return nil, err
				}
				if _, err := coverJPEG(data); err == nil {
					return data, nil
				}
			}
		}
		pos = limit
	}
	return nil, nil
}

// ID3v2.3/v2.4 APIC frames carry artwork independently of the audio stream.
func id3Cover(source string) ([]byte, error) {
	f, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var h [10]byte
	if _, err = io.ReadFull(f, h[:]); err != nil {
		return nil, err
	}
	if string(h[:3]) != "ID3" || (h[3] != 3 && h[3] != 4) || h[5]&0xc0 != 0 {
		return nil, errors.New("unsupported ID3 tag")
	}
	size, err := synchsafe(h[6:])
	if err != nil || size > maxCoverBytes {
		return nil, errors.New("invalid ID3 size")
	}
	data := make([]byte, size)
	if _, err = io.ReadFull(f, data); err != nil {
		return nil, err
	}
	for len(data) >= 10 && data[0] != 0 {
		n := int(binary.BigEndian.Uint32(data[4:8]))
		if h[3] == 4 {
			n, err = synchsafe(data[4:8])
			if err != nil {
				return nil, err
			}
		}
		if n < 0 || n > len(data)-10 {
			return nil, io.ErrUnexpectedEOF
		}
		frame := data[10 : 10+n]
		if string(data[:4]) == "APIC" && data[9] == 0 && len(frame) > 4 {
			encoding := frame[0]
			frame = frame[1:]
			if i := bytes.IndexByte(frame, 0); i >= 0 && i+2 <= len(frame) {
				frame = frame[i+2:]
				step := 1
				if encoding == 1 || encoding == 2 {
					step = 2
				}
				for i := 0; i+step <= len(frame); i += step {
					if frame[i] == 0 && (step == 1 || frame[i+1] == 0) {
						imageData := frame[i+step:]
						if _, err := coverJPEG(imageData); err == nil {
							return imageData, nil
						}
						break
					}
				}
			}
		}
		data = data[10+n:]
	}
	return nil, errors.New("no ID3 cover")
}
func synchsafe(b []byte) (int, error) {
	n := 0
	for _, v := range b {
		if v&0x80 != 0 {
			return 0, errors.New("invalid synchsafe integer")
		}
		n = n<<7 | int(v)
	}
	return n, nil
}
