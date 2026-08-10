package activity_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/agents/internal/activity"
)

func TestParseJiraInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		content      string
		description  string
		labels       []string
		wantJiraKey  string
		wantIsLinked bool
	}{
		{
			name:         "Jira sync label with markdown link",
			content:      "Implement auth handler [DX-1234](https://jira.example.com/browse/DX-1234)",
			description:  "Subtask details",
			labels:       []string{"jira-sync", "work"},
			wantJiraKey:  "DX-1234",
			wantIsLinked: true,
		},
		{
			name:         "Jira sync label only with key in description",
			content:      "Refactor DB pool",
			description:  "Linked to [DX-5678](https://jira.example.com/browse/DX-5678)",
			labels:       []string{"jira-sync"},
			wantJiraKey:  "DX-5678",
			wantIsLinked: true,
		},
		{
			name:         "No jira sync label or link",
			content:      "Clean up logs",
			description:  "Simple task",
			labels:       []string{"routine"},
			wantJiraKey:  "",
			wantIsLinked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			key, isLinked := activity.ParseJiraInfo(tt.content, tt.description, tt.labels)
			assert.Equal(t, tt.wantJiraKey, key)
			assert.Equal(t, tt.wantIsLinked, isLinked)
		})
	}
}

func TestFormatXMLReport(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	report := &activity.Report{
		Timeframe: "7d",
		Since:     now.Add(-7 * 24 * time.Hour),
		Until:     now,
		Todoist: []activity.TodoistTask{
			{
				ID:           "task-1",
				Title:        "Update docs",
				Description:  "Added new API routes",
				CompletedAt:  "2026-08-02T10:00:00Z",
				JiraKey:      "DX-999",
				IsJiraLinked: true,
			},
		},
		GitHub: activity.GitHubUserActivity{
			PRsOpened: []activity.PullRequestInfo{
				{
					Number:     42,
					Title:      "Add activity feature",
					URL:        "https://github.com/org/repo/pull/42",
					Repository: "org/repo",
				},
			},
			PRsMerged: []activity.PullRequestInfo{
				{Number: 40, Title: "Fix linting", URL: "https://github.com/org/repo/pull/40", Repository: "org/repo"},
			},
		},
	}

	xml := activity.FormatXMLReport(report)
	assert.Contains(t, xml, `<activity_report timeframe="7d"`)
	assert.Contains(t, xml, `jira_key="DX-999"`)
	assert.Contains(t, xml, `jira_linked="true"`)
	assert.Contains(t, xml, `Add activity feature`)
}

func TestFormatXMLReport_WithCommentsAndMovement(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	report := &activity.Report{
		Timeframe: "7d",
		Since:     now.Add(-7 * 24 * time.Hour),
		Until:     now,
		Todoist: []activity.TodoistTask{
			{
				ID:           "task-1",
				Title:        "Update docs",
				Description:  "Added new API routes",
				CompletedAt:  "2026-08-02T10:00:00Z",
				JiraKey:      "DX-999",
				IsJiraLinked: true,
				Comments: []activity.TaskComment{
					{ID: "c1", PostedAt: "2026-08-01T09:00:00Z", Content: "Started working"},
				},
				Movements: []activity.TaskMovement{
					{Date: "2026-08-01T08:30:00Z", Action: "moved_to_in_progress", From: "To Do", To: "In Progress"},
				},
			},
		},
	}

	xml := activity.FormatXMLReport(report)
	assert.Contains(t, xml, `<comment posted_at="2026-08-01T09:00:00Z">Started working</comment>`)
	assert.Contains(
		t,
		xml,
		`<movement date="2026-08-01T08:30:00Z" action="moved_to_in_progress" from="To Do" to="In Progress"/>`,
	)
}

func TestFilterJiraLinkedTasks(t *testing.T) {
	t.Parallel()

	tasks := []activity.TodoistTask{
		{ID: "1", Title: "Task 1", IsJiraLinked: true, JiraKey: "DX-1"},
		{ID: "2", Title: "Task 2", IsJiraLinked: false},
	}

	filtered := activity.FilterJiraLinkedTasks(tasks)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "1", filtered[0].ID)
}

