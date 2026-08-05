package ticket

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStatusPayloadToAIXML(t *testing.T) {
	t.Parallel()

	t.Run("on branch with ticket found", func(t *testing.T) {
		t.Parallel()
		p := StatusPayload{
			RepoOwner:           "kalverra",
			RepoName:            "chainlink",
			Branch:              "auth-refresh/DX-55",
			IsWork:              true,
			DefaultBranch:       "develop",
			DirtyFiles:          3,
			UntrackedFiles:      1,
			OnTicketBranch:      true,
			TicketFound:         true,
			NeedsTicketDecision: false,
			Ticket: &Ticket{
				ID:          "DX-55",
				Title:       "Auth & Refresh <Tokens>",
				Description: "Fix token expiration & validation",
				Status:      "In Progress",
				URL:         "https://jira.example.com/browse/DX-55",
				ParentID:    "DX-50",
				ParentTitle: "Auth Architecture",
				Comments: []TaskComment{
					{
						ID:       "1001",
						PostedAt: "2026-08-01T10:00:00Z",
						Content:  "Started work on JWT parsing",
					},
				},
			},
		}

		xml := StatusPayloadToAIXML(p)
		expected := `<ticket_status mode="work">
  <repo owner="kalverra" name="chainlink" branch="auth-refresh/DX-55" is_work="true" default_branch="develop"/>
  <git_state dirty="true" dirty_files="3" untracked_files="1"/>
  <decision_hints on_ticket_branch="true" ticket_found="true" needs_ticket_decision="false"/>
  <instructions action="scope_check">
    <rule>Pure Q&amp;A or explanation request -&gt; skip ticketing.</rule>
    <rule>Compare request against active ticket context (auto-written to file):</rule>
    <rule>  - IN_SCOPE -&gt; proceed.</rule>
    <rule>  - TANGENT -&gt; nudge user (offer new ticket/task or park).</rule>
    <rule>  - TICKET_DONE -&gt; suggest summary &amp; close.</rule>
    <rule>Working progress notes: "agents ticket comment &lt;REF&gt; --body "...""</rule>
    <rule>Jira summary update: fetch ticket details -&gt; draft summary -&gt; prompt for explicit user approval -&gt; post via "agents ticket comment &lt;KEY&gt; --jira --body "..."".</rule>
  </instructions>
  <ticket id="DX-55" status="In Progress" url="https://jira.example.com/browse/DX-55">
    <title>Auth &amp; Refresh &lt;Tokens&gt;</title>
    <epic id="DX-50" title="Auth Architecture"/>
    <description>Fix token expiration &amp; validation</description>
    <comments>
      <comment time="2026-08-01T10:00:00Z">Started work on JWT parsing</comment>
    </comments>
  </ticket>
</ticket_status>
`
		assert.Equal(t, expected, xml)
	})

	t.Run("on branch ticket fetch failed with warning", func(t *testing.T) {
		t.Parallel()
		p := StatusPayload{
			Mode:                "work",
			RepoOwner:           "kalverra",
			RepoName:            "chainlink",
			Branch:              "fix/DX-999",
			IsWork:              true,
			DefaultBranch:       "main",
			DirtyFiles:          0,
			UntrackedFiles:      0,
			OnTicketBranch:      true,
			TicketFound:         false,
			NeedsTicketDecision: false,
			Warnings:            []string{"jira fetch failed: 404 Not Found"},
		}

		xml := StatusPayloadToAIXML(p)
		expected := `<ticket_status mode="work">
  <repo owner="kalverra" name="chainlink" branch="fix/DX-999" is_work="true" default_branch="main"/>
  <git_state dirty="false" dirty_files="0" untracked_files="0"/>
  <decision_hints on_ticket_branch="true" ticket_found="false" needs_ticket_decision="false"/>
  <instructions action="scope_check">
    <rule>Pure Q&amp;A or explanation request -&gt; skip ticketing.</rule>
    <rule>Compare request against active ticket context (auto-written to file):</rule>
    <rule>  - IN_SCOPE -&gt; proceed.</rule>
    <rule>  - TANGENT -&gt; nudge user (offer new ticket/task or park).</rule>
    <rule>  - TICKET_DONE -&gt; suggest summary &amp; close.</rule>
    <rule>Working progress notes: "agents ticket comment &lt;REF&gt; --body "...""</rule>
    <rule>Jira summary update: fetch ticket details -&gt; draft summary -&gt; prompt for explicit user approval -&gt; post via "agents ticket comment &lt;KEY&gt; --jira --body "..."".</rule>
  </instructions>
  <warnings>
    <warning>jira fetch failed: 404 Not Found</warning>
  </warnings>
</ticket_status>
`
		assert.Equal(t, expected, xml)
	})

	t.Run("no ticket key in work repo", func(t *testing.T) {
		t.Parallel()
		p := StatusPayload{
			Mode:                "work",
			RepoOwner:           "kalverra",
			RepoName:            "chainlink",
			Branch:              "main",
			IsWork:              true,
			DefaultBranch:       "main",
			DirtyFiles:          2,
			UntrackedFiles:      0,
			OnTicketBranch:      false,
			TicketFound:         false,
			NeedsTicketDecision: true,
			CandidateEpics: []*Ticket{
				{ID: "DX-50", Title: "Auth Architecture", Status: "In Progress"},
			},
			AssignedTickets: []*Ticket{
				{
					ID:          "DX-55",
					Title:       "Auth Refresh",
					Status:      "In Progress",
					ParentID:    "DX-50",
					ParentTitle: "Auth Architecture",
				},
				{ID: "DX-60", Title: "Standalone Task", Status: "To Do"},
			},
		}

		xml := StatusPayloadToAIXML(p)
		expected := `<ticket_status mode="work">
  <repo owner="kalverra" name="chainlink" branch="main" is_work="true" default_branch="main"/>
  <git_state dirty="true" dirty_files="2" untracked_files="0"/>
  <decision_hints on_ticket_branch="false" ticket_found="false" needs_ticket_decision="true"/>
  <instructions action="pick_or_create">
    <rule>Pure Q&amp;A or explanation request -&gt; skip ticketing.</rule>
    <rule>Require explicit user approval before creating new Jira tickets ("agents ticket create").</rule>
    <rule>All new Jira tickets MUST be linked to an Epic. Auto-suggest candidate Epics from candidate_epics.</rule>
    <rule>Pick from assigned_tickets OR propose new Jira ticket with --epic -&gt; checkout branch &lt;short-title&gt;/&lt;KEY&gt;.</rule>
  </instructions>
  <candidate_epics>
    <epic id="DX-50" title="Auth Architecture" status="In Progress"/>
  </candidate_epics>
  <assigned_tickets>
    <ticket id="DX-55" title="Auth Refresh" status="In Progress" epic_id="DX-50" epic_title="Auth Architecture"/>
    <ticket id="DX-60" title="Standalone Task" status="To Do"/>
  </assigned_tickets>
</ticket_status>
`
		assert.Equal(t, expected, xml)
	})

	t.Run("personal mode with active todoist tasks", func(t *testing.T) {
		t.Parallel()
		p := StatusPayload{
			Mode:                "personal",
			RepoOwner:           "kalverra",
			RepoName:            "dotfiles",
			Branch:              "main",
			IsWork:              false,
			DefaultBranch:       "main",
			DirtyFiles:          0,
			UntrackedFiles:      0,
			OnTicketBranch:      false,
			TicketFound:         false,
			NeedsTicketDecision: true,
			AssignedTodoistTasks: []*Ticket{
				{
					ID:    "111",
					Title: "Personal Task <One>",
					URL:   "https://app.todoist.com/app/task/111",
				},
			},
		}

		xml := StatusPayloadToAIXML(p)
		expected := `<ticket_status mode="personal">
  <repo owner="kalverra" name="dotfiles" branch="main" is_work="false" default_branch="main"/>
  <git_state dirty="false" dirty_files="0" untracked_files="0"/>
  <decision_hints on_ticket_branch="false" ticket_found="false" needs_ticket_decision="true"/>
  <instructions action="pick_or_create">
    <rule>Pure Q&amp;A or explanation request -&gt; skip ticketing.</rule>
    <rule>Pick from assigned_todoist_tasks OR propose new Todoist task ("agents ticket create --title "...""). Branch checkout optional.</rule>
  </instructions>
  <assigned_todoist_tasks>
    <task id="111" title="Personal Task &lt;One&gt;" url="https://app.todoist.com/app/task/111"/>
  </assigned_todoist_tasks>
</ticket_status>
`
		assert.Equal(t, expected, xml)
	})

	t.Run("personal mode tasks grouped by project", func(t *testing.T) {
		t.Parallel()
		p := StatusPayload{
			Mode:                "personal",
			RepoOwner:           "kalverra",
			RepoName:            "dotfiles",
			Branch:              "main",
			IsWork:              false,
			DefaultBranch:       "main",
			DirtyFiles:          0,
			UntrackedFiles:      0,
			OnTicketBranch:      false,
			TicketFound:         false,
			NeedsTicketDecision: true,
			AssignedTodoistTasks: []*Ticket{
				{
					ID:          "111",
					Title:       "Task in Work Project",
					URL:         "https://app.todoist.com/app/task/111",
					ProjectID:   "p1",
					ProjectName: "Work",
				},
				{
					ID:          "222",
					Title:       "Task in Personal Project",
					URL:         "https://app.todoist.com/app/task/222",
					ProjectID:   "p2",
					ProjectName: "Personal",
				},
			},
		}

		xml := StatusPayloadToAIXML(p)
		expected := `<ticket_status mode="personal">
  <repo owner="kalverra" name="dotfiles" branch="main" is_work="false" default_branch="main"/>
  <git_state dirty="false" dirty_files="0" untracked_files="0"/>
  <decision_hints on_ticket_branch="false" ticket_found="false" needs_ticket_decision="true"/>
  <instructions action="pick_or_create">
    <rule>Pure Q&amp;A or explanation request -&gt; skip ticketing.</rule>
    <rule>Pick from assigned_todoist_tasks OR propose new Todoist task ("agents ticket create --title "...""). Branch checkout optional.</rule>
  </instructions>
  <assigned_todoist_tasks>
    <project id="p1" name="Work">
      <task id="111" title="Task in Work Project" url="https://app.todoist.com/app/task/111"/>
    </project>
    <project id="p2" name="Personal">
      <task id="222" title="Task in Personal Project" url="https://app.todoist.com/app/task/222"/>
    </project>
  </assigned_todoist_tasks>
</ticket_status>
`
		assert.Equal(t, expected, xml)
	})

	t.Run("personal mode tasks sorted by updated_at per project", func(t *testing.T) {
		t.Parallel()
		p := StatusPayload{
			Mode:                "personal",
			RepoOwner:           "kalverra",
			RepoName:            "dotfiles",
			Branch:              "main",
			IsWork:              false,
			DefaultBranch:       "main",
			DirtyFiles:          0,
			UntrackedFiles:      0,
			OnTicketBranch:      false,
			TicketFound:         false,
			NeedsTicketDecision: true,
			AssignedTodoistTasks: []*Ticket{
				{
					ID:          "111",
					Title:       "Older Task",
					URL:         "https://app.todoist.com/app/task/111",
					ProjectID:   "p1",
					ProjectName: "Work",
					UpdatedAt:   "2026-08-01T10:00:00Z",
				},
				{
					ID:          "222",
					Title:       "Newer Task",
					URL:         "https://app.todoist.com/app/task/222",
					ProjectID:   "p1",
					ProjectName: "Work",
					UpdatedAt:   "2026-08-05T12:00:00Z",
				},
			},
		}

		xml := StatusPayloadToAIXML(p)
		// Assuming reference time during test execution is close to 2026-08-05, we test last_updated relative rendering format
		assert.Contains(t, xml, `last_updated=`)
		assert.NotContains(t, xml, `updated_at=`)
	})
}

func TestFormatRelativeTime(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		input    string
		expected string
	}{
		{input: "2026-08-05T11:45:00Z", expected: "15m"},
		{input: "2026-08-05T09:00:00Z", expected: "3h"},
		{input: "2026-08-04T12:00:00Z", expected: "1d"},
		{input: "2026-06-06T12:00:00Z", expected: "60d"},
		{input: "2024-08-05T12:00:00Z", expected: "2y"},
		{input: "invalid", expected: "invalid"},
		{input: "", expected: ""},
	}

	for _, tt := range tests {
		got := FormatRelativeTime(tt.input, refTime)
		assert.Equal(t, tt.expected, got, "input: %s", tt.input)
	}
}
