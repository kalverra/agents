package implement

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verifyPlan declares a verification gate with two commands and one step.
const verifyPlan = "# Plan: thing\n\n" +
	"## Context\n\ntest context\n\n" +
	"## Verification\n\n" +
	"- test: `go test ./...`\n" +
	"- lint: `golangci-lint run ./...`\n\n" +
	"## Step 1: first\ndo A\n\n" +
	"- **Files**:\n  - a.go\n" +
	"- **Acceptance Criteria**:\n  - [ ] a done\n"

// recordingVerifier captures the commands it is asked to run and fails the
// ones named in failing, up to failTimes occurrences each.
type recordingVerifier struct {
	mu           sync.Mutex
	ran          []string
	failing      map[string]int // command -> remaining failures
	failBaseline bool
	alwaysErr    bool
	errOutputs   []string
	errIdx       int
	onRun        func(command string)
}

func (v *recordingVerifier) run(ctx context.Context, _, command string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ran = append(v.ran, command)
	if v.onRun != nil {
		v.onRun(command)
	}
	isPreflight := ctx.Value(preflightContextKey) != nil
	if isPreflight && !v.failBaseline {
		return "ok", nil
	}
	nextErr := func() string {
		msg := "boom: it is broken"
		if len(v.errOutputs) > 0 {
			msg = v.errOutputs[v.errIdx%len(v.errOutputs)]
			v.errIdx++
		}
		return msg
	}
	if v.alwaysErr {
		return nextErr(), errors.New("exit status 1")
	}
	if n, ok := v.failing[command]; ok && n > 0 {
		v.failing[command] = n - 1
		return nextErr(), errors.New("exit status 1")
	}
	return "ok", nil
}

func (v *recordingVerifier) stopFailing() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.alwaysErr = false
	v.failing = nil
}

func (v *recordingVerifier) calls() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.ran...)
}

func TestClampVerifyOutputKeepsHeadAndTail(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "short", clampVerifyOutput("  short  "))

	head := strings.Repeat("A", 100)
	tail := strings.Repeat("Z", 100)
	long := head + strings.Repeat("m", maxVerifyOutput*2) + tail

	got := clampVerifyOutput(long)
	assert.Less(t, len(got), len(long))
	assert.True(t, strings.HasPrefix(got, head), "head must survive (compiler errors come first)")
	assert.True(t, strings.HasSuffix(got, tail), "tail must survive (test summaries come last)")
	assert.Contains(t, got, "characters elided")
}

func TestFirstVerifyFailureRunsInOrderAndStopsAtFailure(t *testing.T) {
	t.Parallel()
	v := &recordingVerifier{failing: map[string]int{"T": 1}}
	o := &Orchestrator{verifyRun: v.run}

	cmds := Verification{Cmds: []VerifyCommand{
		{Name: "build", Command: "B"},
		{Name: "test", Command: "T"},
		{Name: "lint", Command: "L"},
	}}.Commands()
	failure := o.firstVerifyFailure(context.Background(), cmds)

	require.NotNil(t, failure)
	assert.Equal(t, "test", failure.Command.Name)
	assert.True(t, failure.Failed())
	// Build ran first and passed; lint never ran because test failed.
	assert.Equal(t, []string{"B", "T"}, v.calls())
}

func TestFirstVerifyFailureNilWhenAllPass(t *testing.T) {
	t.Parallel()
	v := &recordingVerifier{}
	o := &Orchestrator{verifyRun: v.run}

	failure := o.firstVerifyFailure(context.Background(), Verification{Cmds: []VerifyCommand{
		{Name: "build", Command: "B"},
		{Name: "test", Command: "T"},
	}}.Commands())
	assert.Nil(t, failure)
	assert.Equal(t, []string{"B", "T"}, v.calls())
}

func TestVerifyGateSkippedWhenPlanDeclaresNothing(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-noverify", mock)
	v := &recordingVerifier{}
	o.verifyRun = v.run

	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	assert.Empty(t, v.calls(), "no ## Verification section means no gate")
	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.True(t, state.Verification.Empty())
}

func TestVerifyGateRunsDeclaredCommands(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-verify-pass", mock)
	v := &recordingVerifier{}
	o.verifyRun = v.run

	require.NoError(t, runWithPlan(t, o, verifyPlan))

	assert.Equal(t, []string{
		"go test ./...", "golangci-lint run ./...", // baseline preflight
		"go test ./...", "golangci-lint run ./...", // step 1 gate
	}, v.calls())
	assert.Contains(t, progress.String(), "verification baseline: test ok, lint ok")
	assert.Contains(t, progress.String(), "verification passed")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, []VerifyCommand{
		{Name: "test", Command: "go test ./..."},
		{Name: "lint", Command: "golangci-lint run ./..."},
	}, state.Verification.Commands())
	// A clean gate must not spawn any extra worker run.
	for _, r := range state.Steps[0].Runs {
		assert.NotContains(t, r.Role, "verify")
	}
}

