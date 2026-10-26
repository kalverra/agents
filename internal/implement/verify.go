package implement

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultVerifyTimeout bounds one plan-declared verification command.
const DefaultVerifyTimeout = 15 * time.Minute

// maxVerifyOutput caps how much of a failing command's output is fed back to
// the worker. Compilers put the real error first and test runners put the
// summary last, so both ends are kept and the middle is elided.
const maxVerifyOutput = 8000

var ansiRe = regexp.MustCompile(`(?:\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\))`)

// stripANSI drops CSI and OSC escape sequences from s.
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// VerifyResult is the outcome of running one verification command.
type VerifyResult struct {
	Command  VerifyCommand
	Output   string
	Err      error
	ExitCode int
	TimedOut bool
	NotFound bool
}

// Failed reports whether the command exited non-zero or could not be run.
func (r VerifyResult) Failed() bool { return r.Err != nil || r.ExitCode != 0 || r.TimedOut }

// VerifyRunner executes one shell command in dir and returns its combined
// output. A non-nil error means the command failed (non-zero exit, timeout,
// or could not start). It is a seam: tests substitute a fake.
type VerifyRunner func(ctx context.Context, dir, command string) (string, error)

// execVerify runs command through the shell in dir, respecting any deadline on
// ctx (or applying DefaultVerifyTimeout when none is present). Commands come from
// plan.md, which the user approves at the plan gate before any of this runs.
func execVerify(ctx context.Context, dir, command string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultVerifyTimeout)
		defer cancel()
	}

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return "", err
	}
	defer devNull.Close() //nolint:errcheck // read-only devNull close

	// The command text comes from plan.md, which the user reads and approves
	// at the plan gate before any step runs.
	//nolint:gosec // G204: planner-authored, human-approved command by design
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdin = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = append(os.Environ(), "CI=1", "NO_COLOR=1", "TERM=dumb", "GIT_TERMINAL_PROMPT=0")

	out, err := cmd.CombinedOutput()
	return string(out), err
}

// clampVerifyOutput trims s to maxVerifyOutput characters, keeping the head
// and the tail and marking what was dropped. ANSI escape sequences are stripped
// before clamping so color codes do not consume context budget.
func clampVerifyOutput(s string) string {
	s = strings.TrimSpace(stripANSI(s))
	if len(s) <= maxVerifyOutput {
		return s
	}
	half := maxVerifyOutput / 2
	head := strings.TrimSpace(s[:half])
	tail := strings.TrimSpace(s[len(s)-half:])
	dropped := len(s) - len(head) - len(tail)
	return head + "\n\n... [" + strconv.Itoa(dropped) + " characters elided] ...\n\n" + tail
}

func formatVerifyTimeout(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		return strings.TrimSuffix(s, "0s")
	}
	return s
}

func classifyVerifyResult(
	cmd VerifyCommand,
	rawOutput string,
	err error,
	timedOut bool,
	timeout time.Duration,
) *VerifyResult {
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			exitCode = exitErr.ExitCode()
		} else if after, ok := strings.CutPrefix(err.Error(), "exit status "); ok {
			if code, parseErr := strconv.Atoi(after); parseErr == nil {
				exitCode = code
			}
		}
	}

	cleaned := strings.TrimSpace(stripANSI(rawOutput))
	firstLine := ""
	if cleaned != "" {
		firstLine = strings.ToLower(strings.Split(cleaned, "\n")[0])
	}

	notFound := exitCode == 127 ||
		strings.Contains(firstLine, "command not found") ||
		strings.Contains(firstLine, "no such file or directory")

	var header string
	switch {
	case timedOut:
		header = fmt.Sprintf("command killed after %s (exit code %d)", formatVerifyTimeout(timeout), exitCode)
	case notFound:
		header = fmt.Sprintf("command not found (exit code %d)", exitCode)
	default:
		header = fmt.Sprintf("exit code %d", exitCode)
	}

	output := header
	if cleaned != "" {
		output = header + "\n\n" + cleaned
	}

	return &VerifyResult{
		Command:  cmd,
		Output:   output,
		Err:      err,
		ExitCode: exitCode,
		TimedOut: timedOut,
		NotFound: notFound,
	}
}

