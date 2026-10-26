package review

import (
	"fmt"
	"strings"

	"github.com/kalverra/agents/pkg/git"
)

// RebaseReason classifies the reason behind a rebase assessment.
type RebaseReason string

// Defined rebase reasons.
const (
	RebaseReasonUpToDate    RebaseReason = "up_to_date"
	RebaseReasonCleanBehind RebaseReason = "clean_behind"
	RebaseReasonConflict    RebaseReason = "conflict"
	RebaseReasonBaseMerged  RebaseReason = "base_merged"
)

// RebaseStatus represents the outcome of evaluating a branch's rebase requirements.
type RebaseStatus struct {
	TargetBranch string       `json:"target_branch"`
	IsIssue      bool         `json:"is_issue"`
	Reason       RebaseReason `json:"reason"`
	BehindCount  int          `json:"behind_count,omitempty"`
	AheadCount   int          `json:"ahead_count,omitempty"`
	Conflicts    []string     `json:"conflicts,omitempty"`
}

// RebaseAssessmentOptions provides parameters to evaluate rebase status.
type RebaseAssessmentOptions struct {
	TargetBranch string
	Trunk        string
	IsBaseMerged bool
	LocalMerge   *git.Mergeability
	PRMergeable  string
}

// AssessRebaseStatus determines whether rebase is genuinely required or if the branch cleanly merges.
func AssessRebaseStatus(opts RebaseAssessmentOptions) *RebaseStatus {
	target := opts.TargetBranch
	if target == "" {
		target = opts.Trunk
	}

	status := &RebaseStatus{
		TargetBranch: target,
	}

	// 1. Base branch in stack is already merged into trunk
	if opts.IsBaseMerged {
		status.IsIssue = true
		status.Reason = RebaseReasonBaseMerged
		return status
	}

	// 2. Merge conflicts exist (either detected locally via git merge-tree or flagged by GitHub PR)
	hasConflicts := strings.EqualFold(opts.PRMergeable, "CONFLICTING") ||
		(opts.LocalMerge != nil && opts.LocalMerge.HasConflict)

	if hasConflicts {
		status.IsIssue = true
		status.Reason = RebaseReasonConflict
		if opts.LocalMerge != nil {
			status.BehindCount = opts.LocalMerge.BehindCount
			status.AheadCount = opts.LocalMerge.AheadCount
			status.Conflicts = opts.LocalMerge.Conflicts
		}
		return status
	}

	// 3. Local git mergeability evaluation
	if opts.LocalMerge != nil {
		status.BehindCount = opts.LocalMerge.BehindCount
		status.AheadCount = opts.LocalMerge.AheadCount

		if opts.LocalMerge.BehindCount > 0 {
			// Behind base branch, but merges cleanly with 0 conflicts
			status.IsIssue = false
			status.Reason = RebaseReasonCleanBehind
			return status
		}

		status.IsIssue = false
		status.Reason = RebaseReasonUpToDate
		return status
	}

	// 4. Fallback when local mergeability unavailable
	status.IsIssue = false
	status.Reason = RebaseReasonUpToDate
	return status
}

// ActionItemMarkdown formats the rebase status for the Action Items bundle section.
func (s *RebaseStatus) ActionItemMarkdown(trunk string) string {
	if s == nil {
		return ""
	}
	target := s.TargetBranch
	if target == "" {
		target = trunk
	}

	switch s.Reason {
	case RebaseReasonConflict:
		if len(s.Conflicts) > 0 {
			return fmt.Sprintf(
				"- **Conflicts with `%s`** (rebase required to resolve %d conflicting file(s): `%s`)\n\n",
				target,
				len(s.Conflicts),
				strings.Join(s.Conflicts, "`, `"),
			)
		}
		return fmt.Sprintf("- **Conflicts with `%s`** (rebase required to resolve merge conflicts)\n\n", target)

	case RebaseReasonBaseMerged:
		dest := trunk
		if dest == "" {
			dest = "trunk"
		}
		return fmt.Sprintf("- **Base branch `%s` merged** (rebase required onto `%s`)\n\n", target, dest)

	case RebaseReasonCleanBehind:
		return fmt.Sprintf("- **Mergeable with `%s`** (behind by %d commit(s), no conflicts - rebase optional)\n\n",
			target, s.BehindCount)

	case RebaseReasonUpToDate:
		return fmt.Sprintf("- **Up-to-date with `%s`** (no rebase needed)\n\n", target)

	default:
		if s.IsIssue {
			return fmt.Sprintf("- **Needs rebase on `%s`** (run `gh stack rebase`)\n\n", target)
		}
		return fmt.Sprintf("- **Up-to-date with `%s`** (no rebase needed)\n\n", target)
	}
}

// StackNoticeMarkdown formats the rebase status for the Stack details bundle section.
func (s *RebaseStatus) StackNoticeMarkdown(trunk string) string {
	if s == nil {
		return ""
	}
	target := s.TargetBranch
	if target == "" {
		target = trunk
	}

	switch s.Reason {
	case RebaseReasonConflict:
		if len(s.Conflicts) > 0 {
			return fmt.Sprintf("\n> **Warning: Conflicts with `%s`** (rebase required to resolve %d conflict(s))\n",
				target, len(s.Conflicts))
		}
		return fmt.Sprintf(
			"\n> **Warning: Conflicts with `%s`** (rebase required to resolve merge conflicts)\n",
			target,
		)

	case RebaseReasonBaseMerged:
		dest := trunk
		if dest == "" {
			dest = "trunk"
		}
		return fmt.Sprintf("\n> **Warning: Base branch `%s` merged** (rebase required onto `%s`)\n", target, dest)

	case RebaseReasonCleanBehind:
		return fmt.Sprintf("\n> **Rebase Status**: Clean merge with `%s` (behind by %d commit(s), rebase optional)\n",
			target, s.BehindCount)

	case RebaseReasonUpToDate:
		return fmt.Sprintf("\n> **Rebase Status**: Up-to-date with `%s` (no rebase needed)\n", target)

	default:
		if s.IsIssue {
			return fmt.Sprintf("\n> **Warning: Branch needs rebase on `%s`** (run `gh stack rebase`)\n", target)
		}
		return fmt.Sprintf("\n> **Rebase Status**: Up-to-date with `%s` (no rebase needed)\n", target)
	}
}