func TestVerifyGateResumesWorkerOnFailureThenPasses(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-verify-fix", mock)
	v := &recordingVerifier{failing: map[string]int{"go test ./...": 1}}
	o.verifyRun = v.run

	require.NoError(t, runWithPlan(t, o, verifyPlan))

	// Preflight passes; step 1 gate fails on test; after the fix the whole set is re-run.
	assert.Equal(t, []string{
		"go test ./...", "golangci-lint run ./...", // baseline preflight
		"go test ./...",                            // step 1 gate fails
		"go test ./...", "golangci-lint run ./...", // step 1 gate re-run passes
	}, v.calls())
	assert.Contains(t, progress.String(), "worker fixing (attempt 1/")

	// The fix ran as a resumed worker conversation, not a fresh one.
	workerCalls := mock.callsFor("worker", 1)
	require.Len(t, workerCalls, 2)
	assert.False(t, workerCalls[0].resumed)
	assert.True(t, workerCalls[1].resumed)
	assert.Contains(t, workerCalls[1].prompt, "failed the repo's verification gate")
	assert.Contains(t, workerCalls[1].prompt, "go test ./...")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	var roles []string
	for _, r := range state.Steps[0].Runs {
		roles = append(roles, r.Role)
	}
	assert.Contains(t, roles, "verify:test (a1)")
}

func TestVerifyGateEscalatesAfterBudget(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-verify-escalate", mock)
	v := &recordingVerifier{
		alwaysErr:  true,
		errOutputs: []string{"err 1", "err 2", "err 3", "err 4"},
	}
	o.verifyRun = v.run

	err := runWithPlan(t, o, verifyPlan)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrVerifyExhausted)
	assert.Contains(t, progress.String(), "ESCALATED")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseEscalated, state.Phase)
	// Budget is DefaultVerifyMaxRounds fix attempts, so the command is run
	// once up front plus once after each attempt, plus 2 commands at preflight.
	assert.Len(t, v.calls(), DefaultVerifyMaxRounds+3)
}

func TestVerifyGateNeverReachesReviewerWhenExhausted(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-verify-no-review", mock)
	o.verifyRun = (&recordingVerifier{alwaysErr: true}).run

	require.Error(t, runWithPlan(t, o, verifyPlan))

	assert.Empty(t, mock.callsFor("reviewer", 1),
		"a change that fails verification must not cost reviewer tokens")
}

func TestResumeAfterVerifyEscalationReVerifiesBeforeReview(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-verify-resume", mock)
	v := &recordingVerifier{alwaysErr: true}
	o.verifyRun = v.run

	require.ErrorIs(t, runWithPlan(t, o, verifyPlan), ErrVerifyExhausted)
	require.Empty(t, mock.callsFor("reviewer", 1))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	require.False(t, state.Steps[0].Verified, "escalated step must not be marked verified")

	// The user fixes the tree by hand and resumes. The gate must run again
	// before the reviewer is paid to look at the diff.
	before := len(v.calls())
	v.stopFailing()
	require.NoError(t, o.Resume(context.Background(), ResumeOpts{}))

	assert.Greater(t, len(v.calls()), before, "resume must re-run the verification gate")
	assert.Len(t, mock.callsFor("reviewer", 1), 1)

	state, err = LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	assert.True(t, state.Steps[0].Verified)
}

func TestStripANSI(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text unchanged",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "csi color escapes",
			input: "\x1b[31mred\x1b[0m and \x1b[1;32mbold green\x1b[0m",
			want:  "red and bold green",
		},
		{
			name:  "csi cursor and clear",
			input: "\x1b[2K\x1b[?25hcleared and show cursor",
			want:  "cleared and show cursor",
		},
		{
			name:  "osc title sequence",
			input: "\x1b]0;Terminal Title\x07real output",
			want:  "real output",
		},
		{
			name:  "osc hyperlink sequence",
			input: "\x1b]8;;https://example.com\x1b\\link text\x1b]8;;\x1b\\",
			want:  "link text",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, stripANSI(tc.input))
		})
	}
}

func TestClampVerifyOutputStripsANSI(t *testing.T) {
	t.Parallel()
	colored := "\x1b[31merror:\x1b[0m \x1b[1msomething broke\x1b[0m"
	clamped := clampVerifyOutput(colored)
	assert.Equal(t, "error: something broke", clamped)

	// Long colored output gets ANSI stripped before clamping head+tail
	head := strings.Repeat("A", 100)
	tail := strings.Repeat("Z", 100)
	longColored := "\x1b[31m" + head + "\x1b[0m" + strings.Repeat(
		"m",
		maxVerifyOutput*2,
	) + "\x1b[32m" + tail + "\x1b[0m"
	clampedLong := clampVerifyOutput(longColored)
	assert.NotContains(t, clampedLong, "\x1b[", "output must have all ANSI stripped")
	assert.True(t, strings.HasPrefix(clampedLong, head))
	assert.True(t, strings.HasSuffix(clampedLong, tail))
	assert.Contains(t, clampedLong, "characters elided")
}

