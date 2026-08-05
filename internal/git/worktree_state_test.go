package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorktreeState(t *testing.T) {
	t.Parallel()

	t.Run("clean repo", func(t *testing.T) {
		t.Parallel()
		dir := initTestRepo(t)
		addAndCommit(t, dir, "file.txt", "hello", "initial commit")

		dirty, untracked, err := WorktreeState(dir)
		require.NoError(t, err)
		assert.Equal(t, 0, dirty)
		assert.Equal(t, 0, untracked)
	})

	t.Run("modified tracked file", func(t *testing.T) {
		t.Parallel()
		dir := initTestRepo(t)
		addAndCommit(t, dir, "file.txt", "hello", "initial commit")
		writeFile(t, dir, "file.txt", "hello world")

		dirty, untracked, err := WorktreeState(dir)
		require.NoError(t, err)
		assert.Equal(t, 1, dirty)
		assert.Equal(t, 0, untracked)
	})

	t.Run("untracked file", func(t *testing.T) {
		t.Parallel()
		dir := initTestRepo(t)
		addAndCommit(t, dir, "file.txt", "hello", "initial commit")
		writeFile(t, dir, "new.txt", "untracked content")

		dirty, untracked, err := WorktreeState(dir)
		require.NoError(t, err)
		assert.Equal(t, 0, dirty)
		assert.Equal(t, 1, untracked)
	})

	t.Run("modified tracked and untracked files", func(t *testing.T) {
		t.Parallel()
		dir := initTestRepo(t)
		addAndCommit(t, dir, "file1.txt", "hello", "initial commit")
		addAndCommit(t, dir, "file2.txt", "world", "second commit")
		writeFile(t, dir, "file1.txt", "hello modified")
		writeFile(t, dir, "new1.txt", "untracked 1")
		writeFile(t, dir, "new2.txt", "untracked 2")

		dirty, untracked, err := WorktreeState(dir)
		require.NoError(t, err)
		assert.Equal(t, 1, dirty)
		assert.Equal(t, 2, untracked)
	})

	t.Run("not a git repo", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, _, err := WorktreeState(dir)
		require.Error(t, err)
	})
}
