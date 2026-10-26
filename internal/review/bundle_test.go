package review

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/internal/review/lsp"
	"github.com/kalverra/agents/pkg/git"
)

func TestResolveStackInfo_FromPR(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:  102,
		HeadRef: "feat/api",
		BaseRef: "feat/auth",
		Stack: &github.PRStack{
			ID:          "stack_1",
			Number:      1,
			Size:        3,
			BaseRefName: "main",
			Entries: []github.PRStackEntry{
				{
					Position:    1,
					Number:      101,
					Title:       "Add auth",
					HeadRefName: "feat/auth",
					BaseRefName: "main",
					State:       "MERGED",
				},
				{
					Position:    2,
					Number:      102,
					Title:       "Add api",
					HeadRefName: "feat/api",
					BaseRefName: "feat/auth",
					State:       "OPEN",
				},
				{
					Position:    3,
					Number:      103,
					Title:       "Add ui",
					HeadRefName: "feat/ui",
					BaseRefName: "feat/api",
					State:       "OPEN",
				},
			},
		},
	}

	stack := ResolveStackInfo(pr, nil, "feat/api")
	require.NotNil(t, stack)
	assert.Equal(t, 2, stack.Position)
	assert.Equal(t, 3, stack.Total)
	assert.Equal(t, "main", stack.Trunk)
	assert.Equal(t, "feat/auth", stack.PrevBranch)
	assert.Equal(t, "feat/ui", stack.NextBranch)
	assert.False(t, stack.NeedsRebase)
	require.Len(t, stack.Layers, 3)
	assert.True(t, stack.Layers[1].IsCurrent)
	assert.Equal(t, 102, stack.Layers[1].PRNumber)
}

func TestResolveStackInfo_FromLocalGit(t *testing.T) {
	t.Parallel()

	localStack := &git.StackData{
		Trunk:         "main",
		CurrentBranch: "feat/api",
		Branches: []git.StackBranch{
			{Name: "feat/auth", IsMerged: true, PR: &git.StackPR{Number: 101, State: "MERGED"}},
			{Name: "feat/api", IsCurrent: true, NeedsRebase: true, PR: &git.StackPR{Number: 102, State: "OPEN"}},
			{Name: "feat/ui", PR: &git.StackPR{Number: 103, State: "OPEN"}},
		},
	}

	stack := ResolveStackInfo(nil, localStack, "feat/api")
	require.NotNil(t, stack)
	assert.Equal(t, 2, stack.Position)
	assert.Equal(t, 3, stack.Total)
	assert.Equal(t, "main", stack.Trunk)
	assert.Equal(t, "feat/auth", stack.PrevBranch)
	assert.Equal(t, "feat/ui", stack.NextBranch)
	assert.True(t, stack.NeedsRebase)
	require.Len(t, stack.Layers, 3)
	assert.True(t, stack.Layers[1].IsCurrent)
}

func TestResolveStackInfo_MergedRemoteAndLocal(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:  102,
		HeadRef: "feat/api",
		BaseRef: "feat/auth",
		Stack: &github.PRStack{
			Size:        2,
			BaseRefName: "main",
			Entries: []github.PRStackEntry{
				{Position: 1, Number: 101, Title: "Add auth", HeadRefName: "feat/auth"},
				{Position: 2, Number: 102, Title: "Add api", HeadRefName: "feat/api"},
			},
		},
	}

	localStack := &git.StackData{
		Trunk:         "main",
		CurrentBranch: "feat/api",
		Branches: []git.StackBranch{
			{Name: "feat/auth"},
			{Name: "feat/api", IsCurrent: true, NeedsRebase: true},
		},
	}

	stack := ResolveStackInfo(pr, localStack, "feat/api")
	require.NotNil(t, stack)
	assert.True(t, stack.NeedsRebase)
	assert.Equal(t, "Add auth", stack.Layers[0].PRTitle)
}

func TestResolveStackInfo_NilWhenNotStacked(t *testing.T) {
	t.Parallel()

	pr := &github.PR{Number: 42, HeadRef: "patch-1", BaseRef: "main"}
	stack := ResolveStackInfo(pr, nil, "patch-1")
	assert.Nil(t, stack)
}

