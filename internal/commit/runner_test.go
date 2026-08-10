package commit_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/commit"
)

type mockLLM struct {
	response string
	err      error
	lastArg  string
}

func (m *mockLLM) Run(_ context.Context, _, prompt string) (string, error) {
	m.lastArg = prompt
	return m.response, m.err
}

func TestRunner_Run_ExplicitMessageSkipped(t *testing.T) {
	t.Parallel()

	r := &commit.Runner{
		LLM: &mockLLM{response: "feat: mock msg"},
	}

	msgFile := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	require.NoError(t, os.WriteFile(msgFile, []byte("explicit msg"), 0600))

	res, err := r.Run(context.Background(), commit.Options{
		CommitMsgFile: msgFile,
		CommitSource:  "message",
	})
	require.NoError(t, err)
	assert.True(t, res.Skipped)
	assert.Equal(t, "explicit msg", res.Message)
}

func TestRunner_Run_GeneratesMessage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// create git repo with staged file
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello"), 0600))
	runGit(t, dir, "add", "foo.txt")

	msgFile := filepath.Join(dir, ".git", "COMMIT_EDITMSG")

	mock := &mockLLM{response: "feat(foo): add foo.txt"}
	r := &commit.Runner{
		LLM: mock,
	}

	res, err := r.Run(context.Background(), commit.Options{
		Dir:           dir,
		CommitMsgFile: msgFile,
	})
	require.NoError(t, err)
	assert.False(t, res.Skipped)
	assert.Equal(t, "feat(foo): add foo.txt", res.Message)

	saved, err := os.ReadFile(msgFile) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "feat(foo): add foo.txt", string(saved))
	assert.Contains(t, mock.lastArg, "<file path=\"foo.txt\"")
}

func TestRunner_Run_PreservesGitComments(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello"), 0600))
	runGit(t, dir, "add", "foo.txt")

	msgFile := filepath.Join(dir, ".git", "COMMIT_EDITMSG")
	gitComments := "# Please enter the commit message for your changes.\n# On branch main\n# Changes to be committed:\n#\tmodified:   foo.txt\n"
	require.NoError(t, os.WriteFile(msgFile, []byte(gitComments), 0600))

	mock := &mockLLM{response: "feat(foo): add foo.txt"}
	r := &commit.Runner{
		LLM: mock,
	}

	res, err := r.Run(context.Background(), commit.Options{
		Dir:           dir,
		CommitMsgFile: msgFile,
	})
	require.NoError(t, err)
	assert.Equal(t, "feat(foo): add foo.txt", res.Message)

	saved, err := os.ReadFile(msgFile) //nolint:gosec
	require.NoError(t, err)
	expected := "feat(foo): add foo.txt\n\n" + gitComments
	assert.Equal(t, expected, string(saved))
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := os.Getenv("GIT_CMD")
	if cmd == "" {
		cmd = "git"
	}
	c := os.Getenv("SHELL")
	_ = c
	// exec git
	e := execCommand(dir, cmd, args...)
	require.NoError(t, e)
}

func execCommand(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...) //nolint:gosec
	cmd.Dir = dir
	return cmd.Run()
}
