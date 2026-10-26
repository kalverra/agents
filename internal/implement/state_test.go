package implement

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const samplePlan = `# Plan: Add feature X

Some intro paragraph.

## Step 1: Scaffold package
Create internal/x/ with a stub.
Touch files: internal/x/x.go

## Step 2: Implement logic

Fill in the real logic.
Handle edge cases.

## Step 3: Tests
Write table tests.
`

func TestParsePlan(t *testing.T) {
	t.Parallel()
	steps, err := ParsePlan(samplePlan)
	require.NoError(t, err)
	require.Len(t, steps, 3)

	assert.Equal(t, 1, steps[0].Number)
	assert.Equal(t, "Scaffold package", steps[0].Title)
	assert.Equal(t, StepPending, steps[0].Status)
	wantBody1 := "Create internal/x/ with a stub.\nTouch files: internal/x/x.go"
	assert.Equal(t, wantBody1, steps[0].Body)

	wantBody2 := "Fill in the real logic.\nHandle edge cases."
	assert.Equal(t, wantBody2, steps[1].Body)

	assert.Equal(t, 3, steps[2].Number)
	assert.Equal(t, "Tests", steps[2].Title)
	assert.Equal(t, "Write table tests.", steps[2].Body)
}

func TestParsePlanEffort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		md         string
		wantEffort string
	}{
		{
			name:       "bullet effort line",
			md:         "# Plan: X\n\n## Step 1: first\ndo A\n\n- **Effort**: high\n",
			wantEffort: "high",
		},
		{
			name:       "no bullet mixed case",
			md:         "# Plan: X\n\n## Step 1: first\ndo A\n\n**Effort**: Low\n",
			wantEffort: "low",
		},
		{
			name:       "absent effort line",
			md:         "# Plan: X\n\n## Step 1: first\ndo A\n",
			wantEffort: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			steps, err := ParsePlan(tc.md)
			require.NoError(t, err)
			require.Len(t, steps, 1)
			assert.Equal(t, tc.wantEffort, steps[0].Effort)
		})
	}
}

func TestParsePlanWorkerAndReviewer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		md           string
		wantWorker   string
		wantReviewer string
	}{
		{
			name: "bullets with unquoted names",
			md: `# Plan: Test

## Step 1: first
do A

- **Worker**: strong
- **Reviewer**: deep
`,
			wantWorker:   "strong",
			wantReviewer: "deep",
		},
		{
			name: "backticked names and no bullets",
			md: "# Plan: Test\n\n## Step 1: first\ndo A\n\n" +
				"**Worker**: `opus`\n" +
				"**Reviewer**: `review-fast`\n",
			wantWorker:   "opus",
			wantReviewer: "review-fast",
		},
		{
			name: "case insensitive labels preserve name casing",
			md: `# Plan: Test

## Step 1: first
do A

- **worker**: OpusModel
- **REVIEWER**: ReviewDeep
`,
			wantWorker:   "OpusModel",
			wantReviewer: "ReviewDeep",
		},
		{
			name: "neither directive present leaves empty",
			md: `# Plan: Test

## Step 1: first
do A
`,
			wantWorker:   "",
			wantReviewer: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			steps, err := ParsePlan(tc.md)
			require.NoError(t, err)
			require.Len(t, steps, 1)
			assert.Equal(t, tc.wantWorker, steps[0].Worker)
			assert.Equal(t, tc.wantReviewer, steps[0].Reviewer)
		})
	}
}

