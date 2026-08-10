<goals>
* Portable skills and rules for ALL agents + harnesses.
* Auto-discover and install skills across agents installed on machine
* Use agent for reasoning, code for everything else. Agent shouldn't waste time fumbling with MCP servers or API calls to get info. Anything deterministic should be a flow in code to gather info for the AI to do what it's best at.
</goals>

<important-files>
GLOBAL_AGENTS.md - machine-wide context to dictate universal rules + personality
USER_AGENTS.md - more private details to inject in GLOBAL_AGENTS.md
skills/ - Markdown skills for agents to use.
prompts/ - Markdown prompt templates for automated activity reports.
cmd/ - Go CLI entry
internal/ - Go CLI internals
</important-files>

<commands>
go mod tidy # tidy dependencies
go build -o agents . # build
go test ./... # test
golangci-lint run ./... --fix # lint
go run . -h # help menu
go run . [cmd] --ai-output # run commands with output specifically for LLM consumption

DO NOT run `go fmt`, `goimports`, or any other base go commands outside of the above
</commands>

<style>
- Use zerolog for all logging. Logging is not user output, it is only for debugging.
- When an official Go package doesn't exist to write a client to an API, use resty.
- Each Go command should utilize the `--ai-output` flag to format output for consumption by LLMs.
- Use `output.Write(command, data, func() {...})` to cleanly handle both JSON and human output paths in one call without branching.
</style>

<docs>
<api>
When making API calls, look for up-to-date, officially supported Go clients. If none exist, build a basic client using resty.
- [Resty Docs](https://resty.dev/)
- [Todoist API](https://developer.todoist.com/api/v1/)
- [Jira API](https://developer.atlassian.com/cloud/jira/platform/rest/v3)
</api>
<agents>
Frequently used agent harnesses. Docs updated frequently, double-check assumptions on settings.
- [Antigravity CLI](https://antigravity.google/docs/cli/overview)
- [Opencode](https://opencode.ai/docs)
- [Claude Code](https://code.claude.com/docs/en/overview)
</agents>
</docs>
