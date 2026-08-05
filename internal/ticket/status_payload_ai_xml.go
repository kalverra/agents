package ticket

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// StatusPayload holds data needed to render ticket status as XML or text.
type StatusPayload struct {
	Mode                 string // "work" or "personal"
	RepoOwner            string
	RepoName             string
	Branch               string
	IsWork               bool
	DefaultBranch        string
	DirtyFiles           int
	UntrackedFiles       int
	OnTicketBranch       bool
	TicketFound          bool
	NeedsTicketDecision  bool
	Ticket               *Ticket
	CandidateEpics       []*Ticket
	AssignedTickets      []*Ticket
	AssignedTodoistTasks []*Ticket
	Warnings             []string
}

// StatusPayloadToAIXML renders ticket status as XML for LLM consumption (--ai-output).
func StatusPayloadToAIXML(p StatusPayload) string {
	mode := p.Mode
	if mode == "" {
		if p.IsWork {
			mode = "work"
		} else {
			mode = "personal"
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<ticket_status mode=%q>\n", xmlTextEscape(mode))

	fmt.Fprintf(&b, "  <repo owner=%q name=%q branch=%q is_work=%q default_branch=%q/>\n",
		xmlTextEscape(p.RepoOwner),
		xmlTextEscape(p.RepoName),
		xmlTextEscape(p.Branch),
		fmt.Sprintf("%t", p.IsWork),
		xmlTextEscape(p.DefaultBranch),
	)

	isDirty := p.DirtyFiles > 0 || p.UntrackedFiles > 0
	fmt.Fprintf(&b, "  <git_state dirty=%q dirty_files=%q untracked_files=%q/>\n",
		fmt.Sprintf("%t", isDirty),
		fmt.Sprintf("%d", p.DirtyFiles),
		fmt.Sprintf("%d", p.UntrackedFiles),
	)

	fmt.Fprintf(&b, "  <decision_hints on_ticket_branch=%q ticket_found=%q needs_ticket_decision=%q/>\n",
		fmt.Sprintf("%t", p.OnTicketBranch),
		fmt.Sprintf("%t", p.TicketFound),
		fmt.Sprintf("%t", p.NeedsTicketDecision),
	)

	writeInstructionsXML(&b, mode, p.NeedsTicketDecision)

	if p.Ticket != nil {
		writeActiveTicketXML(&b, p.Ticket)
	}

	if len(p.Warnings) > 0 {
		b.WriteString("  <warnings>\n")
		for _, w := range p.Warnings {
			fmt.Fprintf(&b, "    <warning>%s</warning>\n", xmlTextEscape(w))
		}
		b.WriteString("  </warnings>\n")
	}

	if len(p.CandidateEpics) > 0 {
		writeCandidateEpicsXML(&b, p.CandidateEpics)
	}

	if len(p.AssignedTickets) > 0 {
		writeAssignedTicketsXML(&b, p.AssignedTickets)
	}

	if len(p.AssignedTodoistTasks) > 0 {
		writeAssignedTodoistTasksXML(&b, p.AssignedTodoistTasks)
	}

	b.WriteString("</ticket_status>\n")
	return b.String()
}

func writeInstructionsXML(b *strings.Builder, mode string, needsDecision bool) {
	if needsDecision {
		b.WriteString("  <instructions action=\"pick_or_create\">\n")
		b.WriteString("    <rule>Pure Q&amp;A or explanation request -&gt; skip ticketing.</rule>\n")
		if mode == "work" {
			b.WriteString(
				"    <rule>Require explicit user approval before creating new Jira tickets (\"agents ticket create\").</rule>\n",
			)
			b.WriteString(
				"    <rule>All new Jira tickets MUST be linked to an Epic. Auto-suggest candidate Epics from candidate_epics.</rule>\n",
			)
			b.WriteString(
				"    <rule>Pick from assigned_tickets OR propose new Jira ticket with --epic -&gt; checkout branch &lt;short-title&gt;/&lt;KEY&gt;.</rule>\n",
			)
		} else {
			b.WriteString(
				"    <rule>Pick from assigned_todoist_tasks OR propose new Todoist task (\"agents ticket create --title \"...\"\"). Branch checkout optional.</rule>\n",
			)
		}
		b.WriteString("  </instructions>\n")
		return
	}

	b.WriteString("  <instructions action=\"scope_check\">\n")
	b.WriteString("    <rule>Pure Q&amp;A or explanation request -&gt; skip ticketing.</rule>\n")
	b.WriteString("    <rule>Compare request against active ticket context (auto-written to file):</rule>\n")
	b.WriteString("    <rule>  - IN_SCOPE -&gt; proceed.</rule>\n")
	b.WriteString("    <rule>  - TANGENT -&gt; nudge user (offer new ticket/task or park).</rule>\n")
	b.WriteString("    <rule>  - TICKET_DONE -&gt; suggest summary &amp; close.</rule>\n")
	b.WriteString("    <rule>Working progress notes: \"agents ticket comment &lt;REF&gt; --body \"...\"\"</rule>\n")
	b.WriteString(
		"    <rule>Jira summary update: fetch ticket details -&gt; draft summary -&gt; prompt for explicit user approval -&gt; post via \"agents ticket comment &lt;KEY&gt; --jira --body \"...\"\".</rule>\n",
	)
	b.WriteString("  </instructions>\n")
}

func writeActiveTicketXML(b *strings.Builder, tk *Ticket) {
	b.WriteString("  <ticket id=" + fmt.Sprintf("%q", xmlTextEscape(tk.ID)))
	if tk.Status != "" {
		b.WriteString(" status=" + fmt.Sprintf("%q", xmlTextEscape(tk.Status)))
	}
	if tk.URL != "" {
		b.WriteString(" url=" + fmt.Sprintf("%q", xmlTextEscape(tk.URL)))
	}
	b.WriteString(">\n")

	writeAIXMLElement(b, "    ", "title", tk.Title, false)

	if tk.ParentID != "" || tk.ParentTitle != "" {
		b.WriteString("    <epic id=" + fmt.Sprintf("%q", xmlTextEscape(tk.ParentID)))
		if tk.ParentTitle != "" {
			b.WriteString(" title=" + fmt.Sprintf("%q", xmlTextEscape(tk.ParentTitle)))
		}
		b.WriteString("/>\n")
	}

	writeAIXMLElement(b, "    ", "description", tk.Description, true)

	if len(tk.Comments) > 0 {
		b.WriteString("    <comments>\n")
		for _, c := range tk.Comments {
			if c.PostedAt != "" {
				fmt.Fprintf(b, "      <comment time=%q>%s</comment>\n",
					xmlTextEscape(c.PostedAt),
					xmlTextEscape(c.Content),
				)
			} else {
				fmt.Fprintf(b, "      <comment>%s</comment>\n",
					xmlTextEscape(c.Content),
				)
			}
		}
		b.WriteString("    </comments>\n")
	}

	b.WriteString("  </ticket>\n")
}

func writeCandidateEpicsXML(b *strings.Builder, candidateEpics []*Ticket) {
	b.WriteString("  <candidate_epics>\n")
	for _, e := range candidateEpics {
		if e == nil {
			continue
		}
		b.WriteString("    <epic id=" + fmt.Sprintf("%q", xmlTextEscape(e.ID)))
		b.WriteString(" title=" + fmt.Sprintf("%q", xmlTextEscape(e.Title)))
		if e.Status != "" {
			b.WriteString(" status=" + fmt.Sprintf("%q", xmlTextEscape(e.Status)))
		}
		b.WriteString("/>\n")
	}
	b.WriteString("  </candidate_epics>\n")
}

func writeAssignedTicketsXML(b *strings.Builder, assignedTickets []*Ticket) {
	b.WriteString("  <assigned_tickets>\n")
	for _, t := range assignedTickets {
		if t == nil {
			continue
		}
		b.WriteString("    <ticket id=" + fmt.Sprintf("%q", xmlTextEscape(t.ID)))
		b.WriteString(" title=" + fmt.Sprintf("%q", xmlTextEscape(t.Title)))
		if t.Status != "" {
			b.WriteString(" status=" + fmt.Sprintf("%q", xmlTextEscape(t.Status)))
		}
		if t.ParentID != "" {
			b.WriteString(" epic_id=" + fmt.Sprintf("%q", xmlTextEscape(t.ParentID)))
		}
		if t.ParentTitle != "" {
			b.WriteString(" epic_title=" + fmt.Sprintf("%q", xmlTextEscape(t.ParentTitle)))
		}
		b.WriteString("/>\n")
	}
	b.WriteString("  </assigned_tickets>\n")
}

func writeAssignedTodoistTasksXML(b *strings.Builder, assignedTasks []*Ticket) {
	b.WriteString("  <assigned_todoist_tasks>\n")

	type projectGroup struct {
		id    string
		name  string
		tasks []*Ticket
	}
	var groups []projectGroup
	groupIndex := make(map[string]int)
	var unassigned []*Ticket

	for _, t := range assignedTasks {
		if t == nil {
			continue
		}
		key := t.ProjectID
		if key == "" {
			key = t.ProjectName
		}
		if key == "" {
			unassigned = append(unassigned, t)
			continue
		}
		idx, exists := groupIndex[key]
		if !exists {
			idx = len(groups)
			groupIndex[key] = idx
			groups = append(groups, projectGroup{
				id:    t.ProjectID,
				name:  t.ProjectName,
				tasks: []*Ticket{},
			})
		}
		groups[idx].tasks = append(groups[idx].tasks, t)
	}

	if len(groups) > 0 {
		for _, g := range groups {
			slices.SortFunc(g.tasks, func(a, b *Ticket) int {
				return cmp.Compare(b.UpdatedAt, a.UpdatedAt)
			})
			b.WriteString("    <project")
			if g.id != "" {
				b.WriteString(" id=" + fmt.Sprintf("%q", xmlTextEscape(g.id)))
			}
			if g.name != "" {
				b.WriteString(" name=" + fmt.Sprintf("%q", xmlTextEscape(g.name)))
			}
			b.WriteString(">\n")
			for _, t := range g.tasks {
				writeTaskItemXML(b, "      ", t)
			}
			b.WriteString("    </project>\n")
		}
		slices.SortFunc(unassigned, func(a, b *Ticket) int {
			return cmp.Compare(b.UpdatedAt, a.UpdatedAt)
		})
		for _, t := range unassigned {
			writeTaskItemXML(b, "    ", t)
		}
	} else {
		tasks := append([]*Ticket(nil), assignedTasks...)
		slices.SortFunc(tasks, func(a, b *Ticket) int {
			if a == nil || b == nil {
				return 0
			}
			return cmp.Compare(b.UpdatedAt, a.UpdatedAt)
		})
		for _, t := range tasks {
			if t == nil {
				continue
			}
			writeTaskItemXML(b, "    ", t)
		}
	}
	b.WriteString("  </assigned_todoist_tasks>\n")
}

func writeTaskItemXML(b *strings.Builder, indent string, t *Ticket) {
	b.WriteString(indent + "<task id=" + fmt.Sprintf("%q", xmlTextEscape(t.ID)))
	b.WriteString(" title=" + fmt.Sprintf("%q", xmlTextEscape(t.Title)))
	if t.URL != "" {
		b.WriteString(" url=" + fmt.Sprintf("%q", xmlTextEscape(t.URL)))
	}
	if rel := FormatRelativeTime(t.UpdatedAt, time.Now()); rel != "" {
		b.WriteString(" last_updated=" + fmt.Sprintf("%q", xmlTextEscape(rel)))
	}
	b.WriteString("/>\n")
}

// FormatRelativeTime converts ISO 8601/RFC 3339 timestamps into human relative duration strings (e.g. "15m", "3h", "1d", "45d", "2y").
func FormatRelativeTime(ts string, now time.Time) string {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		if t2, err2 := time.Parse("2006-01-02T15:04:05.999999999Z07:00", ts); err2 == nil {
			t = t2
		} else if t3, err3 := time.Parse("2006-01-02T15:04:05Z07:00", ts); err3 == nil {
			t = t3
		} else {
			return ts
		}
	}
	diff := max(now.Sub(t), 0)

	days := int(diff.Hours() / 24)
	if days >= 365 {
		return fmt.Sprintf("%dy", days/365)
	}
	if days >= 1 {
		return fmt.Sprintf("%dd", days)
	}
	hours := int(diff.Hours())
	if hours >= 1 {
		return fmt.Sprintf("%dh", hours)
	}
	mins := int(diff.Minutes())
	if mins >= 1 {
		return fmt.Sprintf("%dm", mins)
	}
	return "now"
}
