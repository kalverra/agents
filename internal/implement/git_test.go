package implement

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitError(t *testing.T) {
	t.Parallel()
	inner := errors.New("exit status 1")
	ge := &GitError{
		Args:   []string{"commit", "-m", "test"},
		Err:    inner,
		Output: "check-yaml...Failed\nexit code 1\n",
	}

	assert.Equal(t, inner, ge.Unwrap())
	assert.Contains(t, ge.Error(), "git commit -m test: exit status 1")
	assert.Contains(t, ge.Error(), "check-yaml...Failed")

	emptyOut := &GitError{
		Args: []string{"status"},
		Err:  inner,
	}
	assert.Equal(t, "git status: exit status 1", emptyOut.Error())
}

func TestExecGitterCommitAllOpts(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	// Init a git repo in tmp
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...) //nolint:gosec // test helper
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, string(out))
	}
	run("init")
	run("config", "user.name", "Flow Test")
	run("config", "user.email", "flow@test.local")
	run("config", "commit.gpgsign", "false")

	gitter := ExecGitter{}

	// Nothing to commit initially
	hash, err := gitter.CommitAllOpts(tmp, "initial empty", CommitOpts{})
	require.NoError(t, err)
	assert.Empty(t, hash)

	// Create a file
	err = os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("hello"), 0o600)
	require.NoError(t, err)

	// Commit with normal CommitAll
	hash, err = gitter.CommitAll(tmp, "feat: add file")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)

	// Add failing pre-commit hook in .git/hooks/pre-commit
	hookPath := filepath.Join(tmp, ".git", "hooks", "pre-commit")
	hookContent := "#!/bin/sh\necho 'hook stdout failure report'\nexit 1\n"
	err = os.WriteFile(hookPath, []byte(hookContent), 0o755) //nolint:gosec // executable hook script requires 0o755
	require.NoError(t, err)

	// Modify file
	err = os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("hello 2"), 0o600)
	require.NoError(t, err)

	// Normal commit should fail and capture hook stdout
	_, err = gitter.CommitAllOpts(tmp, "failing commit", CommitOpts{})
	require.Error(t, err)
	var gitErr *GitError
	require.ErrorAs(t, err, &gitErr, "error must wrap GitError")
	assert.Contains(t, gitErr.Output, "hook stdout failure report")

	// Commit with NoVerify should succeed and bypass hook
	noVerifyHash, err := gitter.CommitAllOpts(tmp, "bypassed commit", CommitOpts{NoVerify: true})
	require.NoError(t, err)
	assert.NotEmpty(t, noVerifyHash)
}

func TestExecGitterCommitAllNoGPGSign(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...) //nolint:gosec // test helper
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, string(out))
	}
	run("init")
	run("config", "user.name", "Flow Test")
	run("config", "user.email", "flow@test.local")
	// Enable gpgsign with an invalid gpg program so commit fails if gpg is invoked
	run("config", "commit.gpgsign", "true")
	run("config", "gpg.program", "false")

	gitter := ExecGitter{}
	err := os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("hello"), 0o600)
	require.NoError(t, err)

	// Should succeed because CommitAllOpts passes --no-gpg-sign
	hash, err := gitter.CommitAll(tmp, "commit with no gpg sign")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

func TestExecGitterWorktreeDiff(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...) //nolint:gosec // test helper
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, string(out))
	}
	run("init")
	run("config", "user.name", "Flow Test")
	run("config", "user.email", "flow@test.local")
	run("config", "commit.gpgsign", "false")

	gitter := ExecGitter{}
	err := os.WriteFile(filepath.Join(tmp, "base.txt"), []byte("initial\n"), 0o600)
	require.NoError(t, err)
	_, err = gitter.CommitAll(tmp, "initial commit")
	require.NoError(t, err)

	// Modify base.txt and create untracked new.txt
	err = os.WriteFile(filepath.Join(tmp, "base.txt"), []byte("initial\nmodified\n"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmp, "new.txt"), []byte("new file\n"), 0o600)
	require.NoError(t, err)

	diff, err := gitter.WorktreeDiff(tmp)
	require.NoError(t, err)
	assert.Contains(t, diff, "diff --git c/base.txt w/base.txt", "diff should contain modified file")
	assert.Contains(t, diff, "diff --git c/new.txt w/new.txt", "diff should contain untracked file")
}