func TestClassifyVerifyResult(t *testing.T) {
	t.Parallel()
	cmd := VerifyCommand{Name: "test", Command: "go test ./..."}

	// Normal test failure
	r1 := classifyVerifyResult(cmd, "FAIL: TestX", errors.New("exit status 1"), false, 15*time.Minute)
	assert.True(t, r1.Failed())
	assert.Equal(t, 1, r1.ExitCode)
	assert.False(t, r1.TimedOut)
	assert.False(t, r1.NotFound)
	assert.Contains(t, r1.Output, "exit code 1")
	assert.Contains(t, r1.Output, "FAIL: TestX")

	// Exit code 127
	r2 := classifyVerifyResult(cmd, "sh: foo: not found", errors.New("exit status 127"), false, 15*time.Minute)
	assert.True(t, r2.Failed())
	assert.Equal(t, 127, r2.ExitCode)
	assert.False(t, r2.TimedOut)
	assert.True(t, r2.NotFound)
	assert.Contains(t, r2.Output, "command not found")
	assert.Contains(t, r2.Output, "exit code 127")

	// First line matches "command not found" even with exit code 1
	r3 := classifyVerifyResult(
		cmd,
		"bash: golangci-lint: command not found",
		errors.New("exit status 1"),
		false,
		15*time.Minute,
	)
	assert.True(t, r3.Failed())
	assert.Equal(t, 1, r3.ExitCode)
	assert.True(t, r3.NotFound)

	// First line matches "No such file or directory"
	r4 := classifyVerifyResult(
		cmd,
		"sh: ./run.sh: No such file or directory\nsome detail",
		errors.New("exit status 1"),
		false,
		15*time.Minute,
	)
	assert.True(t, r4.Failed())
	assert.True(t, r4.NotFound)

	// Timed out command
	r5 := classifyVerifyResult(cmd, "running...", context.DeadlineExceeded, true, 45*time.Minute)
	assert.True(t, r5.Failed())
	assert.True(t, r5.TimedOut)
	assert.False(t, r5.NotFound)
	assert.Contains(t, r5.Output, "command killed after")
	assert.Contains(t, r5.Output, "45m")
	assert.Contains(t, r5.Output, "exit code")
}

