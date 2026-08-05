package git

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	//nolint:gosec // test helper with controlled args
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.NoError(t, err, "git %v failed: %s", args, stderr.String())
	return strings.TrimSpace(stdout.String())
}

func initTestRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	runGitCmd(t, dir, "init")
	runGitCmd(t, dir, "symbolic-ref", "HEAD", "refs/heads/master")
	runGitCmd(t, dir, "config", "user.name", "Test Author")
	runGitCmd(t, dir, "config", "user.email", "test@example.com")
	runGitCmd(t, dir, "config", "commit.gpgsign", "false")

	return dir
}

func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()

	full := filepath.Join(dir, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

func addAndCommit(t *testing.T, dir, path, content, message string) string {
	t.Helper()
	return addAndCommitBytes(t, dir, path, []byte(content), message)
}

func addAndCommitBytes(
	t *testing.T,
	dir, path string,
	content []byte,
	message string,
) string {
	t.Helper()

	full := filepath.Join(dir, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, content, 0o600))

	runGitCmd(t, dir, "add", path)
	runGitCmd(t, dir, "commit", "--no-verify", "-m", message)

	return runGitCmd(t, dir, "rev-parse", "HEAD")
}

func detachHEAD(t *testing.T, dir, hash string) {
	t.Helper()
	runGitCmd(t, dir, "checkout", hash)
}

func checkoutBranch(t *testing.T, dir, name string, create bool) {
	t.Helper()
	if create {
		runGitCmd(t, dir, "checkout", "-b", name)
	} else {
		runGitCmd(t, dir, "checkout", name)
	}
}

func renameDefaultBranch(t *testing.T, dir, name string) string {
	t.Helper()
	runGitCmd(t, dir, "branch", "-m", name)
	return runGitCmd(t, dir, "rev-parse", "HEAD")
}

func setOriginDefault(t *testing.T, dir, branch, hash string) {
	t.Helper()
	runGitCmd(t, dir, "update-ref", "refs/remotes/origin/"+branch, hash)
	runGitCmd(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+branch)
}
