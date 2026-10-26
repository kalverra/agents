package git

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Mergeability contains merge conflict and commit divergence information between branches.
type Mergeability struct {
	BaseBranch  string   `json:"base_branch"`
	HeadBranch  string   `json:"head_branch"`
	BehindCount int      `json:"behind_count"`
	AheadCount  int      `json:"ahead_count"`
	HasConflict bool     `json:"has_conflict"`
	Conflicts   []string `json:"conflicts,omitempty"`
}

// CheckMergeability checks if headRef can be cleanly merged with baseRef in dir.
func CheckMergeability(dir, baseRef, headRef string) (*Mergeability, error) {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(baseRef) == "" || strings.TrimSpace(headRef) == "" {
		return nil, errors.New("dir, baseRef, and headRef are required")
	}

	resolvedBase := resolveRef(dir, baseRef)
	if resolvedBase == "" {
		return nil, fmt.Errorf("cannot resolve base ref %q", baseRef)
	}

	resolvedHead := resolveRef(dir, headRef)
	if resolvedHead == "" {
		return nil, fmt.Errorf("cannot resolve head ref %q", headRef)
	}

	countsOut, err := gitOutput(dir, "rev-list", "--left-right", "--count", resolvedBase+"..."+resolvedHead)
	if err != nil {
		return nil, fmt.Errorf("rev-list ahead/behind: %w", err)
	}

	fields := strings.Fields(countsOut)
	if len(fields) < 2 {
		return nil, fmt.Errorf("unexpected rev-list output: %q", countsOut)
	}

	behind, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, fmt.Errorf("parse behind count: %w", err)
	}
	ahead, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, fmt.Errorf("parse ahead count: %w", err)
	}

	result := &Mergeability{
		BaseBranch:  baseRef,
		HeadBranch:  headRef,
		BehindCount: behind,
		AheadCount:  ahead,
	}

	if behind == 0 {
		// Head has all commits from base, no divergence
		return result, nil
	}

	// Behind base: run git merge-tree to test if merge would produce conflicts
	//nolint:gosec // controlled arguments inside repository
	cmd := exec.Command(
		"git",
		"merge-tree",
		"--write-tree",
		resolvedBase,
		resolvedHead,
	) //nolint:gosec // controlled args
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		// Clean merge without conflicts
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		// Merge conflict occurred
		result.HasConflict = true
		result.Conflicts = parseConflictedFiles(string(out))
		return result, nil
	}

	return nil, fmt.Errorf("merge-tree failed: %w: %s", err, strings.TrimSpace(string(out)))
}

func resolveRef(dir, ref string) string {
	if _, err := gitOutput(dir, "rev-parse", "--verify", ref); err == nil {
		return ref
	}
	originRef := "origin/" + ref
	if _, err := gitOutput(dir, "rev-parse", "--verify", originRef); err == nil {
		return originRef
	}
	return ""
}

func parseConflictedFiles(output string) []string {
	seen := make(map[string]struct{})
	var conflicts []string

	lines := strings.SplitSeq(output, "\n")
	for line := range lines {
		// Look for index conflict records: "<mode> <hash> <stage>\t<path>"
		if before, after, ok := strings.Cut(line, "\t"); ok {
			prefix := before
			fields := strings.Fields(prefix)
			if len(fields) == 3 {
				stage := fields[2]
				if stage == "1" || stage == "2" || stage == "3" {
					path := strings.TrimSpace(after)
					if path != "" {
						if _, ok := seen[path]; !ok {
							seen[path] = struct{}{}
							conflicts = append(conflicts, path)
						}
					}
				}
			}
		}

		// Also fallback check for CONFLICT line messages
		if strings.HasPrefix(line, "CONFLICT (") {
			if _, after, ok := strings.CutLast(line, " in "); ok {
				path := strings.TrimRight(strings.TrimSpace(after), ".")
				if path != "" {
					if _, ok := seen[path]; !ok {
						seen[path] = struct{}{}
						conflicts = append(conflicts, path)
					}
				}
			}
		}
	}

	sort.Strings(conflicts)
	return conflicts
}
