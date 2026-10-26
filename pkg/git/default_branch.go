package git

import (
	"fmt"
	"strings"
)

var defaultBranchCandidates = []string{"main", "master"}

// DetectDefaultBranch resolves the repository default branch name.
func DetectDefaultBranch(dir string) (string, error) {
	if name, err := defaultBranchFromOriginHEAD(dir); err == nil && name != "" {
		return name, nil
	}

	if name, err := defaultBranchFromUpstream(dir); err == nil && name != "" {
		return name, nil
	}

	for _, candidate := range defaultBranchCandidates {
		if hasBranchRef(dir, "refs/heads/"+candidate) {
			return candidate, nil
		}
	}

	for _, candidate := range defaultBranchCandidates {
		if hasBranchRef(dir, "refs/remotes/origin/"+candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("could not detect default branch; set one with --base")
}

func defaultBranchFromOriginHEAD(dir string) (string, error) {
	ref, err := gitOutput(dir, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", err
	}

	// ref will look like "refs/remotes/origin/main"
	parts := strings.Split(ref, "/")
	if len(parts) < 4 || parts[0] != "refs" || parts[1] != "remotes" || parts[2] != "origin" {
		return "", fmt.Errorf("origin HEAD does not point to a remote branch")
	}

	return strings.Join(parts[3:], "/"), nil
}

func defaultBranchFromUpstream(dir string) (string, error) {
	// git rev-parse --abbrev-ref @{u} returns e.g. "origin/main"
	upstream, err := gitOutput(dir, "rev-parse", "--abbrev-ref", "@{u}")
	if err != nil {
		return "", err
	}

	parts := strings.Split(upstream, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("no upstream configured")
	}

	// return the branch name without the remote prefix
	return strings.Join(parts[1:], "/"), nil
}

func hasBranchRef(dir, refName string) bool {
	// git show-ref --verify --quiet refName returns exit code 0 if exists
	err := runGit(dir, "show-ref", "--verify", "--quiet", refName)
	return err == nil
}