func TestExecGitterCheckAttr(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...) //nolint:gosec // test helper
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, string(out))
	}
	run("init")
	run("config", "user.name", "Flow Test")
	run("config", "user.email", "flow@test.local")
	run("config", "commit.gpgsign", "false")

	attrContent := "*.pb.go linguist-generated\nbundle.bin -diff\n"
	err := os.WriteFile(filepath.Join(tmp, ".gitattributes"), []byte(attrContent), 0o600)
	require.NoError(t, err)

	gitter := ExecGitter{}
	attrs, err := gitter.CheckAttr(
		tmp,
		[]string{"diff", "linguist-generated"},
		[]string{"model.pb.go", "bundle.bin", "main.go"},
	)
	require.NoError(t, err)
	require.NotNil(t, attrs)

	assert.Equal(t, "set", attrs["model.pb.go"]["linguist-generated"])
	assert.Equal(t, "unset", attrs["bundle.bin"]["diff"])
	assert.Equal(t, "unspecified", attrs["main.go"]["linguist-generated"])
}

func TestExecGitterDirtyIgnoresTicketFiles(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...) //nolint:gosec // test helper
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, string(out))
	}
	run("init")
	run("config", "user.name", "Flow Test")
	run("config", "user.email", "flow@test.local")
	run("config", "commit.gpgsign", "false")
	run("config", "core.excludesFile", "/dev/null")

	// Commit an initial file so we have HEAD
	err := os.WriteFile(filepath.Join(tmp, "init.txt"), []byte("init"), 0o600)
	require.NoError(t, err)
	run("add", ".")
	run("commit", "-m", "init")

	gitter := ExecGitter{}

	// Clean worktree
	dirty, err := gitter.Dirty(tmp)
	require.NoError(t, err)
	assert.False(t, dirty)

	// Untracked .ticket.md file should still report clean
	err = os.WriteFile(filepath.Join(tmp, "DX-1234.ticket.md"), []byte("<ticket>context</ticket>"), 0o600)
	require.NoError(t, err)
	dirty, err = gitter.Dirty(tmp)
	require.NoError(t, err)
	assert.False(t, dirty)

	// Modified/untracked other file should report dirty
	err = os.WriteFile(filepath.Join(tmp, "other.txt"), []byte("other"), 0o600)
	require.NoError(t, err)
	dirty, err = gitter.Dirty(tmp)
	require.NoError(t, err)
	assert.True(t, dirty)
}

func TestExecGitterCommitAllExcludesTicketFiles(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...) //nolint:gosec // test helper
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, string(out))
	}
	run("init")
	run("config", "user.name", "Flow Test")
	run("config", "user.email", "flow@test.local")
	run("config", "commit.gpgsign", "false")
	run("config", "core.excludesFile", "/dev/null")

	// Create a code file and an untracked ticket file
	err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmp, "DX-1234.ticket.md"), []byte("<ticket>DX-1234</ticket>"), 0o600)
	require.NoError(t, err)

	gitter := ExecGitter{}
	hash, err := gitter.CommitAll(tmp, "initial commit")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)

	// Check committed files in HEAD commit
	cmd := exec.Command("git", "-C", tmp, "ls-tree", "-r", "--name-only", "HEAD") //nolint:gosec // test helper
	out, err := cmd.CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "main.go")
	assert.NotContains(t, string(out), "DX-1234.ticket.md")

	// Untracked ticket file should still exist and be untracked
	cmd = exec.Command("git", "-C", tmp, "status", "--porcelain") //nolint:gosec // test helper
	out, err = cmd.CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "DX-1234.ticket.md")
}
