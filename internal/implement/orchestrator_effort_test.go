package implement

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// effortPlan declares a per-step effort override on step 1 and leaves step 2
// on the mode default.
const effortPlan = `# Plan: thing

## Context

test context

## Step 1: first
do A

- **Files**:
  - a.go
- **Effort**: low
- **Acceptance Criteria**:
  - [ ] a done

## Step 2: second
do B

- **Files**:
  - b.go
- **Acceptance Criteria**:
  - [ ] b done
`

func TestStepEffortOverridesWorkerSpec(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-effort", mock)

	require.NoError(t, runWithPlan(t, o, effortPlan))

	// Step 1 declared "low"; the worker harness must receive it.
	step1 := mock.callsFor("worker", 1)
	require.Len(t, step1, 1)
	assert.Contains(t, step1[0].prompt, "--effort low")

	// Step 2 declared nothing; the mode's worker spec has no effort, so no
	// flag should be passed at all.
	step2 := mock.callsFor("worker", 2)
	require.Len(t, step2, 1)
	assert.NotContains(t, step2[0].prompt, "--effort")

	// The reviewer is unaffected by a step's worker-effort override.
	rev := mock.callsFor("reviewer", 1)
	require.Len(t, rev, 1)
	assert.NotContains(t, rev[0].prompt, "--effort low")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, "low", state.Steps[0].Effort)
	assert.Empty(t, state.Steps[1].Effort)

	// The recorded run reports the effective effort, not the mode default.
	require.NotEmpty(t, state.Steps[0].Runs)
	assert.Equal(t, "low", state.Steps[0].Runs[0].Effort)
	require.NotEmpty(t, state.Steps[1].Runs)
	assert.Empty(t, state.Steps[1].Runs[0].Effort)
}

func TestStepEffortAppliesToFixRound(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{rejects: map[int]int{1: 1}}
	o, _ := newTestOrchestrator(t, "full", "s-effort-fix", mock)

	require.NoError(t, runWithPlan(t, o, effortPlan))

	// Initial worker run plus one fix round, both at the step's effort.
	calls := mock.callsFor("worker", 1)
	require.Len(t, calls, 2)
	for _, c := range calls {
		assert.Contains(t, c.prompt, "--effort low")
	}

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	for _, r := range state.Steps[0].Runs {
		if r.Role == "worker" || r.Role == "fix (r1)" {
			assert.Equal(t, "low", r.Effort, "run %q", r.Role)
		}
	}
}

// agentChoicePlan sets worker and reviewer on step 1, leaving step 2 on mode default.
const agentChoicePlan = `# Plan: thing

## Context

test context

## Step 1: first
do A

- **Files**:
  - a.go
- **Worker**: opus
- **Reviewer**: review-deep
- **Acceptance Criteria**:
  - [ ] a done

## Step 2: second
do B

- **Files**:
  - b.go
- **Acceptance Criteria**:
  - [ ] b done
`

const agentChoiceWithEffortPlan = `# Plan: thing

## Context

test context

## Step 1: first
do A

- **Files**:
  - a.go
- **Worker**: opus
- **Effort**: low
- **Reviewer**: review-deep
- **Acceptance Criteria**:
  - [ ] a done

## Step 2: second
do B

- **Files**:
  - b.go
- **Acceptance Criteria**:
  - [ ] b done
`

func TestWorkerSpecFor(t *testing.T) {
	t.Parallel()
	o, _ := newTestOrchestrator(t, "multi", "s-worker-spec", &mockRunner{})

	// Nil step returns mode default
	spec, err := o.workerSpecFor(nil)
	require.NoError(t, err)
	assert.Equal(t, "gemini-3.8-flash-high", spec.Model)

	// Step without selection returns mode default
	step := &Step{Number: 1, Title: "first"}
	spec, err = o.workerSpecFor(step)
	require.NoError(t, err)
	assert.Equal(t, "gemini-3.8-flash-high", spec.Model)

	// Step without selection but with effort override
	stepWithEffort := &Step{Number: 1, Title: "first", Effort: "low"}
	spec, err = o.workerSpecFor(stepWithEffort)
	require.NoError(t, err)
	assert.Equal(t, "gemini-3.8-flash-high", spec.Model)
	assert.Equal(t, "low", spec.Effort)

	// Step with catalog selection
	stepOpus := &Step{Number: 1, Title: "first", Worker: "opus"}
	spec, err = o.workerSpecFor(stepOpus)
	require.NoError(t, err)
	assert.Equal(t, HarnessClaude, spec.Harness)
	assert.Equal(t, "opus", spec.Model)
	assert.Equal(t, "high", spec.Effort)

	// Step with catalog selection and effort override
	stepOpusLow := &Step{Number: 1, Title: "first", Worker: "opus", Effort: "low"}
	spec, err = o.workerSpecFor(stepOpusLow)
	require.NoError(t, err)
	assert.Equal(t, HarnessClaude, spec.Harness)
	assert.Equal(t, "opus", spec.Model)
	assert.Equal(t, "low", spec.Effort)

	// Step with unknown worker yields hard error naming step, title, worker, and allowed options
	stepBad := &Step{Number: 2, Title: "second", Worker: "mystery"}
	_, err = o.workerSpecFor(stepBad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `step 2 "second": unknown **Worker**: "mystery"`)
	assert.Contains(t, err.Error(), "want one of: strong, fast, opus")
}

