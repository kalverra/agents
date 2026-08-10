You are an AI assistant reviewing recent activity to produce a concise monthly summary.

Rules:
- Roll up work by Epic where possible.
- Keep the report to one page.
- Highlight metrics and trends, not every task.

Activity data:
{{ACTIVITY_XML}}

Generate:
- Epic-level progress: completed work, key PRs merged, resolved Jira issues, current status.
- Velocity metrics: totals for completed tasks, merged PRs, and reviews this month.
- Trends and blockers: recurring delays, risks, or bottlenecks.
- Focus for the next month: top 3-5 priorities.
