package transcoder

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlaylist_NaturalSorting(t *testing.T) {
	testCases := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name: "track1, track2, track10",
			input: []string{
				"track10.mp3",
				"track1.mp3",
				"track2.mp3",
				"track20.mp3",
			},
			expected: []string{
				"track1.mp3",
				"track2.mp3",
				"track10.mp3",
				"track20.mp3",
			},
		},
		{
			name: "01 - Track, 02 - Track, 10 - Track",
			input: []string{
				"10 - Track Ten.mp3",
				"01 - Track One.mp3",
				"02 - Track Two.mp3",
				"11 - Track Eleven.mp3",
			},
			expected: []string{
				"01 - Track One.mp3",
				"02 - Track Two.mp3",
				"10 - Track Ten.mp3",
				"11 - Track Eleven.mp3",
			},
		},
		{
			name: "Multi-disc tracks",
			input: []string{
				"Disc 2 Track 01.mp3",
				"Disc 1 Track 10.mp3",
				"Disc 1 Track 01.mp3",
				"Disc 1 Track 02.mp3",
			},
			expected: []string{
				"Disc 1 Track 01.mp3",
				"Disc 1 Track 02.mp3",
				"Disc 1 Track 10.mp3",
				"Disc 2 Track 01.mp3",
			},
		},
		{
			name: "Part numbering up to 100",
			input: []string{
				"Part 100.mp3",
				"Part 2.mp3",
				"Part 1.mp3",
				"Part 10.mp3",
			},
			expected: []string{
				"Part 1.mp3",
				"Part 2.mp3",
				"Part 10.mp3",
				"Part 100.mp3",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			files := make([]string, len(tc.input))
			copy(files, tc.input)
			SortFilesNaturally(files)

			if !reflect.DeepEqual(files, tc.expected) {
				t.Errorf("natural sort mismatch:\nGot:      %v\nExpected: %v", files, tc.expected)
			}
		})
	}
}

func TestPlaylist_ParseM3U(t *testing.T) {
	tempDir := t.TempDir()

	// Create dummy audio files
	trackA := filepath.Join(tempDir, "synth_track_a.mp3")
	trackB := filepath.Join(tempDir, "synth_track_b.mp3")
	trackC := filepath.Join(tempDir, "synth_track_c.mp3")
	_ = os.WriteFile(trackA, []byte("audio"), 0644)
	_ = os.WriteFile(trackB, []byte("audio"), 0644)
	_ = os.WriteFile(trackC, []byte("audio"), 0644)

	// Create M3U playlist specifying a specific order (C -> A -> B)
	playlistPath := filepath.Join(tempDir, "playlist.m3u")
	playlistContent := "#EXTM3U\n" +
		"#EXTINF:100,Synthetic Track C\n" +
		"synth_track_c.mp3\n" +
		"#EXTINF:100,Synthetic Track A\n" +
		"synth_track_a.mp3\n" +
		"#EXTINF:100,Synthetic Track B\n" +
		"synth_track_b.mp3\n"

	if err := os.WriteFile(playlistPath, []byte(playlistContent), 0644); err != nil {
		t.Fatalf("failed to write m3u: %v", err)
	}

	resolved, err := ParseM3U(playlistPath)
	if err != nil {
		t.Fatalf("ParseM3U failed: %v", err)
	}

	expected := []string{trackC, trackA, trackB}
	if !reflect.DeepEqual(resolved, expected) {
		t.Errorf("ParseM3U order mismatch:\nGot:      %v\nExpected: %v", resolved, expected)
	}
}

func TestPlaylist_ResolveAudioFiles(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Single audio file
	singleFile := filepath.Join(tempDir, "single.mp3")
	_ = os.WriteFile(singleFile, []byte("audio"), 0644)

	res, err := ResolveAudioFiles(singleFile)
	if err != nil {
		t.Fatalf("single file resolve failed: %v", err)
	}
	if len(res) != 1 || res[0] != singleFile {
		t.Errorf("expected [%s], got %v", singleFile, res)
	}

	// 2. Directory without playlist -> sorted naturally
	stagedDir := filepath.Join(tempDir, "staged")
	_ = os.MkdirAll(stagedDir, 0755)

	t10 := filepath.Join(stagedDir, "track10.mp3")
	t1 := filepath.Join(stagedDir, "track1.mp3")
	t2 := filepath.Join(stagedDir, "track2.mp3")
	_ = os.WriteFile(t10, []byte("audio"), 0644)
	_ = os.WriteFile(t1, []byte("audio"), 0644)
	_ = os.WriteFile(t2, []byte("audio"), 0644)

	resDir, err := ResolveAudioFiles(stagedDir)
	if err != nil {
		t.Fatalf("dir resolve failed: %v", err)
	}

	expectedDirOrder := []string{t1, t2, t10}
	if !reflect.DeepEqual(resDir, expectedDirOrder) {
		t.Errorf("expected natural sorted order %v, got %v", expectedDirOrder, resDir)
	}

	// 3. Directory with playlist -> playlist takes priority
	m3uPath := filepath.Join(stagedDir, "album.m3u8")
	m3uContent := "#EXTM3U\ntrack10.mp3\ntrack1.mp3\ntrack2.mp3\n"
	_ = os.WriteFile(m3uPath, []byte(m3uContent), 0644)

	resPlaylist, err := ResolveAudioFiles(stagedDir)
	if err != nil {
		t.Fatalf("dir with playlist resolve failed: %v", err)
	}
	expectedPlaylistOrder := []string{t10, t1, t2}
	if !reflect.DeepEqual(resPlaylist, expectedPlaylistOrder) {
		t.Errorf("expected playlist order %v, got %v", expectedPlaylistOrder, resPlaylist)
	}
}