func TestFormatBundleWithPR(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:         42,
		Title:          "Fix things",
		URL:            "https://github.com/o/r/pull/42",
		Author:         "alice",
		BaseRef:        "main",
		HeadRef:        "feature",
		ReviewDecision: "REVIEW_REQUIRED",
		Body:           "Resolves user bug",
		Threads: []github.ReviewThread{
			{
				Path: "main.go",
				Line: 10,
				Comments: []github.Comment{
					{Author: "bob", Body: "nit: rename var", CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
				},
			},
		},
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:      "main.go",
				Status:    "modified",
				Additions: 1,
				Deletions: 1,
				Patch:     "diff --git a/main.go b/main.go\n@@ -1 +1 @@\n-old\n+new\n",
			},
		},
	}

	out := FormatBundle(pr, diff, nil, false, nil)

	assert.Contains(t, out, "# PR #42: Fix things")
	assert.Contains(t, out, "**Author**: @alice")
	assert.Contains(t, out, "**Branch**: `feature` -> `main`")
	assert.Contains(t, out, "Resolves user bug")
	assert.Contains(t, out, "### `main.go` (+1 -1)")
	assert.Contains(t, out, "```diff")
	assert.Contains(t, out, "-old")
	assert.Contains(t, out, "+new")
	assert.Contains(t, out, "> **@bob** (line 10):")
	assert.Contains(t, out, "nit: rename var")
}

func TestFormatBundleWithStack(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:         102,
		Title:          "API routes",
		URL:            "https://github.com/o/r/pull/102",
		Author:         "alice",
		BaseRef:        "feat/auth",
		HeadRef:        "feat/api",
		ReviewDecision: "REVIEW_REQUIRED",
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "feat/auth",
		CurrentBranch: "feat/api",
		Files: []git.FileDiff{
			{
				Path:      "api.go",
				Status:    "added",
				Additions: 5,
				Patch:     "diff --git a/api.go b/api.go\n@@ -0,0 +1,5 @@\n+package api\n",
			},
		},
	}

	stack := &StackInfo{
		Position:    2,
		Total:       3,
		Trunk:       "main",
		NeedsRebase: true,
		PrevBranch:  "feat/auth",
		NextBranch:  "feat/ui",
		Layers: []StackLayer{
			{Position: 1, Branch: "feat/auth", PRNumber: 101, State: "MERGED", PRTitle: "Auth setup"},
			{Position: 2, Branch: "feat/api", PRNumber: 102, State: "OPEN", PRTitle: "API routes", IsCurrent: true},
			{Position: 3, Branch: "feat/ui", PRNumber: 103, State: "OPEN", PRTitle: "Frontend UI"},
		},
	}

	out := FormatBundle(pr, diff, stack, false, nil)

	assert.Contains(t, out, "## Stack (Layer 2 of 3)")
	assert.Contains(t, out, "**Warning: Branch needs rebase on `feat/auth`**")
	assert.Contains(t, out, "```mermaid")
	assert.Contains(t, out, "flowchart LR")
	assert.Contains(t, out, "1: #101 feat/auth")
	assert.Contains(t, out, "2: #102 feat/api (current)")
	assert.Contains(t, out, "3: #103 feat/ui")
}

func TestFormatBundleWithSuggestionAndDiffHunk(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:  22873,
		Author:  "alice",
		BaseRef: "develop",
		HeadRef: "feature",
		Threads: []github.ReviewThread{
			{
				Path:      "core/engine.go",
				StartLine: 1174,
				Line:      1178,
				Comments: []github.Comment{
					{
						Author: "Copilot",
						Body:   "keep a deadline after WithoutCancel",
						Suggestions: []github.Suggestion{
							{
								Source: github.SuggestionSourceAutomated,
								Code:   "ctx = context.WithoutCancel(ctx)\nif deadline, ok := ctx.Deadline(); ok {\n\tctx, _ = context.WithDeadline(ctx, deadline)\n}",
							},
						},
						DiffHunk: "@@ -10 +10 @@\n+ctx = context.WithoutCancel(ctx)",
					},
				},
			},
		},
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "develop",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:      "core/engine.go",
				Status:    "modified",
				Additions: 1,
				Deletions: 0,
				Patch:     "diff --git a/core/engine.go b/core/engine.go\n@@ -1174,5 +1174,6 @@\n+ctx = context.WithoutCancel(ctx)\n",
			},
		},
	}

	out := FormatBundle(pr, diff, nil, false, nil)

	assert.Contains(t, out, "```suggestion")
	assert.Contains(t, out, "context.WithDeadline")
	assert.Contains(t, out, "keep a deadline after WithoutCancel")
}

func TestFormatBundleWithoutPR(t *testing.T) {
	t.Parallel()

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:      "main.go",
				Status:    "modified",
				Additions: 1,
				Deletions: 0,
				Patch:     "diff --git a/main.go b/main.go\n@@ -0,0 +1 @@\n+new\n",
			},
		},
	}

	out := FormatBundle(nil, diff, nil, false, nil)

	assert.Contains(t, out, "# Branch: `feature` -> `main`")
	assert.Contains(t, out, "### `main.go` (+1 -0)")
	assert.Contains(t, out, "```diff")
	assert.Contains(t, out, "+new")
}

func TestFilename(t *testing.T) {
	t.Parallel()

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feature/foo",
	}

	name := Filename(&github.PR{Number: 99}, diff)
	require.Equal(t, "pr-99_review.md", name)

	name = Filename(nil, diff)
	require.Equal(t, "feature-foo_main_review.md", name)
}

