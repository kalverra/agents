# agents

One CLI to set up Claude Code, Antigravity and OpenCode from one place. It writes your global rules into each harness,
merges permissions, installs skills with [`gh skill`](https://cli.github.com/manual/gh_skill_install), and runs plugin
commands that do deterministic work for your agents.

**Use the agent for reasoning. Use code for everything else.** Skills and rules stay small. Plugin commands fetch exactly
the data an agent needs, so it doesn't burn context on MCP servers or shell output.

## Install

```sh
brew install --cask kalverra/tap/agents
# or
go install github.com/kalverra/agents@latest
```

Skills need GitHub CLI 2.90 or newer (`gh skill`).

## Quick start

```sh
agents discover              # Which harnesses are on this machine
agents install --dry-run     # Show the plan
agents install               # Pick harnesses, rules and skills, then write them
agents install --reconfigure # Pick again later
agents uninstall             # Remove what install wrote
agents skills update         # Update installed skills (gh skill update)
```

`install` only touches what it owns:

- **Rules** go between `<!-- agents:begin -->` and `<!-- agents:end -->` in `~/.claude/CLAUDE.md`,
  `~/.gemini/GEMINI.md` and `~/.config/opencode/AGENTS.md`. Text outside the markers stays.
- **Permissions** from `config.yaml` merge into each harness's settings. Uninstall removes only entries agents added.
- **Skills** install through `gh skill`. Built-in skills are pinned to this binary's release tag; a dev build installs
  the latest. Skills can also come from [other repos](#skills).

## Files

| Path | Contents |
|---|---|
| `~/.config/agents/config.yaml` | Chosen harnesses, rules, skills, permissions and plugins |
| `~/.config/agents/rules/*.md` | Your own rules. A file with a built-in rule's name replaces it. |
| `~/.config/agents/implement.yaml` | `agents implement` modes (see [`implement.example.yaml`](implement.example.yaml)) |
| `~/.local/share/agents/plugins/<name>` | Plugins added from a git URL |
| `~/.local/share/agents/plugin-data/<name>` | Each plugin's data |
| `~/.local/state/agents/installed.json` | What install wrote, so uninstall can remove it |

All paths follow `XDG_CONFIG_HOME`, `XDG_DATA_HOME` and `XDG_STATE_HOME`, on macOS too.

## Skills

Built-in skills live in [`skills/`](skills). You can also install them without the CLI:

```sh
gh skill install kalverra/agents refactoring --agent claude-code --scope user
```

To install a skill from another GitHub repo, name it `owner/repo/name`, with an optional `@ref` (a tag or commit), in
`config.yaml` or `--skills`. Without a ref, gh installs the repo's latest release, else its default branch. A remote
skill replaces any built-in or plugin skill with the same name. Once named, it shows up in `agents install --reconfigure`.

```yaml
skills:
  - refactoring
  - mattpocock/skills/grilling
  - mattpocock/skills/handoff@v1.3.1
```

## Plugins

A plugin adds commands, rules and skills. It uses the
[Claude Code plugin layout](https://code.claude.com/docs/en/plugins-reference), so the same repo also works as a Claude
Code plugin.

```sh
agents plugin add ~/src/my-plugin               # Use a local dir in place
agents plugin add git@github.com:me/my-plugin.git@v1.0.0  
agents plugin list
agents plugin update
agents install                                   # Offer the plugin's rules and skills
```

Plugins run with your permissions and no sandbox. Only add plugins you trust.

**Pick one way to install a plugin per harness.** For Claude Code, use either `agents plugin add` or
`claude plugin install`, not both, or its skills show up twice.

### Layout

```text
my-plugin/
├── .claude-plugin/plugin.json   # Optional
├── bin/agents-hello             # Runs as `agents hello`
├── rules/hello.md               # Offered by `agents install`
└── skills/hello/SKILL.md        # Offered by `agents install`
```

`plugin.json` uses the Claude Code manifest. agents reads two keys from its free-form `metadata`:

```json
{
  "name": "my-plugin",
  "metadata": {
    "agents-build": "go build -o bin/ ./cmd/...",
    "agents-commands": "hello=Say hello;ticket=Ticket helpers"
  }
}
```

- `agents-build` runs in the plugin dir on `plugin add` and `plugin update`, after you confirm.
- `agents-commands` gives each command its line in `agents --help`.

### Commands

`agents <cmd>` looks for a command in this order:

1. Built-in commands.
2. `bin/agents-<cmd>` in each plugin, in `config.yaml` order.
3. `agents-<cmd>` on `PATH`.

A command can be any executable. Args, stdin, stdout and the exit code pass through. It gets these env vars (Go plugins
can read them with [`pkg/plugin`](pkg/plugin)):

| Var | Value |
|---|---|
| `AGENTS_AI_OUTPUT` | `1` when `--ai-output` is on. Print compact output for LLMs. |
| `AGENTS_LOG_LEVEL` | zerolog level name |
| `AGENTS_CONFIG_DIR`, `AGENTS_DATA_DIR`, `AGENTS_STATE_DIR` | agents' own dirs |
| `AGENTS_PLUGIN_ROOT` | The plugin's dir. Unset for `PATH` commands. |
| `AGENTS_PLUGIN_DATA` | The plugin's data dir, created on first run. Unset for `PATH` commands. |
| `AGENTS_BIN` | The `agents` binary that ran the command |

### Precedence

When two sources share a name:

- **Commands:** built-in, then the earlier plugin.
- **Rules:** your `~/.config/agents/rules`, then built-in, then the earlier plugin.
- **Skills:** built-in, then the earlier plugin.

## Release

Push a `v*` tag. [`release.yaml`](.github/workflows/release.yaml) runs goreleaser, which builds darwin and linux for
amd64 and arm64 and updates the `kalverra/homebrew-tap` cask. It needs a `HOMEBREW_TAP_GITHUB_TOKEN` secret with write
access to the tap. Skills share the binary's tags, so `gh skill install kalverra/agents@vX` matches binary `vX`.

Check a release locally with `goreleaser release --snapshot --clean`.