func TestExecVerifyStdinDevNull(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	// cat will block indefinitely unless stdin is /dev/null
	start := time.Now()
	out, err := execVerify(context.Background(), tmp, "cat")
	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestExecVerifyEnv(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	out, err := execVerify(
		context.Background(),
		tmp,
		"echo CI=$CI NO_COLOR=$NO_COLOR TERM=$TERM GIT_TERMINAL_PROMPT=$GIT_TERMINAL_PROMPT PATH=$PATH",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "CI=1")
	assert.Contains(t, out, "NO_COLOR=1")
	assert.Contains(t, out, "TERM=dumb")
	assert.Contains(t, out, "GIT_TERMINAL_PROMPT=0")
	assert.Contains(t, out, "PATH=/")
}

func TestExecVerifyTimeoutProcessGroupKilled(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := execVerify(ctx, tmp, "sleep 10")
	require.Error(t, err)
	assert.Less(
		t,
		time.Since(start),
		3*time.Second,
		"command should terminate quickly when context deadline is exceeded",
	)
}

func TestExecVerifyProcessGroupKilledChildren(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cmd := fmt.Sprintf("sleep 60 & echo $! > %s; wait", pidFile)
	_, err := execVerify(ctx, tmp, cmd)
	require.Error(t, err)

	data, err := os.ReadFile(pidFile) //nolint:gosec // test helper reading temp file
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	err = syscall.Kill(pid, 0)
	assert.ErrorIs(t, err, syscall.ESRCH, "orphaned child should be killed with process group")
}

func TestFirstVerifyFailureClassificationAndTimeout(t *testing.T) {
	t.Parallel()

	// Normal test failure
	v1 := &recordingVerifier{failing: map[string]int{"T": 1}}
	o1 := &Orchestrator{verifyRun: v1.run}
	res1 := o1.firstVerifyFailure(context.Background(), []VerifyCommand{{Name: "test", Command: "T"}})
	require.NotNil(t, res1)
	assert.Equal(t, 1, res1.ExitCode)
	assert.False(t, res1.TimedOut)
	assert.False(t, res1.NotFound)
	assert.Contains(t, res1.Output, "exit code 1")

	// 127 command not found
	v2 := func(_ context.Context, _, _ string) (string, error) {
		return "sh: foo: command not found", errors.New("exit status 127")
	}
	o2 := &Orchestrator{verifyRun: v2}
	res2 := o2.firstVerifyFailure(context.Background(), []VerifyCommand{{Name: "lint", Command: "foo"}})
	require.NotNil(t, res2)
	assert.Equal(t, 127, res2.ExitCode)
	assert.False(t, res2.TimedOut)
	assert.True(t, res2.NotFound)

	// Timed out command
	v3 := func(ctx context.Context, _, _ string) (string, error) {
		<-ctx.Done()
		return "still going", ctx.Err()
	}
	o3 := &Orchestrator{
		verifyRun: v3,
		cfg: &Config{
			VerifyTimeout: "50ms",
		},
	}
	res3 := o3.firstVerifyFailure(context.Background(), []VerifyCommand{{Name: "test", Command: "slow"}})
	require.NotNil(t, res3)
	assert.True(t, res3.TimedOut)
	assert.Contains(t, res3.Output, "command killed after")
	assert.Contains(t, res3.Output, "50ms")
}

func TestVerifyPreflightAllPass(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-preflight-pass", mock)
	v := &recordingVerifier{}
	o.verifyRun = v.run

	state := &State{
		SessionName: "s-preflight-pass",
		Verification: Verification{Cmds: []VerifyCommand{
			{Name: "build", Command: "go build ./..."},
			{Name: "test", Command: "go test ./..."},
			{Name: "lint", Command: "golangci-lint run ./..."},
		}},
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	err := o.verifyPreflight(context.Background(), state)
	require.NoError(t, err)

	assert.Equal(t, []string{"go build ./...", "go test ./...", "golangci-lint run ./..."}, v.calls())
	assert.Contains(t, progress.String(), "verification baseline: build ok, test ok, lint ok")
	assert.Len(t, state.Verification.Commands(), 3)
}

func TestVerifyPreflightRunsAllCommandsEvenWhenFirstFails(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-preflight-all", mock)
	v := &recordingVerifier{
		failBaseline: true,
		failing:      map[string]int{"go build ./...": 1},
	}
	o.verifyRun = v.run
	fc := &fakeConfirmer{interactive: true, verifyAction: VerifyBaselineKeep}
	o.confirmer = fc

	state := &State{
		SessionName: "s-preflight-all",
		Verification: Verification{Cmds: []VerifyCommand{
			{Name: "build", Command: "go build ./..."},
			{Name: "test", Command: "go test ./..."},
			{Name: "lint", Command: "golangci-lint run ./..."},
		}},
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	err := o.verifyPreflight(context.Background(), state)
	require.NoError(t, err)

	// All 3 commands ran even though the first one failed.
	assert.Equal(t, []string{"go build ./...", "go test ./...", "golangci-lint run ./..."}, v.calls())
	require.Len(t, fc.verifyCalls, 1)
	assert.Equal(t, "build", fc.verifyCalls[0].name)
}

func TestVerifyPreflightDropFailingCommand(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-preflight-drop", mock)
	v := &recordingVerifier{
		failBaseline: true,
		failing:      map[string]int{"golangci-lint run ./...": 1},
	}
	o.verifyRun = v.run
	fc := &fakeConfirmer{interactive: true, verifyAction: VerifyBaselineDrop}
	o.confirmer = fc

	state := &State{
		SessionName: "s-preflight-drop",
		Verification: Verification{Cmds: []VerifyCommand{
			{Name: "test", Command: "go test ./..."},
			{Name: "lint", Command: "golangci-lint run ./..."},
		}},
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	err := o.verifyPreflight(context.Background(), state)
	require.NoError(t, err)

	assert.Equal(t, []VerifyCommand{{Name: "test", Command: "go test ./..."}}, state.Verification.Commands())
	assert.Contains(t, progress.String(), "verification baseline: test ok")

	// Verify state persisted to disk has dropped command
	loaded, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, []VerifyCommand{{Name: "test", Command: "go test ./..."}}, loaded.Verification.Commands())
}

func TestVerifyPreflightKeepFailingCommand(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-preflight-keep", mock)
	v := &recordingVerifier{
		failBaseline: true,
		failing:      map[string]int{"golangci-lint run ./...": 1},
	}
	o.verifyRun = v.run
	fc := &fakeConfirmer{interactive: true, verifyAction: VerifyBaselineKeep}
	o.confirmer = fc

	state := &State{
		SessionName: "s-preflight-keep",
		Verification: Verification{Cmds: []VerifyCommand{
			{Name: "test", Command: "go test ./..."},
			{Name: "lint", Command: "golangci-lint run ./..."},
		}},
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	err := o.verifyPreflight(context.Background(), state)
	require.NoError(t, err)

	// Both commands kept
	assert.Len(t, state.Verification.Commands(), 2)
	assert.Contains(t, progress.String(), "verification baseline: kept \"lint\" (workers will fix)")
	assert.Contains(
		t,
		progress.String(),
		"verification baseline: test ok, lint failed at baseline (kept — workers will fix)",
	)
	assert.NotContains(t, progress.String(), "lint ok")
}

func TestVerifyPreflightAbort(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-preflight-abort", mock)
	v := &recordingVerifier{
		failBaseline: true,
		failing:      map[string]int{"go test ./...": 1},
	}
	o.verifyRun = v.run
	fc := &fakeConfirmer{interactive: true, verifyAction: VerifyBaselineAbort}
	o.confirmer = fc

	state := &State{
		SessionName: "s-preflight-abort",
		Verification: Verification{Cmds: []VerifyCommand{
			{Name: "test", Command: "go test ./..."},
		}},
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	err := o.verifyPreflight(context.Background(), state)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrVerifyBaseline)
}

func TestVerifyPreflightNonInteractiveDropsAndWarns(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-preflight-noninteractive", mock)
	v := &recordingVerifier{
		failBaseline: true,
		failing:      map[string]int{"golangci-lint run ./...": 1},
	}
	o.verifyRun = v.run
	// Non-interactive confirmer
	o.confirmer = &fakeConfirmer{interactive: false}

	state := &State{
		SessionName: "s-preflight-noninteractive",
		Verification: Verification{Cmds: []VerifyCommand{
			{Name: "test", Command: "go test ./..."},
			{Name: "lint", Command: "golangci-lint run ./..."},
		}},
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	err := o.verifyPreflight(context.Background(), state)
	require.NoError(t, err)

	assert.Contains(t, progress.String(), "WARNING: dropping verification command \"lint\"")
	assert.Equal(t, []VerifyCommand{{Name: "test", Command: "go test ./..."}}, state.Verification.Commands())
}

func TestVerifyPreflightConfirmerWithoutVerifyPrompterDropsAndWarns(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-preflight-noprompter", mock)
	v := &recordingVerifier{
		failBaseline: true,
		failing:      map[string]int{"golangci-lint run ./...": 1},
	}
	o.verifyRun = v.run
	// Confirmer that does NOT implement VerifyPrompter
	o.confirmer = &plainConfirmer{interactive: true}

	state := &State{
		SessionName: "s-preflight-noprompter",
		Verification: Verification{Cmds: []VerifyCommand{
			{Name: "test", Command: "go test ./..."},
			{Name: "lint", Command: "golangci-lint run ./..."},
		}},
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	err := o.verifyPreflight(context.Background(), state)
	require.NoError(t, err)

	assert.Contains(t, progress.String(), "WARNING: dropping verification command \"lint\"")
	assert.Equal(t, []VerifyCommand{{Name: "test", Command: "go test ./..."}}, state.Verification.Commands())
}

func TestVerifyPreflightNotFoundReportedDistinctly(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-preflight-notfound", mock)
	// Runner returns 127 command not found
	o.verifyRun = func(_ context.Context, _, _ string) (string, error) {
		return "sh: golangci-lint: command not found", errors.New("exit status 127")
	}
	fc := &fakeConfirmer{interactive: true, verifyAction: VerifyBaselineDrop}
	o.confirmer = fc

	state := &State{
		SessionName: "s-preflight-notfound",
		Verification: Verification{Cmds: []VerifyCommand{
			{Name: "lint", Command: "golangci-lint run ./..."},
		}},
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	err := o.verifyPreflight(context.Background(), state)
	require.NoError(t, err)

	require.Len(t, fc.verifyCalls, 1)
	assert.True(t, fc.verifyCalls[0].notFound, "NotFound must be true for 127/command not found")
	assert.Equal(t, "lint", fc.verifyCalls[0].name)

	// Also test non-interactive warning message indicates not installed
	o.confirmer = &fakeConfirmer{interactive: false}
	state.Verification.Cmds = []VerifyCommand{{Name: "lint", Command: "golangci-lint run ./..."}}
	progress.Reset()
	require.NoError(t, o.verifyPreflight(context.Background(), state))
	assert.Contains(t, progress.String(), "golangci-lint is not installed or not on PATH")
}

func TestVerifyPreflightDropAllLeavesEmptyGateNoop(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-preflight-dropall", mock)
	v := &recordingVerifier{
		failBaseline: true,
		alwaysErr:    true,
	}
	o.verifyRun = v.run
	o.confirmer = &fakeConfirmer{interactive: false} // drops all

	state := &State{
		SessionName: "s-preflight-dropall",
		Verification: Verification{Cmds: []VerifyCommand{
			{Name: "test", Command: "go test ./..."},
			{Name: "lint", Command: "golangci-lint run ./..."},
		}},
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	err := o.verifyPreflight(context.Background(), state)
	require.NoError(t, err)
	assert.True(t, state.Verification.Empty())

	// verifyGate on empty verification is a no-op
	v.ran = nil
	step := &Step{Number: 1, Title: "first"}
	spec := RoleSpec{Harness: "claude", Model: "sonnet"}
	h, err := harnessFor(o.harnesses, spec)
	require.NoError(t, err)
	require.NoError(t, o.verifyGate(context.Background(), state, step, spec, h, 1))
	assert.Empty(t, v.calls(), "verifyGate must be no-op when all commands were dropped")
}

func TestVerifyPreflightSkipsWhenNoVerification(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-preflight-empty", mock)
	v := &recordingVerifier{}
	o.verifyRun = v.run

	state := &State{SessionName: "s-preflight-empty"}
	require.NoError(t, SaveState(o.SessionDir(), state))

	require.NoError(t, o.verifyPreflight(context.Background(), state))
	assert.Empty(t, v.calls(), "no commands should run when Verification is empty")
	assert.NotContains(t, progress.String(), "verification baseline:")
}

func TestVerifyPreflightAutofixMutatesTreeAndCommits(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-preflight-autofix", mock)
	fg := o.git.(*fakeGitter)
	fg.head = "base-head-123"

	// Simulate mutation on the first run of "lint --fix"
	var ranCommands []string
	v := &recordingVerifier{
		onRun: func(cmd string) {
			ranCommands = append(ranCommands, cmd)
			if cmd == "golangci-lint run --fix" && len(ranCommands) == 2 {
				// Mutate worktree on first lint run
				fg.worktreeDiff = "diff --git a/file.go b/file.go\n+fixed\n"
			}
		},
	}
	o.verifyRun = v.run

	state := &State{
		SessionName: "s-preflight-autofix",
		BaseCommit:  "base-head-123",
		Verification: Verification{Cmds: []VerifyCommand{
			{Name: "test", Command: "go test ./..."},
			{Name: "lint", Command: "golangci-lint run --fix"},
		}},
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	require.NoError(t, o.verifyPreflight(context.Background(), state))

	// Commit was created with expected message
	require.Len(t, fg.commits, 1)
	assert.Equal(t, "chore(implement): baseline autofix from verification pre-flight", fg.commits[0])
	// BaseCommit was advanced to the new commit hash
	assert.Equal(t, "c1", state.BaseCommit)
	assert.NotEqual(t, "base-head-123", state.BaseCommit)
	assert.Contains(t, progress.String(), "committed autofix")
	// The mutated command was re-run once after committing:
	// Initial sweep: test, lint.
	// After commit: lint re-run.
	assert.Equal(t, []string{"go test ./...", "golangci-lint run --fix", "golangci-lint run --fix"}, v.calls())

	// Persisted state has new BaseCommit
	loaded, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, "c1", loaded.BaseCommit)
}

func TestVerifyPreflightMutatesNothingCreatesNoCommit(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-preflight-nocmt", mock)
	fg := o.git.(*fakeGitter)
	fg.head = "initial-head"

	v := &recordingVerifier{}
	o.verifyRun = v.run

	state := &State{
		SessionName: "s-preflight-nocmt",
		BaseCommit:  "initial-head",
		Verification: Verification{Cmds: []VerifyCommand{
			{Name: "test", Command: "go test ./..."},
		}},
	}
	require.NoError(t, SaveState(o.SessionDir(), state))

	require.NoError(t, o.verifyPreflight(context.Background(), state))

	assert.Empty(t, fg.commits)
	assert.Equal(t, "initial-head", state.BaseCommit)
}

func TestVerifyGatePerCommandBudgetPasses(t *testing.T) {
	t.Parallel()
	const multiVerifyPlan = "# Plan: thing\n\n" +
		"## Context\n\ntest context\n\n" +
		"## Verification\n\n" +
		"- build: `go build ./...`\n" +
		"- test: `go test ./...`\n" +
		"- lint: `golangci-lint run ./...`\n\n" +
		"## Step 1: first\ndo A\n\n" +
		"- **Files**:\n  - a.go\n" +
		"- **Acceptance Criteria**:\n  - [ ] a done\n"

	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-verify-multi-pass", mock)
	// Each command fails once, then passes.
	v := &recordingVerifier{
		failing: map[string]int{
			"go build ./...":          1,
			"go test ./...":           1,
			"golangci-lint run ./...": 1,
		},
	}
	o.verifyRun = v.run

	require.NoError(t, runWithPlan(t, o, multiVerifyPlan))

	assert.Contains(t, progress.String(), "verification passed")
	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.True(t, state.Steps[0].Verified)

	var roles []string
	for _, r := range state.Steps[0].Runs {
		roles = append(roles, r.Role)
	}
	assert.Contains(t, roles, "verify:build (a1)")
	assert.Contains(t, roles, "verify:test (a1)")
	assert.Contains(t, roles, "verify:lint (a1)")
}

func TestVerifyGateSingleCommandBudgetExhausted(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-verify-single-cmd-budget", mock)
	v := &recordingVerifier{
		alwaysErr:  true,
		errOutputs: []string{"err 1", "err 2", "err 3", "err 4"},
	}
	o.verifyRun = v.run

	err := runWithPlan(t, o, verifyPlan)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrVerifyExhausted)
	assert.Contains(t, progress.String(), "ESCALATED")
	assert.Contains(t, err.Error(), "test")
	assert.Contains(t, err.Error(), "still failing after 2 fix attempt(s)")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseEscalated, state.Phase)
}

func TestVerifyGateTotalAttemptsCapExceeded(t *testing.T) {
	t.Parallel()
	const twoCmdPlan = "# Plan: thing\n\n" +
		"## Context\n\ntest context\n\n" +
		"## Verification\n\n" +
		"- build: `go build ./...`\n" +
		"- test: `go test ./...`\n\n" +
		"## Step 1: first\ndo A\n\n" +
		"- **Files**:\n  - a.go\n" +
		"- **Acceptance Criteria**:\n  - [ ] a done\n"

	mock := &mockRunner{}
	cfg := testConfig()
	cfg.VerifyMaxRounds = 5
	cfg.VerifyMaxTotalRounds = 3
	repo := t.TempDir()
	mock.sessionDir = filepath.Join(repo, ".agents", "local", "s-verify-total-cap")
	buf := &bytes.Buffer{}
	o, err := NewOrchestrator(cfg, "full", repo, "s-verify-total-cap", mock, zerolog.Nop(), buf)
	require.NoError(t, err)
	fg := &fakeGitter{head: "base-sha"}
	o.git = fg
	mock.git = fg
	o.confirmer = &fakeConfirmer{interactive: true, answer: true}
	o.openDiff = func(_, _ string) error { return nil }
	require.NoError(t, o.EnsureSessionDir())

	sweep := 0
	vRun := func(ctx context.Context, _, command string) (string, error) {
		if ctx.Value(preflightContextKey) != nil {
			return "ok", nil
		}
		if command == "go build ./..." {
			sweep++
			if sweep%2 == 1 {
				return fmt.Sprintf("build err %d", sweep), errors.New("exit status 1")
			}
			return "ok", nil
		}
		if command == "go test ./..." {
			if sweep%2 == 0 {
				return fmt.Sprintf("test err %d", sweep), errors.New("exit status 1")
			}
			return "ok", nil
		}
		return "ok", nil
	}
	o.verifyRun = vRun

	err = runWithPlan(t, o, twoCmdPlan)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrVerifyExhausted)
	assert.Contains(t, buf.String(), "ESCALATED")
	assert.Contains(t, err.Error(), "total fix attempts exceeded 3")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseEscalated, state.Phase)
}

func TestVerifyGateEscalatesOnWorkerNoChanges(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{
		noVerifyChanges: true, // worker edits nothing
	}
	o, progress := newTestOrchestrator(t, "full", "s-verify-no-changes", mock)
	v := &recordingVerifier{alwaysErr: true}
	o.verifyRun = v.run

	err := runWithPlan(t, o, verifyPlan)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrVerifyExhausted)
	assert.Contains(t, progress.String(), "ESCALATED")
	assert.Contains(t, progress.String(), "worker made no changes")
	assert.Contains(t, err.Error(), "worker made no changes")

	// Worker ran exactly 1 fix attempt before early escalation
	workerCalls := mock.callsFor("worker", 1)
	require.Len(t, workerCalls, 2) // initial work + 1 fix attempt
	assert.True(t, workerCalls[1].resumed)

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseEscalated, state.Phase)
	require.NotEmpty(t, state.Steps[0].Runs)
	assert.Equal(t, "verify:test (a1)", state.Steps[0].Runs[len(state.Steps[0].Runs)-1].Role)
}

func TestVerifyGateEscalatesOnIdenticalFailureOutput(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-verify-identical-out", mock)
	// Output is identical ("boom: it is broken") on both runs
	v := &recordingVerifier{alwaysErr: true}
	o.verifyRun = v.run

	err := runWithPlan(t, o, verifyPlan)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrVerifyExhausted)
	assert.Contains(t, progress.String(), "ESCALATED")
	assert.Contains(t, progress.String(), "identical failure output after fix attempt 1")
	assert.Contains(t, err.Error(), "identical failure output after fix attempt 1")

	// Stopped after 1 fix attempt without burning remaining budget
	workerCalls := mock.callsFor("worker", 1)
	require.Len(t, workerCalls, 2) // initial work + 1 fix attempt
}

func TestVerifyGateAutofixCommandNotMistakenForWorkerProgress(t *testing.T) {
	t.Parallel()
	const autofixPlan = "# Plan: thing\n\n" +
		"## Context\n\ntest context\n\n" +
		"## Verification\n\n" +
		"- lint: `golangci-lint run --fix`\n\n" +
		"## Step 1: first\ndo A\n\n" +
		"- **Files**:\n  - a.go\n" +
		"- **Acceptance Criteria**:\n  - [ ] a done\n"

	mock := &mockRunner{
		noVerifyChanges: true, // Worker edits nothing!
	}
	o, progress := newTestOrchestrator(t, "full", "s-verify-autofix-progress", mock)
	fg := o.git.(*fakeGitter)

	// Command mutates the tree during its verify run, but exits non-zero
	v := &recordingVerifier{
		onRun: func(_ string) {
			// verification command modifies tree
			fg.worktreeDiff = "diff --git a/pkg.go b/pkg.go\n+// autofix edit\n"
		},
		alwaysErr: true,
	}
	o.verifyRun = v.run

	err := runWithPlan(t, o, autofixPlan)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrVerifyExhausted)
	// Because digest was taken AFTER the verify run and before the worker run,
	// and worker made no changes, it must detect "worker made no changes"!
	assert.Contains(t, progress.String(), "worker made no changes")
	assert.Contains(t, err.Error(), "worker made no changes")
}

