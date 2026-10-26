// Package review formats PR review threads and branch diffs for LLM consumption.
package review

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/internal/review/lsp"
	"github.com/kalverra/agents/pkg/git"
)

var hunkHeaderRegex = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// StackInfo represents stack metadata for review bundle and CLI result.
type StackInfo struct {
	ID           string        `json:"id,omitempty"`
	Number       int           `json:"number,omitempty"`
	Position     int           `json:"position"`
	Total        int           `json:"total"`
	Trunk        string        `json:"trunk"`
	NeedsRebase  bool          `json:"needs_rebase,omitempty"`
	PrevBranch   string        `json:"prev_branch,omitempty"`
	NextBranch   string        `json:"next_branch,omitempty"`
	Layers       []StackLayer  `json:"layers,omitempty"`
	RebaseStatus *RebaseStatus `json:"rebase_status,omitempty"`
}

// StackLayer represents an individual layer/branch in a stack.
type StackLayer struct {
	Position  int    `json:"position"`
	Branch    string `json:"branch"`
	BaseRef   string `json:"base_ref,omitempty"`
	PRNumber  int    `json:"pr_number,omitempty"`
	PRURL     string `json:"pr_url,omitempty"`
	PRTitle   string `json:"pr_title,omitempty"`
	State     string `json:"state,omitempty"`
	IsCurrent bool   `json:"is_current"`
}

// ResolveStackInfo builds unified stack metadata from GitHub PR and local git stack data.
func ResolveStackInfo(pr *github.PR, localStack *git.StackData, currentBranch string) *StackInfo {
	if pr != nil && pr.Stack != nil && len(pr.Stack.Entries) > 0 {
		return resolveFromPRStack(pr, localStack, currentBranch)
	}
	if localStack != nil && len(localStack.Branches) > 0 {
		return resolveFromLocalStack(localStack, currentBranch)
	}
	return nil
}

func resolveFromPRStack(pr *github.PR, localStack *git.StackData, currentBranch string) *StackInfo {
	info := &StackInfo{
		ID:     pr.Stack.ID,
		Number: pr.Stack.Number,
		Total:  pr.Stack.Size,
		Trunk:  pr.Stack.BaseRefName,
	}
	if info.Total <= 0 {
		info.Total = len(pr.Stack.Entries)
	}

	info.Layers = make([]StackLayer, len(pr.Stack.Entries))
	currIdx := -1
	for i, entry := range pr.Stack.Entries {
		isCurrent := (entry.HeadRefName != "" && entry.HeadRefName == currentBranch) ||
			(entry.Number > 0 && entry.Number == pr.Number)
		if isCurrent {
			currIdx = i
			info.Position = entry.Position
		}

		info.Layers[i] = StackLayer{
			Position:  entry.Position,
			Branch:    entry.HeadRefName,
			BaseRef:   entry.BaseRefName,
			PRNumber:  entry.Number,
			PRURL:     entry.URL,
			PRTitle:   entry.Title,
			State:     entry.State,
			IsCurrent: isCurrent,
		}
	}

	if currIdx >= 0 {
		if currIdx > 0 {
			info.PrevBranch = info.Layers[currIdx-1].Branch
		} else {
			info.PrevBranch = info.Trunk
		}
		if currIdx < len(info.Layers)-1 {
			info.NextBranch = info.Layers[currIdx+1].Branch
		}
	}

	if localStack != nil {
		for _, b := range localStack.Branches {
			if (b.Name == currentBranch || b.IsCurrent) && b.NeedsRebase {
				info.NeedsRebase = true
				break
			}
		}
	}

	return info
}

func resolveFromLocalStack(localStack *git.StackData, currentBranch string) *StackInfo {
	currIdx := -1
	for i, b := range localStack.Branches {
		if b.IsCurrent || (currentBranch != "" && b.Name == currentBranch) {
			currIdx = i
			break
		}
	}

	if currIdx < 0 {
		return nil
	}

	info := &StackInfo{
		Position:    currIdx + 1,
		Total:       len(localStack.Branches),
		Trunk:       localStack.Trunk,
		NeedsRebase: localStack.Branches[currIdx].NeedsRebase,
	}

	if currIdx > 0 {
		info.PrevBranch = localStack.Branches[currIdx-1].Name
	} else {
		info.PrevBranch = localStack.Trunk
	}

	if currIdx < len(localStack.Branches)-1 {
		info.NextBranch = localStack.Branches[currIdx+1].Name
	}

	info.Layers = make([]StackLayer, len(localStack.Branches))
	for i, b := range localStack.Branches {
		prNum := 0
		prState := ""
		if b.PR != nil {
			prNum = b.PR.Number
			prState = b.PR.State
		}
		baseRef := localStack.Trunk
		if i > 0 {
			baseRef = localStack.Branches[i-1].Name
		}
		info.Layers[i] = StackLayer{
			Position:  i + 1,
			Branch:    b.Name,
			BaseRef:   baseRef,
			PRNumber:  prNum,
			State:     prState,
			IsCurrent: i == currIdx,
		}
	}

	return info
}

// BundleOptions defines configuration and inputs for bundle formatting.
type BundleOptions struct {
	PR                 *github.PR
	Diff               *git.BranchDiffResult
	Stack              *StackInfo
	IncludeResolved    bool
	SuggestedReviewers []git.ReviewerSuggestion
	ExternalUsages     []ExternalUsage
	RepoRoot           string
	DiffsDir           string
	MaxPatchLines      int
	CIStatus           *github.CIStatusRollup
	RebaseStatus       *RebaseStatus
}

// FormatBundle renders PR review threads and branch diff as a structured Markdown document.
func FormatBundle(
	pr *github.PR,
	diff *git.BranchDiffResult,
	stack *StackInfo,
	includeResolved bool,
	suggestedReviewers []git.ReviewerSuggestion,
) string {
	return FormatBundleWithOptions(BundleOptions{
		PR:                 pr,
		Diff:               diff,
		Stack:              stack,
		IncludeResolved:    includeResolved,
		SuggestedReviewers: suggestedReviewers,
	})
}

