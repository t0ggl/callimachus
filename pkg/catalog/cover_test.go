package catalog

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"context"
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
					book := BookDetails{Title: "Art", Author: "Artist", Year: 2024}
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
		got, err := embeddedCover(source)
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
