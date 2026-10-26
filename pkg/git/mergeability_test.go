package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckMergeability_UpToDate(t *testing.T) {
	t.Parallel()

	dir := initTestRepo(t)
	addAndCommit(t, dir, "file.txt", "initial content\n", "initial commit")

	m, err := CheckMergeability(dir, "master", "master")
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.Equal(t, "master", m.BaseBranch)
	assert.Equal(t, "master", m.HeadBranch)
	assert.Equal(t, 0, m.BehindCount)
	assert.Equal(t, 0, m.AheadCount)
	assert.False(t, m.HasConflict)
	assert.Empty(t, m.Conflicts)
}

func TestCheckMergeability_HeadAhead(t *testing.T) {
	t.Parallel()

	dir := initTestRepo(t)
	addAndCommit(t, dir, "file.txt", "initial content\n", "initial commit")

	runGitCmd(t, dir, "checkout", "-b", "feature")
	addAndCommit(t, dir, "file2.txt", "feature content\n", "feat commit")

	m, err := CheckMergeability(dir, "master", "feature")
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.Equal(t, 0, m.BehindCount)
	assert.Equal(t, 1, m.AheadCount)
	assert.False(t, m.HasConflict)
	assert.Empty(t, m.Conflicts)
}

func TestCheckMergeability_CleanBehind(t *testing.T) {
	t.Parallel()

	dir := initTestRepo(t)
	addAndCommit(t, dir, "file1.txt", "base content\n", "initial commit")

	runGitCmd(t, dir, "checkout", "-b", "feature")
	addAndCommit(t, dir, "file2.txt", "feature content\n", "feat commit")

	runGitCmd(t, dir, "checkout", "master")
	addAndCommit(t, dir, "file3.txt", "master content\n", "master commit")

	m, err := CheckMergeability(dir, "master", "feature")
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.Equal(t, 1, m.BehindCount)
	assert.Equal(t, 1, m.AheadCount)
	assert.False(t, m.HasConflict)
	assert.Empty(t, m.Conflicts)
}

func TestCheckMergeability_Conflict(t *testing.T) {
	t.Parallel()

	dir := initTestRepo(t)
	addAndCommit(t, dir, "file.txt", "line 1\n", "initial commit")

	runGitCmd(t, dir, "checkout", "-b", "feature")
	addAndCommit(t, dir, "file.txt", "feature change\n", "feat commit")

	runGitCmd(t, dir, "checkout", "master")
	addAndCommit(t, dir, "file.txt", "master conflicting change\n", "master commit")

	m, err := CheckMergeability(dir, "master", "feature")
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.Equal(t, 1, m.BehindCount)
	assert.Equal(t, 1, m.AheadCount)
	assert.True(t, m.HasConflict)
	assert.Contains(t, m.Conflicts, "file.txt")
}

func TestCheckMergeability_InvalidRefs(t *testing.T) {
	t.Parallel()

	dir := initTestRepo(t)
	addAndCommit(t, dir, "file.txt", "content\n", "init")

	_, err := CheckMergeability(dir, "nonexistent-base", "master")
	assert.Error(t, err)
}