// FormatBundleWithOptions formats the review bundle with full options including tiers, TOC, and external usages.
func FormatBundleWithOptions(opts BundleOptions) string {
	var b strings.Builder
	estSize := 8192
	if opts.Diff != nil {
		estSize += len(opts.Diff.Patch)
	}
	if opts.PR != nil {
		estSize += len(opts.PR.Body) + len(opts.PR.Threads)*512
	}
	b.Grow(estSize)

	repoRoot := resolveRepoRoot(opts)

	fileThreads, lineThreads, notInterleaved := collectThreads(opts.PR, opts.IncludeResolved)

	threadCounts := make(map[string]int)
	for p, ts := range fileThreads {
		threadCounts[p] += len(ts)
	}
	for p, lm := range lineThreads {
		for _, ts := range lm {
			threadCounts[p] += len(ts)
		}
	}

	assessmentMap, allUnresolvedAssessments := buildAssessmentMap(opts.PR, opts.Diff, repoRoot)

	opts.RebaseStatus = resolveRebaseStatus(opts, repoRoot)

	writePRHeader(&b, opts.PR, opts.Diff)
	b.WriteString("- **Stats**: ")
	b.WriteString(formatDiffStats(opts.Diff))
	b.WriteString("\n\n")

	writeActionItemsSection(&b, opts, allUnresolvedAssessments)

	if opts.Stack != nil {
		writeStackSection(&b, opts.Stack)
	}

	writeCIStatusSection(&b, opts.CIStatus)

	if opts.PR != nil && opts.PR.Body != "" {
		cleaned := github.StripBloat(opts.PR.Body)
		if cleaned != "" {
			b.WriteString("## PR Description\n\n")
			for line := range strings.SplitSeq(cleaned, "\n") {
				b.WriteString("> ")
				b.WriteString(line)
				b.WriteByte('\n')
			}
			b.WriteString("\n")
		}
	}

	if len(opts.SuggestedReviewers) > 0 {
		b.WriteString("## Suggested Reviewers\n\n")
		for _, r := range opts.SuggestedReviewers {
			fmt.Fprintf(&b, "- %s (suggested to review: %s)\n", r.Name, strings.Join(r.Files, ", "))
		}
		b.WriteString("\n")
	}

	semanticFiles, formattingFiles, deletionFiles, unreadableFiles, diffFileMap := partitionFiles(
		opts.Diff,
		threadCounts,
	)

	// Dump per-file diffs to diffsDir for all modified files
	if opts.DiffsDir != "" && opts.Diff != nil {
		for _, f := range opts.Diff.Files {
			if f.Patch != "" {
				dumpFullPatch(opts.DiffsDir, f.Path, f.Patch)
			}
		}
	}

	// Placeholder for TOC
	b.WriteString("{{TOC_PLACEHOLDER}}\n\n")

	writeExternalUsagesSection(&b, opts.ExternalUsages)
	writeSemanticSection(
		&b,
		semanticFiles,
		fileThreads,
		lineThreads,
		opts.MaxPatchLines,
		opts.DiffsDir,
		repoRoot,
		assessmentMap,
	)
	writeDeletedSection(&b, deletionFiles, opts.ExternalUsages)
	writeFormattingSection(&b, formattingFiles)
	writeUnreadableSection(&b, unreadableFiles)
	fileDiffMap := buildFileDiffMap(opts.Diff)
	writeUnresolvedThreadsSection(&b, fileThreads, lineThreads, notInterleaved, diffFileMap, fileDiffMap, assessmentMap)

	docTemplate := b.String()
	initialEntries := buildInitialTOC(
		semanticFiles,
		deletionFiles,
		formattingFiles,
		unreadableFiles,
		threadCounts,
		opts.DiffsDir,
	)

	return AssembleBundleWithTOC(docTemplate, initialEntries, func(doc string) []TOCEntry {
		return measureTOCLineNumbers(doc, initialEntries)
	})
}

func buildFileDiffMap(diff *git.BranchDiffResult) map[string]*git.FileDiff {
	fileDiffMap := make(map[string]*git.FileDiff)
	if diff != nil {
		for i := range diff.Files {
			fileDiffMap[diff.Files[i].Path] = &diff.Files[i]
		}
	}
	return fileDiffMap
}

func buildAssessmentMap(
	pr *github.PR,
	diff *git.BranchDiffResult,
	repoRoot string,
) (map[string]*ThreadAssessment, []ThreadAssessment) {
	fileDiffMap := buildFileDiffMap(diff)

	assessmentMap := make(map[string]*ThreadAssessment)
	var allUnresolvedAssessments []ThreadAssessment
	if pr != nil {
		for i := range pr.Threads {
			t := &pr.Threads[i]
			fDiff := fileDiffMap[t.Path]
			assessment := AssessThread(t, fDiff, repoRoot)
			assessmentMap[t.ID] = &assessment
			if !t.IsResolved {
				allUnresolvedAssessments = append(allUnresolvedAssessments, assessment)
			}
		}
	}
	return assessmentMap, allUnresolvedAssessments
}

func measureTOCLineNumbers(doc string, initialEntries []TOCEntry) []TOCEntry {
	lines := strings.Split(doc, "\n")
	measureMap := make(map[string]int)
	for i, line := range lines {
		if after, ok := strings.CutPrefix(line, "### "); ok {
			measureMap[after] = i + 1
		} else if strings.HasPrefix(line, "## Formatting & Low-Signal Changes") {
			measureMap["## Formatting & Low-Signal Changes"] = i + 1
		}
	}

	updated := make([]TOCEntry, len(initialEntries))
	copy(updated, initialEntries)

	for i := range updated {
		e := &updated[i]
		cleanTitle := strings.Trim(e.Title, "`*")
		for header, lineNum := range measureMap {
			if strings.Contains(header, cleanTitle) {
				e.StartLine = lineNum
				break
			}
		}
	}

	for i := range updated {
		if updated[i].StartLine <= 0 {
			continue
		}
		nextStart := -1
		for j := i + 1; j < len(updated); j++ {
			if updated[j].StartLine > updated[i].StartLine {
				nextStart = updated[j].StartLine
				break
			}
		}
		if nextStart > 0 {
			updated[i].EndLine = nextStart - 1
		} else {
			end := len(lines)
			for k := updated[i].StartLine; k < len(lines); k++ {
				if strings.HasPrefix(lines[k], "## ") {
					end = k
					break
				}
			}
			updated[i].EndLine = end
		}
	}
	return updated
}

