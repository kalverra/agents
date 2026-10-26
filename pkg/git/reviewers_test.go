package git

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func addAndCommitWithDate(t *testing.T, dir, path, content, message, author, email string, date time.Time) string {
	t.Helper()
	full := filepath.Join(dir, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))

	runGitCmd(t, dir, "add", path)

	dateStr := date.Format(time.RFC3339)
	runGitCmd(t, dir, "config", "user.name", author)
	runGitCmd(t, dir, "config", "user.email", email)

	// Create commit script to set dates easily
	env := os.Environ()
	env = append(env, "GIT_AUTHOR_DATE="+dateStr, "GIT_COMMITTER_DATE="+dateStr)

	runGitCmdWithEnv(t, dir, env, "commit", "--no-verify", "-m", message)

	return runGitCmd(t, dir, "rev-parse", "HEAD")
}

func runGitCmdWithEnv(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...) //nolint:gosec
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.NoError(t, err, "git %v failed: %s", args, stderr.String())
	return strings.TrimSpace(stdout.String())
}

func TestSuggestReviewers(t *testing.T) {
	t.Parallel()

	dir := initTestRepo(t)
	now := time.Now()

	// Author 1 creates file A 2 months ago (60 days)
	_ = addAndCommitWithDate(
		t,
		dir,
		"A.txt",
		"A1\nA2\nA3\nA4\n",
		"init A",
		"Author Old",
		"old@example.com",
		now.Add(-60*24*time.Hour),
	)
	renameDefaultBranch(t, dir, "main")

	// Author 2 modifies file A 2 hours ago
	_ = addAndCommitWithDate(
		t,
		dir,
		"A.txt",
		"A1 mod\nA2\nA3\nA4\n",
		"mod A",
		"Author Recent",
		"recent@example.com",
		now.Add(-2*time.Hour),
	)

	// Author Old modifies file B 2 months ago (lots of lines to try and beat the decay if we used simple counts)
	_ = addAndCommitWithDate(
		t,
		dir,
		"B.txt",
		"B1\nB2\nB3\nB4\nB5\nB6\n",
		"init B",
		"Author Old",
		"old@example.com",
		now.Add(-60*24*time.Hour),
	)

	baseHash := runGitCmd(t, dir, "rev-parse", "HEAD")

	patchA := `diff --git a/A.txt b/A.txt
--- a/A.txt
+++ b/A.txt
@@ -1,4 +1,4 @@
-A1 mod
+A1 new
 A2
 A3
 A4
`

	patchB := `diff --git a/B.txt b/B.txt
--- a/B.txt
+++ b/B.txt
@@ -1,6 +1,6 @@
-B1
-B2
-B3
+B1 new
+B2 new
+B3 new
 B4
 B5
 B6
`

	diffResult := &BranchDiffResult{
		MergeBase: baseHash,
		Files: []FileDiff{
			{Path: "A.txt", Patch: patchA, Status: "modified"},
			{Path: "B.txt", Patch: patchB, Status: "modified"},
		},
	}

	reviewers, err := SuggestReviewers(dir, diffResult, "Current Author", 3)
	require.NoError(t, err)

	require.Len(t, reviewers, 2)

	// Author Recent should be first because 2 hours ago > 2 months ago, even though Old changed 3 lines and Recent changed 1
	assert.Equal(t, "Author Recent", reviewers[0].Name)
	assert.ElementsMatch(t, []string{"A.txt"}, reviewers[0].Files)

	assert.Equal(t, "Author Old", reviewers[1].Name)
	assert.ElementsMatch(t, []string{"B.txt"}, reviewers[1].Files)
}