// ErrVerifyExhausted is returned when a plan-declared verification command
// still fails after the configured fix-attempt budget.
var ErrVerifyExhausted = errors.New("verification still failing after max fix attempts")

// ErrVerifyBaseline is returned when plan baseline verification pre-flight
// fails and is aborted by the user.
var ErrVerifyBaseline = errors.New("verification baseline aborted")

type verifyContextKey struct{}

var preflightContextKey = verifyContextKey{}

// firstVerifyFailure runs the declared commands in order and returns the
// first one that fails, or nil when they all pass.
func (o *Orchestrator) firstVerifyFailure(ctx context.Context, cmds []VerifyCommand) *VerifyResult {
	timeout := o.verifyTimeout()
	for _, c := range cmds {
		cmdCtx, cancel := context.WithTimeout(ctx, timeout)
		out, err := o.verifyRun(cmdCtx, o.repoDir, c.Command)
		timedOut := errors.Is(cmdCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded)
		cancel()
		if err != nil || timedOut {
			if err == nil {
				err = context.DeadlineExceeded
			}
			return classifyVerifyResult(c, out, err, timedOut, timeout)
		}
	}
	return nil
}

func (o *Orchestrator) escalateVerify(
	state *State,
	label string,
	cmd VerifyCommand,
	reason string,
) error {
	o.stopRenderer()
	cmdLabel := fmt.Sprintf("`%s`", cmd.Command)
	if cmd.Name != "" && cmd.Name != cmd.Command {
		cmdLabel = fmt.Sprintf("`%s` (%s)", cmd.Command, cmd.Name)
	}
	o.progressf(
		"%s: ESCALATED — %s: %s; resume with: %s\n",
		label, cmdLabel, reason, o.ResumeCommand(),
	)
	state.Phase = PhaseEscalated
	if err := SaveState(o.sessionDir, state); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s: %s: %s", ErrVerifyExhausted, label, cmdLabel, reason)
}

// maxVerifyPasses bounds fixpoint passes over the verification command sequence.
// A sequence still mutating at the cap indicates non-converging auto-fixers
// (e.g. competing formatters or non-idempotent code generators).
const maxVerifyPasses = 3

// runVerifyPass runs the verification commands in order, stopping at the first
// failure, while tracking which commands mutated the worktree.
func (o *Orchestrator) runVerifyPass(
	ctx context.Context,
	cmds []VerifyCommand,
) (*VerifyResult, []VerifyCommand, error) {
	timeout := o.verifyTimeout()
	var mutated []VerifyCommand

	for _, c := range cmds {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		dBefore, err := o.worktreeDigest()
		if err != nil {
			return nil, nil, fmt.Errorf("verify digest before %s: %w", c.Name, err)
		}

		cmdCtx, cancel := context.WithTimeout(ctx, timeout)
		out, runErr := o.verifyRun(cmdCtx, o.repoDir, c.Command)
		timedOut := errors.Is(cmdCtx.Err(), context.DeadlineExceeded) || errors.Is(runErr, context.DeadlineExceeded)
		cancel()
		if runErr != nil || timedOut {
			if runErr == nil {
				runErr = context.DeadlineExceeded
			}
			return classifyVerifyResult(c, out, runErr, timedOut, timeout), mutated, nil
		}

		dAfter, err := o.worktreeDigest()
		if err != nil {
			return nil, nil, fmt.Errorf("verify digest after %s: %w", c.Name, err)
		}
		if dAfter != dBefore {
			mutated = append(mutated, c)
		}
	}
	return nil, mutated, nil
}

