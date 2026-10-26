package git

import (
	"fmt"
	"path/filepath"
	"strings"
)

const truncateThreshold = 40

// FormatHuman renders a BranchDiffResult as clean, LLM-readable text.
// Strips git metadata noise, groups code vs tooling files, and truncates large config additions.
func FormatHuman(result *BranchDiffResult) string {
	var b strings.Builder

	current := result.CurrentBranch
	if result.HasLocalChanges && result.CurrentBranch == result.BaseBranch {
		current = result.CurrentBranch + " (local changes)"
	}
	fmt.Fprintf(&b, "branch-diff: %s → %s (merge-base %s)\n",
		result.BaseBranch, current, result.MergeBase)

	if len(result.Files) == 0 {
		fmt.Fprintln(&b, "No changes.")
		return b.String()
	}

	var codeFiles, toolingFiles []FileDiff
	for _, f := range result.Files {
		if IsCodeFile(f.Path) {
			codeFiles = append(codeFiles, f)
		} else {
			toolingFiles = append(toolingFiles, f)
		}
	}

	if len(codeFiles) > 0 {
		s := sumStats(codeFiles)
		fmt.Fprintf(&b, "Code:    %s\n", formatGroupStats(len(codeFiles), s))
	}
	if len(toolingFiles) > 0 {
		s := sumStats(toolingFiles)
		fmt.Fprintf(&b, "Tooling: %s\n", formatGroupStats(len(toolingFiles), s))
	}

	for _, f := range append(codeFiles, toolingFiles...) {
		b.WriteByte('\n')
		writeFileDiff(&b, f)
	}

	return b.String()
}

// FormatDiffBody renders the file diff content without the PR diff range header line.
func FormatDiffBody(result *BranchDiffResult) string {
	full := FormatHuman(result)
	_, rest, ok := strings.Cut(full, "\n")
	if !ok {
		return ""
	}
	return strings.TrimPrefix(rest, "\n")
}

func formatGroupStats(count int, s fileStat) string {
	noun := "file"
	if count != 1 {
		noun = "files"
	}
	return fmt.Sprintf("%d %s +%d -%d", count, noun, s.add, s.del)
}

func writeFileDiff(b *strings.Builder, f FileDiff) {
	fmt.Fprintf(
		b,
		"<file path=%q status=%q additions=\"%d\" deletions=\"%d\">\n",
		f.Path,
		f.Status,
		f.Additions,
		f.Deletions,
	)

	if f.Patch != "" {
		if IsDependencyOrLockfile(f.Path) {
			fmt.Fprintln(b, "[patch omitted — dependency/lockfile/generated]")
		} else if !IsCodeFile(f.Path) && f.Status == "added" && f.Additions > truncateThreshold {
			fmt.Fprintf(b, "[truncated — %d-line config file]\n", f.Additions)
		} else if cleaned := cleanFilePatch(f.Patch); cleaned != "" {
			fmt.Fprintln(b, cleaned)
		}
	}

	fmt.Fprintln(b, "</file>")
}

type fileStat struct{ add, del int }

func sumStats(files []FileDiff) fileStat {
	var s fileStat
	for _, f := range files {
		s.add += f.Additions
		s.del += f.Deletions
	}
	return s
}

var codeExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true,
	".jsx": true, ".tsx": true, ".rs": true, ".c": true,
	".cpp": true, ".h": true, ".java": true, ".rb": true,
	".cs": true, ".swift": true, ".kt": true, ".sh": true,
}

// IsCodeFile reports whether path has a recognized source-code extension.
func IsCodeFile(path string) bool {
	return codeExtensions[strings.ToLower(filepath.Ext(path))]
}

// IsDependencyOrLockfile reports whether path is a lockfile or under vendor/node_modules.
func IsDependencyOrLockfile(path string) bool {
	base := filepath.Base(path)
	baseLower := strings.ToLower(base)

	switch baseLower {
	case "go.sum",
		"go.work.sum",
		"package-lock.json",
		"npm-shrinkwrap.json",
		"yarn.lock",
		"pnpm-lock.yaml",
		"pnpm-lock.json",
		"bun.lockb",
		"bun.lock",
		"cargo.lock",
		"gemfile.lock",
		"poetry.lock",
		"pipfile.lock",
		"composer.lock",
		"flake.lock",
		"mix.lock",
		"uv.lock",
		"podfile.lock",
		"berksfile.lock":
		return true
	}

	if strings.HasSuffix(baseLower, ".lock") || strings.HasSuffix(baseLower, ".sum") {
		return true
	}

	parts := strings.SplitSeq(filepath.ToSlash(path), "/")
	for p := range parts {
		if p == "vendor" || p == "node_modules" {
			return true
		}
	}
	return false
}

// cleanFilePatch strips git metadata lines from a unified diff patch,
// keeping only hunk headers and content lines.
func cleanFilePatch(patch string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(patch, "\n") {
		if SkipPatchLine(line) {
			continue
		}
		if strings.HasPrefix(line, "@@") {
			line = trimHunkHeader(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// trimHunkHeader removes context text that go-git appends after the closing @@.
func trimHunkHeader(line string) string {
	rest := line[2:]
	closeIdx := strings.Index(rest, "@@")
	if closeIdx < 0 {
		return line
	}
	return line[:2+closeIdx+2]
}

// SkipPatchLine reports whether a unified-diff line is git metadata rather than hunk content.
func SkipPatchLine(line string) bool {
	return strings.HasPrefix(line, "diff --git ") ||
		strings.HasPrefix(line, "index ") ||
		strings.HasPrefix(line, "new file mode ") ||
		strings.HasPrefix(line, "deleted file mode ") ||
		strings.HasPrefix(line, "--- ") ||
		strings.HasPrefix(line, "+++ ") ||
		line == `\ No newline at end of file`
}