func writeExternalUsagesSection(b *strings.Builder, usages []ExternalUsage) {
	if len(usages) == 0 {
		return
	}
	b.WriteString("## External Usage Context & Remaining References\n\n")
	b.WriteString(
		"The following symbols were changed/removed in this diff and still have references in code outside the diff:\n\n",
	)
	for _, u := range usages {
		refCount := len(u.References)
		refNoun := "references"
		if refCount == 1 {
			refNoun = "reference"
		}
		fmt.Fprintf(b, "### `%s` (%s): %d %s outside diff\n\n", u.Symbol, u.Kind, refCount, refNoun)
		for _, r := range u.References {
			if r.Text != "" {
				fmt.Fprintf(b, "- `%s:%d`: `%s`\n", r.File, r.Line, r.Text)
			} else {
				fmt.Fprintf(b, "- `%s:%d`\n", r.File, r.Line)
			}
		}
		b.WriteString("\n")
	}
}

func writeActionItemsSection(b *strings.Builder, opts BundleOptions, assessments []ThreadAssessment) {
	b.WriteString("## Action Items & Status Summary\n\n")
	writeActionItemThreads(b, assessments)
	writeActionItemCI(b, opts.CIStatus)
	writeActionItemRebase(b, opts)

	if len(opts.ExternalUsages) > 0 {
		fmt.Fprintf(
			b,
			"### External Reference Risks\n\n- %d modified/deleted symbols have remaining references outside the diff (see External Usage Context below).\n\n",
			len(opts.ExternalUsages),
		)
	}
}

func writeActionItemThreads(b *strings.Builder, assessments []ThreadAssessment) {
	if len(assessments) == 0 {
		b.WriteString("### Review Threads\n\n- **All threads resolved**: 0 pending review threads.\n\n")
		return
	}

	fmt.Fprintf(b, "### Unresolved Review Threads (%d)\n\n", len(assessments))
	for _, a := range assessments {
		lineInfo := ""
		if a.Line > 0 {
			lineInfo = fmt.Sprintf(":%d", a.Line)
		}
		anchor := "#" + sanitizeAnchor(a.Path)
		badge := "**Action Required**"
		switch a.Status {
		case StatusResolvedByCommit:
			badge = "**Resolved by Newer Commit**"
		case StatusOutdated:
			badge = "**Outdated (Verify)**"
		}
		fmt.Fprintf(b, "- %s: [`%s%s`](%s) by @%s — %s\n", badge, a.Path, lineInfo, anchor, a.Author, a.Verdict)
	}
	b.WriteString("\n")
}

func writeActionItemCI(b *strings.Builder, ci *github.CIStatusRollup) {
	if ci == nil || ci.Total == 0 {
		return
	}

	b.WriteString("### CI Status Verdict\n\n")
	fmt.Fprintf(b, "- **Summary**: %d/%d passing\n", ci.Pass, ci.Total)

	// Itemize failing checks (required or not — failures may indicate real breakage)
	// and required pending checks. Non-required pending checks are collapsed to a
	// count so they don't pollute the action items.
	if ci.Fail > 0 {
		for _, c := range ci.Checks {
			if resolveCheckBucket(c) == "fail" {
				fmt.Fprintf(b, "  - FAIL `%s`: %s\n", c.Name, c.Verdict())
			}
		}
	}
	if ci.Pending > 0 {
		optionalPending := 0
		for _, c := range ci.Checks {
			if resolveCheckBucket(c) != "pending" {
				continue
			}
			if c.IsRequired {
				fmt.Fprintf(b, "  - PENDING `%s` (BLOCKING - required check)\n", c.Name)
			} else {
				optionalPending++
			}
		}
		if optionalPending > 0 {
			fmt.Fprintf(b, "  - PENDING optional checks (non-blocking): %d\n", optionalPending)
		}
	}
	if ci.FailRequired == 0 && ci.PendingRequired == 0 {
		b.WriteString("- **All required CI checks passing**: Merge is not blocked by required checks.\n")
	}
	b.WriteString("\n")
}

func writeActionItemRebase(b *strings.Builder, opts BundleOptions) {
	b.WriteString("### Rebase Status\n\n")
	if opts.RebaseStatus != nil {
		trunk := ""
		if opts.Stack != nil {
			trunk = opts.Stack.Trunk
		}
		b.WriteString(opts.RebaseStatus.ActionItemMarkdown(trunk))
		return
	}

	if opts.Stack != nil {
		target := opts.Stack.PrevBranch
		if target == "" {
			target = opts.Stack.Trunk
		}
		if opts.Stack.NeedsRebase {
			fmt.Fprintf(b, "- **Needs rebase on `%s`** (run `gh stack rebase`)\n\n", target)
		} else {
			fmt.Fprintf(b, "- **Up-to-date with `%s`** (no rebase needed)\n\n", target)
		}
	} else if opts.Diff != nil && opts.Diff.BaseBranch != "" {
		fmt.Fprintf(b, "- **Up-to-date with `%s`** (no rebase needed)\n\n", opts.Diff.BaseBranch)
	}
}

// resolveCheckBucket normalizes a check's bucket, falling back to its state.
func resolveCheckBucket(c github.CheckRun) string {
	bucket := strings.ToLower(c.Bucket)
	if bucket == "" {
		bucket = strings.ToLower(c.State)
	}
	return bucket
}