func (o *Orchestrator) escalateVerifyAutofix(
	state *State,
	label string,
	mutated []VerifyCommand,
) error {
	names := make([]string, 0, len(mutated))
	cmdStrs := make([]string, 0, len(mutated))
	for _, c := range mutated {
		name := c.Name
		if name == "" {
			name = c.Command
		}
		names = append(names, name)
		cmdStrs = append(cmdStrs, c.Command)
	}

	var escCmd VerifyCommand
	if len(mutated) == 1 {
		escCmd = mutated[0]
	} else {
		escCmd = VerifyCommand{
			Name:    strings.Join(names, ", "),
			Command: strings.Join(cmdStrs, "; "),
		}
	}

	reason := fmt.Sprintf(
		"non-converging auto-fixer: working tree still mutating after %d passes (commands: %s)",
		maxVerifyPasses, strings.Join(names, ", "),
	)
	return o.escalateVerify(state, label, escCmd, reason)
}

// verifyTarget defines the target-specific parameters for runVerifyLoop.
type verifyTarget struct {
	label           string
	workSummaryPath string
	streamLog       func(attempt int) string
	resumeSessionID *string
	usage           **StepUsage
	postFix         func() error
	verified        *bool
	step            *Step
	recordRun       func(run RoleRun)
	liveUsage       func(StepUsage)
}

// finalWorkerVerifyLog derives stream log names for final review fix rounds.
func finalWorkerVerifyLog(attempt int) string {
	base := strings.TrimSuffix(FinalWorkerLog, ".jsonl")
	if attempt <= 1 {
		return base + "-verify.jsonl"
	}
	return fmt.Sprintf("%s-verify-r%d.jsonl", base, attempt)
}

func (o *Orchestrator) verifyHandlePass(
	state *State,
	t verifyTarget,
	mutated []VerifyCommand,
	pass int,
) (done bool, err error) {
	if len(mutated) == 0 {
		o.progressf("%s: verification passed\n", t.label)
		if t.verified != nil {
			*t.verified = true
		}
		return true, SaveState(o.sessionDir, state)
	}

	// Commands passed, but one or more commands mutated the tree.
	if pass >= maxVerifyPasses {
		return false, o.escalateVerifyAutofix(state, t.label, mutated)
	}

	// Regenerate diff if hook provided.
	if t.postFix != nil {
		if err := t.postFix(); err != nil {
			return false, fmt.Errorf("%s verify auto-fix diff: %w", t.label, err)
		}
	}
	if err := SaveState(o.sessionDir, state); err != nil {
		return false, err
	}

	names := make([]string, 0, len(mutated))
	for _, c := range mutated {
		n := c.Name
		if n == "" {
			n = c.Command
		}
		names = append(names, n)
	}
	o.progressf(
		"%s: verification auto-fixed working tree (%s) — re-running verification pass %d\n",
		t.label, strings.Join(names, ", "), pass+1,
	)
	return false, nil
}

func (o *Orchestrator) checkVerifyFailureBudgets(
	state *State,
	label string,
	failure *VerifyResult,
	cmdKey string,
	cmdAttempts, totalAttempts, maxAttemptsPerCmd, maxTotalAttempts int,
	lastFailureOutput map[string]string,
) error {
	// 1. Escalate early on byte-identical failure output for the same command after a fix attempt.
	if prevOut, ok := lastFailureOutput[cmdKey]; ok && prevOut == failure.Output {
		return o.escalateVerify(
			state, label, failure.Command,
			fmt.Sprintf("identical failure output after fix attempt %d", cmdAttempts),
		)
	}

	// 2. Escalate when single command budget exhausted.
	if cmdAttempts >= maxAttemptsPerCmd {
		return o.escalateVerify(
			state, label, failure.Command,
			fmt.Sprintf("still failing after %d fix attempt(s)", maxAttemptsPerCmd),
		)
	}

	// 3. Escalate when total attempts across all commands cap is reached.
	if totalAttempts >= maxTotalAttempts {
		return o.escalateVerify(
			state, label, failure.Command,
			fmt.Sprintf("total fix attempts exceeded %d", maxTotalAttempts),
		)
	}
	return nil
}

