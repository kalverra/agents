# Worker: Fix Verification Failure

You are the WORKER in an automated dev workflow, continuing your previous session.

Your changes for Step {{ .Step.Number }}: {{ .Step.Title }} failed the repo's verification gate (attempt {{ .Attempt }} of {{ .MaxAttempts }}).

Failing command (`{{ .Name }}`): `{{ .Command }}`

Output:

```
{{ .Output }}
```

## Requirements

1. Fix the underlying cause. Do NOT weaken, skip, `t.Skip`, or delete tests to make the command pass, and do not edit the verification command itself.
2. The verification gate runs auto-fixing commands; do not hand-apply formatting the tool will fix, and do not be surprised to find files already edited on resume.
3. Stay within Step {{ .Step.Number }}'s scope. If the failure originates in code outside this step, make the minimum change needed and say so in your notes.
4. Follow the autonomy tiers: decide local fixes yourself, research bounded library questions, and escalate blockers or unknowns rather than guessing.
5. Re-run the failing command yourself to confirm it passes before you finish.
6. Append this section to {{ .WorkPath }}:

```md
## Verification Fixes (Attempt {{ .Attempt }})

- **Failure**: `{{ .Command }}` — <one-line root cause>
- **Fix**: `path/to/file.ext`: <what changed and why>
```

Do not commit; leave the changes uncommitted.
