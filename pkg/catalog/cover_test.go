package catalog

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

func testCoverJPEG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 3, 4))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func atom(kind string, data []byte) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b, uint32(8+len(data)))
	copy(b[4:], kind)
	return append(b, data...)
}
func TestEmbeddedCovers(t *testing.T) {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 4, 5))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{"jpeg", testCoverJPEG(t), true}, {"png", pngData.Bytes(), true}, {"missing", nil, false}, {"malformed", []byte("bad image"), false}, {"unsupported", []byte("<svg/>"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, ext := range []string{".epub", ".m4b"} {
				t.Run(ext, func(t *testing.T) {
					root := t.TempDir()
					src := filepath.Join(t.TempDir(), "source"+ext)
					if ext == ".epub" {
						createTestEPUBWithCover(t, src, tc.data)
					} else {
						payload := atom("moov", atom("udta", atom("meta", append(make([]byte, 4), atom("ilst", atom("covr", atom("data", append(make([]byte, 8), tc.data...))))...))))
						if err := os.WriteFile(src, payload, 0600); err != nil {
							t.Fatal(err)
						}
					}
					before, _ := os.ReadFile(src)
					cat := New(testutil.NewTestDB(t))
					book := BookDetails{Title: "Synthetic Illustrated", Author: "Synthetic Illustrator"}
					res, err := cat.IngestBook(context.Background(), root, book, src, nil)
					if err != nil {
						t.Fatal(err)
					}
					if (res.Organized.CoverPath != "") != tc.want {
						t.Fatalf("cover=%q want %v", res.Organized.CoverPath, tc.want)
					}
					after, _ := os.ReadFile(res.Organized.MediaPath)
					if !bytes.Equal(before, after) {
						t.Fatal("source changed")
					}
					if tc.want {
						path := res.Organized.CoverPath
						data, _ := os.ReadFile(path)
						if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
							t.Fatal(err)
						}
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
						report, err := ScanLibrary(root)
						if err != nil {
							t.Fatal(err)
						}
						if _, err := cat.ReconcileScanReport(context.Background(), report); err != nil {
							t.Fatal(err)
						}
						restored, _ := os.ReadFile(path)
						if !bytes.Equal(data, restored) {
							t.Fatal("rescan did not restore cover")
						}
						if !ensureCover(res.Organized.MediaPath, path, testCoverJPEG(t)) {
							t.Fatal("preserve failed")
						}
						preserved, _ := os.ReadFile(path)
						if !bytes.Equal(data, preserved) {
							t.Fatal("existing cover replaced")
						}
					}
				})
			}
		})
	}
}
func TestMP4MalformedAtoms(t *testing.T) {
	for _, data := range [][]byte{atom("moov", []byte{0, 0, 0, 1, 'c', 'o', 'v', 'r'}), {0, 0, 0, 4, 'm', 'o', 'o', 'v'}} {
		if _, err := mp4Cover(bytes.NewReader(data), 0, int64(len(data)), 0, false); err == nil {
			t.Fatal("accepted malformed atom")
		}
	}
}

