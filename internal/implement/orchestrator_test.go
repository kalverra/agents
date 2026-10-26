package implement

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRunner fakes agent execution: it writes the artifact files the real
// agents would write and emits stream events carrying session IDs.
type mockRunner struct {
	sessionDir string
	// rejects maps step number -> how many REQUEST_CHANGES verdicts to emit
	// before approving.
	rejects map[int]int
	// skipWork maps step number -> true to simulate worker not writing its
	// summary.
	skipWork map[int]bool
	// skipFix maps step number -> true to simulate a fix-round worker run
	// destroying the step summary (the initial round wrote it).
	skipFix map[int]bool
	// finalRejects is how many REQUEST_CHANGES verdicts the final whole-diff
	// review emits before approving.
	finalRejects int
	// omitCommit maps step number -> true to write the work summary without
	// a COMMIT line (orchestrator must fall back to a generated message).
	omitCommit map[int]bool
	// skipFinalWork simulates a final fix round failing to write final-work.md.
	skipFinalWork bool
	// streamErr simulates a stream runner execution failure.
	streamErr error
	// agyRealUsage emits only agy's real stream shape (tokens, no cost field)
	// instead of the generic multi-shape usage lines, to exercise agy cost
	// estimation end to end.
	agyRealUsage bool
	// agyStepUsage emits per-turn step_update usage events before agy's
	// cumulative result event, matching agy's real multi-turn streams.
	agyStepUsage bool
	// agyTruncated emits per-turn step_update usage events but no final
	// result event, simulating a stream that dies before the run report.
	agyTruncated    bool
	noVerifyChanges bool
	git             *fakeGitter
	// escalations maps step number -> escalation questions to inject into initial work.md
	escalations map[int][]string
	// nextEscalations maps step number -> escalation questions to inject into work.md on ask resume
	nextEscalations map[int][]string

	// Recorded calls.
	streamCalls []mockCall
}

type mockCall struct {
	harness string // "agy" | "claude" | "opencode"
	role    string // "worker" | "reviewer"
	step    int
	prompt  string
	resumed bool // ran with a session ID
	final   bool // whole-diff final review / final fix round
}

var (
	stepRe = regexp.MustCompile(`Step (\d+):`)
	roleRe = regexp.MustCompile(`You are the (WORKER|REVIEWER)`)
	fixRe  = regexp.MustCompile(`(?i)reviewer requested changes`)
	// verifyFixRe recognizes the harness-run verification gate's fix prompt,
	// which resumes the worker conversation without a reviewer verdict.
	verifyFixRe = regexp.MustCompile(`(?i)failed the repo's verification gate`)
	// These match the exact instructional phrasing in the prompt templates,
	// so they find the current step's own write target rather than, say, a
	// previous step's work.md mentioned earlier in the same prompt.
	workTargetRe     = regexp.MustCompile(`summary to (\S+)`)
	fixTargetRe      = regexp.MustCompile(`fix section to (\S+)`)
	verifyTargetRe   = regexp.MustCompile(`Append this section to (\S+)`)
	reviewTargetRe   = regexp.MustCompile(`findings to (\S+)`)
	escalateTargetRe = regexp.MustCompile(`summary in (\S+)`)
	resumeArgs       = map[string]string{
		"agy":      "--conversation",
		"claude":   "--resume",
		"opencode": "--session",
	}
)

