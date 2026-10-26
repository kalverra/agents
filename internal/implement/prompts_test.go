package implement

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerPromptContract(t *testing.T) {
	t.Parallel()
	step := Step{Number: 2, Title: "Implement logic", Body: "Fill in logic.\nHandle edge cases."}
	p := WorkerPrompt(
		"/repo/.agents/local/s1",
		step,
		[]string{"/repo/.agents/local/s1/1-first/work.md: did x"},
		"",
		Verification{},
	)

	for _, want := range []string{
		"Step 2: Implement logic",
		"Fill in logic.",
		"/repo/.agents/local/s1/plan.md",
		"/repo/.agents/local/s1/2-implement-logic/work.md",
		"Do not commit",
		"1-first/work.md: did x",
		"authoritative scope",
		"full suite of tests/linters",
		"focus on the WHY of your changes",
		"architectural intent",
		"## Key Changes",
		"## Decisions",
		"## Summary",
		"## Notes & Deviations",
		"COMMIT: ",
	} {
		assert.Contains(t, p, want)
	}
}

func TestWorkerPromptSessionContext(t *testing.T) {
	t.Parallel()
	step := Step{Number: 1, Title: "Do it", Body: "Body."}

	plain := WorkerPrompt("/repo/.agents/local/s1", step, nil, "", Verification{})
	assert.NotContains(t, plain, "<ticket_context")
	assert.NotContains(t, plain, "session context")

	ticketXML := "<ticket_context id=\"DX-123\">\n  <title>Fix bug</title>\n</ticket_context>"
	withCtx := WorkerPrompt("/repo/.agents/local/s1", step, nil, ticketXML, Verification{})
	assert.Contains(t, withCtx, ticketXML)
	assert.Contains(t, withCtx, "session context")
}

func TestWorkerPromptUnknownContract(t *testing.T) {
	t.Parallel()
	step := Step{Number: 1, Title: "Do it", Body: "Body."}
	p := WorkerPrompt("/repo/.agents/local/s1", step, nil, "", Verification{})

	for _, want := range []string{
		"**Unknown**:",
		"do NOT reverse-engineer binaries, decompile, or scrape undocumented internals",
		"## Notes & Deviations",
		"burns the step budget",
	} {
		assert.Contains(t, p, want)
	}
}

func TestWorkerPromptVerificationContract(t *testing.T) {
	t.Parallel()
	step := Step{Number: 1, Title: "Do it", Body: "Body."}

	// No declared gate: the worker stays responsible for the full suite.
	none := WorkerPrompt("/repo/.agents/local/s1", step, nil, "", Verification{})
	assert.Contains(t, none, "Run the repo's full suite of tests/linters")
	assert.NotContains(t, none, "The orchestrator runs the repo's verification itself")

	// Declared gate: the worker is told to iterate with targeted filters.
	gated := WorkerPrompt("/repo/.agents/local/s1", step, nil, "", Verification{
		Cmds: []VerifyCommand{
			{Name: "test", Command: "go test ./..."},
			{Name: "lint", Command: "golangci-lint run ./..."},
		},
	})
	assert.Contains(t, gated, "The orchestrator runs the repo's verification itself")
	assert.Contains(t, gated, "go test ./...")
	assert.Contains(t, gated, "golangci-lint run ./...")
	assert.Contains(t, gated, "targeted filters")
	assert.NotContains(t, gated, "Run the repo's full suite of tests/linters")
}

func TestVerifyFixPromptContract(t *testing.T) {
	t.Parallel()
	step := Step{Number: 2, Title: "Wire it up"}
	cmd := VerifyCommand{Name: "test", Command: "go test ./..."}
	p := VerifyFixPrompt("/repo/.agents/local/s1", step, cmd, "FAIL: boom", 1, 2)

	for _, want := range []string{
		"Step 2: Wire it up",
		"failed the repo's verification gate",
		"attempt 1 of 2",
		"go test ./...",
		"FAIL: boom",
		"/repo/.agents/local/s1/2-wire-it-up/work.md",
		"Do NOT weaken, skip, `t.Skip`, or delete tests",
		"Do not commit",
	} {
		assert.Contains(t, p, want)
	}
}

