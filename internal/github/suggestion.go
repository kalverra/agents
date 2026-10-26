package github

import (
	"regexp"
	"strings"
)

var suggestionBlockRegex = regexp.MustCompile("(?s)```suggestion\n(.*?)```")

// ExtractSuggestions pulls GitHub ```suggestion blocks out of a comment body.
// Returns the remaining comment text and any suggested replacement code.
func ExtractSuggestions(body string) (string, []Suggestion) {
	matches := suggestionBlockRegex.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return strings.TrimSpace(body), nil
	}

	var suggestions []Suggestion
	for _, m := range matches {
		suggestions = append(suggestions, Suggestion{
			Code:   strings.TrimRight(m[1], "\n"),
			Source: SuggestionSourceBody,
		})
	}

	clean := suggestionBlockRegex.ReplaceAllString(body, "")
	return strings.TrimSpace(clean), suggestions
}