func writeCIStatusSection(b *strings.Builder, ci *github.CIStatusRollup) {
	if ci == nil || ci.Total == 0 {
		return
	}

	b.WriteString("## CI Verification Status\n\n")
	b.WriteString(
		"> **Verification Policy**: Trust passing CI runs for automated checks (tests, linters, builds). Only manually run verification if CI is failing, missing, or testing unstaged/local changes.\n\n",
	)

	var summaryParts []string
	if ci.Fail > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d failing", ci.Fail))
	}
	if ci.Pass > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d passing", ci.Pass))
	}
	if ci.Pending > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d pending", ci.Pending))
	}
	if ci.Skip > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d skipped", ci.Skip))
	}

	fmt.Fprintf(b, "**Summary**: %d/%d passing", ci.Pass, ci.Total)
	if len(summaryParts) > 0 {
		fmt.Fprintf(b, " (%s)", strings.Join(summaryParts, ", "))
	}
	b.WriteString("\n\n")

	// Only failing checks and required pending checks are itemized — they gate
	// the merge or may indicate real breakage. Everything else (passing,
	// non-required pending, skipped) is collapsed to the summary counts so the
	// bundle stays high-signal.
	var failingChecks, pendingRequired []github.CheckRun
	optionalPending := 0
	for _, c := range ci.Checks {
		switch resolveCheckBucket(c) {
		case "fail", "failure", "error", "cancel":
			failingChecks = append(failingChecks, c)
		case "pending":
			if c.IsRequired {
				pendingRequired = append(pendingRequired, c)
			} else {
				optionalPending++
			}
		}
	}

	if len(failingChecks) > 0 || len(pendingRequired) > 0 || optionalPending > 0 {
		b.WriteString("### Checks Requiring Attention\n\n")
		for _, c := range failingChecks {
			writeSingleCheckRun(b, c, "FAIL")
		}
		for _, c := range pendingRequired {
			writeSingleCheckRun(b, c, "PENDING")
		}
		if optionalPending > 0 {
			fmt.Fprintf(b, "- PENDING optional checks (non-blocking): %d\n", optionalPending)
		}
		b.WriteString("\n")
	}
}

func writeSingleCheckRun(b *strings.Builder, c github.CheckRun, status string) {
	checkName := c.Name
	if c.Workflow != "" && !strings.HasPrefix(checkName, c.Workflow) {
		checkName = fmt.Sprintf("%s / %s", c.Workflow, c.Name)
	}

	linkPart := ""
	if c.Link != "" {
		linkPart = fmt.Sprintf(" - [View Run](%s)", c.Link)
	}

	stateDisplay := c.Bucket
	if stateDisplay == "" {
		stateDisplay = c.State
	}
	if c.IsRequired {
		stateDisplay += " — required"
	}

	fmt.Fprintf(b, "- %s `%s` (%s)%s\n", status, checkName, stateDisplay, linkPart)
}

func resolveRepoRoot(opts BundleOptions) string {
	if opts.RepoRoot != "" {
		return opts.RepoRoot
	}
	if opts.Diff != nil {
		return opts.Diff.RepoPath
	}
	return ""
}

func partitionFiles(diff *git.BranchDiffResult, threadCounts map[string]int) (
	semantic, formatting, deletion, unreadable []git.FileDiff,
	diffFileMap map[string]bool,
) {
	diffFileMap = make(map[string]bool)
	if diff == nil {
		return nil, nil, nil, nil, diffFileMap
	}
	ranked := RankFiles(diff.Files, threadCounts)
	for _, f := range ranked {
		diffFileMap[f.Path] = true
		c := ClassifyFile(f)
		switch c.Tier {
		case TierFormatting:
			formatting = append(formatting, f)
		case TierDeletion:
			deletion = append(deletion, f)
		case TierUnreadable:
			unreadable = append(unreadable, f)
		default:
			semantic = append(semantic, f)
		}
	}
	return semantic, formatting, deletion, unreadable, diffFileMap
}

func dumpFullPatch(diffsDir, filePath, patch string) string {
	if diffsDir == "" || filePath == "" || patch == "" {
		return ""
	}
	target := filepath.Join(diffsDir, filePath+".diff")
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return ""
	}
	if err := os.WriteFile(target, []byte(patch), 0o600); err != nil {
		return ""
	}
	return target
}

func writeSemanticSection(
	b *strings.Builder,
	files []git.FileDiff,
	fileThreads map[string][]*github.ReviewThread,
	lineThreads map[string]map[int][]*github.ReviewThread,
	maxLines int,
	diffsDir string,
	repoRoot string,
	assessments map[string]*ThreadAssessment,
) {
	if len(files) == 0 {
		return
	}
	if maxLines <= 0 {
		maxLines = 60
	}
	b.WriteString("## Semantic Changes\n\n")
	for _, f := range files {
		fmt.Fprintf(b, "### `%s` (+%d -%d)\n\n", f.Path, f.Additions, f.Deletions)

		if fThreads, ok := fileThreads[f.Path]; ok {
			for _, t := range fThreads {
				writeMarkdownThread(b, t, false, "", assessments[t.ID])
				b.WriteString("\n")
			}
			delete(fileThreads, f.Path)
		}

		if f.Patch != "" {
			switch {
			case git.IsDependencyOrLockfile(f.Path):
				b.WriteString("*Skipped: dependency/lockfile/generated*\n\n")
			case !git.IsCodeFile(f.Path) && f.Status == "added" && f.Additions > 40:
				dumpPath := dumpFullPatch(diffsDir, f.Path, f.Patch)
				if dumpPath != "" {
					relDisplay := dumpPath
					if repoRoot != "" {
						if rel, err := filepath.Rel(repoRoot, dumpPath); err == nil {
							relDisplay = filepath.ToSlash(rel)
						}
					}
					fmt.Fprintf(
						b,
						"*Skipped: %d-line config file*. Full diff dumped to: [`%s`](file://%s)\n\n",
						f.Additions,
						relDisplay,
						dumpPath,
					)
				} else {
					fmt.Fprintf(b, "*Skipped: %d-line config file*\n\n", f.Additions)
				}
			default:
				hasThreads := len(lineThreads[f.Path]) > 0
				patchLimit := maxLines
				if hasThreads {
					patchLimit = 0
				}
				formatMarkdownPatch(
					b,
					f.Patch,
					f.Path,
					lineThreads[f.Path],
					patchLimit,
					diffsDir,
					repoRoot,
					assessments,
				)
			}
		}
	}
}

func writeDeletedSection(b *strings.Builder, files []git.FileDiff, usages []ExternalUsage) {
	if len(files) == 0 {
		return
	}
	b.WriteString("## Deleted Files\n\n")
	for _, f := range files {
		fmt.Fprintf(b, "### [Deleted] `%s` (-%d lines)\n\n", f.Path, f.Deletions)

		baseName := filepath.Base(f.Path)
		var refs []lsp.Reference
		for _, u := range usages {
			if u.Symbol == baseName || u.Symbol == f.Path {
				refs = u.References
				break
			}
		}
		if len(refs) > 0 {
			fmt.Fprintf(b, "> **%d remaining references found outside diff:**\n", len(refs))
			for _, r := range refs {
				fmt.Fprintf(b, "> - `%s:%d`\n", r.File, r.Line)
			}
			b.WriteString(">\n\n")
		} else {
			b.WriteString("> Clean deletion: 0 remaining references found in codebase.\n\n")
		}
	}
}

