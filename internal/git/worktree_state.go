package git

import (
	"strings"
)

// WorktreeState runs git status --porcelain and returns counts of dirty tracked files and untracked files.
func WorktreeState(dir string) (dirty int, untracked int, err error) {
	out, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		return 0, 0, err
	}
	if out == "" {
		return 0, 0, nil
	}
	lines := strings.SplitSeq(out, "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "??") {
			untracked++
		} else {
			dirty++
		}
	}
	return dirty, untracked, nil
}