func TestFormatBundle_FileComment_OnDiffFile(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:  23506,
		Author:  "author",
		BaseRef: "develop",
		HeadRef: "feature",
		Threads: []github.ReviewThread{
			{
				Path:        "core/web/assets/index.html",
				SubjectType: "FILE",
				Line:        1,
				Comments: []github.Comment{
					{Author: "jmank88", Body: "Did you bump operator-ui?"},
				},
			},
		},
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "develop",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:      "core/web/assets/index.html",
				Status:    "modified",
				Additions: 5,
				Deletions: 2,
				Patch:     "diff --git a/core/web/assets/index.html b/core/web/assets/index.html\n@@ -50,6 +50,9 @@\n+<script src=\"app.js\"></script>\n",
			},
		},
	}

	out := FormatBundle(pr, diff, nil, false, nil)

	assert.Contains(t, out, "### `core/web/assets/index.html` (+5 -2)")
	assert.Contains(t, out, "> **@jmank88** (file comment):")
	assert.Contains(t, out, "Did you bump operator-ui?")
	assert.NotContains(t, out, "(line 1)")
	assert.NotContains(t, out, "## Unresolved Threads")

	commentIdx := strings.Index(out, "> **@jmank88** (file comment):")
	diffIdx := strings.Index(out, "```diff")
	require.NotEqual(t, -1, commentIdx)
	require.NotEqual(t, -1, diffIdx)
	assert.Less(t, commentIdx, diffIdx, "file comment should appear before diff hunk")
}

func TestFormatBundle_FileComment_OnFileNotInDiff(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:  23506,
		Author:  "author",
		BaseRef: "develop",
		HeadRef: "feature",
		Threads: []github.ReviewThread{
			{
				Path:        "core/web/assets/index.html",
				SubjectType: "FILE",
				Line:        1,
				Comments: []github.Comment{
					{Author: "jmank88", Body: "Did you bump operator-ui?"},
				},
			},
		},
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "develop",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:      "other/file.go",
				Status:    "modified",
				Additions: 1,
				Deletions: 0,
				Patch:     "diff --git a/other/file.go b/other/file.go\n@@ -1,1 +1,2 @@\n+var x = 1\n",
			},
		},
	}

	out := FormatBundle(pr, diff, nil, false, nil)

	assert.Contains(t, out, "## Unresolved Threads")
	assert.Contains(t, out, "### `core/web/assets/index.html`")
	assert.Contains(t, out, "> **@jmank88** (file comment):")
	assert.Contains(t, out, "Did you bump operator-ui?")
	assert.NotContains(t, out, "(line 1)")
}

func TestFormatBundle_FileComment_LineZeroFallback(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:  100,
		Author:  "author",
		BaseRef: "main",
		HeadRef: "docs",
		Threads: []github.ReviewThread{
			{
				Path: "README.md",
				Line: 0,
				Comments: []github.Comment{
					{Author: "reviewer", Body: "Please update docs"},
				},
			},
		},
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "docs",
		Files: []git.FileDiff{
			{
				Path:      "README.md",
				Status:    "modified",
				Additions: 2,
				Deletions: 0,
				Patch:     "diff --git a/README.md b/README.md\n@@ -10,1 +10,3 @@\n+new doc\n",
			},
		},
	}

	out := FormatBundle(pr, diff, nil, false, nil)

	assert.Contains(t, out, "> **@reviewer** (file comment):")
	assert.NotContains(t, out, "(line 0)")
	assert.NotContains(t, out, "## Unresolved Threads")
}

func TestFormatBundle_LineComment_NotInDiff_ShowsFilePath(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:  200,
		Author:  "author",
		BaseRef: "main",
		HeadRef: "feature",
		Threads: []github.ReviewThread{
			{
				Path:        "service.go",
				SubjectType: "LINE",
				Line:        42,
				Comments: []github.Comment{
					{Author: "reviewer", Body: "Check error handling"},
				},
			},
		},
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feature",
		Files:         []git.FileDiff{},
	}

	out := FormatBundle(pr, diff, nil, false, nil)

	assert.Contains(t, out, "## Unresolved Threads")
	assert.Contains(t, out, "### `service.go`")
	assert.Contains(t, out, "> **@reviewer** (line 42):")
	assert.Contains(t, out, "Check error handling")
}

