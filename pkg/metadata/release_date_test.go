package metadata_test

import (
	"testing"

	"github.com/t0ggl/callimachus/pkg/metadata"
)

func TestNormalizeReleaseDate(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		year int
		want string
	}{
		{"full ISO", "2028-05-10", 2028, "2028-05-10"},
		{"RFC3339", "2028-05-10T00:00:00Z", 0, "2028-05-10"},
		{"named full date", "June 15, 2021", 0, "2021-06-15"},
		{"day month year", "15 Jun 2021", 0, "2021-06-15"},
		{"slash unpadded", "2028/5/3", 0, "2028-05-03"},
		{"partial YYYY-MM", "2028-05", 0, "2028-05"},
		{"partial unpadded month", "2028-5", 0, "2028-05"},
		{"slash month year", "05/2028", 0, "2028-05"},
		{"month name and year", "November 2026", 0, "2026-11"},
		{"september abbrev", "Sept. 2027", 0, "2027-09"},
		{"zero day partial", "2028-05-00", 0, "2028-05"},
		{"zero month partial", "2028-00-00", 0, "2028"},
		{"year only string", "2028", 0, "2028"},
		{"season and year", "Fall 2028", 0, "2028"},
		{"empty date with future year", "", 2029, "2029"},
		{"TBA with future year", "TBA", 2029, "2029"},
		{"invalid calendar day falls back to year", "2028-02-30", 0, "2028"},
		{"empty", "", 0, ""},
		{"unparseable", "TBA", 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := metadata.NormalizeReleaseDate(tc.raw, tc.year); got != tc.want {
				t.Errorf("NormalizeReleaseDate(%q, %d) = %q, want %q", tc.raw, tc.year, got, tc.want)
			}
		})
	}
}

func TestIsFutureRelease(t *testing.T) {
	const today = "2026-11-19"
	tests := []struct {
		name string
		raw  string
		year int
		want bool
	}{
		{"future day", "2026-11-20", 0, true},
		{"today", "2026-11-19", 0, false},
		{"past day", "2026-11-18", 0, false},
		{"future month same year", "2026-12", 0, true},
		{"future month name", "December 2026", 0, true},
		{"future month next year", "2027-01", 0, true},
		{"current month", "2026-11", 0, false},
		{"past month", "2026-10", 0, false},
		{"future year string", "2027", 0, true},
		{"empty date future year", "", 2027, true},
		{"current year string", "2026", 0, false},
		{"empty date current year", "", 2026, false},
		{"past year", "2025", 0, false},
		{"unknown", "", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := metadata.IsFutureRelease(tc.raw, tc.year, today); got != tc.want {
				t.Errorf("IsFutureRelease(%q, %d, %q) = %v, want %v", tc.raw, tc.year, today, got, tc.want)
			}
		})
	}
}

func TestMergeReleaseDate(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		incoming string
		want     string
	}{
		{"fill empty", "", "2028", "2028"},
		{"upgrade year to full", "2028", "2028-05-10", "2028-05-10"},
		{"upgrade month to full", "2028-05", "2028-05-10", "2028-05-10"},
		{"preserve day when incoming is year-only", "2028-05-10", "2028", "2028-05-10"},
		{"preserve day when incoming is month-only", "2028-05-10", "2028-05", "2028-05-10"},
		{"update when year moves", "2028-05-10", "2029", "2029"},
		{"update when month moves", "2028-05-10", "2028-09", "2028-09"},
		{"update when day moves", "2028-05-10", "2028-06-01", "2028-06-01"},
		{"keep current when incoming empty", "2028-05-10", "", "2028-05-10"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := metadata.MergeReleaseDate(tc.current, tc.incoming); got != tc.want {
				t.Errorf("MergeReleaseDate(%q, %q) = %q, want %q", tc.current, tc.incoming, got, tc.want)
			}
		})
	}
}
