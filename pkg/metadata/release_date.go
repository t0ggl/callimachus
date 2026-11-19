package metadata

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reSept                = regexp.MustCompile(`(?i)\bsept\.?\b`)
	reMonthDot            = regexp.MustCompile(`(?i)\b(jan|feb|mar|apr|jun|jul|aug|sep|oct|nov|dec)\.`)
	rePartialMonthZeroDay = regexp.MustCompile(`^(\d{4})-(0[1-9]|1[0-2])-00$`)
	rePartialYearZeroDate = regexp.MustCompile(`^(\d{4})-00-00$`)
	reFourDigitYear       = regexp.MustCompile(`(?:^|\D)(1\d{3}|2\d{3})(?:\D|$)`)
)

var fullDateLayouts = []string{
	time.DateOnly,
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006/01/02",
	"2006.01.02",
	"2006-1-2",
	"2006/1/2",
	"January 2, 2006",
	"Jan 2, 2006",
	"January 2 2006",
	"Jan 2 2006",
	"2 January 2006",
	"2 Jan 2006",
	"2006 January 2",
	"2006 Jan 2",
}

var monthYearLayouts = []string{
	"2006-01",
	"2006/01",
	"2006.01",
	"2006-1",
	"2006/1",
	"01/2006",
	"1/2006",
	"01-2006",
	"1-2006",
	"January 2006",
	"Jan 2006",
	"January, 2006",
	"Jan, 2006",
	"2006 January",
	"2006 Jan",
}

// NormalizeReleaseDate normalizes a release date string (or fallback year) to
// ISO precision ("YYYY-MM-DD", "YYYY-MM", or "YYYY") without inventing missing
// month or day components.
func NormalizeReleaseDate(raw string, year int) string {
	s := strings.TrimSpace(raw)
	if s != "" {
		clean := reMonthDot.ReplaceAllString(reSept.ReplaceAllString(s, "Sep"), "$1")
		for _, layout := range fullDateLayouts {
			if t, err := time.Parse(layout, clean); err == nil && t.Year() >= 1 && t.Year() <= 9999 {
				return t.Format(time.DateOnly)
			}
		}
		if m := rePartialMonthZeroDay.FindStringSubmatch(clean); len(m) == 3 {
			return m[1] + "-" + m[2]
		}
		if m := rePartialYearZeroDate.FindStringSubmatch(clean); len(m) == 2 {
			return m[1]
		}
		for _, layout := range monthYearLayouts {
			if t, err := time.Parse(layout, clean); err == nil && t.Year() >= 1 && t.Year() <= 9999 {
				return t.Format("2006-01")
			}
		}
		if t, err := time.Parse("2006", clean); err == nil && t.Year() >= 1 && t.Year() <= 9999 {
			return t.Format("2006")
		}
		if m := reFourDigitYear.FindStringSubmatch(clean); len(m) == 2 {
			return m[1]
		}
	}
	if year >= 1 && year <= 9999 {
		return fmt.Sprintf("%04d", year)
	}
	return ""
}

// ReleaseYear extracts the four-digit release year from raw or fallback.
func ReleaseYear(raw string, fallback int) int {
	norm := NormalizeReleaseDate(raw, fallback)
	if len(norm) < 4 {
		return 0
	}
	y, err := strconv.Atoi(norm[:4])
	if err != nil {
		return 0
	}
	return y
}

// IsFutureRelease reports whether the normalized release date or year is
// strictly after today ("YYYY-MM-DD") at the known precision of the date.
func IsFutureRelease(raw string, year int, today string) bool {
	if len(today) < 10 {
		today = time.Now().UTC().Format(time.DateOnly)
	} else {
		today = today[:10]
	}
	norm := NormalizeReleaseDate(raw, year)
	switch len(norm) {
	case 10:
		return norm > today
	case 7:
		return norm > today[:7]
	case 4:
		return norm > today[:4]
	default:
		return false
	}
}

// MergeReleaseDate selects the updated normalized release date without
// downgrading precision when an incoming value only matches the existing period.
func MergeReleaseDate(current, incoming string) string {
	cur := NormalizeReleaseDate(current, 0)
	inc := NormalizeReleaseDate(incoming, 0)
	if inc == "" {
		return cur
	}
	if cur == "" {
		return inc
	}
	if len(inc) < len(cur) && strings.HasPrefix(cur, inc) {
		return cur
	}
	return inc
}