func TestFormatBundle_FileComment_Resolved_Filter(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:  300,
		Author:  "author",
		BaseRef: "main",
		HeadRef: "feature",
		Threads: []github.ReviewThread{
			{
				Path:        "index.html",
				SubjectType: "FILE",
				Line:        1,
				IsResolved:  true,
				Comments: []github.Comment{
					{Author: "reviewer", Body: "Resolved file note"},
				},
			},
		},
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:      "index.html",
				Status:    "modified",
				Additions: 1,
				Patch:     "diff --git a/index.html b/index.html\n@@ -1,1 +1,2 @@\n+line\n",
			},
		},
	}

	outWithout := FormatBundle(pr, diff, nil, false, nil)
	assert.NotContains(t, outWithout, "Resolved file note")

	outWith := FormatBundle(pr, diff, nil, true, nil)
	assert.Contains(t, outWith, "Resolved file note")
	assert.Contains(t, outWith, "> **@reviewer** (file comment):")
}

func TestFormatBundle_HeaderTraceability(t *testing.T) {
	t.Parallel()

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feature",
		MergeBase:     "abc1234",
		Head:          "def5678",
		DiffSource:    "local_git",
	}

	out := FormatBundle(nil, diff, nil, false, nil)
	assert.Contains(t, out, "- **Merge-Base**: `abc1234`")
	assert.Contains(t, out, "- **HEAD**: `def5678`")
	assert.Contains(t, out, "- **Diff Source**: local git")
}

func TestFormatBundle_TieredSections(t *testing.T) {
	t.Parallel()

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feature",
		MergeBase:     "abc1234",
		Head:          "def5678",
		Files: []git.FileDiff{
			{
				Path:      "core/router.go",
				Status:    "modified",
				Additions: 5,
				Deletions: 2,
				Patch:     "@@ -10,2 +10,5 @@\n-oldRoute\n+newRoute\n",
			},
			{
				Path:      "pkg/imports.go",
				Status:    "modified",
				Additions: 2,
				Deletions: 2,
				Patch:     "@@ -1,2 +1,2 @@\n-import \"b\"\n+import \"a\"\n",
			},
			{
				Path:      "old_helper.go",
				Status:    "deleted",
				Deletions: 30,
			},
			{
				Path:   "tools/docker/.env",
				Status: "unreadable",
			},
		},
	}

	out := FormatBundle(nil, diff, nil, false, nil)

	// TOC should be present and list all files
	assert.Contains(t, out, "## Table of Contents")
	assert.Contains(t, out, "core/router.go")
	assert.Contains(t, out, "pkg/imports.go")
	assert.Contains(t, out, "old_helper.go")
	assert.Contains(t, out, "tools/docker/.env")

	// Semantic changes should render router diff
	assert.Contains(t, out, "## Semantic Changes")
	assert.Contains(t, out, "### `core/router.go` (+5 -2)")
	assert.Contains(t, out, "+newRoute")

	// Formatting changes should render summary table, not full diff
	assert.Contains(t, out, "## Formatting & Low-Signal Changes")
	assert.Contains(t, out, "`pkg/imports.go`")
	assert.NotContains(t, out, "+import \"a\"")

	// Deleted files section
	assert.Contains(t, out, "## Deleted Files")
	assert.Contains(t, out, "### [Deleted] `old_helper.go` (-30 lines)")

	// Unreadable files section
	assert.Contains(t, out, "## Unreadable Files")
	assert.Contains(t, out, "### `tools/docker/.env` (unreadable in sandbox)")
	assert.Contains(t, out, "Cannot access file on disk")
	assert.Contains(t, out, "sandbox read-deny")
}

func TestFormatBundle_OutdatedThreadBadges(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number: 1,
		Threads: []github.ReviewThread{
			{
				Path: "removed_file.go",
				Line: 10,
				Comments: []github.Comment{
					{Author: "alice", Body: "File comment on removed file"},
				},
			},
			{
				Path: "modified.go",
				Line: 999, // Line not in current diff hunk
				Comments: []github.Comment{
					{Author: "bob", Body: "Line comment on line outside diff"},
				},
			},
		},
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:      "modified.go",
				Status:    "modified",
				Additions: 1,
				Patch:     "@@ -10,1 +10,2 @@\n+line\n",
			},
		},
	}

	out := FormatBundle(pr, diff, nil, false, nil)
	assert.Contains(t, out, "## Unresolved Threads")
	assert.NotContains(t, out, "**Outdated Thread**")
	assert.Contains(t, out, "> **Status**: Unresolved: comment requires attention")
	assert.Contains(t, out, "> **Note**: File is not in current diff (reverted, merged, or renamed).")
	assert.Contains(t, out, "> **Note**: Thread anchored outside modified diff hunks (line 999).")
}

