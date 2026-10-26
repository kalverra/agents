package review

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/pkg/git"
)

// ThreadStatus indicates whether an unresolved review thread needs attention or was fixed.
type ThreadStatus string

const (
	// StatusActionRequired indicates the thread is unresolved and requires author action.
	StatusActionRequired ThreadStatus = "action_required"
	// StatusResolvedByCommit indicates the suggestion has already been applied in subsequent code.
	StatusResolvedByCommit ThreadStatus = "resolved_by_commit"
	// StatusOutdated indicates the code was changed by a subsequent commit but no explicit suggestion exists.
	StatusOutdated ThreadStatus = "outdated"
)

// ThreadAssessment summarizes the status and staleness of a review thread.
type ThreadAssessment struct {
	ThreadID          string       `json:"thread_id"`
	Path              string       `json:"path"`
	Line              int          `json:"line"`
	Author            string       `json:"author"`
	Status            ThreadStatus `json:"status"`
	Verdict           string       `json:"verdict"`
	SuggestionApplied bool         `json:"suggestion_applied"`
	HasSuggestion     bool         `json:"has_suggestion"`
	SuggestedCode     string       `json:"suggested_code,omitempty"`
}

// AssessThread examines a review thread, comparing suggested code against current files to verify staleness.
func AssessThread(thread *github.ReviewThread, fileDiff *git.FileDiff, repoRoot string) ThreadAssessment {
	if thread == nil {
		return ThreadAssessment{}
	}

	author := ""
	if len(thread.Comments) > 0 {
		author = thread.Comments[0].Author
	}

	assessment := ThreadAssessment{
		ThreadID: thread.ID,
		Path:     thread.Path,
		Line:     thread.Line,
		Author:   author,
		Status:   StatusActionRequired,
	}

	var suggestions []github.Suggestion
	for _, c := range thread.Comments {
		suggestions = append(suggestions, c.Suggestions...)
	}

	if len(suggestions) > 0 {
		assessment.HasSuggestion = true
		assessment.SuggestedCode = suggestions[0].Code

		// Read target file content
		var fileContent string
		if repoRoot != "" && thread.Path != "" {
			targetFile := filepath.Join(repoRoot, thread.Path)
			//nolint:gosec // targetFile is constructed from workspace repo root and repo-relative path
			if data, err := os.ReadFile(targetFile); err == nil {
				fileContent = string(data)
			}
		}

		if fileContent == "" && fileDiff != nil {
			// Extract added lines from patch
			var adds strings.Builder
			for line := range strings.SplitSeq(fileDiff.Patch, "\n") {
				if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
					adds.WriteString(line[1:])
					adds.WriteByte('\n')
				}
			}
			fileContent = adds.String()
		}

		applied := false
		if fileContent != "" {
			for _, s := range suggestions {
				if codeContainsSuggestion(fileContent, s.Code) {
					applied = true
					break
				}
			}
		}

		if applied {
			assessment.Status = StatusResolvedByCommit
			assessment.SuggestionApplied = true
			assessment.Verdict = "Resolved by newer commit (suggestion already applied in current code)"
		} else {
			assessment.Status = StatusActionRequired
			assessment.SuggestionApplied = false
			assessment.Verdict = "Unresolved: suggestion not applied in current code"
		}
		return assessment
	}

	// No suggestion
	if thread.IsOutdated {
		assessment.Status = StatusOutdated
		assessment.Verdict = "Outdated: target lines shifted or modified by newer commit (verify manually)"
	} else {
		assessment.Status = StatusActionRequired
		assessment.Verdict = "Unresolved: comment requires attention"
	}

	return assessment
}

func codeContainsSuggestion(content, suggestion string) bool {
	normContent := normalizeCodeBlock(content)
	normSuggestion := normalizeCodeBlock(suggestion)
	if normSuggestion == "" {
		return false
	}
	return strings.Contains(normContent, normSuggestion)
}

func normalizeCodeBlock(s string) string {
	var lines []string
	for raw := range strings.SplitSeq(s, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return strings.Join(lines, "\n")
}
