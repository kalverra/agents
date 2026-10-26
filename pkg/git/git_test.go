package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitOutputIncludesStderr(t *testing.T) {
	t.Parallel()

	// Run git in a non-repo directory — git writes a useful message to stderr.
	dir := t.TempDir()
	_, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository", "error should include git's stderr, not just exit code")
}

func TestBranchCommitMessages(t *testing.T) {
	t.Parallel()

	t.Run("empty base branch returns nil", func(t *testing.T) {
		t.Parallel()
		dir := initTestRepo(t)
		addAndCommit(t, dir, "file.txt", "content", "initial commit")

		msgs, err := BranchCommitMessages(dir, "")
		require.NoError(t, err)
		assert.Empty(t, msgs)
	})

	t.Run("HEAD is on base branch returns nil", func(t *testing.T) {
		t.Parallel()
		dir := initTestRepo(t)
		addAndCommit(t, dir, "file.txt", "content", "initial commit")
		renameDefaultBranch(t, dir, "main")

		msgs, err := BranchCommitMessages(dir, "main")
		require.NoError(t, err)
		assert.Empty(t, msgs)
	})

	t.Run("returns commits between base and HEAD", func(t *testing.T) {
		t.Parallel()
		dir := initTestRepo(t)
		addAndCommit(t, dir, "README.md", "base\n", "initial commit")
		renameDefaultBranch(t, dir, "main")

		checkoutBranch(t, dir, "feature/TEST-1", true)
		addAndCommit(t, dir, "a.txt", "a\n", "feat: first commit on branch")
		addAndCommit(t, dir, "b.txt", "b\n", "fix: second commit on branch\n\nDetailed description here.")

		msgs, err := BranchCommitMessages(dir, "main")
		require.NoError(t, err)
		require.Len(t, msgs, 2)
		// git log outputs newest first
		assert.Equal(t, "fix: second commit on branch\n\nDetailed description here.", msgs[0])
		assert.Equal(t, "feat: first commit on branch", msgs[1])
	})

	t.Run("invalid base branch returns error", func(t *testing.T) {
		t.Parallel()
		dir := initTestRepo(t)
		addAndCommit(t, dir, "README.md", "base\n", "initial commit")

		_, err := BranchCommitMessages(dir, "nonexistent-branch")
		require.Error(t, err)
	})
}

func TestRecentCommitMessages(t *testing.T) {
	t.Parallel()

	t.Run("n <= 0 returns nil", func(t *testing.T) {
		t.Parallel()
		dir := initTestRepo(t)
		addAndCommit(t, dir, "file.txt", "content", "initial commit")

		msgs, err := RecentCommitMessages(dir, 0)
		require.NoError(t, err)
		assert.Empty(t, msgs)

		msgs, err = RecentCommitMessages(dir, -1)
		require.NoError(t, err)
		assert.Empty(t, msgs)
	})

	t.Run("returns latest n commits in reverse chronological order", func(t *testing.T) {
		t.Parallel()
		dir := initTestRepo(t)
		addAndCommit(t, dir, "a.txt", "a\n", "first commit")
		addAndCommit(t, dir, "b.txt", "b\n", "second commit")
		addAndCommit(t, dir, "c.txt", "c\n", "third commit\n\nDetailed body.")

		msgs, err := RecentCommitMessages(dir, 2)
		require.NoError(t, err)
		require.Len(t, msgs, 2)
		assert.Equal(t, "third commit\n\nDetailed body.", msgs[0])
		assert.Equal(t, "second commit", msgs[1])
	})

	t.Run("returns all commits if total less than n", func(t *testing.T) {
		t.Parallel()
		dir := initTestRepo(t)
		addAndCommit(t, dir, "a.txt", "a\n", "first commit")

		msgs, err := RecentCommitMessages(dir, 10)
		require.NoError(t, err)
		require.Len(t, msgs, 1)
		assert.Equal(t, "first commit", msgs[0])
	})

	t.Run("non-git directory returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		_, err := RecentCommitMessages(dir, 5)
		require.Error(t, err)
	})
}