// runVerifyLoop is the verification loop shared by the per-step gate and
// the final whole-diff gate: run the command sequence to a stable
// fixpoint, resume the worker on failure, honor the budgets, escalate
// when exhausted.
func (o *Orchestrator) runVerifyLoop(
	ctx context.Context,
	state *State,
	spec RoleSpec,
	h Harness,
	t verifyTarget,
) error {
	cmds := state.Verification.Commands()
	if len(cmds) == 0 {
		return nil
	}
	maxAttemptsPerCmd := o.VerifyMaxRounds()
	maxTotalAttempts := o.VerifyMaxTotalRounds()
	if t.verified != nil {
		*t.verified = false
	}

	attempts := make(map[string]int)
	lastFailureOutput := make(map[string]string)
	totalAttempts := 0
	pass := 1

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		o.progressf("%s: verifying (%d command(s))\n", t.label, len(cmds))

		failure, mutated, err := o.runVerifyPass(ctx, cmds)
		if err != nil {
			return err
		}

		if failure == nil {
			done, passErr := o.verifyHandlePass(state, t, mutated, pass)
			if passErr != nil || done {
				return passErr
			}
			pass++
			continue
		}

		cmdKey := failure.Command.Name
		if cmdKey == "" {
			cmdKey = failure.Command.Command
		}
		cmdAttempts := attempts[cmdKey]

		o.log.Warn().
			Str("target", t.label).
			Str("command", failure.Command.Command).
			Str("name", cmdKey).
			Int("cmd_attempts", cmdAttempts).
			Int("total_attempts", totalAttempts).
			Err(failure.Err).
			Msg("verification command failed")

		if err := o.checkVerifyFailureBudgets(
			state, t.label, failure, cmdKey,
			cmdAttempts, totalAttempts, maxAttemptsPerCmd, maxTotalAttempts,
			lastFailureOutput,
		); err != nil {
			return err
		}

		// Record failure output to detect identical failure output on next run of this command.
		lastFailureOutput[cmdKey] = failure.Output

		cmdAttempts++
		attempts[cmdKey] = cmdAttempts
		totalAttempts++

		// Capture worktree digest AFTER the failed verification run and BEFORE the worker fix run.
		beforeDigest, err := o.worktreeDigest()
		if err != nil {
			return fmt.Errorf("%s verify before-fix digest: %w", t.label, err)
		}

		fixStart := time.Now()
		o.setView(state, "fixing", cmdAttempts, h.Name(), fixStart)
		o.progressf(
			"%s: `%s` failed — worker fixing (attempt %d/%d)\n",
			t.label, failure.Command.Command, cmdAttempts, maxAttemptsPerCmd,
		)

		var promptStep Step
		if t.step != nil {
			promptStep = *t.step
		} else {
			promptStep = Step{Title: t.label}
		}
		prompt := RenderPrompt("verify_fix", VerifyFixPromptData{
			Step:        promptStep,
			WorkPath:    t.workSummaryPath,
			Name:        failure.Command.Name,
			Command:     failure.Command.Command,
			Output:      clampVerifyOutput(failure.Output),
			Attempt:     cmdAttempts,
			MaxAttempts: maxAttemptsPerCmd,
		})

		logName := ""
		if t.streamLog != nil {
			logName = t.streamLog(totalAttempts)
		}
		var resumeID string
		if t.resumeSessionID != nil {
			resumeID = *t.resumeSessionID
		}
		sid, usage, err := o.runHeadless(
			ctx, spec, h, "worker", prompt,
			resumeID, logName,
			t.liveUsage,
		)
		if err != nil {
			return fmt.Errorf("worker verify-fix %s: %w", t.label, err)
		}
		if sid != "" && t.resumeSessionID != nil && *t.resumeSessionID == "" {
			*t.resumeSessionID = sid
		}
		if t.usage != nil {
			*t.usage = &usage
		}

		// Capture worktree digest AFTER worker returns.
		afterDigest, err := o.worktreeDigest()
		if err != nil {
			return fmt.Errorf("%s verify after-fix digest: %w", t.label, err)
		}

		fixFinished := time.Now()
		if t.recordRun != nil {
			t.recordRun(RoleRun{
				Role:       fmt.Sprintf("verify:%s (a%d)", cmdKey, cmdAttempts),
				Round:      cmdAttempts,
				Harness:    h.Name(),
				Model:      spec.Model,
				Effort:     spec.Effort,
				StartedAt:  &fixStart,
				FinishedAt: &fixFinished,
				Duration:   fixFinished.Sub(fixStart),
				Usage:      usage,
				Status:     "approved",
			})
		}

		// Escalate early without further fixes if the worker made no changes to the worktree.
		if afterDigest == beforeDigest {
			return o.escalateVerify(
				state, t.label, failure.Command,
				"worker made no changes",
			)
		}

		if t.postFix != nil {
			if err := t.postFix(); err != nil {
				return fmt.Errorf("%s verify-fix diff: %w", t.label, err)
			}
		}
		if err := SaveState(o.sessionDir, state); err != nil {
			return err
		}

		pass = 1
	}
}

