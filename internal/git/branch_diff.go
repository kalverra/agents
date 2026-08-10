package git

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// BranchDiffOptions configures BranchDiff.
type BranchDiffOptions struct {
	Base         string
	ContextLines int
}

// DiffStats summarizes patch size.
type DiffStats struct {
	FilesChanged int `json:"files_changed"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
}

// FileDiff is a per-file patch entry.
type FileDiff struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"`
}

// BranchDiffResult is the normalized branch diff payload.
type BranchDiffResult struct {
	RepoPath        string     `json:"repo_path"`
	CurrentBranch   string     `json:"current_branch"`
	BaseBranch      string     `json:"base_branch"`
	MergeBase       string     `json:"merge_base"`
	Head            string     `json:"head"`
	HasLocalChanges bool       `json:"has_local_changes"`
	Stats           DiffStats  `json:"stats"`
	Files           []FileDiff `json:"files"`
	Patch           string     `json:"patch"`
}

type fileChange struct {
	path   string
	status string
}

type fileStats struct {
	additions int
	deletions int
	binary    bool
}

type fileEntry struct {
	path      string
	status    string
	additions int
	deletions int
	patch     string
	binary    bool
}

// StagedDiff returns changes staged for commit (git diff --cached).
func StagedDiff(dir string, opts BranchDiffOptions) (*BranchDiffResult, error) {
	repoPath, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not a git repository (or any parent): %w", err)
	}

	currentBranch, _ := gitOutput(dir, "branch", "--show-current")
	headCommit, _ := gitOutput(dir, "rev-parse", "HEAD")

	nameStatusOut, err := gitOutput(dir, "diff", "--cached", "--name-status", "--no-renames", "-z")
	if err != nil {
		return nil, fmt.Errorf("reading staged diff name status: %w", err)
	}
	trackedChanges := parseNameStatus(nameStatusOut)

	numstatOut, err := gitOutput(dir, "diff", "--cached", "--numstat", "--no-renames", "-z")
	if err != nil {
		return nil, fmt.Errorf("reading staged diff numstat: %w", err)
	}
	trackedStats := parseNumstat(numstatOut)

	contextLines := opts.ContextLines
	if contextLines <= 0 {
		contextLines = 3
	}

	combinedDiff, err := gitOutput(dir, "diff", "--cached", "--no-renames", fmt.Sprintf("-U%d", contextLines))
	if err != nil {
		return nil, fmt.Errorf("running git diff --cached: %w", err)
	}
	filePatches := parseCombinedDiff(combinedDiff, trackedChanges)

	allFiles := make(map[string]fileEntry)
	for _, tc := range trackedChanges {
		stats := trackedStats[tc.path]
		patch := filePatches[tc.path]

		status := tc.status
		if stats.binary {
			status = "binary"
		}

		allFiles[tc.path] = fileEntry{
			path:      tc.path,
			status:    status,
			additions: stats.additions,
			deletions: stats.deletions,
			patch:     patch,
			binary:    stats.binary,
		}
	}

	var paths []string
	for path := range allFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	fileDiffs := make([]FileDiff, 0, len(paths))
	var stats DiffStats
	var patchBuf strings.Builder

	for _, path := range paths {
		fe := allFiles[path]
		fileDiffs = append(fileDiffs, FileDiff{
			Path:      fe.path,
			Status:    fe.status,
			Additions: fe.additions,
			Deletions: fe.deletions,
			Patch:     fe.patch,
		})

		if fe.patch != "" {
			patchBuf.WriteString(fe.patch)
			if !strings.HasSuffix(fe.patch, "\n") {
				patchBuf.WriteByte('\n')
			}
		}

		stats.FilesChanged++
		stats.Insertions += fe.additions
		stats.Deletions += fe.deletions
	}

	return &BranchDiffResult{
		RepoPath:        repoPath,
		CurrentBranch:   currentBranch,
		Head:            shortHash(headCommit),
		HasLocalChanges: stats.FilesChanged > 0,
		Stats:           stats,
		Files:           fileDiffs,
		Patch:           patchBuf.String(),
	}, nil
}

