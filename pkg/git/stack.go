package git

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StackPR holds pull request information for a branch in a stack.
type StackPR struct {
	Number int    `json:"number"`
	State  string `json:"state"` // "OPEN", "MERGED", "QUEUED", etc.
	URL    string `json:"url,omitempty"`
}

// StackBranch represents a branch in a stack.
type StackBranch struct {
	Name        string   `json:"name"`
	Head        string   `json:"head,omitempty"`
	Base        string   `json:"base,omitempty"`
	IsCurrent   bool     `json:"isCurrent"`
	IsMerged    bool     `json:"isMerged"`
	IsQueued    bool     `json:"isQueued"`
	NeedsRebase bool     `json:"needsRebase"`
	PR          *StackPR `json:"pr,omitempty"`
}

// StackData represents stack state for prompt and statusline formatting.
type StackData struct {
	Trunk         string        `json:"trunk"`
	CurrentBranch string        `json:"currentBranch"`
	Branches      []StackBranch `json:"branches"`
}

type rawGHStackPullRequest struct {
	Number int  `json:"number"`
	Merged bool `json:"merged"`
}

type rawGHStackBranch struct {
	Branch      string                 `json:"branch"`
	Base        string                 `json:"base"`
	Head        string                 `json:"head"`
	PullRequest *rawGHStackPullRequest `json:"pullRequest"`
}

type rawGHStack struct {
	Trunk struct {
		Branch string `json:"branch"`
		Head   string `json:"head"`
	} `json:"trunk"`
	Branches []rawGHStackBranch `json:"branches"`
}

type rawGHStackFile struct {
	SchemaVersion int          `json:"schemaVersion"`
	Stacks        []rawGHStack `json:"stacks"`
}

// FormatStack formats a StackData struct into a concise prompt string.
// Returns "" if on trunk, not in a stack, or empty/invalid data.
func FormatStack(data StackData) string {
	if len(data.Branches) == 0 {
		return ""
	}

	if data.CurrentBranch != "" && data.Trunk != "" && data.CurrentBranch == data.Trunk {
		return ""
	}

	currIdx := -1
	for i, b := range data.Branches {
		if b.IsCurrent || (data.CurrentBranch != "" && b.Name == data.CurrentBranch) {
			currIdx = i
			break
		}
	}

	if currIdx < 0 {
		return ""
	}

	current := data.Branches[currIdx]
	posStr := fmt.Sprintf("%d/%d", currIdx+1, len(data.Branches))

	var prStr string
	if current.PR != nil && current.PR.Number > 0 {
		stateSuffix := ""
		state := strings.ToUpper(strings.TrimSpace(current.PR.State))
		if state == "MERGED" || current.IsMerged {
			stateSuffix = "✓"
		} else if state == "QUEUED" || current.IsQueued {
			stateSuffix = "◎"
		}
		prStr = fmt.Sprintf("#%d%s", current.PR.Number, stateSuffix)
	}

	parts := []string{posStr}
	if prStr != "" {
		parts = append(parts, prStr)
	}
	if current.NeedsRebase {
		parts = append(parts, "⚠")
	}

	return strings.Join(parts, " ")
}

// FormatStackJSON parses gh stack view --json output and formats it.
func FormatStackJSON(raw []byte) (string, error) {
	var data StackData
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", fmt.Errorf("unmarshal stack JSON: %w", err)
	}
	return FormatStack(data), nil
}

// FindGitDir finds the resolved .git directory starting from startDir.
func FindGitDir(startDir string) (string, error) {
	if startDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		startDir = cwd
	}

	curr := filepath.Clean(startDir)
	for {
		candidate := filepath.Join(curr, ".git")
		fi, err := os.Stat(candidate)
		if err == nil {
			if fi.IsDir() {
				return candidate, nil
			}
			// .git file (worktree or submodule)
			data, err := os.ReadFile(candidate) //nolint:gosec // candidate is .git file inside repository
			if err == nil {
				line := strings.TrimSpace(string(data))
				if after, ok := strings.CutPrefix(line, "gitdir:"); ok {
					gitDir := strings.TrimSpace(after)
					if !filepath.IsAbs(gitDir) {
						gitDir = filepath.Join(curr, gitDir)
					}
					return filepath.Clean(gitDir), nil
				}
			}
		}

		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}

	return "", os.ErrNotExist
}

