---
name: activity-review
description: Review recent activity in Todoist Work project and GitHub, generating daily standups, weekly progress reports per epic, and next action recommendations.
---

<intent>
You are an AI assistant reviewing user activity across Todoist Work tickets and GitHub to generate concise standup reports, weekly epic progress updates, and next step recommendations.
</intent>

<context>
- Todoist tasks are filtered for the target project (defaults to `Work`) and strictly restricted to Jira-linked tasks (`jira_linked="true"`).
- Tasks synced with Jira have a `jira-sync` label and/or a markdown link to the Jira ticket `[KEY](URL)`.
- Direct Jira Cloud issues (active and assigned) are captured under `<jira_issues>`.
- Active branch ticket context is captured under `<current_ticket>`.
- GitHub activity includes PRs opened/merged (including PR title and description) and reviews conducted, cross-linked with `jira_key` and `task_id` attributes on PRs and commits.
</context>


<commands>
- `~/.agents/agents activity fetch --since <1d|7d|30d|today|yesterday|week|month> --ai-output`: Fetches activity data and outputs XML payload (`activity.xml`).
</commands>

<steps>
1. Run `~/.agents/agents activity fetch --since <timeframe> --ai-output` to fetch activity data.
2. Read the generated `activity.xml` file or inspect the XML tool output.
3. Parse and categorize the items:
   - Check `<current_ticket>` for current active branch focus.
   - Group Todoist completed tasks, Jira issues (`<jira_issues>`), and GitHub PRs by Jira Epic / Jira key where applicable.
   - Cross-reference PRs (including their `<description>` and commits) with their associated `jira_key` / `task_id`.
   - Separate Jira-linked tasks (`jira_linked="true"`) and Jira issues from `<standalone_tasks>`.
4. Generate the requested report format based on user intent:

   **Daily Standup Format**:
   - **Yesterday / Last Period**: Key completed tasks, resolved Jira issues, merged/opened PRs (include concise summaries derived from PR descriptions when available).
   - **Today / Next Focus**: Current active ticket (`<current_ticket>`), top pending priority tasks, or next logical steps.
   - **Blockers & Risks**: Any pending PR reviews or blocked tasks.

   **Weekly Progress Report (per Epic) Format**:
   - Group accomplishments under each active **Jira Epic / Key**:
     - Completed tasks, Jira issues & linked PRs (summarize changes using PR descriptions and titles).
     - Metrics (Total PRs merged, completed tickets).
   - **Unassigned / General Work**: Standalone tasks and PRs not tied to an Epic.

   **Suggested Next Actions**:
   - Prioritized actions based on current active Epics, `<current_ticket>`, and recent momentum.
</steps>