// BranchDiff returns all changes since merge-base with the base branch, including worktree changes.
func BranchDiff(dir string, opts BranchDiffOptions) (*BranchDiffResult, error) {
	repoPath, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not a git repository (or any parent): %w", err)
	}

	currentBranch, err := gitOutput(dir, "branch", "--show-current")
	if err != nil {
		return nil, fmt.Errorf("could not determine current branch: %w", err)
	}
	if currentBranch == "" {
		return nil, fmt.Errorf("HEAD is detached; check out a branch first")
	}

	baseBranch := opts.Base
	if baseBranch == "" {
		baseBranch, err = DetectDefaultBranch(dir)
		if err != nil {
			return nil, err
		}
	}

	headCommit, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("reading HEAD commit: %w", err)
	}

	diffBase := baseBranch
	if hasBranchRef(dir, "refs/remotes/origin/"+baseBranch) {
		diffBase = "refs/remotes/origin/" + baseBranch
	} else if upstream, err := gitOutput(dir, "rev-parse", "--abbrev-ref", baseBranch+"@{u}"); err == nil && upstream != "" {
		diffBase = upstream
	}

	mergeBase, err := gitOutput(dir, "merge-base", diffBase, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("finding merge base: %w", err)
	}

	nameStatusOut, err := gitOutput(dir, "diff", "--name-status", "--no-renames", "-z", mergeBase)
	if err != nil {
		return nil, fmt.Errorf("reading diff name status: %w", err)
	}
	trackedChanges := parseNameStatus(nameStatusOut)

	numstatOut, err := gitOutput(dir, "diff", "--numstat", "--no-renames", "-z", mergeBase)
	if err != nil {
		return nil, fmt.Errorf("reading diff numstat: %w", err)
	}
	trackedStats := parseNumstat(numstatOut)

	untrackedOut, err := gitOutput(dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("listing untracked files: %w", err)
	}
	untrackedFiles := parseUntracked(untrackedOut)

	contextLines := opts.ContextLines
	if contextLines <= 0 {
		contextLines = 3
	}

	combinedDiff, err := gitOutput(dir, "diff", "--no-renames", fmt.Sprintf("-U%d", contextLines), mergeBase)
	if err != nil {
		return nil, fmt.Errorf("running git diff: %w", err)
	}
	filePatches := parseCombinedDiff(combinedDiff, trackedChanges)

	allFiles := make(map[string]fileEntry)

	for _, tc := range trackedChanges {
		stats := trackedStats[tc.path]
		patch := filePatches[tc.path]

		status := tc.status
		if stats.binary {
			status = "binary"
		}

		allFiles[tc.path] = fileEntry{
			path:      tc.path,
			status:    status,
			additions: stats.additions,
			deletions: stats.deletions,
			patch:     patch,
			binary:    stats.binary,
		}
	}

	for _, path := range untrackedFiles {
		fe, err := readUntrackedFile(repoPath, path)
		if err != nil {
			continue // file may have vanished between ls-files and read
		}
		allFiles[path] = fe
	}

	var paths []string
	for path := range allFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	fileDiffs := make([]FileDiff, 0, len(paths))
	var stats DiffStats
	var patchBuf strings.Builder

	for _, path := range paths {
		fe := allFiles[path]
		fileDiffs = append(fileDiffs, FileDiff{
			Path:      fe.path,
			Status:    fe.status,
			Additions: fe.additions,
			Deletions: fe.deletions,
			Patch:     fe.patch,
		})

		if fe.patch != "" {
			patchBuf.WriteString(fe.patch)
			if !strings.HasSuffix(fe.patch, "\n") {
				patchBuf.WriteByte('\n')
			}
		}

		stats.FilesChanged++
		stats.Insertions += fe.additions
		stats.Deletions += fe.deletions
	}

	return &BranchDiffResult{
		RepoPath:        repoPath,
		CurrentBranch:   currentBranch,
		BaseBranch:      baseBranch,
		MergeBase:       shortHash(mergeBase),
		Head:            shortHash(headCommit),
		HasLocalChanges: headCommit == mergeBase && stats.FilesChanged > 0,
		Stats:           stats,
		Files:           fileDiffs,
		Patch:           patchBuf.String(),
	}, nil
}