func readGitBranch(gitDir string) string {
	headBytes, err := os.ReadFile(filepath.Join(gitDir, "HEAD")) //nolint:gosec // reading HEAD within git directory
	if err != nil {
		return ""
	}
	trimmed := strings.TrimSpace(string(headBytes))
	if after, ok := strings.CutPrefix(trimmed, "ref: refs/heads/"); ok {
		return after
	}
	return trimmed
}

func readBranchRefHead(gitDir, branchName string) string {
	refPath := filepath.Join(gitDir, "refs", "heads", filepath.Clean(branchName))
	bytes, err := os.ReadFile(refPath) //nolint:gosec // reading branch ref within git directory
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(bytes))
}

// DetectStack inspects the repository starting at dir to find stack data for the current branch.
func DetectStack(dir, currentBranch string) (*StackData, error) {
	gitDir, err := FindGitDir(dir)
	if err != nil {
		return nil, err
	}

	ghStackPath := filepath.Join(gitDir, "gh-stack")
	raw, err := os.ReadFile(ghStackPath) //nolint:gosec // reading gh-stack within git directory
	if err != nil {
		return nil, err
	}

	var sf rawGHStackFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, fmt.Errorf("parsing gh-stack: %w", err)
	}

	if currentBranch == "" {
		currentBranch = readGitBranch(gitDir)
	}

	for _, s := range sf.Stacks {
		if currentBranch == s.Trunk.Branch {
			return &StackData{
				Trunk:         s.Trunk.Branch,
				CurrentBranch: currentBranch,
			}, nil
		}

		matchedIdx := -1
		for i, b := range s.Branches {
			if b.Branch == currentBranch {
				matchedIdx = i
				break
			}
		}

		if matchedIdx >= 0 {
			// Matched stack found
			baseBranchName := s.Trunk.Branch
			if matchedIdx > 0 {
				baseBranchName = s.Branches[matchedIdx-1].Branch
			}

			// Fast disk check for rebase: check if base branch HEAD has moved beyond recorded base SHA
			needsRebase := false
			currentBaseSHA := s.Branches[matchedIdx].Base
			if currentBaseSHA != "" {
				parentHead := readBranchRefHead(gitDir, baseBranchName)
				if parentHead != "" && parentHead != currentBaseSHA {
					needsRebase = true
				}
			}

			branches := make([]StackBranch, len(s.Branches))
			for i, b := range s.Branches {
				isCurr := i == matchedIdx
				var pr *StackPR
				if b.PullRequest != nil && b.PullRequest.Number > 0 {
					state := "OPEN"
					if b.PullRequest.Merged {
						state = "MERGED"
					}
					pr = &StackPR{
						Number: b.PullRequest.Number,
						State:  state,
					}
				}

				branches[i] = StackBranch{
					Name:        b.Branch,
					Head:        b.Head,
					Base:        b.Base,
					IsCurrent:   isCurr,
					IsMerged:    b.PullRequest != nil && b.PullRequest.Merged,
					NeedsRebase: isCurr && needsRebase,
					PR:          pr,
				}
			}

			return &StackData{
				Trunk:         s.Trunk.Branch,
				CurrentBranch: currentBranch,
				Branches:      branches,
			}, nil
		}
	}

	return nil, nil
}

// GetStackStatus returns the formatted stack string for dir and branch, or "" if not in a stack.
func GetStackStatus(dir, currentBranch string) string {
	data, err := DetectStack(dir, currentBranch)
	if err != nil || data == nil {
		return ""
	}
	return FormatStack(*data)
}
