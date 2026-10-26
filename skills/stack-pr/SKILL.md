---
name: stack-pr
description: Breakup large branches into PR stacks using gh stack.
compatibility: Requires the agents CLI (agents pr-review), the gh-stack extension, and the gh-stack skill (gh skill install github/gh-stack).
---

<intent>
Analyze large branch/PR diff via pr-review bundle. Plan dependency-ordered stack layers. Execute stack via gh stack.
</intent>

<workflow>
1. Run `agents pr-review` (use `--base <trunk>` if branching off custom base).
2. Read generated review bundle from `review_path`.
3. Analyze diff. Isolate independent layers bottom-to-top:
   - Bottom (Layer 1): schemas, migrations, types, database models.
   - Middle (Layer 2): core business logic, services, domain rules.
   - Top (Layer 3+): API routes, CLI commands, UI components, end-to-end glue.
4. Output proposed stack using <plan-format>.
5. STOP. Wait for user approval.
6. After approval:
   - Initialize stack on bottom branch: `gh stack init <branch-1>`
   - Stage layer 1 files: `git add <files>`
   - Prompt user to commit: `git commit -m "<msg>"` (DO NOT commit directly unless user approves)
   - Add next branch: `gh stack add <branch-2>`
   - Repeat stage + commit per layer
   - Push and open draft PRs: `gh stack submit --auto`
7. Clean up review bundle: `agents pr-review clean`
</workflow>

<rules>
- DO NOT make commits directly. Ask user to commit or use `--no-gpg-sign` only if user explicitly approves.
- ALWAYS use non-interactive `gh stack` flags (`--json`, `--auto`, `--yes`).
- ALWAYS run `agents pr-review` first. Do not run ad-hoc `git diff` or `gh pr diff`.
- Clean up review bundle when finished with `agents pr-review clean`.
</rules>

<plan-format>
```md
## Stack Plan

Summary of split rationale.

### Layers (Bottom -> Top)

1. **`branch-name-1`** (Base: `main`)
   - Intent: Core types and models
   - Files: `models/`, `db/migrations/`
   - PR Title: `feat(db): add user schema`

2. **`branch-name-2`** (Base: `branch-name-1`)
   - Intent: API routes and handlers
   - Files: `api/`, `services/`
   - PR Title: `feat(api): add user endpoints`
```
</plan-format>

<references>
* `gh stack --help`: CLI help menu
* The `gh-stack` skill: official gh-stack skill and CLI guide
</references>
