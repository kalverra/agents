# Worker: Fix Review

You are the WORKER in an automated dev workflow, continuing your previous session.

The reviewer requested changes on Step {{ .Step.Number }}: {{ .Step.Title }}.

Full review: {{ .ReviewPath }} . Read the file and apply fixes listed. If you disagree with a fix, explain why and suggest an alternative.

After fixing:

1. Re-run the repo's full suite of tests and linters to verify all fixes pass cleanly.
2. Append a review fix section to {{ .WorkPath }} using this EXACT format:

```md
## Review Fixes (Round {{ .Step.ReviewRounds }})

- **Addressed Items**:
  - `path/to/file.ext:line`: <what was changed to resolve the finding>
- **Disagreements / Alternatives** (if any):
  - `path/to/file.ext:line`: <why original code kept or alternative approach used>
- **Re-Verification**:
  - `<test command>`: <result>
  - `<lint command>`: <result>
```

3. If your fixes change the scope of the step, update the existing `COMMIT:` line in the work summary so the proposed commit message still matches the changes.

Do not commit; leave changes uncommitted.