func TestFormatXMLReport_WithPRCommits(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	report := &activity.Report{
		Timeframe: "7d",
		Since:     now.Add(-7 * 24 * time.Hour),
		Until:     now,
		GitHub: activity.GitHubUserActivity{
			PRsOpened: []activity.PullRequestInfo{
				{
					Number:     42,
					Title:      "Add activity feature",
					URL:        "https://github.com/org/repo/pull/42",
					Repository: "org/repo",
					Commits: []activity.CommitInfo{
						{SHA: "f24adc6", Date: "2026-08-03T10:00:00Z", Message: "Properly cache plugins"},
					},
				},
			},
		},
	}

	xml := activity.FormatXMLReport(report)
	assert.Contains(t, xml, `<commit sha="f24adc6" date="2026-08-03T10:00:00Z">Properly cache plugins</commit>`)
}

func TestFormatXMLReport_WithCrossLinkedActivityAndStandalone(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	report := &activity.Report{
		Timeframe: "7d",
		Since:     now.Add(-7 * 24 * time.Hour),
		Until:     now,
		CurrentTicket: &activity.CurrentTicketInfo{
			ID:     "DX-100",
			Title:  "Active feature branch ticket",
			Source: "jira",
		},
		Todoist: []activity.TodoistTask{
			{
				ID:           "task-linked",
				Title:        "Linked task [DX-999]",
				JiraKey:      "DX-999",
				IsJiraLinked: true,
			},
			{
				ID:           "task-standalone",
				Title:        "Standalone personal task",
				IsJiraLinked: false,
			},
		},
		JiraIssues: []activity.JiraIssueInfo{
			{
				Key:       "DX-999",
				Summary:   "Implement feature DX-999",
				Status:    "In Progress",
				EpicKey:   "DX-10",
				EpicTitle: "Core DX Epic",
			},
		},
		GitHub: activity.GitHubUserActivity{
			PRsOpened: []activity.PullRequestInfo{
				{
					Number:     42,
					Title:      "DX-999: Add activity feature",
					URL:        "https://github.com/org/repo/pull/42",
					Repository: "org/repo",
					JiraKey:    "DX-999",
					Commits: []activity.CommitInfo{
						{
							SHA:     "f24adc6",
							Date:    "2026-08-03T10:00:00Z",
							Message: "DX-999: add core logic",
							JiraKey: "DX-999",
						},
					},
				},
			},
		},
	}

	xml := activity.FormatXMLReport(report)
	assert.Contains(t, xml, `<current_ticket id="DX-100" source="jira">Active feature branch ticket</current_ticket>`)
	assert.Contains(t, xml, `<jira_issues>`)
	assert.Contains(t, xml, `<issue key="DX-999" status="In Progress" epic_key="DX-10" epic_title="Core DX Epic">`)
	assert.Contains(t, xml, `<task id="task-linked" jira_linked="true" jira_key="DX-999">`)
	assert.NotContains(t, xml, "task-standalone")
	assert.NotContains(t, xml, "<standalone_tasks>")
	assert.Contains(t, xml, `jira_key="DX-999"`)
}

func TestExtractKeyAndTaskID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input       string
		wantJiraKey string
	}{
		{"DX-123: fix auth bug", "DX-123"},
		{"[FEAT-456] add new button", "FEAT-456"},
		{"no ticket here", ""},
	}

	for _, tt := range tests {
		gotKey, _ := activity.ExtractKeyAndTaskID(tt.input)
		assert.Equal(t, tt.wantJiraKey, gotKey)
	}
}