// verifyGate runs the plan's verification commands against the working tree
// to a stable fixpoint and, on failure, resumes the worker to fix it — up to
// VerifyMaxRounds attempts per command, bounded by VerifyMaxTotalRounds, then
// escalates. If commands auto-fix files, the diff is regenerated and another
// pass runs (up to maxVerifyPasses). It also detects no-progress (unchanged
// worktree or identical failure output) and escalates early. It is a no-op
// when the plan declared no commands.
//
// diffRound is the step diff round to refresh after a fix or auto-fix, so the
// reviewer sees the post-verification diff rather than the one that failed or
// was unformatted.
func (o *Orchestrator) verifyGate(
	ctx context.Context,
	state *State,
	step *Step,
	spec RoleSpec,
	h Harness,
	diffRound int,
) error {
	t := verifyTarget{
		label:           fmt.Sprintf("step %d", step.Number),
		workSummaryPath: filepath.Join(o.sessionDir, step.WorkFile()),
		streamLog: func(attempt int) string {
			return step.StreamLogFile("worker-verify", attempt)
		},
		resumeSessionID: &step.WorkSessionID,
		usage:           &step.WorkerUsage,
		postFix: func() error {
			return o.writeStepDiff(step, diffRound)
		},
		verified:  &step.Verified,
		step:      step,
		liveUsage: o.liveUsage(state, "worker"),
		recordRun: func(run RoleRun) {
			run.DiffStat = step.DiffStat
			step.Runs = append(step.Runs, run)
		},
	}
	return o.runVerifyLoop(ctx, state, spec, h, t)
}

// finalVerifyGate runs verification after final review whole-diff fixes or
// on the approve path when final fix rounds occurred.
func (o *Orchestrator) finalVerifyGate(ctx context.Context, state *State) error {
	cmds := state.Verification.Commands()
	if len(cmds) == 0 {
		return nil
	}
	wh, err := harnessFor(o.harnesses, o.mode.Worker)
	if err != nil {
		return err
	}
	t := verifyTarget{
		label:           "final review",
		workSummaryPath: filepath.Join(o.sessionDir, FinalWorkFile),
		streamLog:       finalWorkerVerifyLog,
		resumeSessionID: &state.FinalWorkSessionID,
		usage:           &state.FinalWorkerUsage,
		verified:        &state.FinalVerified,
	}
	return o.runVerifyLoop(ctx, state, o.mode.Worker, wh, t)
}

