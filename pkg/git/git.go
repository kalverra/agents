// Package git provides helpers for interacting with local git repositories.
package git

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
)

// RepoInfo holds the parsed owner, repo name, and current branch of a git repository.
type RepoInfo struct {
	Owner  string
	Name   string
	Branch string
}

var sshRemoteRe = regexp.MustCompile(`^[\w.-]+@[\w.-]+:([\w.-]+)/([\w.-]+?)(?:\.git)?$`)

// RepoRoot returns the absolute path of the top-level directory of the git repository at dir.
func RepoRoot(dir string) (string, error) {
	return gitOutput(dir, "rev-parse", "--show-toplevel")
}

// DetectRepo inspects the git repository at dir and returns its owner, name, and current branch.
func DetectRepo(dir string) (*RepoInfo, error) {
	if err := runGit(dir, "rev-parse", "--show-toplevel"); err != nil {
		return nil, fmt.Errorf("not a git repository (or any parent): %w", err)
	}

	remote, err := gitOutput(dir, "remote", "get-url", "origin")
	if err != nil {
		return nil, fmt.Errorf("no 'origin' remote configured: %w", err)
	}

	owner, name, err := parseRemoteURL(remote)
	if err != nil {
		return nil, err
	}

	branch, err := gitOutput(dir, "branch", "--show-current")
	if err != nil {
		return nil, fmt.Errorf("could not determine current branch: %w", err)
	}
	if branch == "" {
		return nil, fmt.Errorf("HEAD is detached; check out a branch first")
	}

	return &RepoInfo{Owner: owner, Name: name, Branch: branch}, nil
}

func parseRemoteURL(remote string) (owner, name string, err error) {
	remote = strings.TrimSpace(remote)

	if m := sshRemoteRe.FindStringSubmatch(remote); m != nil {
		return m[1], m[2], nil
	}

	u, parseErr := url.Parse(remote)
	if parseErr == nil && u.Host != "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 {
			repo := strings.TrimSuffix(parts[1], ".git")
			return parts[0], repo, nil
		}
	}

	return "", "", fmt.Errorf("cannot parse remote URL %q into owner/repo", remote)
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // git binary is hardcoded, not user-controlled
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...) //nolint:gosec // git binary is hardcoded, not user-controlled
	cmd.Dir = dir
	return cmd.Run()
}

// ReadFileSnippet reads a file from the worktree or HEAD and returns a formatted snippet around targetLine.
func ReadFileSnippet(dir, path string, targetLine, contextLines int) string {
	out, err := gitOutput(dir, "show", "HEAD:"+path)
	if err != nil {
		return ""
	}
	lines := strings.Split(out, "\n")
	start := targetLine - 1 - contextLines
	end := targetLine - 1 + contextLines
	if start < 0 {
		start = 0
	}
	if end >= len(lines) {
		end = len(lines) - 1
	}
	if start > end {
		return ""
	}

	var b strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i+1, lines[i])
	}
	return strings.TrimRight(b.String(), "\n")
}

// BranchCommitMessages returns the commit messages on the current branch that are ahead of baseBranch.
// If baseBranch is empty or HEAD is on baseBranch, it returns nil, nil.
func BranchCommitMessages(dir, baseBranch string) ([]string, error) {
	baseBranch = strings.TrimSpace(baseBranch)
	if baseBranch == "" {
		return nil, nil
	}

	currentBranch, err := gitOutput(dir, "branch", "--show-current")
	if err == nil && currentBranch != "" && currentBranch == baseBranch {
		return nil, nil
	}

	out, err := gitOutput(dir, "log", baseBranch+"..HEAD", "--format=%B%x00")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	raw := strings.Split(out, "\x00")
	messages := make([]string, 0, len(raw))
	for _, m := range raw {
		m = strings.TrimSpace(m)
		if m != "" {
			messages = append(messages, m)
		}
	}
	return messages, nil
}

// RecentCommitMessages returns the commit messages for the latest n commits on HEAD in reverse chronological order.
// If n <= 0, it returns nil, nil.
func RecentCommitMessages(dir string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}

	out, err := gitOutput(dir, "log", fmt.Sprintf("-n%d", n), "--format=%B%x00")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	raw := strings.Split(out, "\x00")
	messages := make([]string, 0, len(raw))
	for _, m := range raw {
		m = strings.TrimSpace(m)
		if m != "" {
			messages = append(messages, m)
		}
	}
	return messages, nil
}