func TestVerifyGateAutofixRegeneratesDiffAndReRunsSequence(t *testing.T) {
	t.Parallel()
	const autofixPlan = "# Plan: thing\n\n" +
		"## Context\n\ntest context\n\n" +
		"## Verification\n\n" +
		"- lint: `golangci-lint run --fix`\n" +
		"- test: `go test ./...`\n\n" +
		"## Step 1: first\ndo A\n\n" +
		"- **Files**:\n  - a.go\n" +
		"- **Acceptance Criteria**:\n  - [ ] a done\n"

	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-verify-autofix-rerun", mock)
	fg := o.git.(*fakeGitter)

	autofixDone := false
	v := &recordingVerifier{
		onRun: func(cmd string) {
			// Don't mutate during preflight
			isPreflight := strings.Contains(cmd, "--fix") && !autofixDone && len(mock.callsFor("worker", 1)) == 0
			if isPreflight {
				return
			}
			if strings.Contains(cmd, "--fix") && !autofixDone {
				autofixDone = true
				fg.worktreeDiff = "diff --git a/pkg.go b/pkg.go\n--- a/pkg.go\n+++ b/pkg.go\n@@ -0,0 +1,2 @@\n+package pkg\n+// autofixed by linter\n"
			}
		},
	}
	o.verifyRun = v.run

	err := runWithPlan(t, o, autofixPlan)
	require.NoError(t, err)

	// Preflight (lint, test) + Pass 1 (lint --fix mutates, test) + Pass 2 (lint --fix no-op, test)
	assert.Equal(t, []string{
		"golangci-lint run --fix", "go test ./...", // preflight
		"golangci-lint run --fix", "go test ./...", // pass 1 (mutates)
		"golangci-lint run --fix", "go test ./...", // pass 2 (clean)
	}, v.calls())

	assert.Contains(t, progress.String(), "verification auto-fixed working tree")
	assert.Contains(t, progress.String(), "verification passed")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	require.NotEmpty(t, state.Steps)
	assert.True(t, state.Steps[0].Verified)

	// r1.diff must contain the autofixed content
	diffPath := filepath.Join(o.SessionDir(), "1-first", "r1.diff")
	diffBytes, err := os.ReadFile(diffPath) //nolint:gosec // test file read
	require.NoError(t, err)
	assert.Contains(t, string(diffBytes), "autofixed by linter")
}

