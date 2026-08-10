package skills_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/cmd/skills"
)

func TestCommitMsgCmd_InstallHookGlobal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cmd := skills.CommitMsgCmd
	cmd.SetArgs([]string{"--install-hook", "--global"})

	err := cmd.ExecuteContext(context.Background())
	require.NoError(t, err)

	hookPath := filepath.Join(tmpHome, ".config", "git", "hooks", "prepare-commit-msg")
	info, err := os.Stat(hookPath)
	require.NoError(t, err)
	assert.NotEqual(t, 0, info.Mode()&0111, "hook script should be executable")

	content, err := os.ReadFile(hookPath) //nolint:gosec
	require.NoError(t, err)
	assert.Contains(t, string(content), "commit-msg")
	assert.Contains(t, string(content), "command -v lefthook")
	assert.Contains(t, string(content), "lefthook run prepare-commit-msg")
}

func TestCommitMsgCmd_InstallHookWithEval(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cmd := skills.CommitMsgCmd
	cmd.SetArgs([]string{"--install-hook", "--global", "--eval"})

	err := cmd.ExecuteContext(context.Background())
	require.NoError(t, err)

	prepPath := filepath.Join(tmpHome, ".config", "git", "hooks", "prepare-commit-msg")
	prepContent, err := os.ReadFile(prepPath) //nolint:gosec
	require.NoError(t, err)
	assert.Contains(t, string(prepContent), "--eval")

	postPath := filepath.Join(tmpHome, ".config", "git", "hooks", "commit-msg")
	postContent, err := os.ReadFile(postPath) //nolint:gosec
	require.NoError(t, err)
	assert.Contains(t, string(postContent), "--finalize-eval")
}
