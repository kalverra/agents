package commit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/agents/internal/commit"
	"github.com/kalverra/agents/internal/git"
	"github.com/kalverra/agents/internal/ticket"
)

func TestFormatBundle(t *testing.T) {
	t.Parallel()

	diff := &git.BranchDiffResult{
		CurrentBranch: "feature/PROJ-123",
		BaseBranch:    "main",
		Stats: git.DiffStats{
			FilesChanged: 1,
			Insertions:   5,
			Deletions:    0,
		},
		Files: []git.FileDiff{
			{
				Path:      "main.go",
				Status:    "modified",
				Additions: 5,
				Deletions: 0,
				Patch:     "@@ -1,3 +1,8 @@\n+func New() {}\n",
			},
		},
	}

	tk := &ticket.Ticket{
		ID:          "PROJ-123",
		Title:       "Add commit generator",
		Description: "Implement AI commit message generator hook",
		Status:      "In Progress",
	}

	xml := commit.FormatBundle(diff, tk)

	assert.Contains(t, xml, `<commit_context branch="feature/PROJ-123">`)
	assert.Contains(t, xml, `<ticket id="PROJ-123" status="In Progress">`)
	assert.Contains(t, xml, `<title>Add commit generator</title>`)
	assert.Contains(t, xml, `<file path="main.go" status="modified" additions="5" deletions="0">`)
	assert.Contains(t, xml, `+func New() {}`)
}

func TestFormatBundle_NoTicket(t *testing.T) {
	t.Parallel()

	diff := &git.BranchDiffResult{
		CurrentBranch: "main",
		Files: []git.FileDiff{
			{Path: "foo.txt", Status: "added", Additions: 1, Patch: "+foo\n"},
		},
	}

	xml := commit.FormatBundle(diff, nil)
	assert.Contains(t, xml, `<commit_context branch="main">`)
	assert.NotContains(t, xml, `<ticket`)
	assert.Contains(t, xml, `<file path="foo.txt"`)
}
