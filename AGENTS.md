# Agents

## Intent

`agents` is a Go CLI that sets up AI harnesses (Claude Code, Antigravity, OpenCode) from one place: global rules,
permissions, skills through `gh skill`, and plugin commands. [README.md](README.md) is the user guide and the plugin
contract (layout, `AGENTS_*` env vars, command lookup order, precedence). Update it when you change any of those.

*Top Design Rule*: use agents for reasoning; write deterministic Go code for data gathering and execution.

Core stays generic and publishable. Personal or work-specific features go in a plugin (mine is
`~/Projects/agents-personal`).

## Agent Harnesses

These are the harnesses I use. Skills, rules and commands must work in all of them.

- [Antigravity CLI](https://antigravity.google/docs/cli/overview): `agy` personal Pro subscription.
- [Claude Code](https://code.claude.com/docs/en/overview): `claude` through company enterprise subscription.
- [OpenCode](https://opencode.ai/v2/docs): `opencode` with
  [Fireworks API](https://docs.fireworks.ai/getting-started/introduction) credits.

## Development Commands

```bash
go test ./...                              # Run test suite
golangci-lint run ./... --fix              # Format and lint
go build -o agents .                       # Build CLI binary
go mod tidy                                # Sync module dependencies
go run . [cmd] --ai-output                 # Run command with machine-readable output for LLMs
goreleaser release --snapshot --clean      # Check the release build
```

Format and lint exclusively with `golangci-lint run ./... --fix`. Running bare `go fmt` breaks line-length formatting
and import order.

## Code Conventions

- **Command output**: Call [`output.Write(cmd, data, humanFn)`](pkg/output/output.go) for command results. When
  `--ai-output` is active, it prints raw strings or compact single-line JSON to `stdout` and skips `humanFn`. In human
  mode, it executes `humanFn` and ignores `data`.
- **Logging**: Log debug info exclusively to `stderr` using `zerolog`. Reserve `stdout` for command output and
  `--ai-output`.
- **Paths**: Get config, data and state dirs from `internal/paths`. Never hardcode `~/.agents` or `~/.config`.
- **Builds**: Core builds with `CGO_ENABLED=0`. Keep cgo dependencies out of `go.mod`.
- **Version**: `internal/buildinfo` is the one source for the binary's version and the skills pin. Release builds set it
  through ldflags in [`.goreleaser.yaml`](.goreleaser.yaml).
- **API clients**: Prefer official Go SDKs. When no official Go SDK exists, build HTTP clients with `resty.dev/v3`.

## Implement Runner (`agents implement`)

Executes an existing plan or spec (`openspec/changes/<name>/`, `specs/<name>/`, or `--plan plan.md`) through worker and
reviewer agent harnesses (`agy`, `claude`, `opencode`).

```bash
agents implement --list                                    # List available modes
agents implement --mode <mode> --session <name>            # Run against auto-detected spec or pass --spec / --plan
agents implement --mode <mode> --session <name> --resume   # Resume session (--accept-plan-changes if plan.md edited)
```

- **Configuration**: `.agents/implement.yaml` in the repo, else `~/.config/agents/implement.yaml` (see
  [`implement.example.yaml`](implement.example.yaml)).
- **Verification gate**: The harness runs verification commands defined in `implement.yaml`'s `verification:` map after
  each step, overridden by any `## Verification` section in the spec or `plan.md`.
- **Review gate**: Requires worker and reviewer agreement per step before committing (`COMMIT: <msg>`). Concludes with a
  whole-diff agent review and a human approval gate (`zed --diff`).
- **Artifacts**: Session state and step logs live under `.agents/local/<session>/`.

## Repository Structure

- [`cmd/`](cmd): Cobra commands. Keep them thin; logic goes in `internal/`.
- [`internal/`](internal): Core packages. Start with `install` (plan and apply), `harness` (one type per harness),
  `rules`, `settings`, `ghskill`, `plugins` and `dispatch` (plugin command lookup and run).
- [`internal/implement/`](internal/implement): Multi-agent spec execution engine.
- [`pkg/`](pkg): Packages plugins import: `output`, `git`, and `plugin` (the env contract).
- [`rules/`](rules): Built-in rules, embedded in the binary.
- [`skills/`](skills): Built-in skills, embedded for the picker and installed from GitHub with `gh skill`. They follow
  the [Agent Skills spec](https://agentskills.io); [`skills.yaml`](.github/workflows/skills.yaml) CI validates them.

## Context Pointers

### External APIs

- [Resty Docs](https://resty.dev/): Build custom HTTP clients when no official Go SDK exists.
- [`gh skill`](https://cli.github.com/manual/gh_skill_install): Skill install flags and agent IDs.
- [Claude Code plugins](https://code.claude.com/docs/en/plugins-reference): The plugin layout agents plugins share.