func TestFormatBundle_DeletedLineThread_InterleavesInline(t *testing.T) {
	t.Parallel()

	// Replicates PR smartcontractkit/chainlink#23506 incident
	patch := `@@ -26,13 +26,6 @@ package router
 line26
 line27
 line28
-line29
-line30
-line31
-line32
-line33
-line34
-line35
 line36
 line37
 line38
`

	pr := &github.PR{
		Number: 23506,
		Threads: []github.ReviewThread{
			{
				ID:       "thread_del_35",
				Path:     "system-tests/lib/cre/chiprouter/router.go",
				Line:     35,
				DiffSide: "LEFT",
				Comments: []github.Comment{
					{Author: "Tofel", Body: "why?"},
				},
			},
		},
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "develop",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:      "system-tests/lib/cre/chiprouter/router.go",
				Status:    "modified",
				Additions: 0,
				Deletions: 7,
				Patch:     patch,
			},
		},
	}

	out := FormatBundle(pr, diff, nil, false, nil)
	// Must interleave inline in Semantic Changes, not relegated to Unresolved Threads
	assert.NotContains(t, out, "## Unresolved Threads")
	assert.NotContains(t, out, "Outdated Thread")
	assert.Contains(t, out, "> **@Tofel** (line 35):")
	assert.Contains(t, out, "> why?")
	assert.Contains(t, out, "> **Status**: Unresolved: comment requires attention")
}

func TestFormatBundle_OutdatedThread_GenuinelyOutdated(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number: 1,
		Threads: []github.ReviewThread{
			{
				ID:         "thread_outdated",
				Path:       "modified.go",
				Line:       999,
				IsOutdated: true, // Marked outdated by GitHub
				Comments: []github.Comment{
					{Author: "carol", Body: "Target code shifted"},
				},
			},
		},
	}

	diff := &git.BranchDiffResult{
		Files: []git.FileDiff{
			{
				Path:   "modified.go",
				Patch:  "@@ -10,1 +10,2 @@\n+line\n",
				Status: "modified",
			},
		},
	}

	out := FormatBundle(pr, diff, nil, false, nil)
	assert.Contains(t, out, "## Unresolved Threads")
	assert.Contains(
		t,
		out,
		"> **Outdated (Verify)**: Outdated: target lines shifted or modified by newer commit (verify manually)",
	)
	assert.Contains(t, out, "> **Note**: Thread anchored outside modified diff hunks (line 999).")
}

func TestFormatBundle_TruncatedDiff_DoesNotSayOutsideHunks(t *testing.T) {
	t.Parallel()

	var patchBuilder strings.Builder
	patchBuilder.WriteString("@@ -1,500 +1,500 @@\n")
	for i := 1; i <= 500; i++ {
		fmt.Fprintf(&patchBuilder, "+line %d\n", i)
	}

	pr := &github.PR{
		Number: 1,
		Threads: []github.ReviewThread{
			{
				ID:   "thread_truncated",
				Path: "big.go",
				Line: 450,
				Comments: []github.Comment{
					{Author: "dave", Body: "Comment in truncated region"},
				},
			},
		},
	}

	diff := &git.BranchDiffResult{
		Files: []git.FileDiff{
			{
				Path:      "big.go",
				Status:    "modified",
				Additions: 500,
				Patch:     patchBuilder.String(),
			},
		},
	}

	opts := BundleOptions{
		PR:            pr,
		Diff:          diff,
		MaxPatchLines: 60,
	}

	out := FormatBundleWithOptions(opts)
	assert.Contains(t, out, "## Unresolved Threads")
	assert.NotContains(t, out, "Outdated Thread")
	assert.NotContains(t, out, "Thread anchored outside modified diff hunks")
	assert.Contains(t, out, "> **Status**: Unresolved: comment requires attention")
	assert.Contains(t, out, "Comment in truncated region")
}

func TestFormatBundle_ExternalUsageContext(t *testing.T) {
	t.Parallel()

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:      "config.go",
				Status:    "modified",
				Additions: 1,
				Patch:     "@@ -1,1 +1,1 @@\n-var x = 1\n",
			},
		},
	}

	opts := BundleOptions{
		Diff: diff,
		ExternalUsages: []ExternalUsage{
			{
				Symbol: "CTF_CONFIGS",
				Kind:   KindEnvVar,
				References: []lsp.Reference{
					{File: "chiprouter/router.go", Line: 29, Text: "os.Setenv(\"CTF_CONFIGS\", ...)"},
				},
			},
		},
	}

	out := FormatBundleWithOptions(opts)
	assert.Contains(t, out, "## External Usage Context & Remaining References")
	assert.Contains(t, out, "`CTF_CONFIGS` (env_var): 1 reference outside diff")
	assert.Contains(t, out, "chiprouter/router.go:29")
}

func TestFormatBundle_TruncationMarker(t *testing.T) {
	t.Parallel()

	var patchLines []string
	patchLines = append(patchLines, "@@ -1,20 +1,20 @@")
	for i := 1; i <= 20; i++ {
		patchLines = append(patchLines, "+line")
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:      "long.go",
				Status:    "modified",
				Additions: 20,
				Patch:     strings.Join(patchLines, "\n"),
			},
		},
	}

	opts := BundleOptions{
		Diff:          diff,
		MaxPatchLines: 5,
	}

	out := FormatBundleWithOptions(opts)
	assert.Contains(t, out, "**[Diff truncated at 5 lines")
	assert.Contains(t, out, "git diff -U5 -- long.go")
}