func writeFormattingSection(b *strings.Builder, files []git.FileDiff) {
	if len(files) == 0 {
		return
	}
	b.WriteString("## Formatting & Low-Signal Changes\n\n")
	fmt.Fprintf(
		b,
		"The following %d files contain only formatting, whitespace, or import changes with no semantic logic changes:\n\n",
		len(files),
	)
	b.WriteString("| File | Category | Changes |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, f := range files {
		c := ClassifyFile(f)
		fmt.Fprintf(b, "| `%s` | %s | +%d -%d |\n", f.Path, c.Category, f.Additions, f.Deletions)
	}
	b.WriteString("\n")
}

func writeUnreadableSection(b *strings.Builder, files []git.FileDiff) {
	if len(files) == 0 {
		return
	}
	b.WriteString("## Unreadable Files\n\n")
	for _, f := range files {
		fmt.Fprintf(b, "### `%s` (unreadable in sandbox)\n\n", f.Path)
		b.WriteString(
			"> **Unreadable file**: Cannot access file on disk (sandbox read-deny or permission issue). Emitted as unreadable rather than deletion.\n\n",
		)
	}
}

func isLineInDiffHunks(patch string, line int) bool {
	if line <= 0 || patch == "" {
		return false
	}
	for l := range strings.SplitSeq(patch, "\n") {
		m := hunkHeaderRegex.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		oldStart, _ := strconv.Atoi(m[1])
		oldCount := 1
		if m[2] != "" {
			oldCount, _ = strconv.Atoi(m[2])
		}
		newStart, _ := strconv.Atoi(m[3])
		newCount := 1
		if m[4] != "" {
			newCount, _ = strconv.Atoi(m[4])
		}

		if oldCount > 0 && line >= oldStart && line < oldStart+oldCount {
			return true
		}
		if newCount > 0 && line >= newStart && line < newStart+newCount {
			return true
		}
	}
	return false
}

func writeUnresolvedThreadsSection(
	b *strings.Builder,
	fileThreads map[string][]*github.ReviewThread,
	lineThreads map[string]map[int][]*github.ReviewThread,
	notInterleaved []github.ReviewThread,
	diffFileMap map[string]bool,
	fileDiffMap map[string]*git.FileDiff,
	assessments map[string]*ThreadAssessment,
) {
	for _, threads := range fileThreads {
		for _, t := range threads {
			notInterleaved = append(notInterleaved, *t)
		}
	}
	for _, threadsByLine := range lineThreads {
		for _, threads := range threadsByLine {
			for _, t := range threads {
				notInterleaved = append(notInterleaved, *t)
			}
		}
	}

	if len(notInterleaved) == 0 {
		return
	}

	b.WriteString("## Unresolved Threads\n\n")
	slices.SortStableFunc(notInterleaved, func(a, b github.ReviewThread) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return a.Line - b.Line
	})
	lastPath := ""
	for i := range notInterleaved {
		t := &notInterleaved[i]
		if t.Path != "" && t.Path != lastPath {
			fmt.Fprintf(b, "### `%s`\n\n", t.Path)
			lastPath = t.Path
		}

		locationNote := ""
		if !diffFileMap[t.Path] {
			locationNote = "File is not in current diff (reverted, merged, or renamed)."
		} else if t.Line > 0 {
			fDiff := fileDiffMap[t.Path]
			if fDiff == nil || !isLineInDiffHunks(fDiff.Patch, t.Line) {
				locationNote = fmt.Sprintf("Thread anchored outside modified diff hunks (line %d).", t.Line)
			}
		}

		writeMarkdownThread(b, t, true, locationNote, assessments[t.ID])
		b.WriteString("\n")
	}
}

func buildInitialTOC(
	semanticFiles, deletionFiles, formattingFiles, unreadableFiles []git.FileDiff,
	threadCounts map[string]int,
	diffsDir string,
) []TOCEntry {
	var entries []TOCEntry
	for _, f := range semanticFiles {
		anchor := "#" + sanitizeAnchor(f.Path)
		diffPath := ""
		if diffsDir != "" && f.Patch != "" {
			diffPath = filepath.ToSlash(filepath.Join("diffs", f.Path+".diff"))
		}
		entries = append(entries, TOCEntry{
			Title:    fmt.Sprintf("`%s`", f.Path),
			Anchor:   anchor,
			Tier:     TierSemantic,
			Changes:  fmt.Sprintf("+%d -%d", f.Additions, f.Deletions),
			Threads:  threadCounts[f.Path],
			DiffPath: diffPath,
		})
	}
	for _, f := range deletionFiles {
		anchor := "#deleted-" + sanitizeAnchor(f.Path)
		diffPath := ""
		if diffsDir != "" && f.Patch != "" {
			diffPath = filepath.ToSlash(filepath.Join("diffs", f.Path+".diff"))
		}
		entries = append(entries, TOCEntry{
			Title:    fmt.Sprintf("`%s`", f.Path),
			Anchor:   anchor,
			Tier:     TierDeletion,
			Changes:  fmt.Sprintf("-%d", f.Deletions),
			DiffPath: diffPath,
		})
	}
	if len(formattingFiles) > 0 {
		entries = append(entries, TOCEntry{
			Title:   fmt.Sprintf("*Formatting & Imports (%d files)*", len(formattingFiles)),
			Anchor:  "#formatting--low-signal-changes",
			Tier:    TierFormatting,
			Changes: formatTotalChanges(formattingFiles),
		})
	}
	for _, f := range unreadableFiles {
		anchor := "#" + sanitizeAnchor(f.Path)
		entries = append(entries, TOCEntry{
			Title:   fmt.Sprintf("`%s`", f.Path),
			Anchor:  anchor,
			Tier:    TierUnreadable,
			Changes: "-",
		})
	}
	return entries
}

