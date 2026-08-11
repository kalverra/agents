package skills_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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
	assert.Contains(t, string(prepContent), "/dev/tty")

	postPath := filepath.Join(tmpHome, ".config", "git", "hooks", "commit-msg")
	postContent, err := os.ReadFile(postPath) //nolint:gosec
	require.NoError(t, err)
	assert.Contains(t, string(postContent), "--finalize-eval")
}

func TestResolveEvalDir(t *testing.T) {
	t.Parallel()

	dir, err := skills.ResolveEvalDir("/custom/eval/path")
	require.NoError(t, err)
	assert.Equal(t, "/custom/eval/path", dir)

	dir, err = skills.ResolveEvalDir("")
	require.NoError(t, err)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".agents", "eval", "live_feedback"), dir)
}

func TestInstallHook_Delegation(t *testing.T) {
	tests := []struct {
		name          string
		hookFile      string
		lefthookYML   string
		preCommitCfg  bool
		wantLefthook  bool
		wantPreCommit bool
	}{
		{
			name:         "prepare-commit-msg: lefthook skips when hook not in config",
			hookFile:     "prepare-commit-msg",
			lefthookYML:  "pre-commit:\n  commands:\n    test:\n      run: echo\n",
			wantLefthook: false,
		},
		{
			name:         "prepare-commit-msg: lefthook runs when hook in config",
			hookFile:     "prepare-commit-msg",
			lefthookYML:  "prepare-commit-msg:\n  commands:\n    test:\n      run: echo\n",
			wantLefthook: true,
		},
		{
			name:          "prepare-commit-msg: pre-commit runs when config present",
			hookFile:      "prepare-commit-msg",
			preCommitCfg:  true,
			wantPreCommit: true,
		},
		{
			name:         "commit-msg: lefthook skips when hook not in config",
			hookFile:     "commit-msg",
			lefthookYML:  "pre-commit:\n  commands:\n    test:\n      run: echo\n",
			wantLefthook: false,
		},
		{
			name:         "commit-msg: lefthook runs when hook in config",
			hookFile:     "commit-msg",
			lefthookYML:  "commit-msg:\n  commands:\n    test:\n      run: echo\n",
			wantLefthook: true,
		},
		{
			name:          "commit-msg: pre-commit runs when config present",
			hookFile:      "commit-msg",
			preCommitCfg:  true,
			wantPreCommit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpHome := t.TempDir()
			t.Setenv("HOME", tmpHome)

			tmpBin := t.TempDir()
			lefthookMarker := filepath.Join(t.TempDir(), "lefthook_called")
			preCommitMarker := filepath.Join(t.TempDir(), "precommit_called")

			require.NoError(t, os.WriteFile(filepath.Join(tmpBin, "lefthook"), //nolint:gosec
				fmt.Appendf(nil, "#!/bin/sh\ntouch %q\nexit 0\n", lefthookMarker), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(tmpBin, "pre-commit"), //nolint:gosec
				fmt.Appendf(nil, "#!/bin/sh\ntouch %q\nexit 0\n", preCommitMarker), 0755))

			t.Setenv("PATH", tmpBin+":"+os.Getenv("PATH"))

			cmd := skills.CommitMsgCmd
			cmd.SetArgs([]string{"--install-hook", "--global"})
			require.NoError(t, cmd.ExecuteContext(context.Background()))

			hookPath := filepath.Join(tmpHome, ".config", "git", "hooks", tt.hookFile)
			content, err := os.ReadFile(hookPath) //nolint:gosec
			require.NoError(t, err)

			exe, err := os.Executable()
			require.NoError(t, err)
			content = bytes.ReplaceAll(content, fmt.Appendf(nil, "%q", exe), []byte("true"))
			require.NoError(t, os.WriteFile(hookPath, content, 0755)) //nolint:gosec

			repoDir := t.TempDir()
			runGitCmd(t, repoDir, "init")
			if tt.lefthookYML != "" {
				require.NoError(t, os.WriteFile(filepath.Join(repoDir, "lefthook.yml"), //nolint:gosec
					[]byte(tt.lefthookYML), 0644))
			}
			if tt.preCommitCfg {
				require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".pre-commit-config.yaml"), //nolint:gosec
					[]byte("repos: []\n"), 0644))
			}

			msgFile := filepath.Join(repoDir, "COMMIT_EDITMSG")
			require.NoError(t, os.WriteFile(msgFile, []byte("test"), 0600))

			hookCmd := exec.Command("sh", hookPath, msgFile, "message", "") //nolint:gosec
			hookCmd.Dir = repoDir
			require.NoError(t, hookCmd.Run(), "hook script should exit 0")

			_, lefthookErr := os.Stat(lefthookMarker)
			if tt.wantLefthook {
				require.NoError(t, lefthookErr, "fake lefthook should have been called")
			} else {
				require.True(t, os.IsNotExist(lefthookErr), "fake lefthook should NOT have been called")
			}

			_, preCommitErr := os.Stat(preCommitMarker)
			if tt.wantPreCommit {
				require.NoError(t, preCommitErr, "fake pre-commit should have been called")
			} else {
				require.True(t, os.IsNotExist(preCommitErr), "fake pre-commit should NOT have been called")
			}
		})
	}
}

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec
	cmd.Dir = dir
	require.NoError(t, cmd.Run())
}