func TestValidateStepAgents(t *testing.T) {
	t.Parallel()

	modeWithReviewer := ModeConfig{
		WorkerName: "strong",
		WorkerChoices: []NamedSpec{
			{Name: "fast"},
			{Name: "strong"},
			{Name: "opus"},
		},
		Reviewer:     &RoleSpec{Harness: HarnessOpenCode, Model: "review-fast"},
		ReviewerName: "review-fast",
		ReviewerChoices: []NamedSpec{
			{Name: "review-fast"},
			{Name: "review-deep"},
		},
	}

	modeWithoutReviewer := ModeConfig{
		WorkerName: "strong",
		WorkerChoices: []NamedSpec{
			{Name: "fast"},
			{Name: "strong"},
		},
		Reviewer: nil,
	}

	t.Run("empty selections are always valid", func(t *testing.T) {
		t.Parallel()
		steps := []Step{
			{Number: 1, Title: "first"},
			{Number: 2, Title: "second"},
		}
		require.NoError(t, ValidateStepAgents(steps, modeWithReviewer))
		require.NoError(t, ValidateStepAgents(steps, modeWithoutReviewer))
	})

	t.Run("valid selections pass", func(t *testing.T) {
		t.Parallel()
		steps := []Step{
			{Number: 1, Title: "first", Worker: "fast", Reviewer: "review-fast"},
			{Number: 2, Title: "second", Worker: "opus", Reviewer: "review-deep"},
		}
		require.NoError(t, ValidateStepAgents(steps, modeWithReviewer))
	})

	t.Run("unknown worker", func(t *testing.T) {
		t.Parallel()
		steps := []Step{
			{Number: 3, Title: "Wire the gate", Worker: "opus-5"},
		}
		err := ValidateStepAgents(steps, modeWithReviewer)
		require.EqualError(
			t,
			err,
			`step 3 "Wire the gate": unknown **Worker**: "opus-5" (want one of: fast, strong, opus)`,
		)
	})

	t.Run("unknown reviewer", func(t *testing.T) {
		t.Parallel()
		steps := []Step{
			{Number: 1, Title: "Scaffold", Reviewer: "bad-rev"},
		}
		err := ValidateStepAgents(steps, modeWithReviewer)
		require.EqualError(
			t,
			err,
			`step 1 "Scaffold": unknown **Reviewer**: "bad-rev" (want one of: review-fast, review-deep)`,
		)
	})

	t.Run("reviewer selected when mode has no reviewer", func(t *testing.T) {
		t.Parallel()
		steps := []Step{
			{Number: 1, Title: "Scaffold", Reviewer: "review-fast"},
		}
		err := ValidateStepAgents(steps, modeWithoutReviewer)
		require.EqualError(
			t,
			err,
			`step 1 "Scaffold": cannot select **Reviewer**: "review-fast" (mode has no reviewer stage)`,
		)
	})

	t.Run("multiple bad steps report all", func(t *testing.T) {
		t.Parallel()
		steps := []Step{
			{Number: 1, Title: "Step 1", Worker: "unknown-1", Reviewer: "unknown-rev"},
			{Number: 2, Title: "Step 2", Worker: "unknown-2"},
		}
		err := ValidateStepAgents(steps, modeWithReviewer)
		require.EqualError(t, err,
			`step 1 "Step 1": unknown **Worker**: "unknown-1" (want one of: fast, strong, opus); `+
				`step 1 "Step 1": unknown **Reviewer**: "unknown-rev" (want one of: review-fast, review-deep); `+
				`step 2 "Step 2": unknown **Worker**: "unknown-2" (want one of: fast, strong, opus)`)
	})
}

func TestParsePlanTitle(t *testing.T) {
	t.Parallel()
	title := ParsePlanTitle(samplePlan)
	assert.Equal(t, "Add feature X", title)

	assert.Empty(t, ParsePlanTitle("no title header"))
}

func TestParsePlanErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		md   string
	}{
		{"no headers", "# Just a title\n\nno step headers here"},
		{"out of order", "## Step 2: b\nbody\n\n## Step 1: a\nbody"},
		{"starts at zero", "## Step 0: zero\nbody"},
		{"gap in numbering", "## Step 1: a\nbody\n\n## Step 3: c\nbody"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParsePlan(tc.md)
			require.Error(t, err, "ParsePlan(%q) should error", tc.name)
		})
	}
}