func sanitizeAnchor(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func formatTotalChanges(files []git.FileDiff) string {
	var adds, dels int
	for _, f := range files {
		adds += f.Additions
		dels += f.Deletions
	}
	return fmt.Sprintf("+%d -%d", adds, dels)
}

func writePRHeader(b *strings.Builder, pr *github.PR, diff *git.BranchDiffResult) {
	if pr != nil {
		if pr.Title != "" {
			fmt.Fprintf(b, "# PR #%d: %s\n\n", pr.Number, pr.Title)
		} else {
			fmt.Fprintf(b, "# PR #%d\n\n", pr.Number)
		}
		fmt.Fprintf(b, "- **Author**: @%s\n", pr.Author)
		fmt.Fprintf(b, "- **Branch**: `%s` -> `%s`\n", pr.HeadRef, pr.BaseRef)
		if pr.ReviewDecision != "" {
			fmt.Fprintf(b, "- **Review Decision**: %s\n", pr.ReviewDecision)
		}
		if pr.URL != "" {
			fmt.Fprintf(b, "- **URL**: %s\n", pr.URL)
		}
	} else if diff != nil {
		fmt.Fprintf(b, "# Branch: `%s` -> `%s`\n\n", diff.CurrentBranch, diff.BaseBranch)
	}

	if diff != nil {
		if diff.MergeBase != "" {
			fmt.Fprintf(b, "- **Merge-Base**: `%s`\n", diff.MergeBase)
		}
		if diff.Head != "" {
			fmt.Fprintf(b, "- **HEAD**: `%s`\n", diff.Head)
		}
		diffSrc := "local git"
		if diff.DiffSource != "" {
			diffSrc = diff.DiffSource
			if diffSrc == "local_git" {
				diffSrc = "local git"
			}
		}
		fmt.Fprintf(b, "- **Diff Source**: %s\n", diffSrc)
	}
}

func formatDiffStats(diff *git.BranchDiffResult) string {
	if diff == nil {
		return "no changes"
	}
	var codeAdd, codeDel, codeCount int
	var toolAdd, toolDel, toolCount int
	for _, f := range diff.Files {
		if git.IsCodeFile(f.Path) {
			codeCount++
			codeAdd += f.Additions
			codeDel += f.Deletions
		} else {
			toolCount++
			toolAdd += f.Additions
			toolDel += f.Deletions
		}
	}

	var statsParts []string
	if codeCount > 0 {
		statsParts = append(statsParts, fmt.Sprintf("code: +%d -%d (%d files)", codeAdd, codeDel, codeCount))
	}
	if toolCount > 0 {
		statsParts = append(statsParts, fmt.Sprintf("tooling: +%d -%d (%d files)", toolAdd, toolDel, toolCount))
	}
	if len(statsParts) == 0 {
		return "no changes"
	}
	return strings.Join(statsParts, ", ")
}

func writeStackSection(b *strings.Builder, stack *StackInfo) {
	fmt.Fprintf(b, "## Stack (Layer %d of %d)\n\n", stack.Position, stack.Total)
	if stack.Trunk != "" {
		fmt.Fprintf(b, "- **Trunk**: `%s`\n", stack.Trunk)
	}
	if stack.PrevBranch != "" {
		fmt.Fprintf(b, "- **Base Layer**: `%s`\n", stack.PrevBranch)
	}
	if stack.NextBranch != "" {
		fmt.Fprintf(b, "- **Next Layer**: `%s`\n", stack.NextBranch)
	}

	target := stack.PrevBranch
	if target == "" {
		target = stack.Trunk
	}
	switch {
	case stack.RebaseStatus != nil:
		b.WriteString(stack.RebaseStatus.StackNoticeMarkdown(stack.Trunk))
	case stack.NeedsRebase:
		fmt.Fprintf(b, "\n> **Warning: Branch needs rebase on `%s`** (run `gh stack rebase`)\n", target)
	case target != "":
		fmt.Fprintf(b, "\n> **Rebase Status**: Up-to-date with `%s` (no rebase needed)\n", target)
	}

	if len(stack.Layers) > 0 {
		b.WriteString("\n```mermaid\nflowchart LR\n")
		for i, layer := range stack.Layers {
			var labelParts []string
			labelParts = append(labelParts, fmt.Sprintf("%d:", layer.Position))
			if layer.PRNumber > 0 {
				labelParts = append(labelParts, fmt.Sprintf("#%d", layer.PRNumber))
			}
			labelParts = append(labelParts, layer.Branch)
			if layer.IsCurrent {
				labelParts = append(labelParts, "(current)")
			}
			nodeLabel := strings.Join(labelParts, " ")
			nodeID := fmt.Sprintf("L%d", i+1)
			fmt.Fprintf(b, "  %s[%q]\n", nodeID, nodeLabel)
		}
		for i := 0; i < len(stack.Layers)-1; i++ {
			fmt.Fprintf(b, "  L%d --> L%d\n", i+1, i+2)
		}
		b.WriteString("```\n\n")
	}
}

func collectThreads(
	pr *github.PR,
	includeResolved bool,
) (map[string][]*github.ReviewThread, map[string]map[int][]*github.ReviewThread, []github.ReviewThread) {
	fileThreads := make(map[string][]*github.ReviewThread)
	lineThreads := make(map[string]map[int][]*github.ReviewThread)
	var notInterleaved []github.ReviewThread

	if pr == nil {
		return fileThreads, lineThreads, notInterleaved
	}

	for i := range pr.Threads {
		t := &pr.Threads[i]
		if t.IsResolved && !includeResolved {
			continue
		}
		if t.Path == "" {
			notInterleaved = append(notInterleaved, *t)
			continue
		}
		if t.IsFileLevel() {
			fileThreads[t.Path] = append(fileThreads[t.Path], t)
			continue
		}
		if t.Line == 0 {
			if t.OriginalLine > 0 {
				t.Line = t.OriginalLine
			} else {
				notInterleaved = append(notInterleaved, *t)
				continue
			}
		}
		if lineThreads[t.Path] == nil {
			lineThreads[t.Path] = make(map[int][]*github.ReviewThread)
		}
		lineThreads[t.Path][t.Line] = append(lineThreads[t.Path][t.Line], t)
	}

	return fileThreads, lineThreads, notInterleaved
}

func emitMatchingThreads(
	b *strings.Builder,
	inCodeFence *bool,
	threads map[int][]*github.ReviewThread,
	assessments map[string]*ThreadAssessment,
	lineNum int,
	side string,
) {
	ts, ok := threads[lineNum]
	if !ok || len(ts) == 0 {
		return
	}
	var matched []*github.ReviewThread
	var remaining []*github.ReviewThread
	for _, t := range ts {
		matches := false
		switch side {
		case "LEFT":
			matches = t.DiffSide == "LEFT" || t.DiffSide == ""
		case "RIGHT":
			matches = t.DiffSide == "RIGHT" || t.DiffSide == ""
		case "BOTH":
			matches = true
		}
		if matches {
			matched = append(matched, t)
		} else {
			remaining = append(remaining, t)
		}
	}
	if len(matched) == 0 {
		return
	}
	if *inCodeFence {
		b.WriteString("```\n\n")
		*inCodeFence = false
	}
	for _, t := range matched {
		var asm *ThreadAssessment
		if assessments != nil {
			asm = assessments[t.ID]
		}
		writeMarkdownThread(b, t, false, "", asm)
	}
	if len(remaining) > 0 {
		threads[lineNum] = remaining
	} else {
		delete(threads, lineNum)
	}
}

func writePatchTruncationNotice(
	b *strings.Builder,
	diffsDir string,
	filePath string,
	patch string,
	repoRoot string,
	maxLines int,
	remainingCount int,
) {
	dumpPath := dumpFullPatch(diffsDir, filePath, patch)
	if dumpPath != "" {
		relDisplay := dumpPath
		if repoRoot != "" {
			if rel, err := filepath.Rel(repoRoot, dumpPath); err == nil {
				relDisplay = filepath.ToSlash(rel)
			}
		}
		fmt.Fprintf(
			b,
			"> **[Diff truncated at %d lines — %d lines remaining]**\n> Full diff dumped to: [`%s`](file://%s)\n\n",
			maxLines,
			remainingCount,
			relDisplay,
			dumpPath,
		)
	} else {
		fmt.Fprintf(
			b,
			"> **[Diff truncated at %d lines — %d lines remaining; inspect file or run `git diff -U5 -- %s`]**\n\n",
			maxLines,
			remainingCount,
			filePath,
		)
	}
}

func formatMarkdownPatch(
	b *strings.Builder,
	patch string,
	filePath string,
	threads map[int][]*github.ReviewThread,
	maxLines int,
	diffsDir string,
	repoRoot string,
	assessments map[string]*ThreadAssessment,
) {
	if maxLines <= 0 {
		maxLines = 400
	}
	oldLine := 0
	newLine := 0
	inCodeFence := false
	renderedLines := 0

	lines := strings.Split(patch, "\n")
	for i, line := range lines {
		if git.SkipPatchLine(line) {
			continue
		}

		if renderedLines >= maxLines {
			remainingCount := len(lines) - i
			if inCodeFence {
				b.WriteString("```\n\n")
			}
			writePatchTruncationNotice(b, diffsDir, filePath, patch, repoRoot, maxLines, remainingCount)
			return
		}
		renderedLines++

		if m := hunkHeaderRegex.FindStringSubmatch(line); m != nil {
			oldLine, _ = strconv.Atoi(m[1])
			newLine, _ = strconv.Atoi(m[3])
			if !inCodeFence {
				b.WriteString("```diff\n")
				inCodeFence = true
			}
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}

		if len(line) > 0 {
			char := line[0]
			switch char {
			case ' ':
				if !inCodeFence {
					b.WriteString("```diff\n")
					inCodeFence = true
				}
				b.WriteString(line)
				b.WriteByte('\n')

				emitMatchingThreads(b, &inCodeFence, threads, assessments, newLine, "RIGHT")
				if oldLine != newLine {
					emitMatchingThreads(b, &inCodeFence, threads, assessments, oldLine, "LEFT")
				}
				oldLine++
				newLine++
			case '+':
				if !inCodeFence {
					b.WriteString("```diff\n")
					inCodeFence = true
				}
				b.WriteString(line)
				b.WriteByte('\n')

				emitMatchingThreads(b, &inCodeFence, threads, assessments, newLine, "RIGHT")
				newLine++
			case '-':
				if !inCodeFence {
					b.WriteString("```diff\n")
					inCodeFence = true
				}
				b.WriteString(line)
				b.WriteByte('\n')

				emitMatchingThreads(b, &inCodeFence, threads, assessments, oldLine, "LEFT")
				oldLine++
			default:
				if inCodeFence {
					b.WriteString(line)
					b.WriteByte('\n')
				}
			}
		} else {
			if !inCodeFence {
				b.WriteString("```diff\n")
				inCodeFence = true
			}
			b.WriteByte('\n')
			emitMatchingThreads(b, &inCodeFence, threads, assessments, newLine, "RIGHT")
			if oldLine != newLine {
				emitMatchingThreads(b, &inCodeFence, threads, assessments, oldLine, "LEFT")
			}
			oldLine++
			newLine++
		}
	}

	if inCodeFence {
		b.WriteString("```\n\n")
	}
}

func writeMarkdownThread(
	b *strings.Builder,
	t *github.ReviewThread,
	includeDiffHunk bool,
	locationNote string,
	assessment *ThreadAssessment,
) {
	if len(t.Comments) == 0 {
		return
	}

	if assessment != nil {
		switch assessment.Status {
		case StatusResolvedByCommit:
			fmt.Fprintf(b, "> **Status**: %s\n>\n", assessment.Verdict)
		case StatusOutdated:
			fmt.Fprintf(b, "> **Outdated (Verify)**: %s\n>\n", assessment.Verdict)
		default:
			fmt.Fprintf(b, "> **Status**: %s\n>\n", assessment.Verdict)
		}
	} else {
		b.WriteString("> **Status**: Unresolved: comment requires attention\n>\n")
	}

	if locationNote != "" {
		fmt.Fprintf(b, "> **Note**: %s\n>\n", locationNote)
	}

	var lineInfo string
	switch {
	case t.IsFileLevel():
		lineInfo = "file comment"
	case t.StartLine > 0 && t.StartLine != t.Line:
		lineInfo = fmt.Sprintf("lines %d-%d", t.StartLine, t.Line)
	case t.Line > 0:
		lineInfo = fmt.Sprintf("line %d", t.Line)
	default:
		lineInfo = "general comment"
	}

	for _, c := range t.Comments {
		fmt.Fprintf(b, "> **@%s** (%s):\n", c.Author, lineInfo)
		for bodyLine := range strings.SplitSeq(github.StripBloat(c.Body), "\n") {
			fmt.Fprintf(b, "> %s\n", bodyLine)
		}
		writeMarkdownSuggestions(b, c)
		b.WriteString(">\n")
	}

	if includeDiffHunk {
		if hunk := threadDiffHunk(t); hunk != "" {
			b.WriteString("> ```diff\n")
			for hunkLine := range strings.SplitSeq(strings.TrimRight(hunk, "\n"), "\n") {
				fmt.Fprintf(b, "> %s\n", hunkLine)
			}
			b.WriteString("> ```\n")
		}
	}
}

func threadDiffHunk(t *github.ReviewThread) string {
	for _, c := range t.Comments {
		if c.DiffHunk != "" {
			return c.DiffHunk
		}
	}
	return ""
}

func writeMarkdownSuggestions(b *strings.Builder, c github.Comment) {
	for _, s := range c.Suggestions {
		b.WriteString(">\n> ```suggestion\n")
		for sugLine := range strings.SplitSeq(s.Code, "\n") {
			fmt.Fprintf(b, "> %s\n", sugLine)
		}
		b.WriteString("> ```\n")
	}
}

// Filename returns the output filename for a review bundle.
func Filename(pr *github.PR, diff *git.BranchDiffResult) string {
	if pr != nil {
		return fmt.Sprintf("pr-%d_review.md", pr.Number)
	}
	safeCurrent := strings.ReplaceAll(diff.CurrentBranch, "/", "-")
	safeBase := strings.ReplaceAll(diff.BaseBranch, "/", "-")
	return fmt.Sprintf("%s_%s_review.md", safeCurrent, safeBase)
}

var nonAlphaNumRe = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// Slugify converts a string to a clean URL/path-friendly slug.
func Slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlphaNumRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// ShortTitleSlug converts a title string into a compact, filesystem-safe slug
// capped at maxLen characters, cleanly breaking at hyphen boundaries when possible.
func ShortTitleSlug(s string, maxLen int) string {
	slug := Slugify(s)
	if maxLen > 0 && len(slug) > maxLen {
		truncated := slug[:maxLen]
		if lastDash := strings.LastIndex(truncated, "-"); lastDash > maxLen/2 {
			truncated = truncated[:lastDash]
		}
		slug = strings.Trim(truncated, "-")
	}
	return slug
}

// Dir returns the directory name for review outputs: "[number]-short-title".
// If pr is nil, it falls back to the sanitized diff branches.
func Dir(pr *github.PR, diff *git.BranchDiffResult) string {
	if pr != nil {
		title := pr.Title
		if title == "" && diff != nil && diff.CurrentBranch != "" {
			title = diff.CurrentBranch
		}
		slug := ShortTitleSlug(title, 40)
		switch {
		case pr.Number > 0 && slug != "":
			return fmt.Sprintf("%d-%s", pr.Number, slug)
		case pr.Number > 0:
			return fmt.Sprintf("%d", pr.Number)
		case slug != "":
			return slug
		}
	}
	if diff != nil {
		safeCurrent := strings.ReplaceAll(diff.CurrentBranch, "/", "-")
		safeBase := strings.ReplaceAll(diff.BaseBranch, "/", "-")
		return fmt.Sprintf("%s_%s_review", safeCurrent, safeBase)
	}
	return "review"
}

// resolveRebaseStatus returns opts.RebaseStatus when set. Otherwise it assesses
// the branch against its target when a repo or the PR's mergeable state is
// available, and copies the result onto opts.Stack. With neither, it falls back
// to the stack's own status.
func resolveRebaseStatus(opts BundleOptions, repoRoot string) *RebaseStatus {
	if opts.RebaseStatus != nil {
		return opts.RebaseStatus
	}
	if repoRoot == "" && (opts.PR == nil || opts.PR.Mergeable == "") {
		if opts.Stack != nil {
			return opts.Stack.RebaseStatus
		}
		return nil
	}

	status := evaluateBundleRebaseStatus(opts, repoRoot)
	if opts.Stack != nil && status != nil {
		opts.Stack.RebaseStatus = status
		opts.Stack.NeedsRebase = status.IsIssue
	}
	return status
}

func evaluateBundleRebaseStatus(opts BundleOptions, repoRoot string) *RebaseStatus {
	targetBranch := ""
	trunk := ""
	isBaseMerged := false
	headBranch := ""

	switch {
	case opts.Stack != nil:
		trunk = opts.Stack.Trunk
		targetBranch = opts.Stack.PrevBranch
		if targetBranch == "" {
			targetBranch = opts.Stack.Trunk
		}
		if opts.Stack.Position > 1 && len(opts.Stack.Layers) >= opts.Stack.Position {
			prevLayer := opts.Stack.Layers[opts.Stack.Position-2]
			if strings.EqualFold(prevLayer.State, "MERGED") {
				isBaseMerged = true
			}
		}
	case opts.Diff != nil && opts.Diff.BaseBranch != "":
		targetBranch = opts.Diff.BaseBranch
	case opts.PR != nil && opts.PR.BaseRef != "":
		targetBranch = opts.PR.BaseRef
	}

	if targetBranch == "" {
		return nil
	}

	switch {
	case opts.Diff != nil && opts.Diff.CurrentBranch != "":
		headBranch = opts.Diff.CurrentBranch
	case opts.PR != nil && opts.PR.HeadRef != "":
		headBranch = opts.PR.HeadRef
	default:
		headBranch = "HEAD"
	}

	var localMerge *git.Mergeability
	if repoRoot != "" {
		localMerge, _ = git.CheckMergeability(repoRoot, targetBranch, headBranch)
	}

	prMergeable := ""
	if opts.PR != nil {
		prMergeable = opts.PR.Mergeable
	}

	return AssessRebaseStatus(RebaseAssessmentOptions{
		TargetBranch: targetBranch,
		Trunk:        trunk,
		IsBaseMerged: isBaseMerged,
		LocalMerge:   localMerge,
		PRMergeable:  prMergeable,
	})
}