func TestVerifyGateNonConvergingAutofixEscalatesAtMaxPasses(t *testing.T) {
	t.Parallel()
	const autofixPlan = "# Plan: thing\n\n" +
		"## Context\n\ntest context\n\n" +
		"## Verification\n\n" +
		"- lint: `golangci-lint run --fix`\n\n" +
		"## Step 1: first\ndo A\n\n" +
		"- **Files**:\n  - a.go\n" +
		"- **Acceptance Criteria**:\n  - [ ] a done\n"

	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-verify-non-converging", mock)
	fg := o.git.(*fakeGitter)

	mutationCount := 0
	v := &recordingVerifier{
		onRun: func(_ string) {
			// Don't mutate during preflight so baseline stays clean
			if len(mock.callsFor("worker", 1)) == 0 {
				return
			}
			mutationCount++
			fg.worktreeDiff = fmt.Sprintf(
				"diff --git a/pkg.go b/pkg.go\n--- a/pkg.go\n+++ b/pkg.go\n@@ -0,0 +1,2 @@\n+package pkg\n+// mutation %d\n",
				mutationCount,
			)
		},
	}
	o.verifyRun = v.run

	err := runWithPlan(t, o, autofixPlan)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrVerifyExhausted)
	assert.Contains(t, err.Error(), "non-converging auto-fixer")
	assert.Contains(t, err.Error(), "lint")
	assert.Contains(t, progress.String(), "ESCALATED")
	assert.Contains(t, progress.String(), "non-converging auto-fixer")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseEscalated, state.Phase)
	assert.False(t, state.Steps[0].Verified)

	// Step 1 gate ran exactly maxVerifyPasses (3) times without looping further.
	stepCalls := 0
	for _, c := range v.calls() {
		if c == "golangci-lint run --fix" {
			stepCalls++
		}
	}
	// 1 preflight call + 3 step gate calls = 4 calls total
	assert.Equal(t, 4, stepCalls)
}

