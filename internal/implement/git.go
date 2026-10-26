package implement

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitError captures a failed git execution, retaining the full combined
// stdout and stderr output alongside the underlying exec error.
type GitError struct {
	Args   []string
	Err    error
	Output string
}

func (e *GitError) Error() string {
	out := strings.TrimSpace(e.Output)
	if out == "" {
		return fmt.Sprintf("git %s: %v", strings.Join(e.Args, " "), e.Err)
	}
	return fmt.Sprintf("git %s: %v (output: %s)", strings.Join(e.Args, " "), e.Err, out)
}

// Unwrap returns the underlying execution error.
func (e *GitError) Unwrap() error {
	return e.Err
}

// CommitOpts configures git commit behavior.
type CommitOpts struct {
	NoVerify bool
}

// Gitter abstracts the git operations the orchestrator performs: session
// baselines, per-step commits, whole-diff generation, and tree archival for
// the human diff view. Tests inject a fake.
type Gitter interface {
	// Head returns the current HEAD commit hash of the repo at dir.
	Head(dir string) (string, error)
	// Dirty reports whether the worktree has any uncommitted (tracked or
	// untracked, non-ignored) changes.
	Dirty(dir string) (bool, error)
	// CommitAll stages every change (tracked + untracked) in dir and commits
	// with message. Returns the commit hash, or "" when there was nothing
	// to commit.
	CommitAll(dir, message string) (string, error)
	// CommitAllOpts stages and commits using the specified CommitOpts.
	CommitAllOpts(dir, message string, opts CommitOpts) (string, error)
	// Diff returns the unified diff between two commits in dir's repo.
	Diff(dir, base, head string) (string, error)
	// WorktreeDiff returns the unified diff of uncommitted changes (tracked + untracked)
	// in dir relative to HEAD.
	WorktreeDiff(dir string) (string, error)
	// CheckAttr queries git attributes for the given paths in dir.
	CheckAttr(dir string, attrs, paths []string) (map[string]map[string]string, error)
	// ArchiveTree extracts the full tracked tree of one commit into destDir.
	ArchiveTree(dir, sha, destDir string) error
}

// ExecGitter runs real git commands via os/exec.
type ExecGitter struct{}

// Head returns the current HEAD commit hash.
func (ExecGitter) Head(dir string) (string, error) {
	out, err := gitOut(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("reading HEAD (is %s a git repo?): %w", dir, err)
	}
	return strings.TrimSpace(out), nil
}

// Dirty reports whether the worktree has uncommitted changes,
// ignoring .ticket.md context files.
func (ExecGitter) Dirty(dir string) (bool, error) {
	out, err := gitOut(dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("checking worktree state (is %s a git repo?): %w", dir, err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			path := parts[len(parts)-1]
			path = strings.Trim(path, `"`)
			if strings.HasSuffix(path, ".ticket.md") {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}

// CommitAll stages everything and commits. Returns "" when the commit was a
// no-op (nothing staged).
func (e ExecGitter) CommitAll(dir, message string) (string, error) {
	return e.CommitAllOpts(dir, message, CommitOpts{})
}

// CommitAllOpts stages everything and commits with the specified CommitOpts.
func (ExecGitter) CommitAllOpts(dir, message string, opts CommitOpts) (string, error) {
	if _, err := gitOut(dir, "add", "-A", "--", ":(exclude)*.ticket.md"); err != nil {
		return "", fmt.Errorf("staging changes: %w", err)
	}
	// --quiet exits non-zero when there ARE staged changes; a real failure
	// surfaces on the commit itself.
	if _, err := gitOut(dir, "diff", "--cached", "--quiet"); err == nil {
		return "", nil
	}
	args := []string{"commit", "--no-gpg-sign", "-m", message}
	if opts.NoVerify {
		args = append(args, "--no-verify")
	}
	if _, err := gitOut(dir, args...); err != nil {
		return "", fmt.Errorf("committing %q: %w", message, err)
	}
	hash, err := gitOut(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("reading new commit hash: %w", err)
	}
	return strings.TrimSpace(hash), nil
}

// Diff returns the unified diff between two commits.
func (ExecGitter) Diff(dir, base, head string) (string, error) {
	out, err := gitOut(dir, "diff", base+"..."+head)
	if err != nil {
		return "", fmt.Errorf("diffing %s...%s: %w", base, head, err)
	}
	return out, nil
}

// WorktreeDiff returns the unified diff of uncommitted changes (tracked + untracked)
// in dir relative to HEAD.
func (ExecGitter) WorktreeDiff(dir string) (string, error) {
	// Add untracked files with intent-to-add so they appear in git diff HEAD.
	_, _ = gitOut(dir, "add", "-N", "-A", "--", ":(exclude)*.ticket.md")
	out, err := gitOut(dir, "diff", "HEAD")
	if err != nil {
		return "", fmt.Errorf("diffing worktree against HEAD: %w", err)
	}
	return out, nil
}

// CheckAttr queries git attributes for the given paths in dir.
func (ExecGitter) CheckAttr(dir string, attrs, paths []string) (map[string]map[string]string, error) {
	if len(paths) == 0 || len(attrs) == 0 {
		return nil, nil
	}
	args := append([]string{"check-attr"}, attrs...)
	args = append(args, "--")
	args = append(args, paths...)
	out, err := gitOut(dir, args...)
	if err != nil {
		return nil, fmt.Errorf("checking git attributes: %w", err)
	}
	result := make(map[string]map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ": ", 3)
		if len(parts) == 3 {
			p := cleanGitPath(parts[0])
			attr := parts[1]
			val := parts[2]
			if result[p] == nil {
				result[p] = make(map[string]string)
			}
			result[p][attr] = val
		}
	}
	return result, nil
}

// ArchiveTree extracts the tracked tree of one commit into destDir via
// `git archive` (tar.gz piped through Go's archive readers; no external tar).
func (ExecGitter) ArchiveTree(dir, sha, destDir string) error {
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", destDir, err)
	}
	cmd := exec.Command( //nolint:gosec // sha is orchestrator-controlled
		"git",
		"-C",
		dir,
		"archive",
		"--format=tar.gz",
		sha,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("archiving %s: %w", sha, err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("archiving %s: %w", sha, err)
	}
	extractErr := extractTarGz(stdout, destDir)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive %s: %w (stderr: %s)", sha, err, strings.TrimSpace(stderr.String()))
	}
	if extractErr != nil {
		return fmt.Errorf("extracting archive of %s: %w", sha, extractErr)
	}
	return nil
}

func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("reading gzip stream: %w", err)
	}
	defer gz.Close() //nolint:errcheck // read-only stream
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		target := filepath.Join(destDir, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			//nolint:gosec // archive paths are cleaned above
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil { //nolint:gosec // bounded by archive entry
				_ = f.Close() //nolint:errcheck // best-effort on error path
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			// Skip symlinks and other special entries: the diff view only
			// needs regular files.
		}
	}
}

func gitOut(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...) //nolint:gosec // fixed git subcommands
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := cmd.Run(); err != nil {
		return "", &GitError{
			Args:   args,
			Err:    err,
			Output: combined.String(),
		}
	}
	return combined.String(), nil
}
