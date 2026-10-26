package implement

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/kalverra/agents/pkg/git"
)

// IsLockfile reports whether path is a package manager lockfile, dependency
// checksum file, or under vendor/node_modules.
func IsLockfile(filePath string) bool {
	clean := filepath.ToSlash(filePath)
	base := filepath.Base(clean)
	baseLower := strings.ToLower(base)

	switch baseLower {
	case "packages.lock.json", "pdm.lock":
		return true
	}

	return git.IsDependencyOrLockfile(clean)
}

type gitattrRule struct {
	baseDir string            // Repo-relative directory containing the .gitattributes file (e.g. "" or "sub")
	pattern string            // Pattern from the line
	attrs   map[string]string // Attribute values: "unset", "set", or string value
}

// Gitattributes holds rules parsed from .gitattributes files.
type Gitattributes struct {
	rules []gitattrRule
}

// ParseGitattributes parses the text of a .gitattributes file located in baseDir.
func ParseGitattributes(content, baseDir string) *Gitattributes {
	ga := &Gitattributes{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pattern := fields[0]
		attrs := make(map[string]string)
		for _, token := range fields[1:] {
			switch {
			case token == "binary":
				attrs["diff"] = "unset"
				attrs["merge"] = "unset"
				attrs["text"] = "unset"
			case strings.HasPrefix(token, "-"):
				attrs[token[1:]] = "unset"
			case strings.HasPrefix(token, "!"):
				delete(attrs, token[1:])
			case strings.Contains(token, "="):
				parts := strings.SplitN(token, "=", 2)
				attrs[parts[0]] = parts[1]
			default:
				attrs[token] = "set"
			}
		}
		ga.rules = append(ga.rules, gitattrRule{
			baseDir: filepath.ToSlash(filepath.Clean(baseDir)),
			pattern: pattern,
			attrs:   attrs,
		})
	}
	return ga
}