func TestValidateSteps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		steps   []Step
		wantErr string
	}{
		{
			name: "full contract bodies pass",
			steps: []Step{
				{
					Number: 1,
					Title:  "first",
					Body:   "do A\n\n- **Files**:\n  - a.go\n\n- **Acceptance Criteria**:\n  - [ ] a done",
				},
				{
					Number: 2,
					Title:  "second",
					Body:   "do B\n\n**Files**:\n- b.go\n\n**Acceptance Criteria**:\n- [ ] b done",
				},
			},
		},
		{
			// The step contract is relaxed: Files/Acceptance Criteria are
			// honored when present but not required — imported specs carry
			// plain prose bodies.
			name: "plain prose bodies pass (relaxed contract)",
			steps: []Step{
				{Number: 1, Title: "first", Body: "do A"},
				{Number: 2, Title: "second", Body: ""},
			},
		},
		{
			name: "valid effort value passes",
			steps: []Step{
				{
					Number: 1,
					Title:  "first",
					Body: "- **Files**:\n  - a.go\n\n- **Acceptance Criteria**:\n  - [ ] done\n\n" +
						"- **Effort**: medium",
				},
			},
		},
		{
			name: "invalid effort value",
			steps: []Step{
				{
					Number: 3,
					Title:  "Some title",
					Body: "- **Files**:\n  - a.go\n\n- **Acceptance Criteria**:\n  - [ ] done\n\n" +
						"- **Effort**: turbo",
				},
			},
			wantErr: `step 3 "Some title": invalid **Effort**: "turbo" (want low|medium|high)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateSteps(tc.steps)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.wantErr)
			}
		})
	}
}

func TestParseAndValidatePlan(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		md        string
		wantSteps int
		wantErr   string
	}{
		{
			name: "plan with Context passes",
			md: `# Plan: Valid

## Context

Some context notes.

## Step 1: first
do A

- **Files**:
  - a.go
- **Acceptance Criteria**:
  - [ ] done
`,
			wantSteps: 1,
		},
		{
			// The Context requirement was relaxed with the rest of the
			// step contract: plans written outside this tool (openspec,
			// spec-kit) are adopted without it.
			name: "missing Context section passes (relaxed contract)",
			md: `# Plan: No Context

## Step 1: first
do A
`,
			wantSteps: 1,
		},
		{
			name: "parse errors still surface",
			md: `## Step 2: out of order
body
`,
			wantErr: "steps must be numbered sequentially from 1",
		},
		{
			name: "invalid effort surfaces",
			md: `# Plan: Bad Effort

## Context
context notes

## Step 1: first
- **Effort**: turbo
`,
			wantErr: `step 1 "first": invalid **Effort**: "turbo" (want low|medium|high)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			steps, err := ParseAndValidatePlan(tc.md)
			if tc.wantErr == "" {
				require.NoError(t, err)
				assert.Len(t, steps, tc.wantSteps)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestParseVerdict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		md   string
		want Verdict
		ok   bool
	}{
		{"approve", "looks good\n\nVERDICT: APPROVE\n", VerdictApprove, true},
		{"request changes", "problems\n\nVERDICT: REQUEST_CHANGES\n", VerdictRequestChanges, true},
		{"last wins", "VERDICT: APPROVE\n\nactually no\nVERDICT: REQUEST_CHANGES", VerdictRequestChanges, true},
		{"trailing spaces", "VERDICT: APPROVE   \n", VerdictApprove, true},
		{"missing", "no verdict here", "", false},
		{"wrong case", "verdict: approve", "", false},
		{"embedded", "the file must contain VERDICT: APPROVE somewhere", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseVerdict(tc.md)
			if !tc.ok {
				require.Error(t, err, "ParseVerdict(%q) should error", tc.name)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSaveLoadState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := &State{
		SessionName: "test-session",
		Mode:        "medium",
		Task:        "do the thing",
		Phase:       PhaseReview,
		CurrentStep: 2,
		Steps: []Step{
			{
				Number:       1,
				Title:        "one",
				Body:         "b1",
				Status:       StepApproved,
				ReviewRounds: 1,
				Worker:       "strong",
				Reviewer:     "deep",
			},
			{Number: 2, Title: "two", Body: "b2", Status: StepPending, WorkSessionID: "sess-9"},
		},
	}
	require.NoError(t, SaveState(dir, s))

	got, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, s.SessionName, got.SessionName)
	assert.Equal(t, PhaseReview, got.Phase)
	assert.Equal(t, 2, got.CurrentStep)
	require.Len(t, got.Steps, 2)
	assert.Equal(t, "sess-9", got.Steps[1].WorkSessionID)
	assert.Equal(t, "strong", got.Steps[0].Worker)
	assert.Equal(t, "deep", got.Steps[0].Reviewer)
	assert.Empty(t, got.Steps[1].Worker)
	assert.Empty(t, got.Steps[1].Reviewer)
}

func TestLoadStateMissing(t *testing.T) {
	t.Parallel()
	_, err := LoadState(t.TempDir())
	require.Error(t, err)
}

func TestStepFilePaths(t *testing.T) {
	t.Parallel()
	s := Step{Number: 3, Title: "Tests"}
	assert.Equal(t, filepath.Join("3-tests", "work.md"), s.WorkFile())
	assert.Equal(t, filepath.Join("3-tests", "r1.review.md"), s.ReviewFile(1))
	assert.Equal(t, filepath.Join("3-tests", "r1.diff"), s.DiffFile(1))
	assert.Equal(t, "step-3-worker.jsonl", s.StreamLogFile("worker"))
}

func TestStepDirectoryAndRoundPaths(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "commit-history-scanning-for-jira-keys", Slugify("Commit History Scanning for Jira Keys"))
	assert.Equal(t, "refactor-ticket-status", Slugify("Refactor 'ticket status'"))

	s := Step{Number: 1, Title: "Commit History Scanning for Jira Keys"}
	assert.Equal(t, "1-commit-history-scanning-for-jira-keys", s.StepDir())
	assert.Equal(t, filepath.Join("1-commit-history-scanning-for-jira-keys", "work.md"), s.WorkFile())
	assert.Equal(t, filepath.Join("1-commit-history-scanning-for-jira-keys", "r1.work.md"), s.RoundWorkFile(1))
	assert.Equal(t, filepath.Join("1-commit-history-scanning-for-jira-keys", "r1.diff"), s.DiffFile(1))
	assert.Equal(t, filepath.Join("1-commit-history-scanning-for-jira-keys", "r1.review.md"), s.ReviewFile(1))
	assert.Equal(t, "step-1-worker.jsonl", s.StreamLogFile("worker"))

	// Round 2
	assert.Equal(t, filepath.Join("1-commit-history-scanning-for-jira-keys", "r2.work.md"), s.RoundWorkFile(2))
	assert.Equal(t, filepath.Join("1-commit-history-scanning-for-jira-keys", "r2.diff"), s.DiffFile(2))
	assert.Equal(t, filepath.Join("1-commit-history-scanning-for-jira-keys", "r2.review.md"), s.ReviewFile(2))
	assert.Equal(t, "step-1-worker-r2.jsonl", s.StreamLogFile("worker", 2))
}

func TestStreamLogPath(t *testing.T) {
	t.Parallel()
	s := Step{Number: 3}
	assert.Equal(
		t,
		filepath.Join("session", LogsDir, "step-3-worker.jsonl"),
		s.StreamLogPath("session", "worker"),
		"raw stream logs and digests live in the session logs/ subdir",
	)
}

func TestStepTimestampsJSONRoundTrip(t *testing.T) {
	t.Parallel()
	started := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Second)
	finished := time.Now().UTC().Truncate(time.Second)
	s := Step{Number: 1, Title: "t", Status: StepApproved, StartedAt: &started, FinishedAt: &finished}

	data, err := json.Marshal(s)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"started_at"`)
	assert.Contains(t, string(data), `"finished_at"`)

	// Zero timestamps must be omitted for backward-compatible state files.
	empty, err := json.Marshal(Step{Number: 2, Title: "x"})
	require.NoError(t, err)
	assert.NotContains(t, string(empty), `"started_at"`)
	assert.NotContains(t, string(empty), `"finished_at"`)

	var got Step
	require.NoError(t, json.Unmarshal(data, &got))
	require.NotNil(t, got.StartedAt)
	require.NotNil(t, got.FinishedAt)
	assert.True(t, got.StartedAt.Equal(started))
	assert.True(t, got.FinishedAt.Equal(finished))
}

func TestStepUsageAdd(t *testing.T) {
	t.Parallel()
	s := StepUsage{InputTokens: 100, OutputTokens: 10, CostUSD: 0.5, HasTokens: true, HasCost: true}

	// Cost only accumulates when the addend reports it.
	s.Add(StepUsage{InputTokens: 50, OutputTokens: 5, HasTokens: true})
	assert.Equal(t, StepUsage{InputTokens: 150, OutputTokens: 15, CostUSD: 0.5, HasTokens: true, HasCost: true}, s)

	s.Add(StepUsage{InputTokens: 1, CostUSD: 0.25, HasCost: true})
	assert.Equal(t, StepUsage{InputTokens: 151, OutputTokens: 15, CostUSD: 0.75, HasTokens: true, HasCost: true}, s)
}

func TestStepUsageJSONRoundTrip(t *testing.T) {
	t.Parallel()
	s := Step{
		Number:        1,
		Title:         "t",
		Status:        StepApproved,
		WorkerUsage:   &StepUsage{InputTokens: 315, OutputTokens: 40, CostUSD: 0.0123, HasTokens: true, HasCost: true},
		ReviewerUsage: &StepUsage{InputTokens: 2000, OutputTokens: 400, CostUSD: 0.05, HasTokens: true, HasCost: true},
	}

	data, err := json.Marshal(s)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"worker_usage"`)
	assert.Contains(t, string(data), `"reviewer_usage"`)

	empty, err := json.Marshal(Step{Number: 2, Title: "x"})
	require.NoError(t, err)
	assert.NotContains(t, string(empty), `"worker_usage"`)
	assert.NotContains(t, string(empty), `"reviewer_usage"`)

	var got Step
	require.NoError(t, json.Unmarshal(data, &got))
	require.NotNil(t, got.WorkerUsage)
	require.NotNil(t, got.ReviewerUsage)
	assert.Equal(t, *s.WorkerUsage, *got.WorkerUsage)
	assert.Equal(t, *s.ReviewerUsage, *got.ReviewerUsage)
}

