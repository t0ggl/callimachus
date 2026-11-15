package metadata

import (
	"math"
	"strings"
	"unicode"
)

// AutoMatchThreshold is the minimum confidence score required for automated metadata ingestion.
const AutoMatchThreshold = 0.85

// normalizeString lowercases the input and retains only alphanumeric characters separated by single spaces.
func normalizeString(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}

// levenshtein calculates the edit distance between two rune sequences.
func levenshtein(s1, s2 string) int {
	r1 := []rune(s1)
	r2 := []rune(s2)
	len1 := len(r1)
	len2 := len(r2)

	if len1 == 0 {
		return len2
	}
	if len2 == 0 {
		return len1
	}

	prev := make([]int, len2+1)
	curr := make([]int, len2+1)

	for j := 0; j <= len2; j++ {
		prev[j] = j
	}

	for i := 1; i <= len1; i++ {
		curr[0] = i
		for j := 1; j <= len2; j++ {
			cost := 0
			if r1[i-1] != r2[j-1] {
				cost = 1
			}
			curr[j] = min(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		copy(prev, curr)
	}

	return prev[len2]
}

// TitleSimilarity computes the normalized Levenshtein similarity between two title strings in [0.0, 1.0].
func TitleSimilarity(t1, t2 string) float64 {
	n1 := normalizeString(t1)
	n2 := normalizeString(t2)

	if n1 == "" && n2 == "" {
		return 1.0
	}
	if n1 == "" || n2 == "" {
		return 0.0
	}
	if n1 == n2 {
		return 1.0
	}

	dist := levenshtein(n1, n2)
	maxLen := max(len([]rune(n1)), len([]rune(n2)))
	if maxLen == 0 {
		return 1.0
	}

	sim := 1.0 - float64(dist)/float64(maxLen)
	if sim < 0.0 {
		return 0.0
	}
	return sim
}

// BestTitleSimilarity evaluates variations of parsed title and candidate title/subtitle to find the best match.
func BestTitleSimilarity(parsedTitle, candidateTitle, candidateSubtitle string) float64 {
	candidates := []string{candidateTitle}
	if candidateSubtitle != "" {
		candidates = append(candidates,
			candidateTitle+" "+candidateSubtitle,
			candidateTitle+": "+candidateSubtitle,
			candidateTitle+" - "+candidateSubtitle,
		)
	}

	parsedOptions := []string{parsedTitle}
	for _, sep := range []string{":", " - ", " – ", " — "} {
		if idx := strings.Index(parsedTitle, sep); idx != -1 {
			base := strings.TrimSpace(parsedTitle[:idx])
			if base != "" {
				parsedOptions = append(parsedOptions, base)
			}
		}
	}

	best := 0.0
	for _, p := range parsedOptions {
		for _, c := range candidates {
			sim := TitleSimilarity(p, c)
			if sim > best {
				best = sim
			}
		}
	}
	return best
}

var authorNoiseTokens = map[string]bool{
	"and":  true,
	"with": true,
	"feat": true,
	"ft":   true,
	"by":   true,
	"dr":   true,
	"mr":   true,
	"mrs":  true,
}

func tokenizeAuthor(s string) []string {
	norm := normalizeString(s)
	fields := strings.Fields(norm)
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		if !authorNoiseTokens[f] && len(f) > 0 {
			tokens = append(tokens, f)
		}
	}
	return tokens
}

func matchTokenSet(setA, setB []string) float64 {
	if len(setA) == 0 || len(setB) == 0 {
		return 0.0
	}

	usedB := make([]bool, len(setB))
	matchedA := make([]bool, len(setA))
	matchedCount := 0

	// Pass 1: Exact matches
	for i, a := range setA {
		for j, b := range setB {
			if !usedB[j] && a == b {
				usedB[j] = true
				matchedA[i] = true
				matchedCount++
				break
			}
		}
	}

	// Pass 2: Initial prefix matches (1-to-1)
	for i, a := range setA {
		if matchedA[i] {
			continue
		}
		for j, b := range setB {
			if !usedB[j] {
				if (len(a) == 1 && strings.HasPrefix(b, a)) || (len(b) == 1 && strings.HasPrefix(a, b)) {
					usedB[j] = true
					matchedA[i] = true
					matchedCount++
					break
				}
			}
		}
	}

	if matchedCount == 0 {
		return 0.0
	}

	// Count effective tokens in A (ignore unmatched 1-character initials if remaining tokens matched)
	effA := 0
	for i, a := range setA {
		if matchedA[i] {
			effA++
		} else if len(a) > 1 {
			effA++
		}
	}

	// Count effective tokens in B (ignore unmatched 1-character initials if remaining tokens matched)
	effB := 0
	for j, b := range setB {
		if usedB[j] {
			effB++
		} else if len(b) > 1 {
			effB++
		}
	}

	union := effA + effB - matchedCount
	if union <= 0 {
		return 1.0
	}

	return float64(matchedCount) / float64(union)
}

// AuthorSimilarity computes token set overlap / Jaccard index between parsed author and candidate authors in [0.0, 1.0].
// It handles inverted "Last, First", co-authors, and initials.
func AuthorSimilarity(parsedAuthor string, candidateAuthors []string) float64 {
	pTokens := tokenizeAuthor(parsedAuthor)
	if len(pTokens) == 0 || len(candidateAuthors) == 0 {
		return 0.0
	}

	best := 0.0
	var allCandTokens []string

	for _, cand := range candidateAuthors {
		cTokens := tokenizeAuthor(cand)
		if len(cTokens) > 0 {
			allCandTokens = append(allCandTokens, cTokens...)
			sim := matchTokenSet(pTokens, cTokens)
			if sim > best {
				best = sim
			}
		}
	}

	if len(candidateAuthors) > 1 {
		combinedSim := matchTokenSet(pTokens, allCandTokens)
		if combinedSim > best {
			best = combinedSim
		}
	}

	return math.Min(1.0, best)
}

// YearProximity computes the proximity score for two publication years in [0.0, 1.0].
// Returns 1.0 for exact match, 0.8 for ±1 year, 0.5 for ±2 years, 0.3 for ±3, 0.1 for ±4, and 0 for >5 years or missing year.
func YearProximity(parsedYear, candidateYear int) float64 {
	if parsedYear <= 0 || candidateYear <= 0 {
		return 0.0
	}

	diff := parsedYear - candidateYear
	if diff < 0 {
		diff = -diff
	}

	switch diff {
	case 0:
		return 1.0
	case 1:
		return 0.8
	case 2:
		return 0.5
	case 3:
		return 0.3
	case 4:
		return 0.1
	default:
		return 0.0
	}
}

// Score computes the deterministic heuristic confidence score in [0.0, 1.0]
// using the formula: Confidence = 0.45 * TitleSimilarity + 0.35 * AuthorOverlap + 0.20 * YearProximity.
func Score(parsed ParsedRelease, candidate BookMetadata) float64 {
	titleSim := BestTitleSimilarity(parsed.Title, candidate.Title, candidate.Subtitle)
	authorSim := AuthorSimilarity(parsed.Author, candidate.Authors)
	yearSim := YearProximity(parsed.Year, candidate.Year)

	confidence := 0.45*titleSim + 0.35*authorSim + 0.20*yearSim

	if confidence < 0.0 {
		return 0.0
	}
	if confidence > 1.0 {
		return 1.0
	}
	return confidence
}
