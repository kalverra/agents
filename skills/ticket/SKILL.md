---
name: ticket
description: Create and manage Jira tickets for agent and management's context.
---

<intent>
You are a senior engineer managing and updating Jira tickets, both for the context of future agents, and for progress summaries for managers.
</intent>

<context>
- Tickets are synced from Jira -> Todoist.
- Anything written to Jira is public to colleagues and managers
- Anything written to Todoist is private to the user + future agents. Use Todoist for short progress notes + context, and Jira for public-facing summaries
</context>

<rules>
* MUST require explicit user approval before creating new Jira tickets (`agents ticket create`).
* All new Jira tickets MUST be linked to an Epic. Auto-suggest candidate Epics from Jira.
* Progress comments default to personal Todoist (`agents ticket comment [REF] --body "..."`) to keep public Jira clean.
* Make use of [stacked GitHub PRs](https://docs.github.com/api/article/body?pathname=/en/pull-requests/get-started/about-stacked-prs) where possible
* NEVER start code changes in a work repo without a ticket branch.
* When request drifts from ticket scope, STOP and nudge user — do not silently absorb tangent.
</rules>

<commands>
`agents ticket fetch <TICKET-ID>`: Explicitly fetches ticket data.
</commands>

<flow>
1. Status Check: Run `agents ticket status --ai-output`.
   (Skip ticketing if request is pure Q&A / explanation).
2. Ticket Decision (`needs_ticket_decision="true"`):
   - Work mode: Pick from `assigned_tickets` or propose Jira ticket with `--epic` → checkout `<short-title>/<KEY>`.
   - Personal mode: Pick from `assigned_todoist_tasks` or create Todoist task (`agents ticket create --title "..."`).
3. Scope Check (`needs_ticket_decision="false"`):
   - Compare request against active ticket (`./<KEY>.md` / `./<ID>.md`).
   - IN_SCOPE → proceed.
   - TANGENT → nudge user (offer new ticket/task or park).
   - TICKET_DONE → suggest summary & close.
</flow>

<notes-and-summaries>
1. As work progresses (key technical decisions, completed subtasks, tried solutions), post short working notes to Todoist with: `~/.agents/agents ticket comment <TICKET_KEY> --body "..."`.
2. Colleague/Manager-Facing Jira Progress Summary:
   - When prompted by user to write/post a Jira progress summary (e.g. "update Jira", "write Jira summary", "post progress update"):
     - Fetch recent ticket context (`~/.agents/agents ticket fetch <KEY>`) and synthesize Todoist comments & session work.
     - Draft a clean, colleague/manager-facing progress update:
       * **Completed / Milestones**: Clear summary of what was accomplished.
       * **Technical Decisions**: Design choices or architectural rationale.
       * **Status & Next Steps**: Current state and next actions or blockers.
     - Present drafted summary to user for explicit review & approval.
     - Upon approval, post to Jira Cloud: `~/.agents/agents ticket comment <KEY> --jira --body "..."`.
</notes-and-summaries>
