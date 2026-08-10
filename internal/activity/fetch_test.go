package activity_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/activity"
	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/git"
	github "github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/internal/ticket"
)

func TestFetcher_Fetch_NoClients(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	f := &activity.Fetcher{
		Config: &config.Config{},
		Logger: zerolog.New(nil),
	}

	report, err := f.Fetch(context.Background(), now, "7d", "Work")
	require.NoError(t, err)
	assert.Equal(t, "7d", report.Timeframe)
	assert.Equal(t, now.Add(-7*24*time.Hour), report.Since)
	assert.Equal(t, now, report.Until)
	assert.Equal(t, "Work", report.Project)
	assert.Empty(t, report.Todoist)
	assert.Empty(t, report.JiraIssues)
	assert.Empty(t, report.GitHub.PRsOpened)
}

func TestFetcher_Fetch_CurrentTicket(t *testing.T) {
	t.Parallel()

	f := &activity.Fetcher{
		Config: &config.Config{},
		Logger: zerolog.New(nil),
		DetectRepo: func(_ string) (*git.RepoInfo, error) {
			return &git.RepoInfo{Branch: "DX-123-fix-things", Owner: "me", Name: "repo"}, nil
		},
	}

	report, err := f.Fetch(context.Background(), time.Now(), "7d", "Work")
	require.NoError(t, err)
	require.NotNil(t, report.CurrentTicket)
	assert.Equal(t, "DX-123", report.CurrentTicket.ID)
	assert.Equal(t, "jira", report.CurrentTicket.Source)
}

func TestFetcher_Fetch_Todoist(t *testing.T) {
	t.Parallel()

	td := &fakeTodoist{
		projectID: "proj-1",
		tasks: []ticket.TodoistCompletedTask{
			{
				ID:          "td-1",
				Content:     "Fix [DX-42](https://jira.example.com/DX-42)",
				Description: "details",
				CompletedAt: "2026-08-05T10:00:00Z",
				Labels:      []string{"jira-sync"},
				URL:         "https://todoist.com/td-1",
			},
		},
		byID: map[string]*ticket.Ticket{
			"td-1": {
				ID: "td-1",
				Comments: []ticket.TaskComment{
					{ID: "c-1", PostedAt: "2026-08-05T11:00:00Z", Content: "note"},
				},
			},
		},
	}

	f := &activity.Fetcher{
		Config:  &config.Config{},
		Logger:  zerolog.New(nil),
		Todoist: td,
	}

	report, err := f.Fetch(context.Background(), time.Now(), "7d", "Work")
	require.NoError(t, err)
	require.Len(t, report.Todoist, 1)
	assert.Equal(t, "td-1", report.Todoist[0].ID)
	assert.Equal(t, "DX-42", report.Todoist[0].JiraKey)
	assert.True(t, report.Todoist[0].IsJiraLinked)
	require.Len(t, report.Todoist[0].Comments, 1)
	assert.Equal(t, "note", report.Todoist[0].Comments[0].Content)
}

func TestFetcher_Fetch_Jira(t *testing.T) {
	t.Parallel()

	jira := &fakeJira{
		assigned: []*ticket.Ticket{
			{ID: "DX-1", Title: "Old open", Status: "Open", UpdatedAt: "2026-07-25T00:00:00Z"},
			{ID: "DX-2", Title: "In progress", Status: "In Progress", UpdatedAt: "2026-08-06T00:00:00Z"},
		},
	}

	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	f := &activity.Fetcher{
		Config: &config.Config{},
		Logger: zerolog.New(nil),
		Jira:   jira,
	}

	report, err := f.Fetch(context.Background(), now, "7d", "Work")
	require.NoError(t, err)
	require.Len(t, report.JiraIssues, 1)
	assert.Equal(t, "DX-2", report.JiraIssues[0].Key)
}

func TestFetcher_Fetch_GitHub(t *testing.T) {
	t.Parallel()

	gh := &fakeGitHub{
		activity: &github.UserActivity{
			PRsOpened: []github.PullRequestSummary{
				{Number: 1, Title: "DX-55 feature", Repository: "me/repo"},
			},
			PRsMerged: []github.PullRequestSummary{
				{Number: 2, Title: "DX-66 fix", Repository: "me/repo"},
			},
			PRReviews: []github.PullRequestSummary{
				{Number: 3, Title: "Reviewed", Repository: "me/repo"},
			},
		},
		commits: map[string][]github.CommitSummary{
			"me/repo#1": {{SHA: "abc1234", Message: "DX-55 initial commit"}},
		},
	}

	f := &activity.Fetcher{
		Config: &config.Config{},
		Logger: zerolog.New(nil),
		GitHub: gh,
	}

	report, err := f.Fetch(context.Background(), time.Now(), "7d", "Work")
	require.NoError(t, err)
	require.Len(t, report.GitHub.PRsOpened, 1)
	assert.Equal(t, 1, report.GitHub.PRsOpened[0].Number)
	assert.Equal(t, "DX-55", report.GitHub.PRsOpened[0].JiraKey)
	require.Len(t, report.GitHub.PRsOpened[0].Commits, 1)
	assert.Equal(t, "abc1234", report.GitHub.PRsOpened[0].Commits[0].SHA)
	assert.Len(t, report.GitHub.PRsMerged, 1)
	assert.Len(t, report.GitHub.PRReviews, 1)
}

