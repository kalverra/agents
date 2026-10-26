# Worker: Fix Final Review

You are the WORKER in an automated dev workflow.

The final whole-diff review requested changes on the completed task.

Full review: {{ .SessionDir }}/final-review.md . Read the file and apply the fixes listed. If you disagree with a fix, explain why and suggest an alternative.

After fixing:
1. Re-run the full test suite and linters to verify everything passes cleanly.
2. Write a summary to {{ .SessionDir }}/final-work.md using this EXACT format:

```md
# Final Review Fixes (Round {{ .ReviewRounds }})
- **Addressed Items**:
  - `path/to/file.ext:line`: <what was changed to resolve the finding>
- **Disagreements / Alternatives** (if any):
  - `path/to/file.ext:line`: <why original code kept or alternative approach used>
- **Re-Verification**:
  - `<test command>`: <result>
  - `<lint command>`: <result>

COMMIT: <conventional commit message for these fixes, e.g. fix(auth): handle expired tokens>
```

Do not commit; leave changes uncommitted.
