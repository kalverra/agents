package review

import (
	"fmt"
	"testing"
	"time"

	"github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/internal/review/lsp"
	"github.com/kalverra/agents/pkg/git"
)

func BenchmarkFormatBundleWithOptions(b *testing.B) {
	pr := &github.PR{
		Number:         42,
		Title:          "Refactor authentication and ticket pipeline",
		URL:            "https://github.com/kalverra/agents/pull/42",
		Author:         "alice",
		BaseRef:        "main",
		HeadRef:        "feat/auth-pipeline",
		ReviewDecision: "REVIEW_REQUIRED",
		Body:           "This PR optimizes pipeline execution and refactors auth token resolution.",
		Threads: []github.ReviewThread{
			{
				Path: "cmd/auth.go",
				Line: 25,
				Comments: []github.Comment{
					{Author: "reviewer1", Body: "Consider returning early here.", CreatedAt: time.Now()},
				},
			},
		},
	}

	files := make([]git.FileDiff, 20)
	for i := range 20 {
		files[i] = git.FileDiff{
			Path:      fmt.Sprintf("internal/pkg%d/worker.go", i),
			Status:    "modified",
			Additions: 15,
			Deletions: 5,
			Patch: fmt.Sprintf(`@@ -10,10 +10,20 @@
-func oldWorker%d() error {
+func newWorker%d() error {
+	// new implementation
+	return nil
+}
`, i, i),
		}
	}

	diff := &git.BranchDiffResult{
		BaseBranch:    "main",
		CurrentBranch: "feat/auth-pipeline",
		Files:         files,
		Stats: git.DiffStats{
			FilesChanged: 20,
			Insertions:   300,
			Deletions:    100,
		},
	}

	opts := BundleOptions{
		PR:   pr,
		Diff: diff,
		Stack: &StackInfo{
			Position:   1,
			Total:      2,
			Trunk:      "main",
			NextBranch: "feat/ui",
		},
		CIStatus: &github.CIStatusRollup{
			Total: 5,
			Pass:  4,
			Fail:  1,
		},
		ExternalUsages: []ExternalUsage{
			{
				Symbol: "DetectRepo",
				Kind:   KindFunction,
				References: []lsp.Reference{
					{File: "cmd/root.go", Line: 45},
					{File: "cmd/skills/ticket.go", Line: 120},
				},
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out := FormatBundleWithOptions(opts)
		if len(out) == 0 {
			b.Fatal("unexpected empty bundle")
		}
	}
}