func TestFormatBundle_TruncationDumpsToFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	diffsDir := filepath.Join(tmpDir, "review_diffs")

	var patchLines []string
	patchLines = append(patchLines, "@@ -1,20 +1,20 @@")
	for i := 1; i <= 20; i++ {
		patchLines = append(patchLines, "+line")
	}

	diff := &git.BranchDiffResult{
		RepoPath:      tmpDir,
		BaseBranch:    "main",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:      "pkg/long.go",
				Status:    "modified",
				Additions: 20,
				Patch:     strings.Join(patchLines, "\n"),
			},
		},
	}

	opts := BundleOptions{
		Diff:          diff,
		RepoRoot:      tmpDir,
		DiffsDir:      diffsDir,
		MaxPatchLines: 5,
	}

	out := FormatBundleWithOptions(opts)
	assert.Contains(t, out, "**[Diff truncated at 5 lines")
	assert.Contains(t, out, "Full diff dumped to:")
	assert.Contains(t, out, "pkg/long.go.diff")

	dumpedFile := filepath.Join(diffsDir, "pkg", "long.go.diff")
	content, err := os.ReadFile(dumpedFile) //nolint:gosec // test temp path
	require.NoError(t, err)
	assert.Equal(t, strings.Join(patchLines, "\n"), string(content))
}

func TestDir(t *testing.T) {
	t.Parallel()

	// PR with title produces [number]-short-title
	pr := &github.PR{Number: 42, Title: "Short Title"}
	diff := &git.BranchDiffResult{CurrentBranch: "feat/cool", BaseBranch: "main"}
	assert.Equal(t, "42-short-title", Dir(pr, diff))

	// PR with long title truncates cleanly
	prLong := &github.PR{
		Number: 101,
		Title:  "Refactor the authentication middleware to use JWT tokens with rotating refresh keys",
	}
	assert.Equal(t, "101-refactor-the-authentication-middleware", Dir(prLong, diff))

	// PR with empty title falls back to diff branch
	prNoTitle := &github.PR{Number: 42}
	assert.Equal(t, "42-feat-cool", Dir(prNoTitle, diff))

	// PR with empty title and nil diff
	assert.Equal(t, "42", Dir(prNoTitle, nil))

	// No PR, uses diff branches sanitized
	assert.Equal(t, "feat-cool_main_review", Dir(nil, diff))

	// Nil diff and PR fallback
	assert.Equal(t, "review", Dir(nil, nil))
}

func TestShortTitleSlug(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"Short Title", 40, "short-title"},
		{"[FEAT-123] Add user login", 40, "feat-123-add-user-login"},
		{"fix: update token", 40, "fix-update-token"},
		{"VeryLongWordWithoutHyphensExceedingLimit", 20, "verylongwordwithouth"},
		{"", 40, ""},
		{"---", 40, ""},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, ShortTitleSlug(tt.input, tt.maxLen))
	}
}

func TestFormatBundle_CIStatus(t *testing.T) {
	t.Parallel()

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feature",
		Files: []git.FileDiff{
			{
				Path:   "main.go",
				Status: "modified",
				Patch:  "@@ -1,1 +1,1 @@\n+var x = 1\n",
			},
		},
	}

	// Without CI status
	outNoCI := FormatBundleWithOptions(BundleOptions{Diff: diff})
	assert.NotContains(t, outNoCI, "## CI Verification Status")
	assert.Contains(t, outNoCI, "| File / Section | Tier | Changes | Bundle Lines |")
	assert.NotContains(t, outNoCI, "| Threads |")

	// With CI status
	ci := &github.CIStatusRollup{
		Total:   2,
		Pass:    1,
		Fail:    1,
		Pending: 0,
		Skip:    0,
		Checks: []github.CheckRun{
			{
				Name:     "unit-tests",
				Workflow: "test",
				Bucket:   "pass",
				Link:     "https://github.com/org/repo/actions/runs/1",
			},
			{
				Name:     "golangci-lint",
				Workflow: "lint",
				Bucket:   "fail",
				Link:     "https://github.com/org/repo/actions/runs/2",
			},
		},
	}

	opts := BundleOptions{
		Diff:     diff,
		CIStatus: ci,
	}

	out := FormatBundleWithOptions(opts)
	assert.Contains(t, out, "## CI Verification Status")
	assert.Contains(t, out, "Trust passing CI runs for automated checks")
	assert.Contains(t, out, "**Summary**: 1/2 passing (1 failing, 1 passing)")
	// Failing checks are itemized...
	assert.Contains(
		t,
		out,
		"- FAIL `lint / golangci-lint` (fail) - [View Run](https://github.com/org/repo/actions/runs/2)",
	)
	// ...but passing non-required checks are collapsed to the summary counts.
	assert.NotContains(t, out, "`test / unit-tests` (pass)")
}