type fakeTodoist struct {
	projectID string
	tasks     []ticket.TodoistCompletedTask
	byID      map[string]*ticket.Ticket
}

func (f *fakeTodoist) GetProjectIDByName(_ context.Context, _ string) (string, error) {
	return f.projectID, nil
}

func (f *fakeTodoist) FetchCompletedTasks(
	_ context.Context,
	_ string,
) ([]ticket.TodoistCompletedTask, error) {
	return f.tasks, nil
}

func (f *fakeTodoist) Fetch(_ context.Context, id string) (*ticket.Ticket, error) {
	return f.byID[id], nil
}

type fakeJira struct {
	assigned []*ticket.Ticket
}

func (f *fakeJira) ListAssignedTickets(_ context.Context) ([]*ticket.Ticket, error) {
	return f.assigned, nil
}

type fakeGitHub struct {
	activity *github.UserActivity
	commits  map[string][]github.CommitSummary
}

func (f *fakeGitHub) FetchUserActivity(_ context.Context, _ time.Time) (*github.UserActivity, error) {
	return f.activity, nil
}

func (f *fakeGitHub) FetchPRCommits(
	_ context.Context,
	repository string,
	number int,
) ([]github.CommitSummary, error) {
	key := fmt.Sprintf("%s#%d", repository, number)
	return f.commits[key], nil
}

func TestFetcher_Fetch_FiltersOlderActivity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	td := &fakeTodoist{
		projectID: "proj-1",
		tasks: []ticket.TodoistCompletedTask{
			{
				ID:          "td-recent",
				Content:     "Recent task [DX-1](https://jira.example.com/DX-1)",
				CompletedAt: "2026-08-06T10:00:00Z",
				Labels:      []string{"jira-sync"},
			},
			{
				ID:          "td-old",
				Content:     "Old task [DX-2](https://jira.example.com/DX-2)",
				CompletedAt: "2026-07-01T10:00:00Z",
				Labels:      []string{"jira-sync"},
			},
		},
	}

	jira := &fakeJira{
		assigned: []*ticket.Ticket{
			{ID: "DX-10", Title: "Old done ticket", Status: "Done", UpdatedAt: "2026-07-01T00:00:00Z"},
			{ID: "DX-11", Title: "Recent done ticket", Status: "Closed", UpdatedAt: "2026-08-06T09:00:00Z"},
			{ID: "DX-12", Title: "Active ticket", Status: "In Progress", UpdatedAt: "2026-08-06T08:00:00Z"},
		},
	}

	gh := &fakeGitHub{
		activity: &github.UserActivity{
			PRsOpened: []github.PullRequestSummary{
				{Number: 1, Title: "Recent opened PR", CreatedAt: now.Add(-2 * time.Hour), Repository: "me/repo"},
				{Number: 2, Title: "Old opened PR", CreatedAt: now.Add(-30 * 24 * time.Hour), Repository: "me/repo"},
			},
			PRsMerged: []github.PullRequestSummary{
				{Number: 3, Title: "Recent merged PR", MergedAt: now.Add(-1 * time.Hour), Repository: "me/repo"},
				{Number: 4, Title: "Old merged PR", MergedAt: now.Add(-30 * 24 * time.Hour), Repository: "me/repo"},
			},
		},
		commits: map[string][]github.CommitSummary{
			"me/repo#1": {
				{SHA: "new123", Date: "2026-08-06T11:00:00Z", Message: "Recent commit"},
				{SHA: "old123", Date: "2026-07-01T11:00:00Z", Message: "Old commit"},
			},
		},
	}

	f := &activity.Fetcher{
		Config:  &config.Config{},
		Logger:  zerolog.New(nil),
		Todoist: td,
		Jira:    jira,
		GitHub:  gh,
	}

	report, err := f.Fetch(context.Background(), now, "1d", "Work")
	require.NoError(t, err)

	// Todoist: only td-recent should be present
	require.Len(t, report.Todoist, 1)
	assert.Equal(t, "td-recent", report.Todoist[0].ID)

	// Jira: DX-10 (old done) should be filtered out
	require.Len(t, report.JiraIssues, 2)
	keys := []string{report.JiraIssues[0].Key, report.JiraIssues[1].Key}
	assert.Contains(t, keys, "DX-11")
	assert.Contains(t, keys, "DX-12")
	assert.NotContains(t, keys, "DX-10")

	// GitHub: old PRs and old commit filtered out
	require.Len(t, report.GitHub.PRsOpened, 1)
	assert.Equal(t, 1, report.GitHub.PRsOpened[0].Number)
	require.Len(t, report.GitHub.PRsOpened[0].Commits, 1)
	assert.Equal(t, "new123", report.GitHub.PRsOpened[0].Commits[0].SHA)

	require.Len(t, report.GitHub.PRsMerged, 1)
	assert.Equal(t, 3, report.GitHub.PRsMerged[0].Number)
}