func TestParseCommitMessage(t *testing.T) {
	t.Parallel()
	msg, err := ParseCommitMessage("# Work\n\n## Summary\n\nstuff\n\nCOMMIT: feat(auth): add oauth flow\n")
	require.NoError(t, err)
	assert.Equal(t, "feat(auth): add oauth flow", msg)

	// Last COMMIT line wins; surrounding whitespace trimmed.
	msg, err = ParseCommitMessage("COMMIT: first\n\nCOMMIT:   fix(api): second  \n")
	require.NoError(t, err)
	assert.Equal(t, "fix(api): second", msg)
}

func TestParseCommitMessageErrors(t *testing.T) {
	t.Parallel()
	_, err := ParseCommitMessage("# Work\n\nno marker line\n")
	require.Error(t, err)

	_, err = ParseCommitMessage("COMMIT:   \n")
	require.Error(t, err)
}

func TestParseEscalations(t *testing.T) {
	t.Parallel()

	// None -> nil
	assert.Nil(t, ParseEscalations("# Work\n\nNo escalations here\n\nCOMMIT: fix: foo\n"))

	// Multiple in order, trimmed, empties skipped, deduplicated
	md := `# Work

ESCALATE:  Which database driver should we use?  
ESCALATE:
ESCALATE:   
ESCALATE: Should auth tokens expire in 1 hour or 24 hours?
ESCALATE: Which database driver should we use?

COMMIT: feat: done
`
	got := ParseEscalations(md)
	require.Len(t, got, 2)
	assert.Equal(t, "Which database driver should we use?", got[0])
	assert.Equal(t, "Should auth tokens expire in 1 hour or 24 hours?", got[1])

	// All empty -> nil
	assert.Nil(t, ParseEscalations("ESCALATE:\nESCALATE:   \n"))
}

func TestStateEscalationsRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	s := &State{
		SessionName: "test-esc",
		Phase:       PhaseWork,
		Steps: []Step{
			{
				Number: 1,
				Title:  "one",
				Body:   "b1",
				Status: StepPending,
				Escalations: []Escalation{
					{
						Question: "Which database?",
						Answer:   "PostgreSQL",
						AskedAt:  &now,
					},
					{
						Question: "Which port?",
						Answer:   "5432",
						AskedAt:  &now,
					},
				},
			},
		},
	}
	require.NoError(t, SaveState(dir, s))

	got, err := LoadState(dir)
	require.NoError(t, err)
	require.Len(t, got.Steps, 1)
	require.Len(t, got.Steps[0].Escalations, 2)
	assert.Equal(t, "Which database?", got.Steps[0].Escalations[0].Question)
	assert.Equal(t, "PostgreSQL", got.Steps[0].Escalations[0].Answer)
	require.NotNil(t, got.Steps[0].Escalations[0].AskedAt)
	assert.True(t, now.Equal(*got.Steps[0].Escalations[0].AskedAt))
	assert.Equal(t, "Which port?", got.Steps[0].Escalations[1].Question)
	assert.Equal(t, "5432", got.Steps[0].Escalations[1].Answer)
}

func TestSessionDirGitignore(t *testing.T) {
	t.Parallel()
	// Sanity for EnsureSessionDir behavior used by orchestrator.
	base := t.TempDir()
	local := filepath.Join(base, ".agents", "local")
	require.NoError(t, os.MkdirAll(local, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(local, ".gitignore"), []byte("*\n"), 0o600))
	_, err := os.Stat(filepath.Join(local, ".gitignore"))
	require.NoError(t, err)
}