func TestReviewerSpecFor(t *testing.T) {
	t.Parallel()
	oMulti, _ := newTestOrchestrator(t, "multi", "s-rev-spec-multi", &mockRunner{})
	oSimple, _ := newTestOrchestrator(t, "simple", "s-rev-spec-simple", &mockRunner{})

	// Simple mode has no reviewer: ok is false, err is nil
	_, ok, err := oSimple.reviewerSpecFor(nil)
	require.NoError(t, err)
	assert.False(t, ok)

	stepEmpty := &Step{Number: 1, Title: "first"}
	_, ok, err = oSimple.reviewerSpecFor(stepEmpty)
	require.NoError(t, err)
	assert.False(t, ok)

	// Simple mode with reviewer directive is an error
	stepWithRev := &Step{Number: 1, Title: "first", Reviewer: "review-fast"}
	_, _, err = oSimple.reviewerSpecFor(stepWithRev)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode has no reviewer stage")

	// Multi mode with empty selection returns mode default
	spec, ok, err := oMulti.reviewerSpecFor(stepEmpty)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "fireworks-ai/accounts/fireworks/models/glm-5p3-flash", spec.Model)

	// Multi mode with selection returns chosen spec
	stepDeep := &Step{Number: 1, Title: "first", Reviewer: "review-deep", Effort: "low"}
	spec, ok, err = oMulti.reviewerSpecFor(stepDeep)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "fireworks-ai/accounts/fireworks/models/glm-5p3", spec.Model)
	// Effort on step does NOT override reviewer effort
	assert.Equal(t, "high", spec.Effort)

	// Multi mode with unknown reviewer returns hard error
	stepBadRev := &Step{Number: 3, Title: "third", Reviewer: "nonexistent"}
	_, _, err = oMulti.reviewerSpecFor(stepBadRev)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `step 3 "third": unknown **Reviewer**: "nonexistent"`)
	assert.Contains(t, err.Error(), "want one of: review-fast, review-deep")
}

func TestStepWorkerAndReviewerRouting(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "multi", "s-agent-routing", mock)

	require.NoError(t, runWithPlan(t, o, agentChoicePlan))

	// Step 1: worker runs with opus (claude)
	step1Workers := mock.callsFor("worker", 1)
	require.Len(t, step1Workers, 1)
	assert.Equal(t, HarnessClaude, step1Workers[0].harness)
	assert.Contains(t, step1Workers[0].prompt, "--model opus")
	assert.Contains(t, step1Workers[0].prompt, "--effort high")

	// Step 1: reviewer runs with review-deep (opencode glm-5p3)
	step1Reviewers := mock.callsFor("reviewer", 1)
	require.Len(t, step1Reviewers, 1)
	assert.Equal(t, HarnessOpenCode, step1Reviewers[0].harness)
	assert.Contains(t, step1Reviewers[0].prompt, "glm-5p3")

	// Step 2: worker runs mode default (agy gemini-3.8-flash-high)
	step2Workers := mock.callsFor("worker", 2)
	require.Len(t, step2Workers, 1)
	assert.Equal(t, HarnessAgy, step2Workers[0].harness)
	assert.Contains(t, step2Workers[0].prompt, "gemini-3.8-flash-high")

	// Step 2: reviewer runs mode default (opencode glm-5p3-flash)
	step2Reviewers := mock.callsFor("reviewer", 2)
	require.Len(t, step2Reviewers, 1)
	assert.Equal(t, HarnessOpenCode, step2Reviewers[0].harness)
	assert.Contains(t, step2Reviewers[0].prompt, "glm-5p3-flash")

	// Verify state recorded runs
	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)

	require.NotEmpty(t, state.Steps[0].Runs)
	assert.Equal(t, "opus", state.Steps[0].Runs[0].Model)
	assert.Equal(t, "high", state.Steps[0].Runs[0].Effort)
	assert.Equal(t, "fireworks-ai/accounts/fireworks/models/glm-5p3", state.Steps[0].Runs[1].Model)
	assert.Equal(t, "high", state.Steps[0].Runs[1].Effort)

	require.NotEmpty(t, state.Steps[1].Runs)
	assert.Equal(t, "gemini-3.8-flash-high", state.Steps[1].Runs[0].Model)
	assert.Equal(t, "fireworks-ai/accounts/fireworks/models/glm-5p3-flash", state.Steps[1].Runs[1].Model)
}

