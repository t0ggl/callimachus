package metadata

import (
	"strings"
	"unicode"
)

// NormalizeISBN13 returns the canonical ISBN-13 for a valid ISBN-10 or ISBN-13.
func NormalizeISBN13(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "URN:ISBN:")
	value = strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)

	switch len(value) {
	case 10:
		sum := 0
		for i := range len(value) {
			digit := value[i]
			switch {
			case digit >= '0' && digit <= '9':
				sum += (10 - i) * int(digit-'0')
			case i == 9 && digit == 'X':
				sum += 10
			default:
				return ""
			}
		}
		if sum%11 != 0 {
			return ""
		}
		base := "978" + value[:9]
		return base + string(isbn13CheckDigit(base))
	case 13:
		if !strings.HasPrefix(value, "978") && !strings.HasPrefix(value, "979") {
			return ""
		}
		for i := range len(value) {
			if value[i] < '0' || value[i] > '9' {
				return ""
			}
		}
		if value[12] == isbn13CheckDigit(value[:12]) {
			return value
		}
	}
	return ""
}

func isbn13CheckDigit(digits string) byte {
	sum := 0
	for i := range len(digits) {
		weight := 1 + 2*(i%2)
		sum += weight * int(digits[i]-'0')
	}
	return '0' + byte((10-sum%10)%10)
}
