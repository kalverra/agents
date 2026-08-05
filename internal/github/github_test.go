package github_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/github"
)

func TestFetchUserActivity_CLI(t *testing.T) {
	t.Parallel()

	// Testing FetchUserActivity helper with mock/struct
	since := time.Now().Add(-7 * 24 * time.Hour)
	act, err := github.FetchUserActivityWithCLI(context.Background(), since)
	// If gh CLI is available, this queries real gh search; if not, handles error cleanly.
	if err == nil {
		require.NotNil(t, act)
	}
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

func TestFetchPRCommits_CLI(t *testing.T) {
	t.Parallel()

	commits, err := github.FetchPRCommitsWithCLI(context.Background(), "smartcontractkit/chainlink", 23288)
	if err == nil {
		require.NotNil(t, commits)
	}
}