func TestStepWorkerWithEffortOverride(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "multi", "s-worker-effort", mock)

	require.NoError(t, runWithPlan(t, o, agentChoiceWithEffortPlan))

	// Step 1: worker runs with opus (claude) and low effort
	step1Workers := mock.callsFor("worker", 1)
	require.Len(t, step1Workers, 1)
	assert.Equal(t, HarnessClaude, step1Workers[0].harness)
	assert.Contains(t, step1Workers[0].prompt, "--model opus")
	assert.Contains(t, step1Workers[0].prompt, "--effort low")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, "opus", state.Steps[0].Runs[0].Model)
	assert.Equal(t, "low", state.Steps[0].Runs[0].Effort)

	// Reviewer run still has high effort from catalog spec
	assert.Equal(t, "high", state.Steps[0].Runs[1].Effort)
}

func TestStepFixRoundsPreserveResolvedWorker(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{rejects: map[int]int{1: 1}}
	o, _ := newTestOrchestrator(t, "multi", "s-fix-worker", mock)

	require.NoError(t, runWithPlan(t, o, agentChoicePlan))

	// Initial worker run and fix round both use opus
	calls := mock.callsFor("worker", 1)
	require.Len(t, calls, 2)
	for _, c := range calls {
		assert.Equal(t, HarnessClaude, c.harness)
		assert.Contains(t, c.prompt, "--model opus")
	}

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	for _, r := range state.Steps[0].Runs {
		if r.Role == "worker" || r.Role == "fix (r1)" {
			assert.Equal(t, "opus", r.Model, "run %q model", r.Role)
			assert.Equal(t, "high", r.Effort, "run %q effort", r.Role)
		}
	}
}

func TestNoReviewerModeAutoApprovesAndSetViewSafe(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "simple", "s-norev-safe", mock)

	// setView with role reviewing does not panic even when Reviewer is nil
	state := &State{
		CurrentStep: 1,
		Steps:       []Step{{Number: 1, Title: "first"}},
	}
	assert.NotPanics(t, func() {
		o.setView(state, "reviewing", 1, HarnessClaude)
	})

	require.NoError(t, runWithPlan(t, o, oneStepPlan))
	loaded, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, StepApproved, loaded.Steps[0].Status)
	assert.Empty(t, mock.callsFor("reviewer", 1))
}

func TestPlanValidationRejectsUnknownWorker(t *testing.T) {
	t.Parallel()
	badPlan := `# Plan: thing

## Context
ctx

## Step 1: first
do A

- **Files**:
  - a.go
- **Worker**: ghost-bot
- **Acceptance Criteria**:
  - [ ] a done
`
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "multi", "s-plan-rej-worker", mock)

	err := runWithPlan(t, o, badPlan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown **Worker**: "ghost-bot"`)
	assert.Contains(t, err.Error(), "want one of: strong, fast, opus")
	assert.Empty(t, mock.streamCalls)
}

func TestHandEditedStateUnknownWorkerFailsStep(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "multi", "s-hand-edited", mock)

	// Initialize state in session dir with a hand-edited bad worker
	state := &State{
		Phase:       PhaseWork,
		CurrentStep: 1,
		Steps: []Step{
			{Number: 1, Title: "hacked", Worker: "mystery-bot", Status: StepPending},
		},
	}
	require.NoError(t, os.MkdirAll(o.SessionDir(), 0o750))
	require.NoError(t, SaveState(o.SessionDir(), state))

	err := o.Resume(context.Background(), ResumeOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown **Worker**: "mystery-bot"`)
	assert.Contains(t, err.Error(), "want one of: strong, fast, opus")
}

func TestStepVerifyFixUsesStepWorkerSpec(t *testing.T) {
	t.Parallel()
	const planWithVerify = `# Plan: thing

## Context
test context

## Verification
- test: go test ./...

## Step 1: first
do A

- **Files**:
  - a.go
- **Worker**: opus
- **Acceptance Criteria**:
  - [ ] a done
`
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "multi", "s-verify-fix-worker", mock)
	v := &recordingVerifier{failing: map[string]int{"go test ./...": 1}}
	o.verifyRun = v.run

	require.NoError(t, runWithPlan(t, o, planWithVerify))

	calls := mock.callsFor("worker", 1)
	require.Len(t, calls, 2)
	assert.Equal(t, HarnessClaude, calls[0].harness)
	assert.Contains(t, calls[0].prompt, "--model opus")
	assert.Equal(t, HarnessClaude, calls[1].harness)
	assert.Contains(t, calls[1].prompt, "--model opus")
	assert.True(t, calls[1].resumed)

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	foundVerify := false
	for _, r := range state.Steps[0].Runs {
		if r.Role == "verify:test (a1)" {
			foundVerify = true
			assert.Equal(t, "opus", r.Model)
			assert.Equal(t, "high", r.Effort)
		}
	}
	assert.True(t, foundVerify)
}
