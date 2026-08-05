// Package activity provides types and helpers for activity reporting.
package activity

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kalverra/agents/internal/ticket"
)

var jiraKeyRegex = regexp.MustCompile(`\[([A-Z0-9]+-\d+)\]|([A-Z0-9]+-\d+)`)

// ParseTimestamp attempts to parse common Jira and ISO-8601 timestamp formats.
func ParseTimestamp(ts string) (time.Time, bool) {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05.999-0700",
		"2006-01-02T15:04:05.999-07:00",
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, ts); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ExtractKeyAndTaskID extracts a Jira key (e.g. DX-123) from text.
func ExtractKeyAndTaskID(text string) (jiraKey string, taskID string) {
	if text == "" {
		return "", ""
	}
	if matches := jiraKeyRegex.FindStringSubmatch(text); len(matches) > 0 {
		for _, m := range matches[1:] {
			if m != "" {
				jiraKey = m
				break
			}
		}
	}
	return jiraKey, taskID
}

// TaskComment represents a comment left on a Todoist task.
type TaskComment struct {
	ID       string `json:"id"`
	PostedAt string `json:"posted_at"`
	Content  string `json:"content"`
}

// TaskMovement represents a movement/transition event for a Todoist task.
type TaskMovement struct {
	Date   string `json:"date"`
	Action string `json:"action"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
}

// TodoistTask represents a completed or active task in Todoist.
type TodoistTask struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Description  string         `json:"description,omitempty"`
	CompletedAt  string         `json:"completed_at,omitempty"`
	URL          string         `json:"url,omitempty"`
	Labels       []string       `json:"labels,omitempty"`
	JiraKey      string         `json:"jira_key,omitempty"`
	IsJiraLinked bool           `json:"is_jira_linked"`
	Comments     []TaskComment  `json:"comments,omitempty"`
	Movements    []TaskMovement `json:"movements,omitempty"`
}

// FilterJiraLinkedTasks filters out non-Jira-linked tasks.
func FilterJiraLinkedTasks(tasks []TodoistTask) []TodoistTask {
	var out []TodoistTask
	for _, t := range tasks {
		if t.IsJiraLinked {
			out = append(out, t)
		}
	}
	return out
}

// CommitInfo represents commit details for a PR.
type CommitInfo struct {
	SHA     string `json:"sha"`
	Date    string `json:"date"`
	Message string `json:"message"`
	JiraKey string `json:"jira_key,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
}

// PullRequestInfo represents basic metadata about a GitHub PR.
type PullRequestInfo struct {
	Number     int          `json:"number"`
	Title      string       `json:"title"`
	Body       string       `json:"body,omitempty"`
	URL        string       `json:"url"`
	State      string       `json:"state,omitempty"`
	Repository string       `json:"repository"`
	CreatedAt  time.Time    `json:"created_at,omitzero"`
	MergedAt   time.Time    `json:"merged_at,omitzero"`
	JiraKey    string       `json:"jira_key,omitempty"`
	TaskID     string       `json:"task_id,omitempty"`
	Commits    []CommitInfo `json:"commits,omitempty"`
}

// ReviewInfo represents a GitHub PR review.
type ReviewInfo struct {
	PRNumber   int    `json:"pr_number"`
	PRTitle    string `json:"pr_title"`
	State      string `json:"state"`
	Repository string `json:"repository"`
	URL        string `json:"url"`
}

// GitHubUserActivity holds user contributions on GitHub.
type GitHubUserActivity struct {
	PRsOpened []PullRequestInfo `json:"prs_opened"`
	PRsMerged []PullRequestInfo `json:"prs_merged"`
	PRReviews []ReviewInfo      `json:"pr_reviews"`
}

// CurrentTicketInfo holds active branch ticket context.
type CurrentTicketInfo struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Source string `json:"source"`
}

