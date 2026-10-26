package review_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/internal/review"
	"github.com/kalverra/agents/pkg/git"
)

func TestScaffoldDescription_Basic(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number: 42,
		Body:   "Existing PR body text",
	}
	diff := &git.BranchDiffResult{
		Files: []git.FileDiff{
			{Path: "pkg/auth/auth.go", Status: "modified"},
			{Path: "pkg/auth/auth_test.go", Status: "added"},
		},
	}

	scaffold := review.ScaffoldDescription(pr, diff)

	assert.Contains(t, scaffold, "Existing PR body text")
	assert.Contains(t, scaffold, "## Why")
	assert.Contains(t, scaffold, "## Notes")
	assert.Contains(t, scaffold, "## Changes")
	assert.Contains(t, scaffold, "diff:path/to/file.go#L10-L20")
	assert.Contains(t, scaffold, "<!-- Changed files: pkg/auth/auth.go, pkg/auth/auth_test.go -->")
	assert.Contains(t, scaffold, "```mermaid")
	assert.Contains(t, scaffold, "<details>\n<summary>")
	assert.NotContains(t, scaffold, "```diff")
	assert.Contains(t, scaffold, "## Merge Danger")
	assert.Contains(t, scaffold, "| **Revert** |")
	assert.Contains(t, scaffold, "| **Blast radius** |")
	assert.NotContains(t, scaffold, "## Impact")
}

func TestScaffoldDescription_NilPRAndDiff(t *testing.T) {
	t.Parallel()

	scaffold := review.ScaffoldDescription(nil, nil)

	assert.Contains(t, scaffold, "## Why")
	assert.Contains(t, scaffold, "## Changes")
	assert.Contains(t, scaffold, "## Merge Danger")
	assert.NotContains(t, scaffold, "Changed files")
}