// verifyPreflight runs each declared command once against the clean
// baseline tree. A command that cannot run (missing binary) or that
// already fails is offered to the user: drop it (degrading that check to
// worker-run), keep it anyway, or abort the session.
func (o *Orchestrator) verifyPreflight(ctx context.Context, state *State) error {
	cmds := state.Verification.Commands()
	if len(cmds) == 0 {
		return nil
	}

	initialDigest, err := o.worktreeDigest()
	if err != nil {
		return fmt.Errorf("baseline preflight digest: %w", err)
	}

	timeout := o.verifyTimeout()
	results, mutated, err := o.runPreflightSweep(ctx, timeout, cmds)
	if err != nil {
		return err
	}

	sweepDigest, err := o.worktreeDigest()
	if err != nil {
		return fmt.Errorf("baseline preflight sweep digest: %w", err)
	}

	if sweepDigest != initialDigest {
		if err := o.handleBaselineAutofix(ctx, state, cmds, timeout, mutated, results); err != nil {
			return err
		}
	}

	if err := o.handleBaselinePreflightFailures(state, cmds, results); err != nil {
		return err
	}

	surviving := state.Verification.Commands()
	if len(surviving) > 0 {
		parts := make([]string, 0, len(surviving))
		for _, c := range surviving {
			if res, ok := results[c.Name]; ok && res.Failed() {
				parts = append(parts, c.Name+" failed at baseline (kept — workers will fix)")
			} else {
				parts = append(parts, c.Name+" ok")
			}
		}
		o.progressf("verification baseline: %s\n", strings.Join(parts, ", "))
	}

	return nil
}

func (o *Orchestrator) runPreflightCommand(
	ctx context.Context,
	timeout time.Duration,
	c VerifyCommand,
) *VerifyResult {
	cmdCtx, cancel := context.WithTimeout(context.WithValue(ctx, preflightContextKey, true), timeout)
	out, runErr := o.verifyRun(cmdCtx, o.repoDir, c.Command)
	timedOut := errors.Is(cmdCtx.Err(), context.DeadlineExceeded) || errors.Is(runErr, context.DeadlineExceeded)
	cancel()
	if runErr == nil && timedOut {
		runErr = context.DeadlineExceeded
	}
	return classifyVerifyResult(c, out, runErr, timedOut, timeout)
}

func (o *Orchestrator) runPreflightSweep(
	ctx context.Context,
	timeout time.Duration,
	cmds []VerifyCommand,
) (map[string]*VerifyResult, map[string]bool, error) {
	results := make(map[string]*VerifyResult, len(cmds))
	mutated := make(map[string]bool)

	for _, c := range cmds {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		dBefore, dErr := o.worktreeDigest()
		if dErr != nil {
			return nil, nil, fmt.Errorf("baseline preflight digest before %s: %w", c.Name, dErr)
		}

		res := o.runPreflightCommand(ctx, timeout, c)
		results[c.Name] = res

		dAfter, dErr := o.worktreeDigest()
		if dErr != nil {
			return nil, nil, fmt.Errorf("baseline preflight digest after %s: %w", c.Name, dErr)
		}
		if dAfter != dBefore {
			mutated[c.Name] = true
		}
	}
	return results, mutated, nil
}

