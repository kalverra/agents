### Ticket Status

Before working on a ticket, load its details unless they are already in context (for example in a `<ticket_status>` block).

- Find the ticket key (for example `ABC-123`) in the task, the branch name, or recent commit messages.
- Fetch the ticket with the first tool that works: `agents ticket status --ai-output`, a Jira MCP server, `acli jira workitem view <KEY>`, or `jira issue view <KEY>`.
- If no tool can reach the ticket, say so and continue.