func TestWriteCISections_CollapseNonRequired(t *testing.T) {
	t.Parallel()

	ci := &github.CIStatusRollup{
		Total:           7,
		Pass:            2,
		Fail:            2,
		Pending:         2,
		Skip:            1,
		FailRequired:    1,
		PendingRequired: 1,
		Checks: []github.CheckRun{
			{Name: "build", Bucket: "pass", IsRequired: true},
			{Name: "unit", Bucket: "pass"},
			{Name: "e2e", Bucket: "fail", IsRequired: true},
			{Name: "notify-slack", Bucket: "fail"},
			{Name: "integration", Bucket: "pending", IsRequired: true},
			{Name: "vault-audit", Bucket: "pending"},
			{Name: "cache", Bucket: "skipping"},
		},
	}

	t.Run("action items", func(t *testing.T) {
		t.Parallel()
		var b strings.Builder
		writeActionItemCI(&b, ci)
		out := b.String()

		// Failing checks itemized (required and optional) with verdicts.
		assert.Contains(t, out, "- FAIL `e2e`: BLOCKING (required check failed)")
		assert.Contains(t, out, "- FAIL `notify-slack`: failing (optional check failed)")
		// Required pending itemized as blocking.
		assert.Contains(t, out, "- PENDING `integration` (BLOCKING - required check)")
		// Non-required pending collapsed to a count, not itemized.
		assert.Contains(t, out, "- PENDING optional checks (non-blocking): 1")
		assert.NotContains(t, out, "`vault-audit`")
		// Required checks not all passing, so no clean-merge line.
		assert.NotContains(t, out, "All required CI checks passing")
	})

	t.Run("ci status section", func(t *testing.T) {
		t.Parallel()
		var b strings.Builder
		writeCIStatusSection(&b, ci)
		out := b.String()

		assert.Contains(t, out, "**Summary**: 2/7 passing (")
		// Failing checks itemized with required markers.
		assert.Contains(t, out, "- FAIL `e2e` (fail — required)")
		assert.Contains(t, out, "- FAIL `notify-slack` (fail)")
		// Required pending itemized; optional pending collapsed to a count.
		assert.Contains(t, out, "- PENDING `integration` (pending — required)")
		assert.Contains(t, out, "- PENDING optional checks (non-blocking): 1")
		assert.NotContains(t, out, "`vault-audit`")
		// Passing and skipped checks collapsed to summary counts, never itemized.
		assert.NotContains(t, out, "`build`")
		assert.NotContains(t, out, "`unit`")
		assert.NotContains(t, out, "`cache`")
		assert.NotContains(t, out, "<details>")
	})

	t.Run("all required passing", func(t *testing.T) {
		t.Parallel()
		var b strings.Builder
		writeActionItemCI(&b, &github.CIStatusRollup{
			Total: 2,
			Pass:  2,
			Checks: []github.CheckRun{
				{Name: "build", Bucket: "pass", IsRequired: true},
				{Name: "unit", Bucket: "pass"},
			},
		})
		assert.Contains(t, b.String(), "All required CI checks passing")
	})
}

func TestFormatBundleWithOptions_ActionItems(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:  23506,
		Title:   "Lint Roller",
		Author:  "kalverra",
		BaseRef: "develop",
		HeadRef: "feature",
		Threads: []github.ReviewThread{
			{
				ID:         "thread_1",
				Path:       "system-tests/config.go",
				Line:       150,
				IsResolved: false,
				Comments: []github.Comment{
					{
						Author: "copilot",
						Body:   "Use LookupEnv instead",
						Suggestions: []github.Suggestion{
							{Code: "val, ok := os.LookupEnv(\"FOO\")"},
						},
					},
				},
			},
		},
	}

	ci := &github.CIStatusRollup{
		Total:           2,
		Pass:            1,
		Pending:         1,
		PendingOptional: 1,
		Checks: []github.CheckRun{
			{
				Name:       "lint",
				Bucket:     "pass",
				IsRequired: true,
			},
			{
				Name:       "vault-audit",
				Bucket:     "pending",
				IsRequired: false, // non-blocking!
			},
		},
	}

	stack := &StackInfo{
		Position:    7,
		Total:       7,
		Trunk:       "develop",
		PrevBranch:  "feature-base",
		NeedsRebase: false,
	}

	diff := &git.BranchDiffResult{
		Files: []git.FileDiff{
			{
				Path:      "system-tests/config.go",
				Status:    "modified",
				Additions: 29,
				Deletions: 38,
				Patch:     "@@ -150,2 +150,2 @@\n-os.Getenv(\"FOO\")\n+os.Getenv(\"BAR\")\n",
			},
		},
	}

	out := FormatBundleWithOptions(BundleOptions{
		PR:       pr,
		Diff:     diff,
		Stack:    stack,
		CIStatus: ci,
	})

	// Action Items section at top
	assert.Contains(t, out, "## Action Items & Status Summary")
	// Unresolved thread called out at top with anchor link
	assert.Contains(t, out, "system-tests/config.go:150")
	assert.Contains(t, out, "@copilot")
	assert.Contains(t, out, "suggestion not applied")
	// CI pending check with non-blocking verdict
	assert.Contains(t, out, "- PENDING optional checks (non-blocking): 1")
	assert.NotContains(t, out, "`vault-audit` (pending")
	// Explicit rebase status (never implied by absence!)
	assert.Contains(t, out, "Up-to-date with `feature-base`")
}

