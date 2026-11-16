package ai

import (
	"fmt"
	"strings"

	"github.com/t0ggl/callimachus/pkg/metadata"
)

// BuildSystemPrompt constructs the system instructions directing the LLM to output strict JSON.
func BuildSystemPrompt() string {
	return `You are a book and audiobook metadata disambiguation assistant for Callimachus.
Your task is to analyze a raw release title and determine which candidate book from a list of possibilities is the best match.
Evaluate whether the release represents an anthology/collection/omnibus, determine the cleaned title and author, and assign a confidence score between 0.00 and 1.00.

You must respond ONLY with a raw JSON object matching the following structure:
{
  "matched_candidate_index": 0,
  "confidence": 0.95,
  "reasoning": "Explanation for the match decision",
  "is_anthology": false,
  "cleaned_title": "Clean Title",
  "cleaned_author": "Clean Author"
}

Rules:
1. "matched_candidate_index" must be the 0-based index of the matching candidate. If no candidate matches, use -1.
2. "confidence" must be a float between 0.00 and 1.00.
3. "reasoning" should be a concise summary of why the candidate was selected.
4. "is_anthology" must be true if the release is a collection or anthology of multiple books/stories.
5. "cleaned_title" and "cleaned_author" must be the canonical, human-readable book title and author.
6. Do NOT include markdown code blocks, backticks, or any conversational text. Return only the JSON object.`
}

// BuildUserPrompt constructs the prompt containing the raw release and candidates list.
func BuildUserPrompt(release string, candidates []metadata.BookMetadata) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Raw Release Title: %s\n\nCandidate Books:\n", release))
	for i, c := range candidates {
		sb.WriteString(fmt.Sprintf("[%d]\n", i))
		sb.WriteString(fmt.Sprintf("  Title: %s\n", c.Title))
		if c.Subtitle != "" {
			sb.WriteString(fmt.Sprintf("  Subtitle: %s\n", c.Subtitle))
		}
		if len(c.Authors) > 0 {
			sb.WriteString(fmt.Sprintf("  Authors: %s\n", strings.Join(c.Authors, ", ")))
		}
		if c.Year > 0 {
			sb.WriteString(fmt.Sprintf("  Year: %d\n", c.Year))
		}
		if c.Publisher != "" {
			sb.WriteString(fmt.Sprintf("  Publisher: %s\n", c.Publisher))
		}
		if c.ISBN13 != "" {
			sb.WriteString(fmt.Sprintf("  ISBN13: %s\n", c.ISBN13))
		}
		if c.ASIN != "" {
			sb.WriteString(fmt.Sprintf("  ASIN: %s\n", c.ASIN))
		}
		if c.Series != "" {
			if c.SeriesIndex != nil {
				sb.WriteString(fmt.Sprintf("  Series: %s #%.1f\n", c.Series, *c.SeriesIndex))
			} else {
				sb.WriteString(fmt.Sprintf("  Series: %s\n", c.Series))
			}
		}
	}
	sb.WriteString("\nSelect the matching candidate, assess confidence, and output the required JSON object.")
	return sb.String()
}