func TestVerifyGateMutatingCommandFailsRoutesToFixWorker(t *testing.T) {
	t.Parallel()
	const autofixPlan = "# Plan: thing\n\n" +
		"## Context\n\ntest context\n\n" +
		"## Verification\n\n" +
		"- lint: `golangci-lint run --fix`\n\n" +
		"## Step 1: first\ndo A\n\n" +
		"- **Files**:\n  - a.go\n" +
		"- **Acceptance Criteria**:\n  - [ ] a done\n"

	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-verify-fail-and-mutate", mock)
	fg := o.git.(*fakeGitter)

	runs := 0
	v := &recordingVerifier{
		onRun: func(_ string) {
			runs++
			if runs == 1 {
				// baseline preflight: let it pass without mutation so baseline is clean
				return
			}
			if runs == 2 {
				// Step 1 gate, first run: mutates AND fails
				fg.worktreeDiff = "diff --git a/pkg.go b/pkg.go\n--- a/pkg.go\n+++ b/pkg.go\n@@ -0,0 +1,2 @@\n+package pkg\n+// broken edit\n"
			}
		},
		failing: map[string]int{"golangci-lint run --fix": 1},
	}
	o.verifyRun = v.run

	err := runWithPlan(t, o, autofixPlan)
	require.NoError(t, err)

	assert.Contains(t, progress.String(), "worker fixing (attempt 1/2)")
	assert.Contains(t, progress.String(), "verification passed")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.Equal(t, PhaseDone, state.Phase)
	assert.True(t, state.Steps[0].Verified)

	// Worker should have been called exactly once for verify fix
	workerCalls := mock.callsFor("worker", 1)
	require.Len(t, workerCalls, 2) // initial work + 1 fix attempt
	assert.True(t, workerCalls[1].resumed)
}

