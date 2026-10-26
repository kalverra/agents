package review_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/internal/review"
	"github.com/kalverra/agents/pkg/git"
)

func TestAssessThread_UnresolvedWithUnappliedSuggestion(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.go")
	currentCode := `package config

func loadConfig() {
	prev := os.Getenv("FOO")
	defer os.Setenv("FOO", prev)
}
`
	require.NoError(t, os.WriteFile(filePath, []byte(currentCode), 0o600))

	thread := &github.ReviewThread{
		ID:         "thread_1",
		Path:       "config.go",
		Line:       5,
		IsResolved: false,
		IsOutdated: true,
		Comments: []github.Comment{
			{
				Author: "copilot",
				Body:   "Use LookupEnv instead",
				Suggestions: []github.Suggestion{
					{
						Code: `	val, ok := os.LookupEnv("FOO")
	defer func() {
		if ok {
			os.Setenv("FOO", val)
		} else {
			os.Unsetenv("FOO")
		}
	}()`,
					},
				},
			},
		},
	}

	diff := &git.FileDiff{Path: "config.go"}
	assessment := review.AssessThread(thread, diff, tmpDir)

	assert.Equal(t, review.StatusActionRequired, assessment.Status)
	assert.False(t, assessment.SuggestionApplied)
	assert.True(t, assessment.HasSuggestion)
	assert.Contains(t, assessment.Verdict, "suggestion not applied")
}

func TestAssessThread_ResolvedByNewerCommit(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.go")
	currentCode := `package config

func loadConfig() {
	val, ok := os.LookupEnv("FOO")
	defer func() {
		if ok {
			os.Setenv("FOO", val)
		} else {
			os.Unsetenv("FOO")
		}
	}()
}
`
	require.NoError(t, os.WriteFile(filePath, []byte(currentCode), 0o600))

	thread := &github.ReviewThread{
		ID:         "thread_2",
		Path:       "config.go",
		Line:       5,
		IsResolved: false, // GitHub still shows unresolved conversation
		IsOutdated: true,
		Comments: []github.Comment{
			{
				Author: "copilot",
				Body:   "Use LookupEnv instead",
				Suggestions: []github.Suggestion{
					{
						Code: `	val, ok := os.LookupEnv("FOO")
	defer func() {
		if ok {
			os.Setenv("FOO", val)
		} else {
			os.Unsetenv("FOO")
		}
	}()`,
					},
				},
			},
		},
	}

	diff := &git.FileDiff{Path: "config.go"}
	assessment := review.AssessThread(thread, diff, tmpDir)

	assert.Equal(t, review.StatusResolvedByCommit, assessment.Status)
	assert.True(t, assessment.SuggestionApplied)
	assert.Contains(t, assessment.Verdict, "suggestion already applied")
}

func TestAssessThread_NoSuggestionOutdated(t *testing.T) {
	t.Parallel()

	thread := &github.ReviewThread{
		ID:         "thread_3",
		Path:       "main.go",
		Line:       10,
		IsResolved: false,
		IsOutdated: true,
		Comments: []github.Comment{
			{Author: "alice", Body: "Consider refactoring this helper"},
		},
	}

	assessment := review.AssessThread(thread, nil, "")
	assert.Equal(t, review.StatusOutdated, assessment.Status)
	assert.False(t, assessment.HasSuggestion)
	assert.Contains(t, assessment.Verdict, "shifted or modified")
}

func TestAssessThread_ActiveUnresolvedWithoutSuggestion(t *testing.T) {
	t.Parallel()

	thread := &github.ReviewThread{
		ID:         "thread_4",
		Path:       "main.go",
		Line:       10,
		IsResolved: false,
		IsOutdated: false,
		Comments: []github.Comment{
			{Author: "alice", Body: "This needs a test"},
		},
	}

	assessment := review.AssessThread(thread, nil, "")
	assert.Equal(t, review.StatusActionRequired, assessment.Status)
	assert.Contains(t, assessment.Verdict, "requires attention")
}
