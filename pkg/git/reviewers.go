package git

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+\d+(?:,\d+)? @@`)

// ReviewerSuggestion holds author recommendation details based on git blame.
type ReviewerSuggestion struct {
	Name  string   `json:"name"`
	Files []string `json:"files"`
}

type authorData struct {
	score float64
	files map[string]bool
}

type lineBlame struct {
	author string
	time   int64
}

// SuggestReviewers analyzes the modified files in the BranchDiffResult to determine
// the authors who wrote the lines being changed or deleted. It returns up to `limit`
// most relevant authors, excluding `excludeAuthor`, using a time-decay score.
func SuggestReviewers(
	dir string,
	diffResult *BranchDiffResult,
	excludeAuthor string,
	limit int,
) ([]ReviewerSuggestion, error) {
	authors := make(map[string]*authorData)
	now := time.Now().Unix()

	for _, f := range diffResult.Files {
		if f.Patch == "" || f.Status == "added" || f.Status == "deleted" || f.Status == "binary" {
			continue
		}

		blamedLines := extractBlameLines(f.Patch)
		if len(blamedLines) == 0 {
			continue
		}

		authorsByLine, err := runBlame(dir, diffResult.MergeBase, f.Path)
		if err != nil {
			continue
		}

		for _, lineNum := range blamedLines {
			blame := authorsByLine[lineNum]
			if blame.author != "" && blame.author != excludeAuthor && blame.author != "Not Committed Yet" {
				if authors[blame.author] == nil {
					authors[blame.author] = &authorData{files: make(map[string]bool)}
				}
				data := authors[blame.author]
				data.files[f.Path] = true

				// 7-day half-life decay.
				ageSeconds := max(now-blame.time, 0)
				days := float64(ageSeconds) / 86400.0
				score := math.Exp(-days * math.Ln2 / 7.0)
				data.score += score
			}
		}
	}

	type authorScore struct {
		name string
		data *authorData
	}
	var acs []authorScore
	for name, data := range authors {
		acs = append(acs, authorScore{name, data})
	}

	sort.Slice(acs, func(i, j int) bool {
		if acs[i].data.score == acs[j].data.score {
			return acs[i].name < acs[j].name
		}
		return acs[i].data.score > acs[j].data.score
	})

	var result []ReviewerSuggestion
	for i := 0; i < len(acs) && i < limit; i++ {
		var files []string
		for f := range acs[i].data.files {
			files = append(files, f)
		}
		sort.Strings(files)
		result = append(result, ReviewerSuggestion{
			Name:  acs[i].name,
			Files: files,
		})
	}

	return result, nil
}

func extractBlameLines(patch string) []int {
	var lines []int
	var currentLine int
	inHunk := false
	var hunkAddedLines int
	var hunkDeletedLines int
	var hunkStartLine int

	for line := range strings.SplitSeq(patch, "\n") {
		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			if inHunk && hunkDeletedLines == 0 && hunkAddedLines > 0 {
				if hunkStartLine > 0 {
					lines = append(lines, hunkStartLine)
				}
			}

			currentLine, _ = strconv.Atoi(m[1])
			hunkStartLine = currentLine
			inHunk = true
			hunkAddedLines = 0
			hunkDeletedLines = 0
			continue
		}
		if !inHunk || len(line) == 0 {
			continue
		}

		char := line[0]
		switch char {
		case ' ':
			currentLine++
		case '-':
			lines = append(lines, currentLine)
			hunkDeletedLines++
			currentLine++
		case '+':
			hunkAddedLines++
		}
	}

	if inHunk && hunkDeletedLines == 0 && hunkAddedLines > 0 {
		if hunkStartLine > 0 {
			lines = append(lines, hunkStartLine)
		}
	}

	return lines
}

func runBlame(dir, mergeBase, file string) (map[int]lineBlame, error) {
	out, err := gitOutput(dir, "blame", "--line-porcelain", mergeBase, "--", file)
	if err != nil {
		return nil, err
	}

	authorsByLine := make(map[int]lineBlame)
	var currentLine int
	var currentAuthor string
	var currentTime int64

	for line := range strings.SplitSeq(out, "\n") {
		if len(line) >= 40 && strings.IndexByte(line, ' ') == 40 {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				currentLine, _ = strconv.Atoi(parts[2])
			}
			currentAuthor = ""
			currentTime = 0
			continue
		}
		if after, ok := strings.CutPrefix(line, "author "); ok {
			currentAuthor = after
		} else if after, ok := strings.CutPrefix(line, "author-time "); ok {
			currentTime, _ = strconv.ParseInt(after, 10, 64)
		} else if strings.HasPrefix(line, "\t") {
			if currentAuthor != "" {
				authorsByLine[currentLine] = lineBlame{
					author: currentAuthor,
					time:   currentTime,
				}
			}
		}
	}
	return authorsByLine, nil
}

// GetCurrentAuthor returns the current user.name from git config.
func GetCurrentAuthor(dir string) string {
	name, err := gitOutput(dir, "config", "user.name")
	if err != nil {
		return ""
	}
	return name
}