func TestFormatBundleWithOptions_PerFileDiffSplitting(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	diffsDir := filepath.Join(tmpDir, "diffs")

	diff := &git.BranchDiffResult{
		RepoPath: tmpDir,
		Files: []git.FileDiff{
			{
				Path:      "pkg/large.go",
				Status:    "modified",
				Additions: 100,
				Deletions: 50,
				Patch:     strings.Repeat("+line\n", 80),
			},
			{
				Path:      "pkg/small.go",
				Status:    "modified",
				Additions: 5,
				Deletions: 2,
				Patch:     "+func small() {}\n",
			},
		},
	}

	out := FormatBundleWithOptions(BundleOptions{
		Diff:          diff,
		DiffsDir:      diffsDir,
		MaxPatchLines: 60,
	})

	// Large file was dumped to diffsDir
	largeDiffPath := filepath.Join(diffsDir, "pkg/large.go.diff")
	assert.FileExists(t, largeDiffPath)

	// Small file also dumped to diffsDir for targeted reading
	smallDiffPath := filepath.Join(diffsDir, "pkg/small.go.diff")
	assert.FileExists(t, smallDiffPath)

	// TOC contains links to diffs
	assert.Contains(t, out, "diffs/pkg/large.go.diff")
	assert.Contains(t, out, "diffs/pkg/small.go.diff")
}

func TestFormatBundleWithOptions_SmartRebase_CleanBehind(t *testing.T) {
	t.Parallel()

	stack := &StackInfo{
		Position:    2,
		Total:       3,
		Trunk:       "main",
		PrevBranch:  "feature-base",
		NeedsRebase: false,
		RebaseStatus: &RebaseStatus{
			TargetBranch: "feature-base",
			IsIssue:      false,
			Reason:       RebaseReasonCleanBehind,
			BehindCount:  2,
			AheadCount:   1,
		},
	}

	out := FormatBundleWithOptions(BundleOptions{
		Stack: stack,
	})

	assert.Contains(t, out, "Mergeable with `feature-base`")
	assert.Contains(t, out, "behind by 2 commit(s), no conflicts - rebase optional")
	assert.NotContains(t, out, "**Needs rebase on `feature-base`**")
	assert.Contains(t, out, "Clean merge with `feature-base` (behind by 2 commit(s), rebase optional)")
}

func TestFormatBundleWithOptions_SmartRebase_Conflict(t *testing.T) {
	t.Parallel()

	stack := &StackInfo{
		Position:    2,
		Total:       3,
		Trunk:       "main",
		PrevBranch:  "feature-base",
		NeedsRebase: true,
		RebaseStatus: &RebaseStatus{
			TargetBranch: "feature-base",
			IsIssue:      true,
			Reason:       RebaseReasonConflict,
			BehindCount:  2,
			AheadCount:   1,
			Conflicts:    []string{"config.go"},
		},
	}

	out := FormatBundleWithOptions(BundleOptions{
		Stack: stack,
	})

	assert.Contains(t, out, "Conflicts with `feature-base`")
	assert.Contains(t, out, "rebase required to resolve 1 conflicting file(s): `config.go`")
	assert.Contains(t, out, "**Warning: Conflicts with `feature-base`** (rebase required to resolve 1 conflict(s))")
}

func TestFormatBundleWithOptions_SmartRebase_BaseMerged(t *testing.T) {
	t.Parallel()

	stack := &StackInfo{
		Position:    2,
		Total:       3,
		Trunk:       "main",
		PrevBranch:  "feature-base",
		NeedsRebase: true,
		RebaseStatus: &RebaseStatus{
			TargetBranch: "feature-base",
			IsIssue:      true,
			Reason:       RebaseReasonBaseMerged,
		},
	}

	out := FormatBundleWithOptions(BundleOptions{
		Stack: stack,
	})

	assert.Contains(t, out, "Base branch `feature-base` merged")
	assert.Contains(t, out, "rebase required onto `main`")
	assert.Contains(t, out, "**Warning: Base branch `feature-base` merged** (rebase required onto `main`)")
}