func (m *mockRunner) RunStream( //nolint:gocyclo // test helper mock
	_ context.Context,
	name string,
	args []string,
	_ string,
	onLine func(line []byte),
) error {
	if m.streamErr != nil {
		return m.streamErr
	}
	joined := strings.Join(args, " ")
	var role string
	if match := roleRe.FindStringSubmatch(joined); match != nil {
		role = strings.ToLower(match[1])
	}
	stepNum := 0
	if match := stepRe.FindStringSubmatch(joined); match != nil {
		_, _ = fmt.Sscanf(match[1], "%d", &stepNum) // regex guarantees digits
	}
	resumed := false
	if flag, ok := resumeArgs[name]; ok {
		resumed = strings.Contains(joined, flag+" ")
	}
	isFinalReview := role == "reviewer" && strings.Contains(joined, "final-review.md")
	isVerifyFix := verifyFixRe.MatchString(joined)
	isFinalFix := role == "worker" && strings.Contains(joined, "final-work.md") && !isVerifyFix
	isFix := fixRe.MatchString(joined) && !isFinalFix
	isEscalateAnswer := strings.Contains(joined, "Escalation Answers")
	final := isFinalReview || isFinalFix || (isVerifyFix && strings.Contains(joined, "final-work.md"))
	m.streamCalls = append(
		m.streamCalls,
		mockCall{harness: name, role: role, step: stepNum, prompt: joined, resumed: resumed, final: final},
	)

	emit := func(sid string) {
		if onLine == nil {
			return
		}
		// Tool activity events in both known event shapes (claude + opencode).
		onLine([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`))
		onLine([]byte(`{"type":"part-start","part":{"type":"tool","tool":"read","state":{"title":"Read step file"}}}`))
		if m.agyStepUsage || m.agyTruncated {
			// agy per-turn events: each turn re-counts the whole shared
			// context, so these must never be summed with the result.
			onLine([]byte(`{"event":"step_update","step_update":{"step_type":"agent_response",` +
				`"usage":{"input_tokens":50000,"output_tokens":2000,"cache_read_tokens":500000,` +
				`"total_tokens":552000}}}`))
			onLine([]byte(`{"event":"step_update","step_update":{"step_type":"agent_response",` +
				`"usage":{"input_tokens":30000,"output_tokens":2000,"cache_read_tokens":600000,` +
				`"total_tokens":632000}}}`))
			if m.agyTruncated {
				return
			}
		}
		if m.agyRealUsage || m.agyStepUsage {
			// agy's actual result shape: tokens only, never a dollar cost.
			// input_tokens is fresh input; cache_read_tokens is a separate
			// 1M-token cache hit billed at the cheaper cache-read rate.
			onLine(fmt.Appendf(nil,
				`{"event":"result","result":{"conversation_id":%q,"status":"SUCCESS",`+
					`"usage":{"input_tokens":1000000,"output_tokens":1000000,"thinking_tokens":0,`+
					`"cache_read_tokens":1000000,"total_tokens":3000000}}}`,
				sid,
			))
			return
		}
		// Usage events in both known shapes: opencode step_finish, claude result.
		onLine(
			[]byte(
				`{"type":"step_finish","part":{"type":"step-finish",` +
					`"tokens":{"input":2000,"output":300,"reasoning":100,"cache":{"read":0,"write":0}},"cost":0.05}}`,
			),
		)
		onLine(fmt.Appendf(nil,
			`{"type":"result","subtype":"success","session_id":%q,"result":"done",`+
				`"usage":{"input_tokens":300,"output_tokens":40,"cache_read_input_tokens":10,"cache_creation_input_tokens":5},`+
				`"total_cost_usd":0.0123}`,
			sid,
		))
	}

	switch role {
	case "worker":
		if isFinalFix {
			if m.skipFinalWork {
				emit("final-work")
				return nil
			}
			if err := os.WriteFile(
				filepath.Join(m.sessionDir, "final-work.md"),
				[]byte("# Final Review Fixes\n\napplied fixes\n\nCOMMIT: fix(final): address final review findings\n"),
				0o600,
			); err != nil {
				return err
			}
			emit("final-work")
			return nil
		}
		if !isFix && m.skipWork != nil && m.skipWork[stepNum] {
			emit(fmt.Sprintf("work-%d", stepNum))
			return nil
		}
		var workPath string
		switch {
		case isVerifyFix:
			if match := verifyTargetRe.FindStringSubmatch(joined); match != nil {
				workPath = match[1]
			}
		case isFix:
			if match := fixTargetRe.FindStringSubmatch(joined); match != nil {
				workPath = match[1]
			}
		case isEscalateAnswer:
			if match := escalateTargetRe.FindStringSubmatch(joined); match != nil {
				workPath = match[1]
			}
		default:
			if match := workTargetRe.FindStringSubmatch(joined); match != nil {
				workPath = match[1]
			}
		}
		if workPath == "" {
			return fmt.Errorf("mockRunner: no write target found in worker prompt: %s", truncate(joined, 200))
		}
		// An escalation answer rewrites the work summary with the answers applied.
		if isEscalateAnswer {
			content := fmt.Sprintf("# Work: step %d\n\naddressed escalation answers\n", stepNum)
			if questions, ok := m.nextEscalations[stepNum]; ok && len(questions) > 0 {
				for _, q := range questions {
					content += fmt.Sprintf("\nESCALATE: %s\n", q)
				}
				delete(m.nextEscalations, stepNum)
			}
			if m.omitCommit == nil || !m.omitCommit[stepNum] {
				content += fmt.Sprintf("\nCOMMIT: feat(test): step %d work\n", stepNum)
			}
			if err := os.WriteFile(workPath, []byte(content), 0o600); err != nil {
				return err
			}
			emit(fmt.Sprintf("ask-%d", stepNum))
			return nil
		}
		// A verification fix appends to the existing summary, as the real
		// worker does; it never rewrites the step's work from scratch.
		if isVerifyFix {
			if !m.noVerifyChanges && m.git != nil {
				m.git.worktreeDiff = fmt.Sprintf(
					"diff --git a/pkg.go b/pkg.go\n--- a/pkg.go\n+++ b/pkg.go\n@@ -0,0 +1 @@\n+package pkg\n+// fix %d\n",
					len(m.streamCalls),
				)
			}
			//nolint:gosec // path comes from the prompt this test mock itself rendered
			f, err := os.OpenFile(workPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return err
			}
			if _, err := f.WriteString("\n## Verification Fixes\n\nfixed the failing command\n"); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			emit(fmt.Sprintf("verify-%d", stepNum))
			return nil
		}
		if isFix && m.skipFix != nil && m.skipFix[stepNum] {
			_ = os.Remove(workPath)
			emit(fmt.Sprintf("work-%d", stepNum))
			return nil
		}
		content := fmt.Sprintf("# Work: step %d\n\nimplemented things\n", stepNum)
		if questions, ok := m.escalations[stepNum]; ok && len(questions) > 0 {
			for _, q := range questions {
				content += fmt.Sprintf("\nESCALATE: %s\n", q)
			}
		}
		if m.omitCommit == nil || !m.omitCommit[stepNum] {
			content += fmt.Sprintf("\nCOMMIT: feat(test): step %d work\n", stepNum)
		}
		if isFix {
			content += "\napplied review fixes\n"
		}
		if err := os.MkdirAll(filepath.Dir(workPath), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(workPath, []byte(content), 0o600); err != nil {
			return err
		}
		emit(fmt.Sprintf("work-%d", stepNum))
	case "reviewer":
		if isFinalReview {
			content := "# Final Review\n\nwhole diff looks good\n"
			if m.finalRejects > 0 {
				m.finalRejects--
				content += "\n1. cross-step problem\n\nVERDICT: REQUEST_CHANGES\n"
			} else {
				content += "\nVERDICT: APPROVE\n"
			}
			if err := os.WriteFile(
				filepath.Join(m.sessionDir, "final-review.md"),
				[]byte(content), 0o600,
			); err != nil {
				return err
			}
			emit("final-review")
			return nil
		}
		var reviewPath string
		if match := reviewTargetRe.FindStringSubmatch(joined); match != nil {
			reviewPath = match[1]
		}
		content := "# Review\n\nlooks solid\n"
		if m.rejects != nil && m.rejects[stepNum] > 0 {
			m.rejects[stepNum]--
			content += "\n1. some problem\n\nVERDICT: REQUEST_CHANGES\n"
		} else {
			content += "\nVERDICT: APPROVE\n"
		}
		if err := os.MkdirAll(filepath.Dir(reviewPath), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(reviewPath, []byte(content), 0o600); err != nil {
			return err
		}
		emit(fmt.Sprintf("rev-%d", stepNum))
	default:
		return fmt.Errorf("mockRunner: unrecognized role in args: %s", joined)
	}
	return nil
}

func (m *mockRunner) callsFor(role string, step int) []mockCall {
	var out []mockCall
	for _, c := range m.streamCalls {
		if c.role == role && c.step == step && !c.final {
			out = append(out, c)
		}
	}
	return out
}

func (m *mockRunner) finalCalls(role string) []mockCall {
	var out []mockCall
	for _, c := range m.streamCalls {
		if c.final && c.role == role {
			out = append(out, c)
		}
	}
	return out
}

func testConfig() *Config {
	return &Config{
		Modes: map[string]ModeConfig{
			"full": {
				Worker: RoleSpec{Harness: HarnessClaude, Model: "sonnet"},
				Reviewer: &RoleSpec{
					Harness: HarnessOpenCode,
					Model:   "fireworks-ai/accounts/fireworks/models/glm-5p3-flash",
				},
				WorkerChoices: []NamedSpec{
					{Name: "sonnet", Spec: RoleSpec{Harness: HarnessClaude, Model: "sonnet"}},
				},
				ReviewerChoices: []NamedSpec{
					{
						Name: "glm-flash",
						Spec: RoleSpec{
							Harness: HarnessOpenCode,
							Model:   "fireworks-ai/accounts/fireworks/models/glm-5p3-flash",
						},
					},
				},
			},
			"simple": {
				Worker: RoleSpec{Harness: HarnessAgy, Model: "gemini-3.8-flash-medium"},
				WorkerChoices: []NamedSpec{
					{Name: "gemini-med", Spec: RoleSpec{Harness: HarnessAgy, Model: "gemini-3.8-flash-medium"}},
				},
			},
			"multi": {
				Worker: RoleSpec{Harness: HarnessAgy, Model: "gemini-3.8-flash-high"},
				WorkerChoices: []NamedSpec{
					{Name: "strong", Spec: RoleSpec{Harness: HarnessAgy, Model: "gemini-3.8-flash-high"}},
					{Name: "fast", Spec: RoleSpec{Harness: HarnessAgy, Model: "gemini-3.8-flash-medium"}},
					{Name: "opus", Spec: RoleSpec{Harness: HarnessClaude, Model: "opus", Effort: "high"}},
				},
				Reviewer: &RoleSpec{
					Harness: HarnessOpenCode,
					Model:   "fireworks-ai/accounts/fireworks/models/glm-5p3-flash",
				},
				ReviewerChoices: []NamedSpec{
					{
						Name: "review-fast",
						Spec: RoleSpec{
							Harness: HarnessOpenCode,
							Model:   "fireworks-ai/accounts/fireworks/models/glm-5p3-flash",
						},
					},
					{
						Name: "review-deep",
						Spec: RoleSpec{
							Harness: HarnessOpenCode,
							Model:   "fireworks-ai/accounts/fireworks/models/glm-5p3",
							Effort:  "high",
						},
					},
				},
			},
		},
		ReviewMaxRounds: 2,
	}
}

const twoStepPlan = `# Plan: thing

## Context

test context

## Step 1: first
do A

- **Files**:
  - a.go
- **Acceptance Criteria**:
  - [ ] a done

## Step 2: second
do B

- **Files**:
  - b.go
- **Acceptance Criteria**:
  - [ ] b done
`

func newTestOrchestrator(t *testing.T, mode, session string, mock *mockRunner) (*Orchestrator, *bytes.Buffer) {
	t.Helper()
	repo := t.TempDir()
	mock.sessionDir = filepath.Join(repo, ".agents", "local", session)
	buf := &bytes.Buffer{}
	o, err := NewOrchestrator(testConfig(), mode, repo, session, mock, zerolog.Nop(), buf)
	require.NoError(t, err)
	// Inject test seams (same package): fake git, fake confirmer, noop diff
	// opener. Tests override via type assertion on the fields.
	fg := &fakeGitter{head: "base-sha"}
	o.git = fg
	mock.git = fg
	o.confirmer = &fakeConfirmer{interactive: true, answer: true}
	o.openDiff = func(_, _ string) error { return nil }
	require.NoError(t, o.EnsureSessionDir())
	return o, buf
}

// oneStepPlan is the minimal single-step plan used by simple-mode tests.
const oneStepPlan = `# Plan: thing

## Context

test context

## Step 1: first
do A
`

// runWithPlan starts a fresh session adopting the given plan.md content,
// mirroring the CLI's --plan path.
func runWithPlan(t *testing.T, o *Orchestrator, plan string) error {
	t.Helper()
	planPath := filepath.Join(t.TempDir(), "plan.md")
	require.NoError(t, os.WriteFile(planPath, []byte(plan), 0o600))
	return o.Run(context.Background(), RunOpts{PlanPath: planPath})
}

// fakeGitter fakes all git operations the orchestrator performs.
type fakeGitter struct {
	dirty        bool
	head         string
	emptyCommit  bool // CommitAll reports "nothing to commit"
	commits      []string
	archives     []string // "sha:dest" per ArchiveTree call
	failCommit   bool
	failHook     bool // fails normal commit with GitError, succeeds if NoVerify
	failHookOnce bool // fails first commit with GitError, then clears
	worktreeDiff string
	attrMap      map[string]map[string]string
}

func (f *fakeGitter) Head(string) (string, error) { return f.head, nil }
func (f *fakeGitter) Dirty(string) (bool, error)  { return f.dirty, nil }
func (f *fakeGitter) Diff(string, string, string) (string, error) {
	return "diff --git a/x b/x\n--- a/x\n+++ b/x\n", nil
}

func (f *fakeGitter) WorktreeDiff(string) (string, error) {
	if f.worktreeDiff != "" {
		return f.worktreeDiff, nil
	}
	return "diff --git a/pkg.go b/pkg.go\n--- a/pkg.go\n+++ b/pkg.go\n@@ -0,0 +1 @@\n+package pkg\n", nil
}

func (f *fakeGitter) CheckAttr(_ string, _, _ []string) (map[string]map[string]string, error) {
	return f.attrMap, nil
}

func (f *fakeGitter) CommitAll(dir, message string) (string, error) {
	return f.CommitAllOpts(dir, message, CommitOpts{})
}

func (f *fakeGitter) CommitAllOpts(_, message string, opts CommitOpts) (string, error) {
	if f.failHookOnce {
		f.failHookOnce = false
		return "", &GitError{
			Args:   []string{"commit", "-m", message},
			Err:    errors.New("exit status 1"),
			Output: "pre-commit hook failed: lint error",
		}
	}
	if f.failHook {
		if !opts.NoVerify {
			return "", &GitError{
				Args:   []string{"commit", "-m", message},
				Err:    errors.New("exit status 1"),
				Output: "pre-commit hook failed: lint error",
			}
		}
	} else if f.failCommit {
		return "", errors.New("git commit error")
	}
	f.commits = append(f.commits, message)
	if f.emptyCommit {
		return "", nil
	}
	return fmt.Sprintf("c%d", len(f.commits)), nil
}

func (f *fakeGitter) ArchiveTree(_, sha, dest string) error {
	f.archives = append(f.archives, sha+":"+dest)
	return os.MkdirAll(dest, 0o750)
}

// fakeConfirmer fakes the interactive human gate and hook prompts.
type verifyPromptCall struct {
	name     string
	command  string
	output   string
	notFound bool
}

type fakeConfirmer struct {
	interactive    bool
	answer         bool
	answers        []bool
	prompted       string
	prompts        []string
	hookAction     HookAction
	verifyAction   VerifyBaselineAction
	verifyActions  []VerifyBaselineAction
	verifyCalls    []verifyPromptCall
	askAnswer      string
	askAnswers     []string
	askedQuestions []string
}

func (f *fakeConfirmer) Interactive() bool { return f.interactive }
func (f *fakeConfirmer) Confirm(prompt string) (bool, error) {
	f.prompted = prompt
	f.prompts = append(f.prompts, prompt)
	if len(f.answers) > 0 {
		ans := f.answers[0]
		f.answers = f.answers[1:]
		return ans, nil
	}
	return f.answer, nil
}

func (f *fakeConfirmer) Ask(q string) (string, error) {
	f.askedQuestions = append(f.askedQuestions, q)
	if len(f.askAnswers) > 0 {
		ans := f.askAnswers[0]
		f.askAnswers = f.askAnswers[1:]
		return ans, nil
	}
	return f.askAnswer, nil
}

func (f *fakeConfirmer) PromptHookAction(_, _ string) (HookAction, error) {
	return f.hookAction, nil
}

func (f *fakeConfirmer) PromptVerifyBaseline(
	name, command, output string,
	notFound bool,
) (VerifyBaselineAction, error) {
	f.verifyCalls = append(f.verifyCalls, verifyPromptCall{name, command, output, notFound})
	if len(f.verifyActions) > 0 {
		act := f.verifyActions[0]
		f.verifyActions = f.verifyActions[1:]
		return act, nil
	}
	if f.verifyAction != "" {
		return f.verifyAction, nil
	}
	return VerifyBaselineKeep, nil
}

type plainConfirmer struct {
	interactive bool
}

func (p *plainConfirmer) Interactive() bool              { return p.interactive }
func (p *plainConfirmer) Confirm(_ string) (bool, error) { return true, nil }

func TestSimpleModeFullFlow(t *testing.T) {
	t.Parallel()

	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "simple", "s1", mock)

	require.NoError(t, runWithPlan(t, o, oneStepPlan))

	dir := o.SessionDir()

	// The adopted plan lands in the session dir verbatim, single step.
	raw, err := os.ReadFile(filepath.Join(dir, "plan.md")) //nolint:gosec // test path
	require.NoError(t, err)
	steps, err := ParsePlan(string(raw))
	require.NoError(t, err)
	require.Len(t, steps, 1)

	// Worker ran once, wrote summary, session captured.
	workCalls := mock.callsFor("worker", 1)
	require.Len(t, workCalls, 1)
	assert.False(t, workCalls[0].resumed)
	assert.FileExists(t, filepath.Join(dir, steps[0].WorkFile()))

	// No reviewer configured: no review file, auto-approved.
	assert.NoFileExists(t, filepath.Join(dir, steps[0].ReviewFile(1)))

	// Human-readable digest log exists alongside the raw stream log and
	// renders the mock run's tool call and result.
	digest, err := os.ReadFile(filepath.Join(dir, LogsDir, "step-1-worker.md")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(digest), "### Read")
	assert.Contains(t, string(digest), "## result:")
	// The digest records the exact prompt the agent received.
	assert.Contains(t, string(digest), "## prompt")
	assert.Contains(t, string(digest), "You are the WORKER")
	assert.Contains(t, string(digest), "work.md")

	// Final state: done, session ID captured, step approved.
	state, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	require.Len(t, state.Steps, 1)
	assert.Equal(t, StepApproved, state.Steps[0].Status)
	assert.Equal(t, "work-1", state.Steps[0].WorkSessionID)

	assert.FileExists(t, filepath.Join(dir, "summary.md"))
	assert.FileExists(t, filepath.Join(dir, LogsDir, "step-1-worker.jsonl"))
	assert.FileExists(t, filepath.Join(dir, LogsDir, "step-1-worker.md"))
	assert.DirExists(t, filepath.Join(dir, LogsDir))
	assert.NoFileExists(t, filepath.Join(dir, "step-1-worker.jsonl"))
	assert.FileExists(t, filepath.Join(filepath.Dir(dir), ".gitignore"))
	assert.Contains(t, progress.String(), "plan imported from plan")
}

func TestFullModeFlowApprove(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s2", mock)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	dir := o.SessionDir()

	// Two work + two review calls, none resumed.
	require.Len(t, mock.callsFor("worker", 1), 1)
	require.Len(t, mock.callsFor("worker", 2), 1)
	require.Len(t, mock.callsFor("reviewer", 1), 1)
	require.Len(t, mock.callsFor("reviewer", 2), 1)
	assert.False(t, mock.callsFor("worker", 2)[0].resumed)

	state, err := LoadState(dir)
	require.NoError(t, err)

	for _, f := range []string{
		"plan.md",
		state.Steps[0].WorkFile(), state.Steps[0].ReviewFile(1),
		state.Steps[1].WorkFile(), state.Steps[1].ReviewFile(1),
		"summary.md",
	} {
		assert.FileExists(t, filepath.Join(dir, f))
	}

	assert.Equal(t, PhaseDone, state.Phase)
	assert.Equal(t, StepApproved, state.Steps[0].Status)
	assert.Equal(t, StepApproved, state.Steps[1].Status)

	summary, err := os.ReadFile(filepath.Join(dir, "summary.md")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(summary), "Step 2: second")
	assert.Contains(t, progress.String(), "APPROVED")
}

func TestFixLoop(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{rejects: map[int]int{1: 1}}
	o, _ := newTestOrchestrator(t, "full", "s3", mock)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	dir := o.SessionDir()

	// Step 1: initial work + fix (resumed with captured session ID).
	work1 := mock.callsFor("worker", 1)
	require.Len(t, work1, 2)
	assert.False(t, work1[0].resumed)
	assert.True(t, work1[1].resumed)
	assert.Contains(t, work1[1].prompt, "reviewer requested changes")

	// Step 1 reviewed twice.
	assert.Len(t, mock.callsFor("reviewer", 1), 2)

	state, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	assert.Equal(t, 1, state.Steps[0].ReviewRounds)
	assert.Equal(t, 0, state.Steps[1].ReviewRounds)
}

func TestEscalationAfterMaxRounds(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{rejects: map[int]int{1: 99}}
	o, progress := newTestOrchestrator(t, "full", "s4", mock)

	err := runWithPlan(t, o, twoStepPlan)
	require.ErrorIs(t, err, ErrEscalated)

	dir := o.SessionDir()
	state, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, PhaseEscalated, state.Phase)
	assert.Equal(t, 3, state.Steps[0].ReviewRounds) // max 2 fix rounds, 3rd rejection escalates

	// 1 initial + 2 fix runs for the worker; 3 reviews.
	assert.Len(t, mock.callsFor("worker", 1), 3)
	assert.Len(t, mock.callsFor("reviewer", 1), 3)
	assert.Contains(t, progress.String(), "ESCALATED")
	assert.Contains(t, progress.String(), "resume with: agents implement --mode full --session s4 --resume")

	// Resuming an escalated session re-enters review; reviewer now approves.
	mock.rejects[1] = 0
	require.NoError(t, o.Resume(context.Background(), ResumeOpts{}))
	state, err = LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
}

func TestPlanGateAccepted(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-gate-acc", mock)
	fc := &fakeConfirmer{interactive: true, answer: true}
	o.confirmer = fc

	err := runWithPlan(t, o, twoStepPlan)
	require.NoError(t, err)

	require.NotEmpty(t, fc.prompts)
	assert.Contains(t, fc.prompts[0], "Start work on 2 steps? (y/n)")
	assert.Contains(t, progress.String(), "thing\n  1. first\n  2. second\n")
	assert.Contains(t, progress.String(), "plan accepted: 2 steps")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	require.Len(t, state.Steps, 2)
}

func TestPlanGateNonInteractiveSkipped(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-gate-non-interactive", mock)
	fc := &fakeConfirmer{interactive: false}
	o.confirmer = fc

	err := runWithPlan(t, o, twoStepPlan)
	require.ErrorIs(t, err, ErrAwaitingHuman)

	assert.Empty(t, fc.prompted)
	assert.Contains(t, progress.String(), "skipping plan acceptance gate")
	assert.Contains(t, progress.String(), "plan accepted: 2 steps")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseFinalHuman, state.Phase)
}

func TestWorkerMissingSummary(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{skipWork: map[int]bool{1: true}}
	o, progress := newTestOrchestrator(t, "simple", "s6", mock)

	err := runWithPlan(t, o, oneStepPlan)
	require.ErrorIs(t, err, ErrWorkSummaryMissing)
	assert.Contains(t, progress.String(), "implement failed: worker did not write its step summary")
	assert.Contains(t, progress.String(), "resume with: agents implement --mode simple --session s6 --resume")

	// The captured worker session ID must be persisted before failing so
	// --resume re-enters the conversation instead of redoing the step.
	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseWork, state.Phase)
	require.Len(t, state.Steps, 1)
	assert.Equal(t, "work-1", state.Steps[0].WorkSessionID)
	assert.Contains(t, progress.String(), "agy --conversation work-1")
}

func TestExecutionFailurePrintsResumeCommand(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{streamErr: errors.New("agent process exited 1")}
	o, progress := newTestOrchestrator(t, "full", "s-fail", mock)

	err := runWithPlan(t, o, twoStepPlan)
	require.Error(t, err)
	assert.Contains(t, progress.String(), "implement failed: worker step 1: agent process exited 1")
	assert.Contains(t, progress.String(), "resume with: agents implement --mode full --session s-fail --resume")
}

func TestFixRoundMissingSummary(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{rejects: map[int]int{1: 1}, skipFix: map[int]bool{1: true}}
	o, _ := newTestOrchestrator(t, "full", "s6b", mock)

	err := runWithPlan(t, o, twoStepPlan)
	require.ErrorIs(t, err, ErrWorkSummaryMissing)
	require.ErrorContains(t, err, "after fix round")

	// Session ID and review rounds must survive the failed fix round.
	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseReview, state.Phase)
	require.Len(t, state.Steps, 2)
	assert.Equal(t, "work-1", state.Steps[0].WorkSessionID)
	assert.Equal(t, 1, state.Steps[0].ReviewRounds)
}

func TestResumeMidFlow(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s7", mock)

	// Pre-seed state as if step 1 finished and step 2 is next.
	require.NoError(t, o.EnsureSessionDir())
	dir := o.SessionDir()
	seed := &State{
		SessionName: "s7",
		Mode:        "full",
		Task:        "task",
		Phase:       PhaseWork,
		CurrentStep: 2,
		BaseCommit:  "base-sha",
		Steps: []Step{
			{Number: 1, Title: "first", Body: "do A", Status: StepApproved},
			{Number: 2, Title: "second", Body: "do B", Status: StepPending},
		},
	}
	// No planner run: write plan.md directly (mock interactive would too, but resume skips planning).
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plan.md"), []byte(twoStepPlan), 0o600))
	require.NoError(t, SaveState(dir, seed))

	require.NoError(t, o.Resume(context.Background(), ResumeOpts{}))

	// Only step 2 executed.
	assert.Empty(t, mock.callsFor("worker", 1))
	assert.Empty(t, mock.callsFor("reviewer", 1))
	require.Len(t, mock.callsFor("worker", 2), 1)
	require.Len(t, mock.callsFor("reviewer", 2), 1)

	state, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
}

func TestResumeDoneSession(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "simple", "s8", mock)

	require.NoError(t, o.EnsureSessionDir())
	dir := o.SessionDir()
	require.NoError(t, SaveState(dir, &State{
		SessionName: "s8", Mode: "simple", Task: "t",
		Phase: PhaseDone,
		Steps: []Step{{Number: 1, Title: "x", Status: StepApproved}},
	}))

	err := o.Resume(context.Background(), ResumeOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already complete")
}

func TestRunRefusesStalePlan(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s9", mock)
	dir := o.SessionDir()

	// Pre-existing plan.md from an aborted session must not leak into a new run.
	require.NoError(t, o.EnsureSessionDir())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plan.md"), []byte("stale"), 0o600))

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	raw, err := os.ReadFile(filepath.Join(dir, "plan.md")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.NotEqual(t, "stale", string(raw))
}

func TestUnknownMode(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	repo := t.TempDir()
	_, err := NewOrchestrator(testConfig(), "nope", repo, "x", mock, zerolog.Nop(), &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not defined")
}

func TestOrchestratorPersistsStepTimestamps(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "simple", "t-times", mock)

	require.NoError(t, runWithPlan(t, o, oneStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	require.Len(t, state.Steps, 1)
	require.NotNil(t, state.Steps[0].StartedAt)
	require.NotNil(t, state.Steps[0].FinishedAt)
	assert.False(t, state.Steps[0].FinishedAt.Before(*state.Steps[0].StartedAt))
}

func TestOrchestratorTimestampsPersistedMidWork(t *testing.T) {
	t.Parallel()
	// Worker fails before writing its summary: StartedAt must already be
	// persisted so a resumed session can still show elapsed time.
	mock := &mockRunner{skipWork: map[int]bool{1: true}}
	o, _ := newTestOrchestrator(t, "simple", "t-times-mid", mock)

	require.ErrorIs(t, runWithPlan(t, o, oneStepPlan), ErrWorkSummaryMissing)

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	require.NotNil(t, state.Steps[0].StartedAt)
	assert.Nil(t, state.Steps[0].FinishedAt)
}

func TestOrchestratorRendererShowsActivity(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "t-render", mock)

	buf := &bytes.Buffer{}
	o.SetRenderer(NewRenderer(buf, RendererOpts{TTY: true, Verbose: true, Width: 80, Height: 24, Interval: time.Hour}))

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	out := buf.String()
	assert.Contains(t, out, "● Read", "verbose event tail must show worker tool activity")
	assert.Contains(t, out, "● Read step file", "verbose event tail must show reviewer tool activity")
	assert.Contains(t, out, "APPROVED")
	assert.Contains(t, out, "\x1b[?25h", "renderer must restore the cursor when the flow ends")
}

func TestOrchestratorLiveRendererSuppressesRoutineProgress(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "t-suppress-progress", mock)

	buf := &bytes.Buffer{}
	o.SetRenderer(NewRenderer(buf, RendererOpts{TTY: true, Verbose: false, Width: 80, Height: 24, Interval: time.Hour}))

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	out := buf.String()
	assert.NotContains(t, out, "step 1: reviewing with")
	assert.NotContains(t, out, "step 1: APPROVED")
	assert.NotContains(t, out, "step 1: committed")
	assert.NotContains(t, out, "working with")
	assert.Contains(t, out, "final review: human APPROVED")
}

func TestOrchestratorNonTTYRendererPrintsProgress(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "t-nontty-progress", mock)

	buf := &bytes.Buffer{}
	o.SetRenderer(
		NewRenderer(buf, RendererOpts{TTY: false, Verbose: false, Width: 80, Height: 24, Interval: time.Hour}),
	)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	out := buf.String()
	assert.Contains(t, out, "step 1: reviewing with")
	assert.Contains(t, out, "step 1: APPROVED")
	assert.Contains(t, out, "step 1: committed")
	assert.Contains(t, out, "final review: human APPROVED")
}

func TestOrchestratorPersistsStepUsage(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "t-usage", mock)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	require.Len(t, state.Steps, 2)

	// Worker (claude shape): 300+10+5 in, 40 out, $0.0123.
	for _, step := range state.Steps {
		require.NotNil(t, step.WorkerUsage, "step %d", step.Number)
		assert.Equal(t, StepUsage{
			InputTokens: 315, OutputTokens: 40,
			CostUSD: 0.0123, HasTokens: true, HasCost: true,
		}, *step.WorkerUsage, "step %d worker usage", step.Number)

		// Reviewer (opencode shape): 2000 in, 300+100 out, $0.05.
		require.NotNil(t, step.ReviewerUsage, "step %d", step.Number)
		assert.Equal(t, StepUsage{
			InputTokens: 2000, OutputTokens: 400,
			CostUSD: 0.05, HasTokens: true, HasCost: true,
		}, *step.ReviewerUsage, "step %d reviewer usage", step.Number)
	}

	// Summary reports per-step usage plus session totals.
	summary, err := os.ReadFile(filepath.Join(o.SessionDir(), "summary.md")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(summary), "↓315 ↑40 tok")
	assert.Contains(t, string(summary), "$0.01")
	assert.Contains(t, string(summary), "Total")
}

func TestOrchestratorEstimatesAgyCost(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{agyRealUsage: true}
	o, _ := newTestOrchestrator(t, "simple", "s-agy-cost", mock)

	require.NoError(t, runWithPlan(t, o, oneStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	require.Len(t, state.Steps, 1)

	// agy's own stream never reports a dollar cost (see pricing.go); the
	// orchestrator must back-fill an estimate from agy's token counts and
	// the worker's configured model (gemini-3.8-flash-medium in "simple" mode).
	// 1M fresh input + 1M cached input (cheaper rate) + 1M output.
	usage := state.Steps[0].WorkerUsage
	require.NotNil(t, usage)
	assert.Equal(t, int64(2_000_000), usage.InputTokens)
	assert.Equal(t, int64(1_000_000), usage.CacheRead)
	assert.Equal(t, int64(1_000_000), usage.OutputTokens)
	assert.True(t, usage.HasCost)
	assert.InDelta(t, 0.75+0.075+3.75, usage.CostUSD, 1e-9)
}

func TestOrchestratorAgyUsageCountsResultEventOnly(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{agyStepUsage: true}
	o, _ := newTestOrchestrator(t, "simple", "s-agy-steps", mock)

	require.NoError(t, runWithPlan(t, o, oneStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	require.Len(t, state.Steps, 1)

	// agy per-turn step_update events each re-count the whole conversation
	// context; only the final result event is authoritative. The recorded
	// usage must be the result event alone (2M in), never the sum of the
	// per-turn events plus the result (3.18M in).
	usage := state.Steps[0].WorkerUsage
	require.NotNil(t, usage)
	assert.Equal(t, int64(2_000_000), usage.InputTokens)
	assert.Equal(t, int64(1_000_000), usage.CacheRead)
	assert.Equal(t, int64(1_000_000), usage.OutputTokens)
}

func TestOrchestratorAgyUsageFallsBackToStepEvents(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{agyTruncated: true}
	o, _ := newTestOrchestrator(t, "simple", "s-agy-truncated", mock)

	require.NoError(t, runWithPlan(t, o, oneStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	require.Len(t, state.Steps, 1)

	// Stream died before the cumulative result event: best-effort totals
	// come from summing the per-turn events instead of recording nothing.
	usage := state.Steps[0].WorkerUsage
	require.NotNil(t, usage)
	assert.Equal(t, int64(1_180_000), usage.InputTokens)
	assert.Equal(t, int64(1_100_000), usage.CacheRead)
	assert.Equal(t, int64(4_000), usage.OutputTokens)
}

func TestFixLoopUsageAccumulation(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{rejects: map[int]int{1: 1}}
	o, _ := newTestOrchestrator(t, "full", "t-usage-fix", mock)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	step := state.Steps[0]

	// Fix round resumes the worker conversation: claude's result usage is
	// session-cumulative, so the worker total is REPLACED, not doubled.
	require.NotNil(t, step.WorkerUsage)
	assert.Equal(t, int64(315), step.WorkerUsage.InputTokens)
	assert.InDelta(t, 0.0123, step.WorkerUsage.CostUSD, 1e-9)

	// Reviewer runs a fresh conversation per round: usage adds across rounds.
	require.NotNil(t, step.ReviewerUsage)
	assert.Equal(t, int64(4000), step.ReviewerUsage.InputTokens)
	assert.InDelta(t, 0.1, step.ReviewerUsage.CostUSD, 1e-9)
}

func TestOrchestratorRoundArtifactsAndRoleRuns(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{rejects: map[int]int{1: 1}}
	o, _ := newTestOrchestrator(t, "full", "t-rounds", mock)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	require.NotNil(t, state.StartedAt, "session StartedAt must be set")
	require.NotNil(t, state.FinishedAt, "session FinishedAt must be set")

	step := state.Steps[0]
	stepDir := filepath.Join(o.SessionDir(), step.StepDir())

	// Step directory and round artifacts must exist without overwriting
	assert.FileExists(t, filepath.Join(stepDir, "r1.work.md"))
	assert.FileExists(t, filepath.Join(stepDir, "r1.diff"))
	assert.FileExists(t, filepath.Join(stepDir, "r1.review.md"))

	assert.FileExists(t, filepath.Join(stepDir, "r2.work.md"))
	assert.FileExists(t, filepath.Join(stepDir, "r2.diff"))
	assert.FileExists(t, filepath.Join(stepDir, "r2.review.md"))

	// The live work.md lives in the step directory too, never at session root
	assert.FileExists(t, filepath.Join(o.SessionDir(), step.WorkFile()))
	assert.NoFileExists(t, filepath.Join(o.SessionDir(), "step-1-work.md"))
	assert.NoFileExists(t, filepath.Join(o.SessionDir(), "step-1.diff"))
	assert.NoFileExists(t, filepath.Join(o.SessionDir(), "step-1-review.md"))

	// Work summaries must point to new round diff paths
	r1Work, err := os.ReadFile(filepath.Join(stepDir, "r1.work.md")) //nolint:gosec // test reads session dir artifact
	require.NoError(t, err)
	assert.Contains(t, string(r1Work), filepath.Join(step.StepDir(), "r1.diff"))
	assert.NotContains(t, string(r1Work), "step-1.diff")

	r2Work, err := os.ReadFile(filepath.Join(stepDir, "r2.work.md")) //nolint:gosec // test reads session dir artifact
	require.NoError(t, err)
	assert.Contains(t, string(r2Work), filepath.Join(step.StepDir(), "r2.diff"))
	assert.NotContains(t, string(r2Work), "step-1.diff")

	// Role runs must track full execution history
	require.Len(t, step.Runs, 4)
	assert.Equal(t, "worker", step.Runs[0].Role)
	assert.Equal(t, "approved", step.Runs[0].Status)
	assert.NotNil(t, step.Runs[0].DiffStat)

	assert.Equal(t, "reviewer (r1)", step.Runs[1].Role)
	assert.Equal(t, "rejected", step.Runs[1].Status)

	assert.Equal(t, "fix (r1)", step.Runs[2].Role)
	assert.Equal(t, "approved", step.Runs[2].Status)
	assert.NotNil(t, step.Runs[2].DiffStat)

	assert.Equal(t, "reviewer (r2)", step.Runs[3].Role)
	assert.Equal(t, "approved", step.Runs[3].Status)

	// summary.md must include the Execution Tree section
	summaryData, err := os.ReadFile(filepath.Join(o.SessionDir(), "summary.md"))
	require.NoError(t, err)
	summary := string(summaryData)
	assert.Contains(t, summary, "## Execution Tree")
	assert.Contains(t, summary, "worker")
	assert.Contains(t, summary, "reviewer (r1)")
	assert.Contains(t, summary, "fix (r1)")
	assert.Contains(t, summary, "reviewer (r2)")
}

func TestOrchestratorRendererShowsUsage(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "t-usage-render", mock)

	buf := &bytes.Buffer{}
	o.SetRenderer(
		NewRenderer(buf, RendererOpts{TTY: true, Verbose: false, Width: 120, Height: 24, Interval: time.Hour}),
	)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	out := buf.String()
	assert.Contains(t, out, "↓315 ↑40 tok")
	assert.Contains(t, out, "$0.01")
}

func TestRunRefusesDirtyWorktree(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-dirty", mock)
	o.git.(*fakeGitter).dirty = true

	err := runWithPlan(t, o, twoStepPlan)
	require.ErrorIs(t, err, ErrDirtyWorktree)
	assert.Empty(t, mock.streamCalls)
}

func TestStepCommitsAfterApproval(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-commit", mock)
	fg := o.git.(*fakeGitter)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	// One commit per approved step, using the worker's proposed messages.
	require.Len(t, fg.commits, 2)
	assert.Equal(t, "feat(test): step 1 work", fg.commits[0])
	assert.Equal(t, "feat(test): step 2 work", fg.commits[1])

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, "c1", state.Steps[0].CommitHash)
	assert.Equal(t, "c2", state.Steps[1].CommitHash)
	assert.Equal(t, "base-sha", state.BaseCommit)

	summary, err := os.ReadFile(filepath.Join(o.SessionDir(), "summary.md")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(summary), "c1")
	assert.Contains(t, progress.String(), "committed")
}

func TestStepCommitFallbackMessage(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{omitCommit: map[int]bool{1: true}}
	o, _ := newTestOrchestrator(t, "full", "s-commit-fallback", mock)
	fg := o.git.(*fakeGitter)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	require.Len(t, fg.commits, 2)
	assert.Equal(t, "implement(s-commit-fallback): step 1: first", fg.commits[0])
	assert.Equal(t, "feat(test): step 2 work", fg.commits[1])
}

func TestStepCommitNothingToCommit(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-commit-empty", mock)
	fg := o.git.(*fakeGitter)
	fg.emptyCommit = true

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	require.Len(t, fg.commits, 2)
	assert.Contains(t, progress.String(), "nothing to commit")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Empty(t, state.Steps[0].CommitHash)
	assert.Equal(t, PhaseDone, state.Phase)
}

func TestStepCommitPreCommitHookNoVerify(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-hook-noverify", mock)
	fg := o.git.(*fakeGitter)
	fg.failHook = true
	fc := o.confirmer.(*fakeConfirmer)
	fc.hookAction = HookActionNoVerify

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	require.Len(t, fg.commits, 2)
	assert.Contains(t, progress.String(), "[--no-verify]")
}

func TestStepCommitPreCommitHookEscalate(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-hook-escalate", mock)
	fg := o.git.(*fakeGitter)
	fg.failHook = true
	fc := o.confirmer.(*fakeConfirmer)
	fc.hookAction = HookActionEscalate

	err := runWithPlan(t, o, twoStepPlan)
	require.ErrorIs(t, err, ErrEscalated)
	assert.Contains(t, progress.String(), "pre-commit failed; session escalated")
}

func TestStepCommitPreCommitHookAgentFix(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-hook-agentfix", mock)
	fg := o.git.(*fakeGitter)
	fg.failHookOnce = true
	fc := o.confirmer.(*fakeConfirmer)
	fc.hookAction = HookActionAgentFix

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	assert.Contains(t, progress.String(), "pre-commit hook failure routed to worker agent")
	require.Len(t, fg.commits, 2)
}

func TestFinalReviewHappyPath(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-final", mock)
	fg := o.git.(*fakeGitter)
	fc := o.confirmer.(*fakeConfirmer)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	// Final reviewer ran once with the whole-diff prompt, then human approved.
	finalRev := mock.finalCalls("reviewer")
	require.Len(t, finalRev, 1)
	assert.Contains(t, finalRev[0].prompt, "base-sha")
	assert.FileExists(t, filepath.Join(o.SessionDir(), "final-review.md"))

	assert.Contains(t, fc.prompted, "pprove")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	assert.Equal(t, 0, state.FinalReviewRounds)
	require.NotNil(t, state.FinalReviewerUsage)
	assert.Equal(t, int64(2000), state.FinalReviewerUsage.InputTokens)

	// Diff trees archived from base and head for the human review.
	require.Len(t, fg.archives, 2)
	assert.Contains(t, fg.archives[0], "base-sha:")
	assert.Contains(t, fg.archives[1], "base-sha:")
	assert.FileExists(t, filepath.Join(o.SessionDir(), "final.diff"))

	summary, err := os.ReadFile(filepath.Join(o.SessionDir(), "summary.md")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(summary), "Final review")
	assert.Contains(t, progress.String(), "final review")
}

func TestFinalReviewNoReviewerSkipsAgent(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "simple", "s-final-simple", mock)
	fc := o.confirmer.(*fakeConfirmer)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	assert.Empty(t, mock.finalCalls("reviewer"))
	assert.NoFileExists(t, filepath.Join(o.SessionDir(), "final-review.md"))
	assert.NotEmpty(t, fc.prompted)

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
}

func TestFinalReviewFixLoop(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{finalRejects: 1}
	o, _ := newTestOrchestrator(t, "full", "s-final-fix", mock)
	fg := o.git.(*fakeGitter)

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	// Final reviewer ran twice (reject then approve); worker fixed once.
	assert.Len(t, mock.finalCalls("reviewer"), 2)
	finalWork := mock.finalCalls("worker")
	require.Len(t, finalWork, 1)
	assert.False(t, finalWork[0].resumed)

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	assert.Equal(t, 1, state.FinalReviewRounds)
	assert.Equal(t, "final-work", state.FinalWorkSessionID)

	// Reviewer rounds are fresh conversations: usage adds across rounds.
	require.NotNil(t, state.FinalReviewerUsage)
	assert.Equal(t, int64(4000), state.FinalReviewerUsage.InputTokens)

	// Final fix committed with the worker's proposed message.
	require.Len(t, fg.commits, 3)
	assert.Equal(t, "fix(final): address final review findings", fg.commits[2])
	assert.Equal(t, "c3", state.FinalCommitHash)
}

func TestFinalReviewEscalationAndResume(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{finalRejects: 99}
	o, progress := newTestOrchestrator(t, "full", "s-final-esc", mock)

	err := runWithPlan(t, o, twoStepPlan)
	require.ErrorIs(t, err, ErrEscalated)
	assert.Contains(t, progress.String(), "ESCALATED")
	assert.Contains(t, progress.String(), "resume with: agents implement --mode full --session s-final-esc --resume")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseEscalated, state.Phase)
	// Max 2 fix rounds: 2 fix runs, 3 final reviews.
	assert.Len(t, mock.finalCalls("reviewer"), 3)
	assert.Len(t, mock.finalCalls("worker"), 2)

	// Resume re-enters final review; reviewer now approves.
	mock.finalRejects = 0
	require.NoError(t, o.Resume(context.Background(), ResumeOpts{}))
	state, err = LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
}

func TestFinalHumanRejectEscalates(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-final-no", mock)
	fc := o.confirmer.(*fakeConfirmer)
	fc.answers = []bool{true} // accept plan gate, reject final human review
	fc.answer = false

	err := runWithPlan(t, o, twoStepPlan)
	require.ErrorIs(t, err, ErrEscalated)
	assert.Contains(t, progress.String(), "resume with: agents implement --mode full --session s-final-no --resume")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseEscalated, state.Phase)

	// Human edited or changed their mind; resume re-runs final review + prompt.
	fc.answer = true
	require.NoError(t, o.Resume(context.Background(), ResumeOpts{}))
	assert.Len(t, mock.finalCalls("reviewer"), 2)
	state, err = LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
}

func TestFinalHumanNonInteractive(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-final-wait", mock)
	fc := o.confirmer.(*fakeConfirmer)
	fc.interactive = false
	var opened []string
	o.openDiff = func(base, head string) error {
		opened = append(opened, base, head)
		return nil
	}

	err := runWithPlan(t, o, twoStepPlan)
	require.ErrorIs(t, err, ErrAwaitingHuman)
	assert.Contains(t, progress.String(), "resume with: agents implement --mode full --session s-final-wait --resume")

	// Diff view opened + final.diff written; state parked awaiting human.
	require.Len(t, opened, 2)
	assert.FileExists(t, filepath.Join(o.SessionDir(), "final.diff"))
	assert.Empty(t, fc.prompted)

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseFinalHuman, state.Phase)

	// Resume re-opens the diff and prompts; human approves.
	fc.interactive = true
	require.NoError(t, o.Resume(context.Background(), ResumeOpts{}))
	state, err = LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	assert.NotEmpty(t, fc.prompted)
}

func TestDiffOpenerFallbackWritesDiffFile(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-diff-fallback", mock)
	o.openDiff = func(_, _ string) error { return errors.New("no zed") }

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	assert.FileExists(t, filepath.Join(o.SessionDir(), "final.diff"))
	assert.Contains(t, progress.String(), "final.diff")
}

func TestRunClearsStaleFinalArtifacts(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "simple", "s-stale-final", mock)
	dir := o.SessionDir()
	require.NoError(t, o.EnsureSessionDir())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "final-review.md"), []byte("stale"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "final-work.md"), []byte("stale"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, DiffDir), 0o750))

	require.NoError(t, runWithPlan(t, o, twoStepPlan))
	assert.NoFileExists(t, filepath.Join(dir, "final-review.md"))
	assert.NoFileExists(t, filepath.Join(dir, "final-work.md"))
	// diff/ was cleared then recreated fresh by the human review phase.
	assert.DirExists(t, filepath.Join(dir, DiffDir))
	assert.DirExists(t, filepath.Join(dir, DiffDir, "base"))
	assert.DirExists(t, filepath.Join(dir, DiffDir, "head"))
}

func TestWorkerStepWritesFilteredDiffAndSummaryPointer(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "simple", "s-step-diff", mock)

	// Write .gitattributes in the repo
	attrContent := "*.pb.go linguist-generated\n"
	require.NoError(t, os.WriteFile(filepath.Join(o.repoDir, ".gitattributes"), []byte(attrContent), 0o600))

	// Configure fakeGitter to return a diff containing code, lockfile, and generated file
	fg := o.git.(*fakeGitter)
	fg.worktreeDiff = `diff --git a/feature.go b/feature.go
index 1111111..2222222 100644
--- a/feature.go
+++ b/feature.go
@@ -0,0 +1,5 @@
+package feature
diff --git a/package-lock.json b/package-lock.json
index 3333333..4444444 100644
--- a/package-lock.json
+++ b/package-lock.json
@@ -1,3 +1,3 @@
-"version": "1.0.0"
+"version": "1.0.1"
diff --git a/model.pb.go b/model.pb.go
new file mode 100644
index 0000000..5555555
--- /dev/null
+++ b/model.pb.go
@@ -0,0 +1,10 @@
+package model
`

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	step := state.Steps[0]

	roundDiffFile := filepath.Join(o.SessionDir(), step.DiffFile(1))
	assert.FileExists(t, roundDiffFile)

	rawDiff, err := os.ReadFile(roundDiffFile) //nolint:gosec // test reads session dir artifact
	require.NoError(t, err)
	diffStr := string(rawDiff)
	assert.Contains(t, diffStr, "diff --git a/feature.go b/feature.go")
	assert.NotContains(t, diffStr, "package-lock.json", "lockfiles must be excluded from step diff")
	assert.NotContains(t, diffStr, "model.pb.go", "gitattributes generated files must be excluded from step diff")

	workFile := filepath.Join(o.SessionDir(), step.WorkFile())
	assert.FileExists(t, workFile)

	rawWork, err := os.ReadFile(workFile) //nolint:gosec // test reads session dir artifact
	require.NoError(t, err)
	workStr := string(rawWork)
	assert.Contains(
		t,
		workStr,
		"## Diff\n- Unified diff: `"+roundDiffFile+"`",
		"worker summary must point to round diff",
	)
}

func TestWorkerFixRoundUpdatesDiff(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		rejects: map[int]int{1: 1}, // reject step 1 once
	}
	o, _ := newTestOrchestrator(t, "full", "s-fix-diff", mock)

	fg := o.git.(*fakeGitter)
	fg.worktreeDiff = `diff --git a/code.go b/code.go
index 1111111..2222222 100644
--- a/code.go
+++ b/code.go
@@ -0,0 +1 @@
+initial
`

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	step := state.Steps[0]

	roundDiffFile := filepath.Join(o.SessionDir(), step.DiffFile(2))
	assert.FileExists(t, roundDiffFile)

	workFile := filepath.Join(o.SessionDir(), step.WorkFile())
	rawWork, err := os.ReadFile(workFile) //nolint:gosec // test reads session dir artifact
	require.NoError(t, err)
	assert.Contains(t, string(rawWork), "## Diff\n- Unified diff: `"+roundDiffFile+"`")
}

func setupTestWorkRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec // test helper
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, string(out))
	}
	run("init", "-b", "main")
	run("config", "user.name", "Flow Test")
	run("config", "user.email", "flow@test.local")
	run("config", "commit.gpgsign", "false")
	run("config", "core.excludesFile", "/dev/null")
	run("remote", "add", "origin", "https://github.com/smartcontractkit/chainlink.git")

	err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# chainlink"), 0o600)
	require.NoError(t, err)
	run("add", ".")
	run("commit", "-m", "initial commit")

	if branch != "" && branch != "main" {
		run("checkout", "-b", branch)
	}
	return dir
}

func newTestOrchestratorInRepo(
	t *testing.T,
	repo, mode, session string,
	mock *mockRunner,
) (*Orchestrator, *bytes.Buffer) {
	t.Helper()
	mock.sessionDir = filepath.Join(repo, ".agents", "local", session)
	buf := &bytes.Buffer{}
	o, err := NewOrchestrator(testConfig(), mode, repo, session, mock, zerolog.Nop(), buf)
	require.NoError(t, err)
	o.git = &fakeGitter{head: "base-sha"}
	o.confirmer = &fakeConfirmer{interactive: true, answer: true}
	o.openDiff = func(_, _ string) error { return nil }
	return o, buf
}

func TestOrchestrator_RefuseDefaultBranch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		branch  string
		refuse  bool
		wantErr bool
	}{
		{"refused on default branch", "main", true, true},
		{"allowed on work branch", "DX-1234-fix", true, false},
		{"allowed on default branch when unset", "main", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := setupTestWorkRepo(t, tt.branch)
			o, _ := newTestOrchestratorInRepo(t, repo, "full", "s1", &mockRunner{})
			o.cfg.RefuseDefaultBranch = tt.refuse

			err := runWithPlan(t, o, twoStepPlan)
			if tt.wantErr {
				require.ErrorContains(t, err, `default branch "main"`)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestOrchestrator_ContextCommand(t *testing.T) {
	t.Parallel()
	repo := setupTestWorkRepo(t, "work")
	mock := &mockRunner{}
	o, _ := newTestOrchestratorInRepo(t, repo, "full", "s1", mock)
	// pwd proves the command runs in the repo dir.
	o.cfg.ContextCommand = `echo "<ctx dir=\"$(pwd)\">"`

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	workers := mock.callsFor("worker", 1)
	require.NotEmpty(t, workers)
	assert.Contains(t, workers[0].prompt, `<ctx dir="`+repo+`">`)
}

func TestOrchestrator_ContextCommandFails(t *testing.T) {
	t.Parallel()
	repo := setupTestWorkRepo(t, "work")
	mock := &mockRunner{}
	o, _ := newTestOrchestratorInRepo(t, repo, "full", "s1", mock)
	o.cfg.ContextCommand = "echo boom >&2; exit 3"

	err := runWithPlan(t, o, twoStepPlan)
	require.ErrorContains(t, err, "boom")
	assert.Empty(t, mock.streamCalls, "no agent runs when context_command fails")
}

func TestOrchestrator_Run_IgnoresTicketFileInDirtyCheck(t *testing.T) {
	t.Parallel()
	repo := setupTestWorkRepo(t, "DX-1234-fix-leak")
	ticketMD := `<ticket id="DX-1234" summary="Fix memory leak" link="https://jira.example.com/browse/DX-1234" status="In Progress">
<details>
Fix the leak in the listener loop.
</details>
</ticket>`
	// Create untracked DX-1234.ticket.md in worktree
	require.NoError(t, os.WriteFile(filepath.Join(repo, "DX-1234.ticket.md"), []byte(ticketMD), 0o600))

	mock := &mockRunner{}
	o, _ := newTestOrchestratorInRepo(t, repo, "full", "s1", mock)
	// Use real ExecGitter so git status --porcelain is evaluated
	o.git = ExecGitter{}

	err := runWithPlan(t, o, twoStepPlan)
	require.NoError(t, err)
}

func TestOrchestratorVerifyTimeout(t *testing.T) {
	t.Parallel()

	var oNil *Orchestrator
	assert.Equal(t, DefaultVerifyTimeout, oNil.verifyTimeout())

	oEmpty := &Orchestrator{}
	assert.Equal(t, DefaultVerifyTimeout, oEmpty.verifyTimeout())

	oSet := &Orchestrator{
		cfg: &Config{
			VerifyTimeout: "45m",
		},
	}
	assert.Equal(t, 45*time.Minute, oSet.verifyTimeout())
}

func TestWorktreeDigest(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...) //nolint:gosec // test helper
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, string(out))
	}
	run("init")
	run("config", "user.name", "Flow Test")
	run("config", "user.email", "flow@test.local")
	run("config", "commit.gpgsign", "false")

	gitter := ExecGitter{}
	filePath := filepath.Join(tmp, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("initial\n"), 0o600))
	_, err := gitter.CommitAll(tmp, "initial commit")
	require.NoError(t, err)

	o := &Orchestrator{
		repoDir: tmp,
		git:     gitter,
	}

	// Stable for an unchanged tree
	d1, err := o.worktreeDigest()
	require.NoError(t, err)
	d2, err := o.worktreeDigest()
	require.NoError(t, err)
	assert.Equal(t, d1, d2, "worktreeDigest must be stable on unchanged tree")

	// Differs after a file is modified
	require.NoError(t, os.WriteFile(filePath, []byte("initial\nmodified\n"), 0o600))
	dMod, err := o.worktreeDigest()
	require.NoError(t, err)
	assert.NotEqual(t, d1, dMod, "worktreeDigest must differ after file modification")

	// Revert file modification
	require.NoError(t, os.WriteFile(filePath, []byte("initial\n"), 0o600))
	dReverted, err := o.worktreeDigest()
	require.NoError(t, err)
	assert.Equal(t, d1, dReverted, "worktreeDigest matches initial once modification is reverted")

	// Differs after a file is added (untracked)
	addedPath := filepath.Join(tmp, "untracked.txt")
	require.NoError(t, os.WriteFile(addedPath, []byte("untracked content\n"), 0o600))
	dAdd, err := o.worktreeDigest()
	require.NoError(t, err)
	assert.NotEqual(t, d1, dAdd, "worktreeDigest must differ after file addition")

	// Commit added file so tree is clean again
	_, err = gitter.CommitAll(tmp, "add file")
	require.NoError(t, err)
	dClean, err := o.worktreeDigest()
	require.NoError(t, err)

	// Differs after a file is deleted
	require.NoError(t, os.Remove(addedPath))
	dDel, err := o.worktreeDigest()
	require.NoError(t, err)
	assert.NotEqual(t, dClean, dDel, "worktreeDigest must differ after file deletion")
}

func TestVerifyGateCommitMatchesStepDiffWithAutofix(t *testing.T) {
	t.Parallel()
	repo := setupTestWorkRepo(t, "DX-1234-autofix")
	ticketMD := `<ticket id="DX-1234" summary="Autofix" link="https://jira.example.com/browse/DX-1234" status="In Progress">
<details>
Autofix test.
</details>
</ticket>`
	require.NoError(t, os.WriteFile(filepath.Join(repo, "DX-1234.ticket.md"), []byte(ticketMD), 0o600))

	const autofixPlan = "# Plan: thing\n\n" +
		"## Context\n\ntest context\n\n" +
		"## Verification\n\n" +
		"- lint: `golangci-lint run --fix`\n\n" +
		"## Step 1: first\ndo A\n\n" +
		"- **Files**:\n  - pkg.go\n" +
		"- **Acceptance Criteria**:\n  - [ ] a done\n"

	mock := &mockRunner{}
	o, _ := newTestOrchestratorInRepo(t, repo, "full", "s-autofix-commit", mock)
	o.git = ExecGitter{}

	pkgFile := filepath.Join(repo, "pkg.go")
	autofixDone := false

	v := &recordingVerifier{
		onRun: func(cmd string) {
			isPreflight := strings.Contains(cmd, "--fix") && !autofixDone && len(mock.callsFor("worker", 1)) == 0
			if isPreflight {
				return
			}
			// When worker has run, step 1 verification mutates pkg.go on first pass
			if !autofixDone {
				autofixDone = true
				_ = os.WriteFile(pkgFile, []byte("package pkg\n// autofixed formatting\n"), 0o600)
			}
		},
	}
	o.verifyRun = v.run

	err := runWithPlan(t, o, autofixPlan)
	require.NoError(t, err)

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.True(t, state.Steps[0].Verified)

	// Verify r1.diff content matches git commit diff
	diffPath := filepath.Join(o.SessionDir(), "1-first", "r1.diff")
	diffBytes, err := os.ReadFile(diffPath) //nolint:gosec // test file read
	require.NoError(t, err)
	assert.Contains(t, string(diffBytes), "autofixed formatting")

	// Get commit diff from git
	cmd := exec.Command( //nolint:gosec // test helper
		"git",
		"-C",
		repo,
		"show",
		state.Steps[0].CommitHash,
		"--",
		"pkg.go",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "autofixed formatting")
}

func TestCommitStepRequiresVerifiedWhenCommandsDeclared(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-commit-unverified", mock)

	state := &State{
		SessionName: "s-commit-unverified",
		Verification: Verification{
			Cmds: []VerifyCommand{{Name: "test", Command: "go test ./..."}},
		},
		Steps: []Step{
			{Number: 1, Title: "Step 1", Verified: false},
		},
	}
	step := &state.Steps[0]

	err := o.commitStep(state, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unverified")
}

func TestStateFinalVerifiedRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := &State{
		SessionName:   "test-session",
		FinalVerified: true,
	}
	require.NoError(t, SaveState(dir, s))
	loaded, err := LoadState(dir)
	require.NoError(t, err)
	assert.True(t, loaded.FinalVerified)

	s.FinalVerified = false
	require.NoError(t, SaveState(dir, s))
	loaded, err = LoadState(dir)
	require.NoError(t, err)
	assert.False(t, loaded.FinalVerified)
}

const twoStepVerifyPlan = `# Plan: thing

## Context

test context

## Verification

- build: echo build
- test: echo test

## Step 1: first
do A

- **Files**:
  - a.go
- **Acceptance Criteria**:
  - [ ] a done

## Step 2: second
do B

- **Files**:
  - b.go
- **Acceptance Criteria**:
  - [ ] b done
`

func TestFinalReviewFixRoundRunsVerification(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{finalRejects: 1}
	o, progress := newTestOrchestrator(t, "full", "s-final-verify-run", mock)
	fg := o.git.(*fakeGitter)

	verifyCalls := 0
	o.verifyRun = func(_ context.Context, _, _ string) (string, error) {
		verifyCalls++
		return "ok", nil
	}

	require.NoError(t, runWithPlan(t, o, twoStepVerifyPlan))

	// Preflight (2) + Step 1 (2) + Step 2 (2) = 6 calls before final review.
	// Final review fix round must run verification (2 commands).
	// Total verify calls should be at least 8.
	assert.GreaterOrEqual(t, verifyCalls, 8)
	assert.Contains(t, progress.String(), "final review: verifying (2 command(s))")
	assert.Contains(t, progress.String(), "final review: verification passed")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	assert.True(t, state.FinalVerified)
	assert.Equal(t, 1, state.FinalReviewRounds)
	require.Len(t, fg.commits, 3)
}

func TestFinalReviewFixRoundFailsVerifyAndWorkerFixes(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{finalRejects: 1}
	o, progress := newTestOrchestrator(t, "full", "s-final-verify-fix", mock)

	failOnce := true
	verifyRuns := 0
	o.verifyRun = func(_ context.Context, _, cmd string) (string, error) {
		verifyRuns++
		if cmd == "echo test" && failOnce && len(mock.finalCalls("worker")) == 1 {
			failOnce = false
			return "test failed: assertion error", errors.New("exit status 1")
		}
		return "ok", nil
	}

	require.NoError(t, runWithPlan(t, o, twoStepVerifyPlan))

	assert.Contains(t, progress.String(), "final review: `echo test` failed — worker fixing (attempt 1/2)")
	assert.Contains(t, progress.String(), "final review: verification passed")

	// Final worker was called for the review fix AND resumed for the verify fix
	finalWorkerCalls := mock.finalCalls("worker")
	require.Len(t, finalWorkerCalls, 2)
	assert.True(t, finalWorkerCalls[1].resumed)
	assert.Contains(t, finalWorkerCalls[1].prompt, "failed the repo's verification gate")
	assert.Contains(t, finalWorkerCalls[1].prompt, "final-work.md")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	assert.True(t, state.FinalVerified)
}

func TestFinalReviewVerifyBudgetExhaustionEscalates(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{finalRejects: 1}
	o, progress := newTestOrchestrator(t, "full", "s-final-verify-esc", mock)

	o.verifyRun = func(_ context.Context, _, cmd string) (string, error) {
		// Only fail test command during final review (after step 2 is done)
		if cmd == "echo test" && len(mock.finalCalls("worker")) >= 1 {
			return fmt.Sprintf("test failed: run %d", len(mock.streamCalls)), errors.New("exit status 1")
		}
		return "ok", nil
	}

	err := runWithPlan(t, o, twoStepVerifyPlan)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrVerifyExhausted)
	assert.Contains(t, err.Error(), "final review")
	assert.Contains(t, err.Error(), "echo test")
	assert.Contains(t, progress.String(), "final review: ESCALATED")
	assert.Contains(
		t,
		progress.String(),
		"resume with: agents implement --mode full --session s-final-verify-esc --resume",
	)

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseEscalated, state.Phase)
	assert.False(t, state.FinalVerified)
}

func TestFinalReviewNoFixRoundsSkipsVerification(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{finalRejects: 0}
	o, progress := newTestOrchestrator(t, "full", "s-final-verify-skip", mock)

	verifyCalls := 0
	o.verifyRun = func(_ context.Context, _, _ string) (string, error) {
		verifyCalls++
		return "ok", nil
	}

	require.NoError(t, runWithPlan(t, o, twoStepVerifyPlan))

	// Preflight (2) + Step 1 (2) + Step 2 (2) = exactly 6 calls. No final review verification calls.
	assert.Equal(t, 6, verifyCalls)
	assert.NotContains(t, progress.String(), "final review: verifying")
	assert.NotContains(t, progress.String(), "final review: verification passed")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	assert.False(t, state.FinalVerified) // no final review fix occurred
}

func TestFinalReviewResumedEscalatedSessionReverifies(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{finalRejects: 0}
	o, progress := newTestOrchestrator(t, "full", "s-final-verify-resume", mock)

	verifyCalls := 0
	o.verifyRun = func(_ context.Context, _, _ string) (string, error) {
		verifyCalls++
		return "ok", nil
	}

	// Prepare an escalated session state with 1 final review round that is unverified
	now := time.Now()
	state := &State{
		SessionName: "s-final-verify-resume",
		Mode:        "full",
		Task:        "task",
		Phase:       PhaseEscalated,
		BaseCommit:  "base-sha",
		StartedAt:   &now,
		Verification: Verification{
			Cmds: []VerifyCommand{
				{Name: "build", Command: "echo build"},
				{Name: "test", Command: "echo test"},
			},
		},
		Steps: []Step{
			{Number: 1, Title: "first", Status: StepApproved, Verified: true},
			{Number: 2, Title: "second", Status: StepApproved, Verified: true},
		},
		FinalReviewRounds:  1,
		FinalWorkSessionID: "final-work-sid",
		FinalVerified:      false,
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	// Ensure final-work.md exists so finalCommit can read it
	require.NoError(t, os.WriteFile(
		filepath.Join(o.SessionDir(), FinalWorkFile),
		[]byte("# Final Work\n\nCOMMIT: fix(final): address issues\n"),
		0o600,
	))

	require.NoError(t, o.Resume(context.Background(), ResumeOpts{}))

	// Reviewer approved, so before finalCommit the verification gate must have run!
	assert.Equal(t, 2, verifyCalls)
	assert.Contains(t, progress.String(), "final review: verifying (2 command(s))")
	assert.Contains(t, progress.String(), "final review: verification passed")

	loaded, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, loaded.Phase)
	assert.True(t, loaded.FinalVerified)
}

func TestFinalReviewVerifyAutofixPasses(t *testing.T) {
	t.Parallel()
	repo := setupTestWorkRepo(t, "DX-5678-final-autofix")
	ticketMD := `<ticket id="DX-5678" summary="Autofix" link="https://jira.example.com/browse/DX-5678" status="In Progress">
<details>
Autofix test.
</details>
</ticket>`
	require.NoError(t, os.WriteFile(filepath.Join(repo, "DX-5678.ticket.md"), []byte(ticketMD), 0o600))

	autofixFinalPlan := "# Plan: autofix\n\n## Context\n\nctx\n\n## Verification\n\n- lint: `lint --fix`\n\n" +
		"## Step 1: first\ndo A\n- **Files**:\n  - pkg.go\n- **Acceptance Criteria**:\n  - [ ] a done\n"

	mock := &mockRunner{finalRejects: 1}
	o, progress := newTestOrchestratorInRepo(t, repo, "full", "s-autofix-final", mock)
	o.git = ExecGitter{}

	pkgFile := filepath.Join(repo, "pkg.go")
	autofixDone := false

	v := &recordingVerifier{
		onRun: func(cmd string) {
			// Trigger autofix only once during final review (after final worker ran)
			if strings.Contains(cmd, "--fix") && !autofixDone && len(mock.finalCalls("worker")) == 1 {
				autofixDone = true
				_ = os.WriteFile(pkgFile, []byte("package pkg\n// autofixed in final review\n"), 0o600)
			}
		},
	}
	o.verifyRun = v.run

	require.NoError(t, runWithPlan(t, o, autofixFinalPlan))

	assert.Contains(t, progress.String(), "final review: verification auto-fixed working tree")
	assert.Contains(t, progress.String(), "final review: verification passed")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.True(t, state.FinalVerified)
}

func TestOrchestratorEscalationInteractive(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		escalations: map[int][]string{
			1: {"Which database driver should we use?", "What is the default port?"},
		},
	}
	o, _ := newTestOrchestrator(t, "full", "t-esc-interactive", mock)
	fc := o.confirmer.(*fakeConfirmer)
	fc.askAnswers = []string{"PostgreSQL", "5432"}

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)

	// Questions asked to Confirmer
	require.Len(t, fc.askedQuestions, 2)
	assert.Equal(t, "Which database driver should we use?", fc.askedQuestions[0])
	assert.Equal(t, "What is the default port?", fc.askedQuestions[1])

	// Escalations recorded on Step 1
	step := state.Steps[0]
	require.Len(t, step.Escalations, 2)
	assert.Equal(t, "Which database driver should we use?", step.Escalations[0].Question)
	assert.Equal(t, "PostgreSQL", step.Escalations[0].Answer)
	require.NotNil(t, step.Escalations[0].AskedAt)
	assert.Equal(t, "What is the default port?", step.Escalations[1].Question)
	assert.Equal(t, "5432", step.Escalations[1].Answer)
	require.NotNil(t, step.Escalations[1].AskedAt)

	// Step.Runs contains worker and ask (a1)
	require.GreaterOrEqual(t, len(step.Runs), 2)
	assert.Equal(t, "worker", step.Runs[0].Role)
	assert.Equal(t, "ask (a1)", step.Runs[1].Role)

	// summary.md includes Q&A
	summaryData, err := os.ReadFile(filepath.Join(o.SessionDir(), "summary.md"))
	require.NoError(t, err)
	summary := string(summaryData)
	assert.Contains(t, summary, "Q: Which database driver should we use?")
	assert.Contains(t, summary, "A: PostgreSQL")
	assert.Contains(t, summary, "Q: What is the default port?")
	assert.Contains(t, summary, "A: 5432")
}

func TestOrchestratorEscalationMultipleRounds(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		escalations: map[int][]string{
			1: {"Question round 1?"},
		},
		nextEscalations: map[int][]string{
			1: {"Question round 2?"},
		},
	}
	o, _ := newTestOrchestrator(t, "full", "t-esc-multi-rounds", mock)
	fc := o.confirmer.(*fakeConfirmer)
	fc.askAnswers = []string{"Answer 1", "Answer 2"}

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)

	step := state.Steps[0]
	require.Len(t, step.Escalations, 2)
	assert.Equal(t, "Question round 1?", step.Escalations[0].Question)
	assert.Equal(t, "Answer 1", step.Escalations[0].Answer)
	assert.Equal(t, "Question round 2?", step.Escalations[1].Question)
	assert.Equal(t, "Answer 2", step.Escalations[1].Answer)

	require.GreaterOrEqual(t, len(step.Runs), 3)
	assert.Equal(t, "worker", step.Runs[0].Role)
	assert.Equal(t, "ask (a1)", step.Runs[1].Role)
	assert.Equal(t, "ask (a2)", step.Runs[2].Role)
}

func TestOrchestratorEscalationExceedsMaxRounds(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		escalations: map[int][]string{
			1: {"Q1?"},
		},
		nextEscalations: map[int][]string{
			1: {"Still blocked?"},
		},
	}
	o, progress := newTestOrchestrator(t, "full", "t-esc-exceed-rounds", mock)
	fc := o.confirmer.(*fakeConfirmer)
	fc.askAnswers = []string{"A1", "A2", "A3", "A4"}
	o.cfg.AskMaxRounds = 1

	err := runWithPlan(t, o, twoStepPlan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step 1 exceeded max ask rounds (1)")
	assert.Contains(t, progress.String(), "exceeded max ask rounds")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseEscalated, state.Phase)
}

func TestOrchestratorEscalationNonInteractive(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		escalations: map[int][]string{
			1: {"Need user decision?"},
		},
	}
	o, progress := newTestOrchestrator(t, "full", "t-esc-non-interactive", mock)
	o.confirmer = &fakeConfirmer{interactive: false}

	err := runWithPlan(t, o, twoStepPlan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step 1 worker escalated")
	assert.Contains(t, err.Error(), "Need user decision?")
	assert.Contains(t, progress.String(), "Need user decision?")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseEscalated, state.Phase)

	step := state.Steps[0]
	require.Len(t, step.Escalations, 1)
	assert.Equal(t, "Need user decision?", step.Escalations[0].Question)
	assert.Empty(t, step.Escalations[0].Answer)
}

func TestOrchestratorEscalationEmptyAnswerDefault(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		escalations: map[int][]string{
			1: {"Any preference?"},
		},
	}
	o, _ := newTestOrchestrator(t, "full", "t-esc-empty-ans", mock)
	fc := o.confirmer.(*fakeConfirmer)
	fc.askAnswers = []string{""}

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)

	step := state.Steps[0]
	require.Len(t, step.Escalations, 1)
	assert.Equal(t, "Any preference?", step.Escalations[0].Question)
	assert.Equal(t, "no preference — use your judgement", step.Escalations[0].Answer)
}

func TestWriteSummaryIncludesEscalations(t *testing.T) {
	t.Parallel()
	o, _ := newTestOrchestrator(t, "simple", "t-summary-esc", &mockRunner{})

	now := time.Now()
	state := &State{
		SessionName: "t-summary-esc",
		Mode:        "simple",
		Task:        "do thing",
		Phase:       PhaseDone,
		Steps: []Step{
			{
				Number:       1,
				Title:        "Setup db",
				Status:       StepApproved,
				ReviewRounds: 0,
				Escalations: []Escalation{
					{
						Question: "Which database?",
						Answer:   "PostgreSQL",
						AskedAt:  &now,
					},
				},
			},
		},
	}

	require.NoError(t, o.writeSummary(state))
	data, err := os.ReadFile(filepath.Join(o.SessionDir(), "summary.md"))
	require.NoError(t, err)
	summary := string(data)
	assert.Contains(t, summary, "- Step 1: Setup db [approved]")
	assert.Contains(t, summary, "  - Q: Which database?\n    A: PostgreSQL\n")
}