func parseNameStatus(output string) []fileChange {
	var changes []fileChange
	if len(output) == 0 {
		return changes
	}
	parts := strings.Split(output, "\x00")
	for i := 0; i < len(parts)-1; i += 2 {
		statusChar := parts[i]
		filePath := parts[i+1]

		status := "changed"
		if len(statusChar) > 0 {
			switch statusChar[0] {
			case 'M':
				status = "modified"
			case 'A':
				status = "added"
			case 'D':
				status = "deleted"
			}
		}
		changes = append(changes, fileChange{path: filePath, status: status})
	}
	return changes
}

func parseNumstat(output string) map[string]fileStats {
	stats := make(map[string]fileStats)
	if len(output) == 0 {
		return stats
	}
	parts := strings.SplitSeq(output, "\x00")
	for part := range parts {
		if part == "" {
			continue
		}
		subparts := strings.SplitN(part, "\t", 3)
		if len(subparts) != 3 {
			continue
		}
		filePath := subparts[2]
		if subparts[0] == "-" && subparts[1] == "-" {
			stats[filePath] = fileStats{binary: true}
		} else {
			add, _ := strconv.Atoi(subparts[0])
			del, _ := strconv.Atoi(subparts[1])
			stats[filePath] = fileStats{additions: add, deletions: del}
		}
	}
	return stats
}

func parseUntracked(output string) []string {
	var files []string
	if len(output) == 0 {
		return files
	}
	parts := strings.SplitSeq(output, "\x00")
	for part := range parts {
		if part != "" {
			files = append(files, part)
		}
	}
	return files
}

func parseCombinedDiff(combinedDiff string, trackedChanges []fileChange) map[string]string {
	filePatches := make(map[string]string)
	if combinedDiff == "" {
		return filePatches
	}
	normalized := "\n" + combinedDiff
	chunks := strings.SplitSeq(normalized, "\ndiff --git ")
	for chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}

		before, _, ok := strings.Cut(chunk, "\n")
		var firstLine string
		if !ok {
			firstLine = chunk
		} else {
			firstLine = before
		}

		for _, tc := range trackedChanges {
			if matchPathToDiffHeader(firstLine, tc.path) {
				filePatches[tc.path] = "diff --git " + chunk
				break
			}
		}
	}
	return filePatches
}

func readUntrackedFile(repoPath, path string) (fileEntry, error) {
	diskPath := filepath.Join(repoPath, path)

	//nolint:gosec // path is inside repo worktree, checked by ls-files
	contentBytes, err := os.ReadFile(diskPath)
	if err != nil {
		return fileEntry{}, fmt.Errorf("reading untracked file %q: %w", path, err)
	}

	isBinary := false
	var additions int
	var patch string

	checkSize := min(len(contentBytes), 8000)
	if bytes.IndexByte(contentBytes[:checkSize], 0) >= 0 {
		isBinary = true
	} else {
		contentStr := string(contentBytes)
		lines := strings.Split(contentStr, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		additions = len(lines)

		if additions > 0 {
			var b strings.Builder
			fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", path, additions)
			for _, line := range lines {
				b.WriteString("+")
				b.WriteString(line)
				b.WriteString("\n")
			}
			patch = b.String()
		}
	}

	status := "added"
	if isBinary {
		status = "binary"
	}

	return fileEntry{
		path:      path,
		status:    status,
		additions: additions,
		deletions: 0,
		patch:     patch,
		binary:    isBinary,
	}, nil
}

func matchPathToDiffHeader(firstLine, path string) bool {
	prefixes := []string{" b/", " w/", " i/", " b/\"", " w/\"", " i/\""}
	for _, p := range prefixes {
		suffix := p + path
		if strings.HasSuffix(firstLine, suffix) ||
			(strings.HasSuffix(firstLine, "\"") && strings.HasSuffix(firstLine, suffix+"\"")) {
			return true
		}
	}
	return false
}

func shortHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) >= 7 {
		return hash[:7]
	}
	return hash
}
