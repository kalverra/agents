package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/pkg/git"
)

func TestLocalReviewsDir(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	root, reviewsDir, err := LocalReviewsDir(tmp)
	require.NoError(t, err)
	assert.NotEmpty(t, root)
	assert.DirExists(t, reviewsDir)

	gitIgnorePath := filepath.Join(root, ".agents", "local", ".gitignore")
	assert.FileExists(t, gitIgnorePath)
	content, err := os.ReadFile(gitIgnorePath) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, "*\n", string(content))
}

func TestBuildResult_JSONSerialization(t *testing.T) {
	t.Parallel()

	res := BuildResult{
		ReviewDir:       "102-feat-api",
		ReviewPath:      "102-feat-api/review.md",
		OverallDiffPath: "102-feat-api/overall.diff",
		DiffsDir:        "102-feat-api/diffs",
		PRNumber:        102,
		PRURL:           "https://github.com/o/r/pull/102",
		CurrentBranch:   "feat/api",
		BaseBranch:      "feat/auth",
		MergeBase:       "abc1234",
		Head:            "def5678",
		HasLocalChanges: false,
		UnresolvedCount: 1,
		Stats: git.DiffStats{
			FilesChanged: 2,
			Insertions:   10,
			Deletions:    2,
		},
		Stack: &StackInfo{
			Position:    2,
			Total:       3,
			Trunk:       "main",
			NeedsRebase: true,
			PrevBranch:  "feat/auth",
			NextBranch:  "feat/ui",
			Layers: []StackLayer{
				{Position: 1, Branch: "feat/auth", PRNumber: 101, State: "MERGED"},
				{Position: 2, Branch: "feat/api", PRNumber: 102, State: "OPEN", IsCurrent: true},
				{Position: 3, Branch: "feat/ui", PRNumber: 103, State: "OPEN"},
			},
		},
		CIStatus: &github.CIStatusRollup{
			Total: 2,
			Pass:  2,
			Checks: []github.CheckRun{
				{Name: "test", State: "SUCCESS", Bucket: "pass"},
				{Name: "lint", State: "SUCCESS", Bucket: "pass"},
			},
		},
	}

	data, err := json.Marshal(res)
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "102-feat-api", parsed["review_dir"])
	assert.Equal(t, "102-feat-api/review.md", parsed["review_path"])
	assert.Equal(t, "102-feat-api/overall.diff", parsed["overall_diff_path"])
	assert.Equal(t, "102-feat-api/diffs", parsed["diffs_dir"])
	assert.EqualValues(t, 102, parsed["pr_number"])

	ciRaw, ok := parsed["ci_status"].(map[string]any)
	require.True(t, ok, "ci_status field should be present in JSON")
	assert.EqualValues(t, 2, ciRaw["total"])
	assert.EqualValues(t, 2, ciRaw["pass"])

	stackRaw, ok := parsed["stack"].(map[string]any)
	require.True(t, ok, "stack field should be present in JSON")
	assert.EqualValues(t, 2, stackRaw["position"])
	assert.EqualValues(t, 3, stackRaw["total"])
	assert.Equal(t, "main", stackRaw["trunk"])
	assert.Equal(t, true, stackRaw["needs_rebase"])
	assert.Equal(t, "feat/auth", stackRaw["prev_branch"])
	assert.Equal(t, "feat/ui", stackRaw["next_branch"])
}
