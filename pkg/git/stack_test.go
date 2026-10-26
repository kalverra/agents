package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatStack_EmptyOrInvalid(t *testing.T) {
	t.Parallel()

	assert.Empty(t, FormatStack(StackData{}))
	assert.Empty(t, FormatStack(StackData{Branches: []StackBranch{}}))
}

func TestFormatStack_OnTrunk(t *testing.T) {
	t.Parallel()

	data := StackData{
		Trunk:         "main",
		CurrentBranch: "main",
		Branches: []StackBranch{
			{Name: "b1", IsCurrent: false},
		},
	}
	assert.Empty(t, FormatStack(data))
}

func TestFormatStack_NotInStack(t *testing.T) {
	t.Parallel()

	data := StackData{
		Trunk:         "main",
		CurrentBranch: "feature-unrelated",
		Branches: []StackBranch{
			{Name: "b1", IsCurrent: false},
			{Name: "b2", IsCurrent: false},
		},
	}
	assert.Empty(t, FormatStack(data))
}

func TestFormatStack_SingleBranchNoPR(t *testing.T) {
	t.Parallel()

	data := StackData{
		Trunk:         "main",
		CurrentBranch: "b1",
		Branches: []StackBranch{
			{Name: "b1", IsCurrent: true, NeedsRebase: false},
		},
	}
	assert.Equal(t, "1/1", FormatStack(data))
}

func TestFormatStack_LayerWithOpenPR(t *testing.T) {
	t.Parallel()

	data := StackData{
		Trunk:         "main",
		CurrentBranch: "api",
		Branches: []StackBranch{
			{Name: "auth", IsCurrent: false},
			{Name: "api", IsCurrent: true, PR: &StackPR{Number: 42, State: "OPEN"}},
			{Name: "fe", IsCurrent: false},
		},
	}
	assert.Equal(t, "2/3 #42", FormatStack(data))
}

func TestFormatStack_MergedAndQueuedPR(t *testing.T) {
	t.Parallel()

	dataMerged := StackData{
		Branches: []StackBranch{
			{Name: "auth", IsCurrent: true, PR: &StackPR{Number: 41, State: "MERGED"}},
		},
	}
	assert.Equal(t, "1/1 #41✓", FormatStack(dataMerged))

	dataQueued := StackData{
		Branches: []StackBranch{
			{Name: "auth", IsCurrent: true, PR: &StackPR{Number: 41, State: "QUEUED"}},
		},
	}
	assert.Equal(t, "1/1 #41◎", FormatStack(dataQueued))
}

func TestFormatStack_NeedsRebase(t *testing.T) {
	t.Parallel()

	dataWithPR := StackData{
		Branches: []StackBranch{
			{Name: "auth", IsCurrent: true, NeedsRebase: true, PR: &StackPR{Number: 42, State: "OPEN"}},
		},
	}
	assert.Equal(t, "1/1 #42 ⚠", FormatStack(dataWithPR))

	dataNoPR := StackData{
		Branches: []StackBranch{
			{Name: "auth", IsCurrent: true, NeedsRebase: true},
		},
	}
	assert.Equal(t, "1/1 ⚠", FormatStack(dataNoPR))
}

func TestFormatStackJSON(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"trunk": "main",
		"currentBranch": "api",
		"branches": [
			{"name": "auth", "isCurrent": false},
			{"name": "api", "isCurrent": true, "pr": {"number": 42, "state": "OPEN"}},
			{"name": "fe", "isCurrent": false}
		]
	}`)

	out, err := FormatStackJSON(raw)
	require.NoError(t, err)
	assert.Equal(t, "2/3 #42", out)
}

func TestDetectStack_Filesystem(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	gitDir := filepath.Join(tempDir, ".git")
	err := os.MkdirAll(gitDir, 0o700)
	require.NoError(t, err)

	ghStackContent := `{
		"schemaVersion": 1,
		"stacks": [
			{
				"trunk": {"branch": "main"},
				"branches": [
					{"branch": "b1", "base": "sha1", "pullRequest": {"number": 101, "merged": false}},
					{"branch": "b2", "base": "sha2", "pullRequest": {"number": 102, "merged": false}}
				]
			}
		]
	}`
	err = os.WriteFile(filepath.Join(gitDir, "gh-stack"), []byte(ghStackContent), 0o600)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/b2\n"), 0o600)
	require.NoError(t, err)

	data, err := DetectStack(tempDir, "")
	require.NoError(t, err)
	require.NotNil(t, data)
	assert.Equal(t, "main", data.Trunk)
	assert.Equal(t, "b2", data.CurrentBranch)
	assert.Len(t, data.Branches, 2)
	assert.True(t, data.Branches[1].IsCurrent)
	assert.Equal(t, 102, data.Branches[1].PR.Number)
	assert.Equal(t, "2/2 #102", FormatStack(*data))

	// When on trunk "main"
	err = os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600)
	require.NoError(t, err)
	trunkStatus := GetStackStatus(tempDir, "")
	assert.Empty(t, trunkStatus)

	// When parent branch b1 has moved past b2's base sha2
	err = os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/b2\n"), 0o600)
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o700)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(gitDir, "refs", "heads", "b1"), []byte("sha-new\n"), 0o600)
	require.NoError(t, err)

	rebaseData, err := DetectStack(tempDir, "")
	require.NoError(t, err)
	require.NotNil(t, rebaseData)
	assert.True(t, rebaseData.Branches[1].NeedsRebase)
	assert.Equal(t, "2/2 #102 ⚠", FormatStack(*rebaseData))
}
