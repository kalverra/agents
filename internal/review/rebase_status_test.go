package review

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/pkg/git"
)

func TestAssessRebaseStatus_UpToDate(t *testing.T) {
	t.Parallel()

	status := AssessRebaseStatus(RebaseAssessmentOptions{
		TargetBranch: "main",
		LocalMerge: &git.Mergeability{
			BaseBranch:  "main",
			HeadBranch:  "feat/one",
			BehindCount: 0,
			AheadCount:  2,
			HasConflict: false,
		},
	})
	require.NotNil(t, status)
	assert.False(t, status.IsIssue)
	assert.Equal(t, RebaseReasonUpToDate, status.Reason)
	assert.Equal(t, "main", status.TargetBranch)
	assert.Equal(t, 0, status.BehindCount)
}

func TestAssessRebaseStatus_CleanBehind(t *testing.T) {
	t.Parallel()

	status := AssessRebaseStatus(RebaseAssessmentOptions{
		TargetBranch: "feat/base",
		IsBaseMerged: false,
		LocalMerge: &git.Mergeability{
			BaseBranch:  "feat/base",
			HeadBranch:  "feat/child",
			BehindCount: 3,
			AheadCount:  1,
			HasConflict: false,
		},
	})
	require.NotNil(t, status)
	assert.False(t, status.IsIssue, "clean merge behind base should not be flagged as an issue")
	assert.Equal(t, RebaseReasonCleanBehind, status.Reason)
	assert.Equal(t, 3, status.BehindCount)
	assert.Empty(t, status.Conflicts)
}

func TestAssessRebaseStatus_Conflict(t *testing.T) {
	t.Parallel()

	status := AssessRebaseStatus(RebaseAssessmentOptions{
		TargetBranch: "feat/base",
		LocalMerge: &git.Mergeability{
			BaseBranch:  "feat/base",
			HeadBranch:  "feat/child",
			BehindCount: 2,
			AheadCount:  1,
			HasConflict: true,
			Conflicts:   []string{"config.go", "main.go"},
		},
	})
	require.NotNil(t, status)
	assert.True(t, status.IsIssue, "conflicts must be flagged as an issue")
	assert.Equal(t, RebaseReasonConflict, status.Reason)
	assert.Equal(t, []string{"config.go", "main.go"}, status.Conflicts)
}

func TestAssessRebaseStatus_BaseMerged(t *testing.T) {
	t.Parallel()

	status := AssessRebaseStatus(RebaseAssessmentOptions{
		TargetBranch: "feat/base",
		Trunk:        "main",
		IsBaseMerged: true,
		LocalMerge: &git.Mergeability{
			BaseBranch:  "feat/base",
			HeadBranch:  "feat/child",
			BehindCount: 0,
			HasConflict: false,
		},
	})
	require.NotNil(t, status)
	assert.True(t, status.IsIssue, "merged base in stack must be flagged as an issue")
	assert.Equal(t, RebaseReasonBaseMerged, status.Reason)
}

func TestAssessRebaseStatus_PRMergeableConflicting(t *testing.T) {
	t.Parallel()

	status := AssessRebaseStatus(RebaseAssessmentOptions{
		TargetBranch: "main",
		PRMergeable:  "CONFLICTING",
	})
	require.NotNil(t, status)
	assert.True(t, status.IsIssue)
	assert.Equal(t, RebaseReasonConflict, status.Reason)
}

func TestResolveRebaseStatus(t *testing.T) {
	t.Parallel()

	given := &RebaseStatus{TargetBranch: "main", Reason: RebaseReasonUpToDate}
	stackStatus := &RebaseStatus{TargetBranch: "feat/base", Reason: RebaseReasonCleanBehind}

	tests := []struct {
		name       string
		opts       BundleOptions
		wantTarget string
		wantReason RebaseReason
	}{
		{
			name:       "keeps given status",
			opts:       BundleOptions{RebaseStatus: given},
			wantTarget: "main",
			wantReason: RebaseReasonUpToDate,
		},
		{
			name: "assesses branch outside a stack from PR state",
			opts: BundleOptions{
				PR: &github.PR{BaseRef: "main", HeadRef: "feat/x", Mergeable: "CONFLICTING"},
			},
			wantTarget: "main",
			wantReason: RebaseReasonConflict,
		},
		{
			name:       "falls back to stack status without repo or PR state",
			opts:       BundleOptions{Stack: &StackInfo{RebaseStatus: stackStatus}},
			wantTarget: "feat/base",
			wantReason: RebaseReasonCleanBehind,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := resolveRebaseStatus(tt.opts, "")
			require.NotNil(t, got)
			assert.Equal(t, tt.wantTarget, got.TargetBranch)
			assert.Equal(t, tt.wantReason, got.Reason)
		})
	}

	t.Run("returns nil without any source", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, resolveRebaseStatus(BundleOptions{}, ""))
	})

	t.Run("copies new status onto stack", func(t *testing.T) {
		t.Parallel()

		stack := &StackInfo{Trunk: "main", PrevBranch: "feat/base", RebaseStatus: stackStatus}
		got := resolveRebaseStatus(BundleOptions{
			PR:    &github.PR{Mergeable: "CONFLICTING"},
			Stack: stack,
		}, "")
		require.NotNil(t, got)
		assert.Equal(t, RebaseReasonConflict, got.Reason)
		assert.Same(t, got, stack.RebaseStatus)
		assert.True(t, stack.NeedsRebase)
	})
}