func TestID3Cover(t *testing.T) {
	for _, version := range []byte{3, 4} {
		cover := testCoverJPEG(t)
		// ID3 frames use a different size layout than MP4 atoms.
		payload := append([]byte{0, 'i', 'm', 'a', 'g', 'e', '/', 'j', 'p', 'e', 'g', 0, 3, 0}, cover...)
		frame := make([]byte, 10)
		copy(frame, "APIC")
		if version == 3 {
			binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
		} else {
			copy(frame[4:8], id3Size(len(payload)))
		}
		tag := append(frame, payload...)
		header := []byte{'I', 'D', '3', version, 0, 0}
		header = append(header, id3Size(len(tag))...)
		source := filepath.Join(t.TempDir(), "book.mp3")
		if err := os.WriteFile(source, append(header, tag...), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := EmbeddedCover(source)
		if err != nil || !bytes.Equal(got, cover) {
			t.Fatalf("v%d: %v", version, err)
		}
	}
}
func id3Size(n int) []byte {
	return []byte{byte(n>>21) & 127, byte(n>>14) & 127, byte(n>>7) & 127, byte(n) & 127}
}

func TestCoverFailureAndPreservation(t *testing.T) {
	source := filepath.Join(t.TempDir(), "missing.epub")
	target := filepath.Join(t.TempDir(), "book.jpg")
	if ensureCover(source, target, nil) {
		t.Fatal("missing source produced cover")
	}
	if err := os.WriteFile(target, []byte("malformed old cover"), 0600); err != nil {
		t.Fatal(err)
	}
	if !ensureCover(source, target, testCoverJPEG(t)) {
		t.Fatal("could not repair malformed cover")
	}
	before, _ := os.ReadFile(target)
	if !ensureCover(source, target, []byte("invalid new cover")) {
		t.Fatal("existing cover lost")
	}
	after, _ := os.ReadFile(target)
	if !bytes.Equal(before, after) {
		t.Fatal("valid companion replaced")
	}
	outside := filepath.Join(t.TempDir(), "outside.jpg")
	if err := os.WriteFile(outside, []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	if ensureCover(source, target, testCoverJPEG(t)) {
		t.Fatal("followed cover symlink")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "protected" {
		t.Fatal("symlink target modified")
	}
}

func TestEPUBWithoutCover(t *testing.T) {
	source := filepath.Join("..", "..", "testdata", "epubs", "epub2_valid.epub")
	if _, err := os.Stat(source); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "book.jpg")
	if ensureCover(source, target, nil) {
		t.Fatal("cover generated for coverless EPUB")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("partial cover: %v", err)
	}
}

func TestValidateAndNormalizeCover(t *testing.T) {
	makeJPEG := func(w, h int) []byte {
		var buf bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	makePNG := func(w, h int, alpha uint8) []byte {
		var buf bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		img.Set(0, 0, color.RGBA{0, 0, 0, alpha})
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	makeGIF := func(w, h int) []byte {
		var buf bytes.Buffer
		img := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.White, color.Black})
		if err := gif.Encode(&buf, img, nil); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	// Valid JPEG portrait (2:3)
	jpegData := makeJPEG(400, 600)
	got, err := ValidateAndNormalizeCover(jpegData)
	if err != nil {
		t.Fatalf("valid JPEG rejected: %v", err)
	}
	if got.Width != 400 || got.Height != 600 || got.Format != "jpeg" || !bytes.Equal(got.JPEG, jpegData) {
		t.Fatalf("unexpected JPEG result: %dx%d fmt=%s", got.Width, got.Height, got.Format)
	}

	// Valid square PNG (1:1 audiobook) normalized to JPEG with white alpha composite
	pngData := makePNG(300, 300, 0)
	got, err = ValidateAndNormalizeCover(pngData)
	if err != nil {
		t.Fatalf("valid PNG rejected: %v", err)
	}
	if got.Width != 300 || got.Height != 300 || got.Format != "png" {
		t.Fatalf("unexpected PNG result: %dx%d fmt=%s", got.Width, got.Height, got.Format)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(got.JPEG))
	if err != nil {
		t.Fatalf("normalized PNG is not valid JPEG: %v", err)
	}
	r, g, b, _ := decoded.At(0, 0).RGBA()
	if r < 0xf000 || g < 0xf000 || b < 0xf000 {
		t.Fatalf("transparent PNG pixel was not composited on white: rgb=(%x,%x,%x)", r, g, b)
	}

	// Valid lossless 200x300 WebP normalized to JPEG
	webpData, err := base64.StdEncoding.DecodeString("UklGRiQAAABXRUJQVlA4TBgAAAAvx8BKAAdQqHqUsf8BAEX6/58i+p/6fyk=")
	if err != nil {
		t.Fatal(err)
	}
	got, err = ValidateAndNormalizeCover(webpData)
	if err != nil {
		t.Fatalf("valid WebP rejected: %v", err)
	}
	if got.Width != 200 || got.Height != 300 || got.Format != "webp" {
		t.Fatalf("unexpected WebP result: %dx%d fmt=%s", got.Width, got.Height, got.Format)
	}
	if _, err := jpeg.Decode(bytes.NewReader(got.JPEG)); err != nil {
		t.Fatalf("normalized WebP is not valid JPEG: %v", err)
	}

	// Negative validation cases
	// Craft a minimal valid PNG header claiming 5000x5000 (without allocating pixels) to test max dimension guard quickly.
	oversizedDimPNG := makePNG(1, 1, 255)
	binary.BigEndian.PutUint32(oversizedDimPNG[16:20], 5000)
	binary.BigEndian.PutUint32(oversizedDimPNG[20:24], 5000)
	binary.BigEndian.PutUint32(oversizedDimPNG[29:33], crc32.ChecksumIEEE(oversizedDimPNG[12:29]))

	cases := []struct {
		name       string
		data       []byte
		wantSubstr string
	}{
		{"empty", nil, "empty"},
		{"oversized bytes", bytes.Repeat([]byte{0xff}, MaxUploadedCoverBytes+1), "10 MB"},
		{"non-image", []byte("hello world"), "JPEG, PNG, or WebP"},
		{"gif unsupported", makeGIF(300, 450), "JPEG, PNG, or WebP"},
		{"too small", makeJPEG(150, 225), "too small"},
		{"too large dimensions", oversizedDimPNG, "too large"},
		{"too wide aspect ratio", makeJPEG(600, 300), "aspect ratio"},
		{"too narrow aspect ratio", makeJPEG(200, 500), "aspect ratio"},
		{"truncated jpeg", jpegData[:len(jpegData)/2], "corrupted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateAndNormalizeCover(tc.data)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSubstr)
			}
			var valErr *CoverValidationError
			if !errors.As(err, &valErr) {
				t.Fatalf("expected CoverValidationError, got %T: %v", err, err)
			}
			if !strings.Contains(valErr.Error(), tc.wantSubstr) {
				t.Fatalf("error %q does not contain %q", valErr.Error(), tc.wantSubstr)
			}
		})
	}
}