// LoadRepoGitattributes finds and parses all .gitattributes files in repoDir.
func LoadRepoGitattributes(repoDir string) (*Gitattributes, error) {
	combined := &Gitattributes{}

	err := filepath.WalkDir(repoDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || name == "vendor" || name == ".agents" {
				return filepath.SkipDir
			}
			return nil
		}
		if name == ".gitattributes" {
			relDir, err := filepath.Rel(repoDir, filepath.Dir(p))
			if err != nil {
				return err
			}
			if relDir == "." {
				relDir = ""
			}
			//nolint:gosec // path is discovered within repoDir
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			ga := ParseGitattributes(string(raw), relDir)
			combined.rules = append(combined.rules, ga.rules...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return combined, nil
}

// IsGeneratedOrNoDiff reports whether a path matches rules indicating it is
// generated or should be excluded from diffs (e.g. linguist-generated,
// gitlab-generated, generated, or diff is unset/binary).
func (ga *Gitattributes) IsGeneratedOrNoDiff(filePath string) bool {
	if ga == nil {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filePath))
	clean = strings.TrimPrefix(clean, "./")

	// Rules later in the list or in more specific directories take precedence,
	// but here we check all matches to see if generated or no-diff is specified.
	for _, rule := range slices.Backward(ga.rules) {
		if matchesRule(rule, clean) {
			if rule.attrs["diff"] == "unset" {
				return true
			}
			for _, attr := range []string{"linguist-generated", "gitlab-generated", "generated"} {
				val := strings.ToLower(rule.attrs[attr])
				if val == "set" || val == "true" || val == "yes" || val == "1" {
					return true
				}
			}
		}
	}
	return false
}

func matchesRule(rule gitattrRule, cleanPath string) bool {
	target := cleanPath
	if rule.baseDir != "" && rule.baseDir != "." {
		if !strings.HasPrefix(target, rule.baseDir+"/") {
			return false
		}
		target = strings.TrimPrefix(target, rule.baseDir+"/")
	}

	pat := rule.pattern
	if !strings.Contains(pat, "/") {
		base := path.Base(target)
		matched, err := path.Match(pat, base)
		if err == nil && matched {
			return true
		}
	}

	pat = strings.TrimPrefix(pat, "/")
	if strings.Contains(pat, "**") {
		re := globToRegex(pat)
		return re.MatchString(target)
	}

	matched, err := path.Match(pat, target)
	return err == nil && matched
}

func globToRegex(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(pattern) {
		switch {
		case strings.HasPrefix(pattern[i:], "/**"):
			b.WriteString("(?:/.*)?")
			i += 3
		case strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 3
		case pattern[i] == '*':
			b.WriteString("[^/]*")
			i++
		case pattern[i] == '?':
			b.WriteString("[^/]")
			i++
		case strings.ContainsRune(`.+()|[]{}^$\`, rune(pattern[i])):
			b.WriteByte('\\')
			b.WriteByte(pattern[i])
			i++
		default:
			b.WriteByte(pattern[i])
			i++
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// FileDiffChunk represents one file's section of a unified diff.
type FileDiffChunk struct {
	Path    string
	Content string
}

var diffGitHeaderRe = regexp.MustCompile(`(?m)^diff --git `)

// SplitUnifiedDiff splits a unified diff into individual file diff chunks.
func SplitUnifiedDiff(diff string) []FileDiffChunk {
	trimmed := strings.TrimSpace(diff)
	if trimmed == "" {
		return nil
	}

	locs := diffGitHeaderRe.FindAllStringIndex(diff, -1)
	if len(locs) == 0 {
		return nil
	}

	chunks := make([]FileDiffChunk, 0, len(locs))
	for i := range locs {
		start := locs[i][0]
		end := len(diff)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		chunkText := diff[start:end]
		path := extractDiffPath(chunkText)
		chunks = append(chunks, FileDiffChunk{
			Path:    path,
			Content: chunkText,
		})
	}
	return chunks
}

var (
	diffPlusRe  = regexp.MustCompile(`(?m)^\+\+\+ (?:b/)?([^\s\t\n]+)`)
	diffMinusRe = regexp.MustCompile(`(?m)^--- (?:a/)?([^\s\t\n]+)`)
	diffGitRe   = regexp.MustCompile(`(?m)^diff --git (?:a/)?([^\s\t\n]+) (?:b/)?([^\s\t\n]+)`)
)

func extractDiffPath(chunk string) string {
	if m := diffPlusRe.FindStringSubmatch(chunk); len(m) > 1 && m[1] != "/dev/null" {
		return cleanGitPath(m[1])
	}
	if m := diffMinusRe.FindStringSubmatch(chunk); len(m) > 1 && m[1] != "/dev/null" {
		return cleanGitPath(m[1])
	}
	if m := diffGitRe.FindStringSubmatch(chunk); len(m) > 2 {
		if m[2] != "/dev/null" {
			return cleanGitPath(m[2])
		}
		return cleanGitPath(m[1])
	}
	return ""
}

func cleanGitPath(p string) string {
	p = strings.Trim(p, "\"")
	p = strings.TrimPrefix(p, "b/")
	p = strings.TrimPrefix(p, "a/")
	return filepath.ToSlash(filepath.Clean(p))
}

// FilterUnifiedDiff removes file diff chunks for which isExcluded returns true.
func FilterUnifiedDiff(diff string, isExcluded func(path string) bool) string {
	chunks := SplitUnifiedDiff(diff)
	if len(chunks) == 0 {
		return ""
	}

	var kept []string
	for _, chunk := range chunks {
		if isExcluded != nil && isExcluded(chunk.Path) {
			continue
		}
		kept = append(kept, strings.TrimRight(chunk.Content, "\n"))
	}

	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

// InjectDiffReference inserts or updates a "## Diff" section in a worker summary,
// pointing to the generated unified diff file.
func InjectDiffReference(workSummary, diffPath string) string {
	lines := strings.Split(workSummary, "\n")
	hasDiffSection := false
	diffStart := -1
	diffEnd := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## Diff") {
			hasDiffSection = true
			diffStart = i
			diffEnd = len(lines)
			for j := i + 1; j < len(lines); j++ {
				jTrim := strings.TrimSpace(lines[j])
				if strings.HasPrefix(jTrim, "#") {
					diffEnd = j
					break
				}
			}
			break
		}
	}

	if hasDiffSection {
		var newLines []string
		newLines = append(newLines, lines[:diffStart]...)
		newLines = append(newLines, "## Diff", "- Unified diff: `"+diffPath+"`", "")
		rest := lines[diffEnd:]
		for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
			rest = rest[1:]
		}
		newLines = append(newLines, rest...)
		return strings.Join(newLines, "\n")
	}

	insertIdx := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# Step") {
			insertIdx = i + 1
			for insertIdx < len(lines) && strings.TrimSpace(lines[insertIdx]) == "" {
				insertIdx++
			}
			break
		}
	}

	var newLines []string
	newLines = append(newLines, lines[:insertIdx]...)
	if insertIdx > 0 && len(newLines) > 0 && strings.TrimSpace(newLines[len(newLines)-1]) != "" {
		newLines = append(newLines, "")
	}
	newLines = append(newLines, "## Diff", "- Unified diff: `"+diffPath+"`", "")
	newLines = append(newLines, lines[insertIdx:]...)
	return strings.Join(newLines, "\n")
}

// DiffStat summarizes changes in a unified diff.
type DiffStat struct {
	FilesAdded    int `json:"files_added"`
	FilesModified int `json:"files_modified"`
	FilesDeleted  int `json:"files_deleted"`
	LinesAdded    int `json:"lines_added"`
	LinesDeleted  int `json:"lines_deleted"`
}

// TotalFiles returns the total number of files affected.
func (s DiffStat) TotalFiles() int {
	return s.FilesAdded + s.FilesModified + s.FilesDeleted
}

// IsEmpty reports whether the diff stat contains no changes.
func (s DiffStat) IsEmpty() bool {
	return s.TotalFiles() == 0 && s.LinesAdded == 0 && s.LinesDeleted == 0
}

// ParseDiffStat parses a unified diff string and tallies added/modified/deleted files
// and added/deleted lines.
func ParseDiffStat(diff string) DiffStat {
	chunks := SplitUnifiedDiff(diff)
	var stat DiffStat
	for _, chunk := range chunks {
		isNew := strings.Contains(chunk.Content, "\nnew file mode ") ||
			strings.HasPrefix(chunk.Content, "new file mode ") ||
			strings.Contains(chunk.Content, "--- /dev/null")
		isDel := strings.Contains(chunk.Content, "\ndeleted file mode ") ||
			strings.HasPrefix(chunk.Content, "deleted file mode ") ||
			strings.Contains(chunk.Content, "+++ /dev/null")
		switch {
		case isNew:
			stat.FilesAdded++
		case isDel:
			stat.FilesDeleted++
		default:
			stat.FilesModified++
		}

		lines := strings.Split(chunk.Content, "\n")
		inHunk := false
		for _, line := range lines {
			if strings.HasPrefix(line, "@@") {
				inHunk = true
				continue
			}
			if !inHunk {
				continue
			}
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				stat.LinesAdded++
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				stat.LinesDeleted++
			}
		}
	}
	return stat
}

// FormatDiffStat formats a DiffStat compactly: "+7 -5 (1 edited, 1 added, 1 deleted)".
func FormatDiffStat(stat DiffStat, useColor bool) string {
	if stat.TotalFiles() == 0 {
		return ""
	}
	var fileParts []string
	if stat.FilesModified > 0 {
		fileParts = append(fileParts, fmt.Sprintf("%d edited", stat.FilesModified))
	}
	if stat.FilesAdded > 0 {
		fileParts = append(fileParts, fmt.Sprintf("%d added", stat.FilesAdded))
	}
	if stat.FilesDeleted > 0 {
		fileParts = append(fileParts, fmt.Sprintf("%d deleted", stat.FilesDeleted))
	}
	breakdown := strings.Join(fileParts, ", ")

	plus := fmt.Sprintf("+%d", stat.LinesAdded)
	minus := fmt.Sprintf("-%d", stat.LinesDeleted)

	if useColor {
		plus = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(plus)
		minus = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(minus)
		breakdown = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("(" + breakdown + ")")
		return fmt.Sprintf("%s %s %s", plus, minus, breakdown)
	}

	return fmt.Sprintf("%s %s (%s)", plus, minus, breakdown)
}