func TestReviewerPromptContract(t *testing.T) {
	t.Parallel()
	step := Step{Number: 1, Title: "Scaffold", Body: "stub it"}
	p := ReviewerPrompt("/repo/.agents/local/s1", step, 1)

	for _, want := range []string{
		"Step 1: Scaffold",
		"git diff",
		"/repo/.agents/local/s1/1-scaffold/r1.diff",
		"/repo/.agents/local/s1/1-scaffold/r1.review.md",
		"VERDICT: APPROVE",
		"VERDICT: REQUEST_CHANGES",
		"## Findings",
		"## Decisions",
		"COMMIT",
	} {
		assert.Contains(t, p, want)
	}
}

func TestWorkerPromptAutonomyTiersContract(t *testing.T) {
	t.Parallel()
	step := Step{Number: 1, Title: "Do it", Body: "Body."}
	p := WorkerPrompt("/repo/.agents/local/s1", step, nil, "", Verification{})

	// All three tier headings
	assert.Contains(t, p, "**Decide yourself, do not ask**")
	assert.Contains(t, p, "**Research, then decide, and write down what you learned**")
	assert.Contains(t, p, "**Escalate, do not guess**")

	// **Unknown**: in escalate tier
	assert.Contains(t, p, "**Unknown**:")

	// Permits reading repo source, git log, dependency source, official docs
	assert.Contains(t, p, "git log")
	assert.Contains(t, p, "dependency source")
	assert.Contains(t, p, "official docs")

	// Bans reverse-engineering binaries, decompiling, scraping internals
	assert.Contains(t, p, "reverse-engineer")
	assert.Contains(t, p, "decompile")
	assert.Contains(t, p, "scraping undocumented internals")

	// Mandated summary format includes ## Decisions with "None" allowance and COMMIT: last
	assert.Contains(t, p, "## Decisions")
	decIdx := strings.LastIndex(p, "## Decisions")
	notesIdx := strings.LastIndex(p, "## Notes & Deviations")
	commitIdx := strings.LastIndex(p, "COMMIT: ")
	require.NotEqual(t, -1, decIdx, "expected ## Decisions in prompt")
	require.NotEqual(t, -1, notesIdx, "expected ## Notes & Deviations in prompt")
	require.NotEqual(t, -1, commitIdx, "expected COMMIT: in prompt")
	assert.Less(t, decIdx, notesIdx, "## Decisions must appear before ## Notes & Deviations")
	assert.Less(t, notesIdx, commitIdx, "## Notes & Deviations must appear before COMMIT:")
	assert.Contains(t, p[decIdx:notesIdx], "None")
}

func TestVerifyFixPromptAutonomyPolicyContract(t *testing.T) {
	t.Parallel()
	step := Step{Number: 2, Title: "Wire it up"}
	cmd := VerifyCommand{Name: "test", Command: "go test ./..."}
	p := VerifyFixPrompt("/repo/.agents/local/s1", step, cmd, "FAIL: boom", 1, 2)

	// One-line pointer to autonomy policy / tiers
	assert.Contains(t, p, "autonomy")
}

func TestWorkerPromptEscalateContract(t *testing.T) {
	t.Parallel()
	step := Step{Number: 1, Title: "Do it", Body: "Body."}
	p := WorkerPrompt("/repo/.agents/local/s1", step, nil, "", Verification{})

	assert.Contains(t, p, "ESCALATE: <a single, specific, answerable question>")
}

func TestEscalationAnswerPromptContract(t *testing.T) {
	t.Parallel()
	step := Step{Number: 3, Title: "Wire API"}
	escalations := []Escalation{
		{
			Question: "Use SQLite or PostgreSQL?",
			Answer:   "PostgreSQL for production compatibility",
		},
		{
			Question: "Default port?",
			Answer:   "no preference — use your judgement",
		},
	}
	p := EscalationAnswerPrompt("/repo/.agents/local/s1", step, escalations)

	for _, want := range []string{
		"Step 3: Wire API",
		"/repo/.agents/local/s1/3-wire-api/work.md",
		"Use SQLite or PostgreSQL?",
		"PostgreSQL for production compatibility",
		"Default port?",
		"no preference — use your judgement",
		"## Decisions",
		"Rewrite the summary",
		"Do NOT re-ask an answered question",
		"COMMIT:",
		"Do not commit",
	} {
		assert.Contains(t, p, want)
	}
}