// JiraIssueInfo holds metadata for a Jira issue updated or closed within timeframe.
type JiraIssueInfo struct {
	Key       string `json:"key"`
	Summary   string `json:"summary"`
	Status    string `json:"status"`
	EpicKey   string `json:"epic_key,omitempty"`
	EpicTitle string `json:"epic_title,omitempty"`
	Sprint    string `json:"sprint,omitempty"`
	URL       string `json:"url,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Report holds the aggregated activity across platforms.
type Report struct {
	Timeframe     string             `json:"timeframe"`
	Since         time.Time          `json:"since"`
	Until         time.Time          `json:"until"`
	Project       string             `json:"project"`
	CurrentTicket *CurrentTicketInfo `json:"current_ticket,omitempty"`
	Todoist       []TodoistTask      `json:"todoist"`
	JiraIssues    []JiraIssueInfo    `json:"jira_issues,omitempty"`
	GitHub        GitHubUserActivity `json:"github"`
}

// ParseJiraInfo inspects content, description, and labels for Jira linkage.
func ParseJiraInfo(content, description string, labels []string) (key string, isLinked bool) {
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l), "jira-sync") {
			isLinked = true
			break
		}
	}

	combined := content + " " + description
	if matches := jiraKeyRegex.FindStringSubmatch(combined); len(matches) > 0 {
		for _, m := range matches[1:] {
			if m != "" {
				key = m
				isLinked = true
				break
			}
		}
	}

	return key, isLinked
}

func statusPriority(status string) int {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.Contains(s, "block"):
		return 0
	case strings.Contains(s, "progress"):
		return 1
	case strings.Contains(s, "review"):
		return 2
	case strings.Contains(s, "done") || strings.Contains(s, "close"):
		return 3
	default:
		return 4
	}
}

// FormatXMLReport formats the report into XML suitable for AI consumption.
func FormatXMLReport(r *Report) string {
	var b strings.Builder

	fmt.Fprintf(&b, "<activity_report timeframe=%q since=%q until=%q project=%q>\n",
		r.Timeframe,
		r.Since.Format(time.RFC3339),
		r.Until.Format(time.RFC3339),
		r.Project,
	)

	if r.CurrentTicket != nil {
		fmt.Fprintf(
			&b,
			"  <current_ticket id=%q source=%q>%s</current_ticket>\n",
			escapeXML(r.CurrentTicket.ID),
			escapeXML(r.CurrentTicket.Source),
			escapeXML(r.CurrentTicket.Title),
		)
	}

	linkedTodoist := FilterJiraLinkedTasks(r.Todoist)
	if len(linkedTodoist) > 0 {
		b.WriteString("  <todoist_activity>\n")
		for _, t := range linkedTodoist {
			writeTodoistTaskXML(&b, t, "    ")
		}
		b.WriteString("  </todoist_activity>\n")
	}

	if len(r.JiraIssues) > 0 {
		writeJiraIssuesXML(&b, r.JiraIssues, r.Until)
	}

	writeGitHubActivityXML(&b, &r.GitHub)

	b.WriteString("</activity_report>")
	return b.String()
}

func writeJiraIssuesXML(b *strings.Builder, jiraIssues []JiraIssueInfo, until time.Time) {
	issues := append([]JiraIssueInfo(nil), jiraIssues...)
	slices.SortFunc(issues, func(a, b JiraIssueInfo) int {
		pA := statusPriority(a.Status)
		pB := statusPriority(b.Status)
		if pA != pB {
			return cmp.Compare(pA, pB)
		}
		return cmp.Compare(b.UpdatedAt, a.UpdatedAt)
	})

	b.WriteString("  <jira_issues>\n")
	for _, ji := range issues {
		attrs := fmt.Sprintf(`key=%q status=%q`, ji.Key, ji.Status)
		if rel := ticket.FormatRelativeTime(ji.UpdatedAt, until); rel != "" {
			attrs += fmt.Sprintf(` last_updated=%q`, rel)
		}
		if ji.Sprint != "" {
			attrs += fmt.Sprintf(` sprint=%q`, ji.Sprint)
		}
		if ji.EpicKey != "" {
			attrs += fmt.Sprintf(` epic_key=%q`, ji.EpicKey)
		}
		if ji.EpicTitle != "" {
			attrs += fmt.Sprintf(` epic_title=%q`, ji.EpicTitle)
		}
		if ji.URL != "" {
			attrs += fmt.Sprintf(` url=%q`, ji.URL)
		}
		fmt.Fprintf(b, "    <issue %s>%s</issue>\n", attrs, escapeXML(ji.Summary))
	}
	b.WriteString("  </jira_issues>\n")
}

func writeGitHubActivityXML(b *strings.Builder, gh *GitHubUserActivity) {
	var filteredReviews []ReviewInfo
	for _, rev := range gh.PRReviews {
		if strings.Contains(rev.PRTitle, "Version Packages") ||
			strings.HasPrefix(strings.ToLower(rev.PRTitle), "dependabot") {
			continue
		}
		filteredReviews = append(filteredReviews, rev)
	}

	hasGitHubActivity := len(gh.PRsOpened) > 0 || len(gh.PRsMerged) > 0 || len(filteredReviews) > 0
	if !hasGitHubActivity {
		return
	}

	b.WriteString("  <github_activity>\n")
	if len(gh.PRsOpened) > 0 {
		b.WriteString("    <prs_opened>\n")
		for _, pr := range gh.PRsOpened {
			writePROpenedXML(b, pr)
		}
		b.WriteString("    </prs_opened>\n")
	}

	if len(gh.PRsMerged) > 0 {
		b.WriteString("    <prs_merged>\n")
		for _, pr := range gh.PRsMerged {
			writePRMergedXML(b, pr)
		}
		b.WriteString("    </prs_merged>\n")
	}

	if len(filteredReviews) > 0 {
		b.WriteString("    <pr_reviews>\n")
		for _, rev := range filteredReviews {
			fmt.Fprintf(
				b,
				"      <review repo=%q pr_number=\"%d\" state=%q url=%q>%s</review>\n",
				rev.Repository,
				rev.PRNumber,
				rev.State,
				rev.URL,
				escapeXML(rev.PRTitle),
			)
		}
		b.WriteString("    </pr_reviews>\n")
	}
	b.WriteString("  </github_activity>\n")
}

func writePROpenedXML(b *strings.Builder, pr PullRequestInfo) {
	prAttrs := fmt.Sprintf(`repo=%q number="%d" url=%q`, pr.Repository, pr.Number, pr.URL)
	if pr.JiraKey != "" {
		prAttrs += fmt.Sprintf(` jira_key=%q`, pr.JiraKey)
	}
	if pr.TaskID != "" {
		prAttrs += fmt.Sprintf(` task_id=%q`, pr.TaskID)
	}

	if len(pr.Commits) == 0 && pr.Body == "" {
		fmt.Fprintf(b, "      <pr %s>%s</pr>\n", prAttrs, escapeXML(pr.Title))
		return
	}

	fmt.Fprintf(b, "      <pr %s>\n        <title>%s</title>\n", prAttrs, escapeXML(pr.Title))
	if pr.Body != "" {
		fmt.Fprintf(b, "        <description>%s</description>\n", escapeXML(pr.Body))
	}
	if len(pr.Commits) > 0 {
		b.WriteString("        <commits>\n")
		for _, cm := range pr.Commits {
			cmAttrs := fmt.Sprintf(`sha=%q date=%q`, cm.SHA, cm.Date)
			if cm.JiraKey != "" {
				cmAttrs += fmt.Sprintf(` jira_key=%q`, cm.JiraKey)
			}
			if cm.TaskID != "" {
				cmAttrs += fmt.Sprintf(` task_id=%q`, cm.TaskID)
			}
			fmt.Fprintf(b, "          <commit %s>%s</commit>\n", cmAttrs, escapeXML(cm.Message))
		}
		b.WriteString("        </commits>\n")
	}
	b.WriteString("      </pr>\n")
}

func writePRMergedXML(b *strings.Builder, pr PullRequestInfo) {
	prAttrs := fmt.Sprintf(`repo=%q number="%d" url=%q`, pr.Repository, pr.Number, pr.URL)
	if pr.JiraKey != "" {
		prAttrs += fmt.Sprintf(` jira_key=%q`, pr.JiraKey)
	}
	if pr.TaskID != "" {
		prAttrs += fmt.Sprintf(` task_id=%q`, pr.TaskID)
	}
	if pr.Body == "" {
		fmt.Fprintf(b, "      <pr %s>%s</pr>\n", prAttrs, escapeXML(pr.Title))
	} else {
		fmt.Fprintf(
			b,
			"      <pr %s>\n        <title>%s</title>\n        <description>%s</description>\n      </pr>\n",
			prAttrs,
			escapeXML(pr.Title),
			escapeXML(pr.Body),
		)
	}
}

func writeTodoistTaskXML(b *strings.Builder, t TodoistTask, indent string) {
	attrs := fmt.Sprintf(`id=%q jira_linked="%t"`, t.ID, t.IsJiraLinked)
	if t.JiraKey != "" {
		attrs += fmt.Sprintf(` jira_key=%q`, t.JiraKey)
	}
	if t.CompletedAt != "" {
		attrs += fmt.Sprintf(` completed_at=%q`, t.CompletedAt)
	}
	fmt.Fprintf(b, "%s<task %s>\n", indent, attrs)
	fmt.Fprintf(b, "%s  <title>%s</title>\n", indent, escapeXML(t.Title))
	if t.Description != "" {
		fmt.Fprintf(b, "%s  <description>%s</description>\n", indent, escapeXML(t.Description))
	}
	if t.URL != "" {
		fmt.Fprintf(b, "%s  <url>%s</url>\n", indent, escapeXML(t.URL))
	}
	if len(t.Comments) > 0 {
		fmt.Fprintf(b, "%s  <comments>\n", indent)
		for _, c := range t.Comments {
			fmt.Fprintf(b, "%s    <comment posted_at=%q>%s</comment>\n", indent, c.PostedAt, escapeXML(c.Content))
		}
		fmt.Fprintf(b, "%s  </comments>\n", indent)
	}
	if len(t.Movements) > 0 {
		fmt.Fprintf(b, "%s  <movement_history>\n", indent)
		for _, m := range t.Movements {
			fmt.Fprintf(
				b,
				"%s    <movement date=%q action=%q from=%q to=%q/>\n",
				indent,
				m.Date,
				m.Action,
				m.From,
				m.To,
			)
		}
		fmt.Fprintf(b, "%s  </movement_history>\n", indent)
	}
	fmt.Fprintf(b, "%s</task>\n", indent)
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}