func TestFormatXMLReport_EmptySectionsAndTicketSorting(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	report := &activity.Report{
		Timeframe: "7d",
		Since:     now.Add(-7 * 24 * time.Hour),
		Until:     now,
		Todoist:   []activity.TodoistTask{}, // Empty
		JiraIssues: []activity.JiraIssueInfo{
			{
				Key:       "DX-300",
				Summary:   "Open task recent",
				Status:    "Open",
				UpdatedAt: "2026-08-03T10:00:00Z",
			},
			{
				Key:       "DX-100",
				Summary:   "Blocked task",
				Status:    "Blocked",
				UpdatedAt: "2026-08-02T10:00:00Z",
			},
			{
				Key:       "DX-200",
				Summary:   "In progress task",
				Status:    "In Progress",
				UpdatedAt: "2026-08-03T11:00:00Z",
			},
		},
		GitHub: activity.GitHubUserActivity{}, // Empty
	}

	xml := activity.FormatXMLReport(report)

	// Omit empty sections
	assert.NotContains(t, xml, "<todoist_activity>")
	assert.NotContains(t, xml, "<github_activity>")

	// Contains last_updated
	assert.Contains(t, xml, `last_updated="1h"`)

	// Correct status sorting: Blocked -> In Progress -> Open
	idxBlocked := strings.Index(xml, `key="DX-100"`)
	idxInProgress := strings.Index(xml, `key="DX-200"`)
	idxOpen := strings.Index(xml, `key="DX-300"`)

	assert.Less(t, idxBlocked, idxInProgress, "Blocked should come before In Progress")
	assert.Less(t, idxInProgress, idxOpen, "In Progress should come before Open")
}

func TestFormatXMLReport_WithSprintProperty(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	report := &activity.Report{
		Timeframe: "7d",
		Since:     now.Add(-7 * 24 * time.Hour),
		Until:     now,
		JiraIssues: []activity.JiraIssueInfo{
			{
				Key:       "DX-500",
				Summary:   "Sprint task",
				Status:    "In Progress",
				Sprint:    "DX Sprint 42",
				UpdatedAt: "2026-08-03T11:00:00Z",
			},
		},
	}

	xml := activity.FormatXMLReport(report)
	assert.Contains(t, xml, `sprint="DX Sprint 42"`)
}

func TestFormatXMLReport_WithPRDescription(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	report := &activity.Report{
		Timeframe: "7d",
		Since:     now.Add(-7 * 24 * time.Hour),
		Until:     now,
		GitHub: activity.GitHubUserActivity{
			PRsOpened: []activity.PullRequestInfo{
				{
					Number:     42,
					Title:      "Add activity feature",
					Body:       "Added PR body description for context",
					URL:        "https://github.com/org/repo/pull/42",
					Repository: "org/repo",
				},
			},
			PRsMerged: []activity.PullRequestInfo{
				{
					Number:     41,
					Title:      "Merged feature",
					Body:       "Merged PR description context",
					URL:        "https://github.com/org/repo/pull/41",
					Repository: "org/repo",
				},
			},
		},
	}

	xml := activity.FormatXMLReport(report)
	assert.Contains(t, xml, `<description>Added PR body description for context</description>`)
	assert.Contains(t, xml, `<description>Merged PR description context</description>`)
}

func TestCleanPRDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Remove HTML comments",
			input: "<!-- template comment -->PR summary",
			want:  "PR summary",
		},
		{
			name:  "Remove markdown and HTML images",
			input: "Before ![screenshot](https://example.com/img.png) middle <img src=\"http://example.com/pic.jpg\" width=\"100\"> after",
			want:  "Before middle after",
		},
		{
			name:  "Remove raw URLs and replace markdown links with anchor text",
			input: "See [PR #12](https://github.com/org/repo/pull/12) and raw https://example.com/link",
			want:  "See PR #12 and raw",
		},
		{
			name:  "Strip HTML tags",
			input: "<details><summary>Notes</summary><div>Detailed info</div></details>",
			want:  "NotesDetailed info",
		},
		{
			name:  "Trim excess whitespace and blank lines",
			input: "Line 1\n\n\n\nLine 2  \n  \nLine 3",
			want:  "Line 1\n\nLine 2\n\nLine 3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := activity.CleanPRDescription(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