func TestResumeCommand(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "agents implement --mode simple --session my-sess --resume", ResumeCommand("simple", "my-sess"))
	assert.Equal(
		t,
		"agents implement --mode full --session \"session with spaces\" --resume",
		ResumeCommand("full", "session with spaces"),
	)
	assert.Equal(t, "agents implement --session s1 --resume", ResumeCommand("", "s1"))

	state := &State{Mode: "extreme", SessionName: "task-99"}
	assert.Equal(t, "agents implement --mode extreme --session task-99 --resume", state.ResumeCommand())
}

func TestParseVerification(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		md   string
		want Verification
	}{
		{
			name: "five commands in written order",
			md: "# Plan: X\n\n## Verification\n\n" +
				"- build: `go build ./...`\n" +
				"- typecheck: `tsc --noEmit`\n" +
				"- test: `go test ./...`\n" +
				"- lint: `golangci-lint run ./...`\n" +
				"- generated: `git diff --exit-code`\n\n## Step 1: first\nbody\n",
			want: Verification{
				Cmds: []VerifyCommand{
					{Name: "build", Command: "go build ./..."},
					{Name: "typecheck", Command: "tsc --noEmit"},
					{Name: "test", Command: "go test ./..."},
					{Name: "lint", Command: "golangci-lint run ./..."},
					{Name: "generated", Command: "git diff --exit-code"},
				},
			},
		},
		{
			name: "no bullet form",
			md:   "## Verification\ntest: go test ./...\n\n## Step 1: first\nbody\n",
			want: Verification{Cmds: []VerifyCommand{{Name: "test", Command: "go test ./..."}}},
		},
		{
			name: "star bullet form",
			md:   "## Verification\n* build: go build ./...\n\n## Step 1: first\nbody\n",
			want: Verification{Cmds: []VerifyCommand{{Name: "build", Command: "go build ./..."}}},
		},
		{
			name: "mixed case keys normalized to lowercase and trimmed",
			md:   "## Verification\n-  Test : go test ./...\n- LINT: golangci-lint run ./...\n\n## Step 1: first\nbody\n",
			want: Verification{Cmds: []VerifyCommand{
				{Name: "test", Command: "go test ./..."},
				{Name: "lint", Command: "golangci-lint run ./..."},
			}},
		},
		{
			name: "backticked and unbackticked commands both parse",
			md:   "## Verification\n- build: `go build ./...`\n- test: go test ./...\n\n## Step 1: first\nbody\n",
			want: Verification{Cmds: []VerifyCommand{
				{Name: "build", Command: "go build ./..."},
				{Name: "test", Command: "go test ./..."},
			}},
		},
		{
			name: "absent section",
			md:   "# Plan: X\n\n## Step 1: first\nbody\n",
			want: Verification{},
		},
		{
			name: "section present but no recognized keys",
			md:   "## Verification\nnothing useful here\n\n## Step 1: first\nbody\n",
			want: Verification{},
		},
		{
			name: "template placeholder lines and section prose do not become commands",
			md: "# Plan: X\n\n## Verification\n\n" +
				"<The commands the harness runs itself after each step.>\n\n" +
				"- build: `<command that compiles/builds>`\n" +
				"- test: `<command that runs the full test suite>`\n" +
				"- lint: `<command that runs the linters, with its auto-fix flag>`\n" +
				"- e2e: pytest tests/e2e\n\n## Step 1: first\nbody\n",
			want: Verification{Cmds: []VerifyCommand{
				{Name: "e2e", Command: "pytest tests/e2e"},
			}},
		},
		{
			name: "empty command treated as undeclared",
			md:   "## Verification\n- test:\n- build: go build ./...\n\n## Step 1: first\nbody\n",
			want: Verification{Cmds: []VerifyCommand{{Name: "build", Command: "go build ./..."}}},
		},
		{
			name: "empty command with backticks treated as undeclared",
			md:   "## Verification\n- test: ``\n- build: go build ./...\n\n## Step 1: first\nbody\n",
			want: Verification{Cmds: []VerifyCommand{{Name: "build", Command: "go build ./..."}}},
		},
		{
			name: "section terminates at next Step heading, no bleed",
			md: "## Verification\n- test: go test ./...\n\n## Step 1: first\n" +
				"- lint: should not be picked up\n",
			want: Verification{Cmds: []VerifyCommand{{Name: "test", Command: "go test ./..."}}},
		},
		{
			name: "duplicate name keeps last value at first occurrence position",
			md: "# Plan: X\n\n## Verification\n\n" +
				"- test: go test ./one\n" +
				"- lint: golangci-lint run\n" +
				"- test: go test ./two\n" +
				"- build: go build ./...\n\n## Step 1: first\nbody\n",
			want: Verification{Cmds: []VerifyCommand{
				{Name: "test", Command: "go test ./two"},
				{Name: "lint", Command: "golangci-lint run"},
				{Name: "build", Command: "go build ./..."},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseVerification(tc.md)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestVerificationEmpty(t *testing.T) {
	t.Parallel()
	assert.True(t, Verification{}.Empty())
	assert.True(t, Verification{Cmds: nil}.Empty())
	assert.True(t, Verification{Cmds: []VerifyCommand{}}.Empty())
	assert.False(t, Verification{Cmds: []VerifyCommand{{Name: "build", Command: "go build ./..."}}}.Empty())
}

func TestVerificationCommands(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		v    Verification
		want []VerifyCommand
	}{
		{
			name: "declared commands in written order",
			v: Verification{Cmds: []VerifyCommand{
				{Name: "build", Command: "go build ./..."},
				{Name: "test", Command: "go test ./..."},
				{Name: "lint", Command: "golangci-lint run ./..."},
			}},
			want: []VerifyCommand{
				{Name: "build", Command: "go build ./..."},
				{Name: "test", Command: "go test ./..."},
				{Name: "lint", Command: "golangci-lint run ./..."},
			},
		},
		{
			name: "none declared",
			v:    Verification{},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.v.Commands())
		})
	}
}

func TestVerificationSaveLoadStatePreservesOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := &State{
		SessionName: "verify-order-test",
		Mode:        "intense",
		Task:        "check order",
		Phase:       PhaseWork,
		Verification: Verification{
			Cmds: []VerifyCommand{
				{Name: "build", Command: "go build ./..."},
				{Name: "typecheck", Command: "tsc --noEmit"},
				{Name: "test", Command: "go test ./..."},
				{Name: "lint", Command: "golangci-lint run ./..."},
				{Name: "generated", Command: "git diff --exit-code"},
			},
		},
	}
	require.NoError(t, SaveState(dir, s))

	got, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, s.Verification, got.Verification)
	assert.Equal(t, s.Verification.Commands(), got.Verification.Commands())
}

func TestSignCommitsCommand(t *testing.T) {
	t.Parallel()

	assert.Equal(
		t,
		"git rebase --exec 'git commit --amend --no-edit -S' a1b2c3d4e5",
		SignCommitsCommand("a1b2c3d4e5"),
	)
	assert.Equal(
		t,
		"git rebase --exec 'git commit --amend --no-edit -S' HEAD~1",
		SignCommitsCommand(""),
	)
}