func (o *Orchestrator) handleBaselineAutofix(
	ctx context.Context,
	state *State,
	cmds []VerifyCommand,
	timeout time.Duration,
	mutated map[string]bool,
	results map[string]*VerifyResult,
) error {
	commitMsg := "chore(implement): baseline autofix from verification pre-flight"
	hash, cErr := o.commitBaselineAutofix(commitMsg)
	if cErr != nil {
		return fmt.Errorf("committing baseline autofix: %w", cErr)
	}
	if hash != "" {
		state.BaseCommit = hash
		if sErr := SaveState(o.sessionDir, state); sErr != nil {
			return sErr
		}
		o.progressf("verification baseline: committed autofix %s (%s)\n", shortHash(hash), commitMsg)
	}

	// Re-run mutated commands once after committing, so the recorded
	// baseline reflects the committed tree.
	for _, c := range cmds {
		if !mutated[c.Name] {
			continue
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		results[c.Name] = o.runPreflightCommand(ctx, timeout, c)
	}
	return nil
}

func (o *Orchestrator) handleBaselinePreflightFailures(
	state *State,
	cmds []VerifyCommand,
	results map[string]*VerifyResult,
) error {
	prompter, isPrompter := o.confirmer.(VerifyPrompter)
	isInteractive := isPrompter && o.confirmer.Interactive()

	for _, c := range cmds {
		res, ok := results[c.Name]
		if !ok || !res.Failed() {
			continue
		}

		if !isInteractive {
			reason := "command failed"
			if res.NotFound {
				bin := c.Command
				if fields := strings.Fields(bin); len(fields) > 0 {
					bin = fields[0]
				}
				reason = fmt.Sprintf("%s is not installed or not on PATH", bin)
			} else if res.TimedOut {
				reason = "command timed out"
			}
			o.progressf("WARNING: dropping verification command %q (%s): %s\n", c.Name, c.Command, reason)
			o.log.Warn().
				Str("name", c.Name).
				Str("command", c.Command).
				Str("reason", reason).
				Msg("dropping verification command at baseline pre-flight")

			o.dropVerificationCommand(state, c.Name)
			if sErr := SaveState(o.sessionDir, state); sErr != nil {
				return sErr
			}
			continue
		}

		action, pErr := prompter.PromptVerifyBaseline(c.Name, c.Command, res.Output, res.NotFound)
		if pErr != nil {
			return pErr
		}

		switch action {
		case VerifyBaselineDrop:
			o.progressf("verification baseline: dropped %q\n", c.Name)
			o.dropVerificationCommand(state, c.Name)
			if sErr := SaveState(o.sessionDir, state); sErr != nil {
				return sErr
			}
		case VerifyBaselineKeep:
			o.progressf("verification baseline: kept %q (workers will fix)\n", c.Name)
		case VerifyBaselineAbort:
			return ErrVerifyBaseline
		default:
			return fmt.Errorf("unexpected verification baseline action: %s", action)
		}
	}
	return nil
}

func (o *Orchestrator) dropVerificationCommand(state *State, name string) {
	filtered := make([]VerifyCommand, 0, len(state.Verification.Cmds))
	for _, c := range state.Verification.Cmds {
		if c.Name != name {
			filtered = append(filtered, c)
		}
	}
	state.Verification.Cmds = filtered
}

func (o *Orchestrator) commitBaselineAutofix(message string) (string, error) {
	hash, err := o.git.CommitAll(o.repoDir, message)
	if err == nil {
		return hash, nil
	}

	prompter, isPrompter := o.confirmer.(HookPrompter)
	if !isPrompter || o.confirmer == nil || !o.confirmer.Interactive() {
		o.progressf("\n%s\n", FormatCommitError("Baseline Autofix", err))
		return "", fmt.Errorf("committing baseline autofix: %w", err)
	}

	var hookOutput string
	if gitErr, ok := errors.AsType[*GitError](err); ok {
		hookOutput = gitErr.Output
	} else {
		hookOutput = err.Error()
	}

	action, pErr := prompter.PromptHookAction("Baseline Autofix", hookOutput)
	if pErr != nil {
		return "", fmt.Errorf("prompting hook action: %w", pErr)
	}

	switch action {
	case HookActionNoVerify:
		noVerifyHash, cErr := o.git.CommitAllOpts(o.repoDir, message, CommitOpts{NoVerify: true})
		if cErr != nil {
			return "", fmt.Errorf("committing baseline autofix with --no-verify: %w", cErr)
		}
		return noVerifyHash, nil
	case HookActionRetry:
		return o.commitBaselineAutofix(message)
	default:
		return "", fmt.Errorf("committing baseline autofix: %w", err)
	}
}
