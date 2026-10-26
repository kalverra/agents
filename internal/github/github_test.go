package github_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/github"
)

func TestFetchUserActivity(t *testing.T) {
	t.Parallel()

	client, err := github.NewAuthenticatedClient()
	if err != nil {
		t.Skipf("no GitHub auth available: %v", err)
	}

	since := time.Now().Add(-7 * 24 * time.Hour)
	act, err := github.FetchUserActivity(context.Background(), client, since)
	require.NoError(t, err)
	require.NotNil(t, act)
}

func TestFormatUserActivityXML(t *testing.T) {
	t.Parallel()

	act := &github.UserActivity{
		PRsOpened: []github.PullRequestSummary{
			{
				Number:     10,
				Title:      "[DX-100] Add feature",
				Body:       "Feature details in body",
				URL:        "https://github.com/org/repo/pull/10",
				Repository: "org/repo",
				State:      "OPEN",
			},
		},
		PRsMerged: []github.PullRequestSummary{
			{
				Number:     9,
				Title:      "[DX-100] Fix bug",
				Body:       "Fix details in body",
				URL:        "https://github.com/org/repo/pull/9",
				Repository: "org/repo",
				State:      "MERGED",
			},
		},
	}

	assert.Len(t, act.PRsOpened, 1)
	assert.Len(t, act.PRsMerged, 1)
	assert.Equal(t, "[DX-100] Add feature", act.PRsOpened[0].Title)
	assert.Equal(t, "Feature details in body", act.PRsOpened[0].Body)
	assert.Equal(t, "Fix details in body", act.PRsMerged[0].Body)
}

func TestFetchPRCommits(t *testing.T) {
	t.Parallel()

	if _, err := github.NewAuthenticatedClient(); err != nil {
		t.Skipf("no GitHub auth available: %v", err)
	}

	commits, err := github.FetchPRCommits(context.Background(), "smartcontractkit/chainlink", 23288)
	require.NoError(t, err)
	require.NotEmpty(t, commits)
}

func TestReviewThread_IsFileLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		thread   *github.ReviewThread
		expected bool
	}{
		{
			name:     "nil thread",
			thread:   nil,
			expected: false,
		},
		{
			name: "subjectType FILE with line 1",
			thread: &github.ReviewThread{
				Path:        "index.html",
				SubjectType: "FILE",
				Line:        1,
			},
			expected: true,
		},
		{
			name: "subjectType lowercase file",
			thread: &github.ReviewThread{
				Path:        "index.html",
				SubjectType: "file",
				Line:        0,
			},
			expected: true,
		},
		{
			name: "subjectType LINE",
			thread: &github.ReviewThread{
				Path:        "main.go",
				SubjectType: "LINE",
				Line:        10,
			},
			expected: false,
		},
		{
			name: "empty subjectType with line 0 and path",
			thread: &github.ReviewThread{
				Path: "README.md",
				Line: 0,
			},
			expected: true,
		},
		{
			name: "empty subjectType with line > 0",
			thread: &github.ReviewThread{
				Path: "main.go",
				Line: 15,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.thread.IsFileLevel())
		})
	}
}

func TestFormatPR_FileComment(t *testing.T) {
	t.Parallel()

	pr := &github.PR{
		Number:  23506,
		Title:   "Fix assets",
		Author:  "author",
		BaseRef: "main",
		HeadRef: "fix",
		Threads: []github.ReviewThread{
			{
				Path:        "core/web/assets/index.html",
				SubjectType: "FILE",
				Line:        1,
				Comments: []github.Comment{
					{
						Author:    "jmank88",
						Body:      "Did you bump operator-ui?",
						CreatedAt: time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC),
					},
				},
			},
		},
	}

	out := github.FormatPR(pr, false)
	assert.Contains(t, out, "### Thread: core/web/assets/index.html (file comment)")
	assert.NotContains(t, out, "core/web/assets/index.html:1")
	assert.Contains(t, out, "Did you bump operator-ui?")
}

func TestCheckRun_Verdict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		check    github.CheckRun
		expected string
	}{
		{
			name:     "passing check",
			check:    github.CheckRun{Name: "unit-tests", Bucket: "pass", State: "SUCCESS"},
			expected: "passed",
		},
		{
			name:     "pending required check",
			check:    github.CheckRun{Name: "build", Bucket: "pending", State: "PENDING", IsRequired: true},
			expected: "BLOCKING (required check pending)",
		},
		{
			name:     "pending optional check",
			check:    github.CheckRun{Name: "vault-audit", Bucket: "pending", State: "PENDING", IsRequired: false},
			expected: "non-blocking (optional check pending)",
		},
		{
			name:     "failing required check",
			check:    github.CheckRun{Name: "integration", Bucket: "fail", State: "FAILURE", IsRequired: true},
			expected: "BLOCKING (required check failed)",
		},
		{
			name:     "failing optional check",
			check:    github.CheckRun{Name: "notify-slack", Bucket: "fail", State: "FAILURE", IsRequired: false},
			expected: "failing (optional check failed)",
		},
		{
			name:     "skipped check",
			check:    github.CheckRun{Name: "skip-me", Bucket: "skipping", State: "SKIPPED"},
			expected: "skipped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.check.Verdict())
		})
	}
}

func TestPullRequestSummary_MergedAt(t *testing.T) {
	t.Parallel()

	closedAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	opened := &github.PullRequestSummary{
		Number: 10, Title: "Add feature", State: "OPEN", Repository: "org/repo",
		URL: "https://github.com/org/repo/pull/10",
	}
	merged := &github.PullRequestSummary{
		Number: 9, Title: "Fix bug", State: "MERGED", Repository: "org/repo",
		URL: "https://github.com/org/repo/pull/9", MergedAt: closedAt,
	}
	assert.True(t, opened.MergedAt.IsZero())
	assert.Equal(t, closedAt, merged.MergedAt)
}