func TestReviewerPromptDecisionsContract(t *testing.T) {
	t.Parallel()
	step := Step{Number: 1, Title: "Scaffold", Body: "stub it"}
	p := ReviewerPrompt("/repo/.agents/local/s1", step, 1)

	assert.Contains(t, p, "## Decisions")
	assert.Contains(t, p, "challenge")
}

func TestParseCommitMessageWithDecisions(t *testing.T) {
	t.Parallel()
	summary := `# Step 2 Work: Implement logic

## Summary

Implemented core logic.

## Key Changes

- ` + "`internal/implement/logic.go`" + `: added worker logic
- ` + "`internal/implement/logic_test.go`" + `: added contract tests

## Decisions

- Use stdlib template: repo convention, no extra dependencies needed

## Notes & Deviations

None

COMMIT: feat(implement): add worker logic
`
	msg, err := ParseCommitMessage(summary)
	require.NoError(t, err)
	assert.Equal(t, "feat(implement): add worker logic", msg)

	summaryWithNone := `# Step 2 Work: Implement logic

## Summary

Implemented core logic.

## Key Changes

- ` + "`internal/implement/logic.go`" + `: added worker logic

## Decisions

None

## Notes & Deviations

None

COMMIT: fix(implement): handle edge case
`
	msg2, err := ParseCommitMessage(summaryWithNone)
	require.NoError(t, err)
	assert.Equal(t, "fix(implement): handle edge case", msg2)
}

func TestFixPromptContract(t *testing.T) {
	t.Parallel()
	step := Step{Number: 3, Title: "Tests", ReviewRounds: 1}
	p := FixPrompt("/repo/.agents/local/s1", step, 1)

	for _, want := range []string{
		"Step 3: Tests",
		"/repo/.agents/local/s1/3-tests/r1.review.md",
		"/repo/.agents/local/s1/3-tests/work.md",
		"## Review Fixes",
		"Re-Verification",
		"COMMIT",
	} {
		assert.Contains(t, p, want)
	}
}

func TestFinalReviewerPromptContract(t *testing.T) {
	t.Parallel()
	p := FinalReviewerPrompt("/repo/.agents/local/s1", "build the feature", "abc123")
	for _, want := range []string{
		"build the feature",
		"git diff abc123...HEAD",
		"/repo/.agents/local/s1/final-review.md",
		"*/work.md",
		"VERDICT: APPROVE",
		"VERDICT: REQUEST_CHANGES",
		"## Checklist",
		"## Findings",
	} {
		assert.Contains(t, p, want)
	}
}

func TestFinalFixPromptContract(t *testing.T) {
	t.Parallel()
	p := FinalFixPrompt("/repo/.agents/local/s1", 2)
	for _, want := range []string{
		"You are the WORKER",
		"/repo/.agents/local/s1/final-review.md",
		"/repo/.agents/local/s1/final-work.md",
		"Round 2",
		"COMMIT: ",
		"Do not commit",
	} {
		assert.Contains(t, p, want)
	}
}

func TestPromptOverride(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	customPrompt := "Custom worker for {{ .WorkPath }}"
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "worker.md"), []byte(customPrompt), 0o600))

	rendered, err := RenderPromptWithOverride("worker", tmp, WorkerPromptData{
		Step:     Step{Number: 1, Title: "first"},
		WorkPath: "/path/to/session/1-first/work.md",
	})
	require.NoError(t, err)
	assert.Equal(t, "Custom worker for /path/to/session/1-first/work.md", rendered)
}

func TestPromptOverrideTmplMd(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	customPrompt := "Custom tmpl worker for {{ .WorkPath }}"
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "worker.tmpl.md"), []byte(customPrompt), 0o600))

	rendered, err := RenderPromptWithOverride("worker", tmp, WorkerPromptData{
		Step:     Step{Number: 1, Title: "first"},
		WorkPath: "/path/to/session/1-first/work.md",
	})
	require.NoError(t, err)
	assert.Equal(t, "Custom tmpl worker for /path/to/session/1-first/work.md", rendered)
}