func TestVerifyGateNonMutatingRunsExactlyOnePass(t *testing.T) {
	t.Parallel()
	const plan = "# Plan: thing\n\n" +
		"## Context\n\ntest context\n\n" +
		"## Verification\n\n" +
		"- test: `go test ./...`\n" +
		"- lint: `golangci-lint run ./...`\n\n" +
		"## Step 1: first\ndo A\n\n" +
		"- **Files**:\n  - a.go\n" +
		"- **Acceptance Criteria**:\n  - [ ] a done\n"

	mock := &mockRunner{}
	o, progress := newTestOrchestrator(t, "full", "s-verify-non-mutating-single-pass", mock)
	v := &recordingVerifier{}
	o.verifyRun = v.run

	err := runWithPlan(t, o, plan)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"go test ./...", "golangci-lint run ./...", // preflight
		"go test ./...", "golangci-lint run ./...", // step 1 gate (exactly 1 pass)
	}, v.calls())
	assert.Contains(t, progress.String(), "verification passed")

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	assert.True(t, state.Steps[0].Verified)
}

func TestVerifyFixPromptMentionsAutofixingGate(t *testing.T) {
	t.Parallel()
	step := Step{Number: 1, Title: "Step 1"}
	cmd := VerifyCommand{Name: "lint", Command: "golangci-lint run --fix"}
	p := VerifyFixPrompt("/repo/.agents/local/s1", step, cmd, "lint error", 1, 2)
	assert.Contains(t, p, "auto-fixing")
}
