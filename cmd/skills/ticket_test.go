package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/ticket"
)

func TestTicketStatusAutoWriteFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	key := "DX-999"
	file := filepath.Join(tmpDir, key+".md")

	tk := &ticket.Ticket{
		ID:          key,
		Title:       "Auto write test ticket",
		Description: "Test description",
		Status:      "In Progress",
		URL:         "https://example/browse/DX-999",
	}

	content := ticket.TicketToMarkdownFile(tk)
	err := os.WriteFile(file, []byte(content), 0600)
	require.NoError(t, err)

	data, err := os.ReadFile(file) //nolint:gosec
	require.NoError(t, err)
	assert.Contains(
		t,
		string(data),
		`<ticket summary="Auto write test ticket" link="https://example/browse/DX-999" id="DX-999" status="In Progress">`,
	)
}

func TestFormatBranchDefaultIndication(t *testing.T) {
	t.Parallel()

	gotDefault := formatBranchOutput("main", "main")
	assert.Equal(t, "main (default)", gotDefault)

	gotNonDefault := formatBranchOutput("add-widget/DX-123", "main")
	assert.Equal(t, "add-widget/DX-123", gotNonDefault)
}

func TestFormatTicketTree(t *testing.T) {
	t.Parallel()

	epics := []*ticket.Ticket{
		{ID: "DX-100", Title: "Name of Epic", Status: "In Progress"},
	}
	tickets := []*ticket.Ticket{
		{
			ID:          "DX-111",
			Title:       "Name of linked story",
			Status:      "In Progress",
			ParentID:    "DX-100",
			ParentTitle: "Name of Epic",
		},
		{ID: "DX-222", Title: "Standalone story", Status: "Open"},
	}

	got := formatTicketTree(epics, tickets)
	expected := "- DX-100: Name of Epic\n  - DX-111: Name of linked story [In Progress]\n- DX-222: Standalone story [Open]\n"
	assert.Equal(t, expected, got)
}

func TestDirtyDefaultBranchWarning(t *testing.T) {
	t.Parallel()

	warn := dirtyDefaultBranchWarning(2, 1, "main", "main")
	assert.Equal(t, `uncommitted work on default branch "main" without Jira ticket`, warn)

	noWarnClean := dirtyDefaultBranchWarning(0, 0, "main", "main")
	assert.Empty(t, noWarnClean)

	noWarnBranch := dirtyDefaultBranchWarning(2, 0, "feat/DX-123", "main")
	assert.Empty(t, noWarnBranch)
}
