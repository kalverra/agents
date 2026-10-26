package implement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Errors returned by the orchestrator.
var (
	// ErrNoSteps is returned when an imported plan/spec yields no steps.
	ErrNoSteps = errors.New("imported plan has no implementable steps")
	// ErrEscalated is returned when review fix rounds exceed the configured max.
	ErrEscalated = errors.New("review fix rounds exceeded max; user intervention needed")
	// ErrWorkSummaryMissing is returned when the worker does not write its summary.
	ErrWorkSummaryMissing = errors.New("worker did not write its step summary")
	// ErrReviewMissing is returned when the reviewer does not write its review.
	ErrReviewMissing = errors.New("reviewer did not write its review file")
	// ErrDirtyWorktree is returned when Run starts on a repo with
	// uncommitted changes: they would leak into the session's step commits.
	ErrDirtyWorktree = errors.New("worktree is dirty; commit or stash before starting an implement session")
	// ErrAwaitingHuman is returned when the final human review cannot prompt
	// (no TTY): review the diff, then resume the session.
	ErrAwaitingHuman = errors.New("final diff ready; review it, then resume the session")
	// ErrPlanDrift is returned when a session is resumed after its accepted
	// plan.md was edited: the session escalates instead of executing an
	// unreviewed plan.
	ErrPlanDrift = errors.New("plan.md changed after acceptance")
)

// Orchestrator drives the worker/reviewer pipeline for one session.
type Orchestrator struct {
	cfg        *Config
	modeName   string
	mode       ModeConfig
	repoDir    string
	sessionDir string
	runner     Runner
	harnesses  map[string]Harness
	log        zerolog.Logger
	out        io.Writer
	render     *Renderer
	// git performs all repo operations (commits, diffs, tree archival).
	git Gitter
	// confirmer gates the session on the final human decision.
	confirmer Confirmer
	// openDiff opens the human diff view over the archived base/head trees;
	// an error falls back to the written final.diff file.
	openDiff func(baseDir, headDir string) error
	// verifyRun executes one plan-declared verification command.
	verifyRun VerifyRunner
}

// NewOrchestrator builds an orchestrator. sessionDir is
// <repoDir>/.agents/local/<sessionName>. out receives human progress lines
// (pass io.Discard in AI-output mode).
func NewOrchestrator(
	cfg *Config,
	modeName string,
	repoDir, sessionName string,
	runner Runner,
	log zerolog.Logger,
	out io.Writer,
) (*Orchestrator, error) {
	mode, err := cfg.Mode(modeName)
	if err != nil {
		return nil, err
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Orchestrator{
		cfg:        cfg,
		modeName:   modeName,
		mode:       mode,
		repoDir:    repoDir,
		sessionDir: filepath.Join(repoDir, ".agents", "local", sessionName),
		runner:     runner,
		harnesses:  harnessRegistry(),
		log:        log,
		out:        out,
		git:        ExecGitter{},
		confirmer:  NewTerminalConfirmer(),
		openDiff:   zedDiffOpener,
		verifyRun:  execVerify,
	}, nil
}

// SessionDir returns the session state directory.
func (o *Orchestrator) SessionDir() string { return o.sessionDir }

// ResumeCommand returns the CLI command to resume this session.
func (o *Orchestrator) ResumeCommand() string {
	return ResumeCommand(o.modeName, filepath.Base(o.sessionDir))
}

// SetRenderer attaches a live progress renderer. Progress lines are routed
// through it; without one they go to the orchestrator's plain writer.
func (o *Orchestrator) SetRenderer(r *Renderer) { o.render = r }

// VerifyMaxRounds returns the configured or default verification fix-attempt
// budget.
func (o *Orchestrator) VerifyMaxRounds() int {
	if o.cfg == nil || o.cfg.VerifyMaxRounds == 0 {
		return DefaultVerifyMaxRounds
	}
	return o.cfg.VerifyMaxRounds
}

// VerifyMaxTotalRounds returns the configured or default total verification
// fix-attempt budget across all commands.
func (o *Orchestrator) VerifyMaxTotalRounds() int {
	if o.cfg == nil || o.cfg.VerifyMaxTotalRounds == 0 {
		return DefaultVerifyMaxTotalRounds
	}
	return o.cfg.VerifyMaxTotalRounds
}

// ReviewMaxRounds returns the configured or default review fix-round budget.
func (o *Orchestrator) ReviewMaxRounds() int {
	if o.cfg == nil || o.cfg.ReviewMaxRounds == 0 {
		return DefaultReviewMaxRounds
	}
	return o.cfg.ReviewMaxRounds
}

// AskMaxRounds returns the configured or default worker escalation question rounds budget.
func (o *Orchestrator) AskMaxRounds() int {
	if o.cfg == nil || o.cfg.AskMaxRounds == 0 {
		return DefaultAskMaxRounds
	}
	return o.cfg.AskMaxRounds
}

// verifyTimeout returns the configured timeout for one verification command,
// defaulting to DefaultVerifyTimeout (15m).
func (o *Orchestrator) verifyTimeout() time.Duration {
	if o != nil && o.cfg != nil && o.cfg.VerifyTimeout != "" {
		if d, err := time.ParseDuration(o.cfg.VerifyTimeout); err == nil && d > 0 {
			return d
		}
	}
	return DefaultVerifyTimeout
}

// worktreeDigest is a stable hash of the repo's uncommitted changes. The
// verification gate compares it across runs to tell an auto-fixing
// command's edits from a worker's, and to spot a worker that changed
// nothing between fix attempts.
func (o *Orchestrator) worktreeDigest() (string, error) {
	diff, err := o.git.WorktreeDiff(o.repoDir)
	if err != nil {
		return "", fmt.Errorf("hashing worktree: %w", err)
	}
	sum := sha256.Sum256([]byte(diff))
	return hex.EncodeToString(sum[:]), nil
}

// EnsureSessionDir creates the session dir plus a .gitignore in .agents/local
// so session artifacts are never tracked by the target repo.
func (o *Orchestrator) EnsureSessionDir() error {
	localDir := filepath.Dir(o.sessionDir)
	if err := os.MkdirAll(localDir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", localDir, err)
	}
	gitignore := filepath.Join(localDir, ".gitignore")
	if _, err := os.Stat(gitignore); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(gitignore, []byte("*\n"), 0o600); err != nil {
			return fmt.Errorf("writing .agents/local/.gitignore: %w", err)
		}
	}
	if err := os.MkdirAll(o.sessionDir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", o.sessionDir, err)
	}
	if err := os.MkdirAll(filepath.Join(o.sessionDir, LogsDir), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Join(o.sessionDir, LogsDir), err)
	}
	return nil
}

func (o *Orchestrator) progressf(format string, a ...any) {
	if o.render != nil {
		if o.render.Live() {
			return
		}
		o.render.PlainLine(format, a...)
		return
	}
	if o.out != nil {
		_, _ = fmt.Fprintf(o.out, format, a...)
	}
}

// setView pushes a fresh session snapshot to the renderer, if attached.
// Callers starting a reviewer/fix run pass the run's own start time so the
// active subtask counts its individual timing rather than the step's.
func (o *Orchestrator) setView(state *State, role string, round int, harness string, runStart ...time.Time) {
	if o.render == nil {
		return
	}
	var model, effort string
	switch role {
	case "working", "fixing":
		spec := o.mode.Worker
		for i := range state.Steps {
			if state.Steps[i].Number == state.CurrentStep {
				s, err := o.workerSpecFor(&state.Steps[i])
				if err != nil {
					o.log.Warn().Err(err).Int("step", state.Steps[i].Number).Msg("resolving worker spec for view")
				} else {
					spec = s
				}
				break
			}
		}
		model = spec.Model
		effort = spec.Effort
	case "reviewing":
		if o.mode.Reviewer != nil {
			spec := *o.mode.Reviewer
			for i := range state.Steps {
				if state.Steps[i].Number == state.CurrentStep {
					s, ok, err := o.reviewerSpecFor(&state.Steps[i])
					if err != nil {
						o.log.Warn().Err(err).Int("step", state.Steps[i].Number).Msg("resolving reviewer spec for view")
					} else if ok {
						spec = s
					}
					break
				}
			}
			model = spec.Model
			effort = spec.Effort
		}
	}
	v := buildView(state, role, round, harness, model, effort)
	if len(runStart) > 0 && !runStart[0].IsZero() {
		v.SubtaskStartedAt = runStart[0]
	}
	o.render.SetView(v)
}

// stopRenderer tears down the renderer (restores the cursor on TTYs).
func (o *Orchestrator) stopRenderer() {
	if o.render != nil {
		o.render.Stop()
	}
}

func buildStepView(s Step, currentStep int, role string, round int, harness, model, effort string) StepView {
	sv := StepView{
		Number:   s.Number,
		Title:    s.Title,
		Status:   s.Status,
		Rounds:   s.ReviewRounds,
		DiffStat: s.DiffStat,
	}
	if s.StartedAt != nil && s.FinishedAt != nil {
		sv.Elapsed = s.FinishedAt.Sub(*s.StartedAt)
		sv.HasElapsed = true
	}
	if u := combinedStepUsage(s); u.HasTokens || u.HasCost {
		sv.In = u.InputTokens
		sv.Out = u.OutputTokens
		sv.CostUSD = u.CostUSD
		sv.HasTokens = u.HasTokens
		sv.HasCost = u.HasCost
	}

	for _, run := range s.Runs {
		sub := SubtaskView{
			Role:      run.Role,
			Harness:   run.Harness,
			Model:     run.Model,
			Effort:    run.Effort,
			Status:    run.Status,
			DiffStat:  run.DiffStat,
			In:        run.Usage.InputTokens,
			Out:       run.Usage.OutputTokens,
			CostUSD:   run.Usage.CostUSD,
			HasTokens: run.Usage.HasTokens,
			HasCost:   run.Usage.HasCost,
		}
		if run.Duration > 0 {
			sub.Elapsed = run.Duration
			sub.HasElapsed = true
		} else if run.StartedAt != nil && run.FinishedAt != nil {
			sub.Elapsed = run.FinishedAt.Sub(*run.StartedAt)
			sub.HasElapsed = true
		}
		for _, act := range run.Activities {
			sub.Activities = append(sub.Activities, ActivityItem{
				Label:     act.Label,
				Elapsed:   act.Duration,
				In:        act.In,
				Out:       act.Out,
				HasTokens: act.In > 0 || act.Out > 0,
			})
		}
		sv.Subtasks = append(sv.Subtasks, sub)
	}

	if s.Number == currentStep &&
		(role == "working" || role == "reviewing" || role == "fixing" || role == "committing") {
		hasActive := false
		for j := range sv.Subtasks {
			if sv.Subtasks[j].Status == "pending" {
				sv.Subtasks[j].Active = true
				hasActive = true
				break
			}
		}
		if !hasActive {
			roleLabel := role
			switch role {
			case "working":
				roleLabel = "worker"
			case "reviewing":
				roleLabel = fmt.Sprintf("reviewer (r%d)", round)
			case "fixing":
				roleLabel = fmt.Sprintf("fix (r%d)", round)
			case "committing":
				roleLabel = "pre-commit hooks"
			}
			sv.Subtasks = append(sv.Subtasks, SubtaskView{
				Role:    roleLabel,
				Harness: harness,
				Model:   model,
				Effort:  effort,
				Status:  "pending",
				Active:  true,
			})
		}
	}
	return sv
}

// buildView maps persisted state plus live role info to a SessionView.
func buildView(state *State, role string, round int, harness string, modelEffort ...string) SessionView {
	var model, effort string
	if len(modelEffort) > 0 {
		model = modelEffort[0]
	}
	if len(modelEffort) > 1 {
		effort = modelEffort[1]
	}
	v := SessionView{
		Name:    state.SessionName,
		Current: state.CurrentStep,
		Role:    role,
		Round:   round,
		Harness: harness,
		Model:   model,
		Effort:  effort,
	}
	if state.StartedAt != nil {
		v.SessionStartedAt = *state.StartedAt
		if state.FinishedAt != nil {
			v.SessionElapsed = state.FinishedAt.Sub(*state.StartedAt)
			v.HasSessionElapsed = true
		}
	}
	for _, s := range state.Steps {
		if s.Number == state.CurrentStep && s.StartedAt != nil && s.FinishedAt == nil {
			v.CurrentStartedAt = *s.StartedAt
		}
		v.Steps = append(v.Steps, buildStepView(s, state.CurrentStep, role, round, harness, model, effort))
	}
	return v
}

// snapshotFile copies src's content to dst, creating dst's parent directory
// as needed. Used to archive a step's live work.md into its per-round
// "rN.work.md" snapshot. A missing src is not an error: the caller surfaces
// that separately (e.g. ErrWorkSummaryMissing).
func snapshotFile(src, dst string) error {
	data, err := os.ReadFile(src) //nolint:gosec // session dir is harness-managed
	if err != nil {
		return nil //nolint:nilerr // caller checks src's own existence separately
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil { //nolint:gosec // session dir is harness-managed
		return fmt.Errorf("creating %s: %w", filepath.Dir(dst), err)
	}
	return os.WriteFile(dst, data, 0o600) //nolint:gosec // session dir is harness-managed
}

// combinedStepUsage sums a step's worker and reviewer usage.
func combinedStepUsage(s Step) StepUsage {
	var out StepUsage
	if s.WorkerUsage != nil {
		out = out.Plus(*s.WorkerUsage)
	}
	if s.ReviewerUsage != nil {
		out = out.Plus(*s.ReviewerUsage)
	}
	return out
}

// RunOpts configures a fresh implement session.
type RunOpts struct {
	// Task overrides the session task title (otherwise derived from the
	// imported spec).
	Task string
	// PlanPath adopts an existing plan.md.
	PlanPath string
	// SpecPath imports an external spec dir (openspec change or spec-kit
	// specs/ folder).
	SpecPath string
}

// ResumeOpts configures a session resume.
type ResumeOpts struct {
	// AcceptPlanChanges re-accepts a plan.md edited after acceptance:
	// re-validates, re-runs the human gate, and reconciles step status.
	AcceptPlanChanges bool
}

// Run starts a fresh implement session for the given plan or spec source.
func (o *Orchestrator) Run(ctx context.Context, opts RunOpts) error {
	if err := o.checkBranch(); err != nil {
		return err
	}
	workerCtx, err := o.workerContext(ctx)
	if err != nil {
		return err
	}

	if err := o.EnsureSessionDir(); err != nil {
		return err
	}
	if err := o.clearStaleArtifacts(); err != nil {
		return err
	}
	// A dirty worktree would sweep unrelated changes into step commits.
	dirty, err := o.git.Dirty(o.repoDir)
	if err != nil {
		return fmt.Errorf("implement requires a git repository: %w", err)
	}
	if dirty {
		return ErrDirtyWorktree
	}
	base, err := o.git.Head(o.repoDir)
	if err != nil {
		return fmt.Errorf("implement requires a git repository: %w", err)
	}

	src, err := ResolveSpecSource(o.repoDir, opts.PlanPath, opts.SpecPath)
	if err != nil {
		return err
	}
	imported, err := ImportSpec(o.sessionDir, src)
	if err != nil {
		return fmt.Errorf("importing %s %s: %w", src.Kind, src.Path, err)
	}
	if len(imported.SkippedDone) > 0 {
		o.progressf("skipping %d already-completed task(s): %s\n",
			len(imported.SkippedDone), strings.Join(imported.SkippedDone, "; "))
	}

	raw := []byte(imported.PlanMD)
	steps, err := ParseAndValidatePlan(imported.PlanMD)
	if err != nil {
		return fmt.Errorf("imported plan is invalid: %w (edit the source spec, then re-run)", err)
	}
	if err := ValidateStepAgents(steps, o.mode); err != nil {
		return fmt.Errorf("imported plan is invalid: %w", err)
	}

	accepted, err := o.runPlanGate(raw, steps)
	if err != nil {
		return err
	}
	if !accepted {
		return fmt.Errorf(
			"plan rejected: edit %s (or the source spec), then re-run with --plan %s",
			filepath.Join(o.sessionDir, "plan.md"),
			filepath.Join(o.sessionDir, "plan.md"),
		)
	}

	task := opts.Task
	if task == "" {
		task = imported.Task
	}

	verification := ParseVerification(imported.PlanMD)
	if verification.Empty() && !o.cfg.Verification.Empty() {
		verification = o.cfg.Verification
	}

	now := time.Now()
	state := &State{
		SessionName:  filepath.Base(o.sessionDir),
		Mode:         o.modeName,
		Task:         task,
		Phase:        PhaseWork,
		Steps:        steps,
		PlanSource:   imported.Source,
		PlanHash:     hashBytes(raw),
		Verification: verification,
		CurrentStep:  steps[0].Number,
		BaseCommit:   base,
		StartedAt:    &now,
	}
	if err := SaveState(o.sessionDir, state); err != nil {
		return err
	}
	defer o.stopRenderer()
	o.setView(state, "", 0, "")
	o.progressf("plan imported from %s %s\n", src.Kind, src.Path)
	o.progressf("plan accepted: %d steps\n", len(steps))
	if cmds := verification.Commands(); len(cmds) > 0 {
		names := make([]string, 0, len(cmds))
		for _, c := range cmds {
			names = append(names, c.Name)
		}
		o.progressf("verification gate: %s (harness-run after each step)\n", strings.Join(names, ", "))
	} else {
		o.progressf(
			"verification gate: none declared (implement.yaml verification or spec ## Verification); workers self-verify\n",
		)
	}

	if err := o.verifyPreflight(ctx, state); err != nil {
		return err
	}
	if err := SaveState(o.sessionDir, state); err != nil {
		return err
	}
	if err := o.drive(ctx, state, workerCtx); err != nil {
		o.stopRenderer()
		if !errors.Is(err, ErrEscalated) && !errors.Is(err, ErrAwaitingHuman) {
			o.progressf("implement failed: %v\nresume with: %s\n", err, o.ResumeCommand())
		}
		return err
	}
	return nil
}

// clearStaleArtifacts removes leftover outputs from an aborted session so a
// fresh run starts clean.
func (o *Orchestrator) clearStaleArtifacts() error {
	for _, name := range []string{"plan.md", FinalReviewFile, FinalWorkFile, FinalDiffFile} {
		if err := os.Remove(filepath.Join(o.sessionDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clearing stale %s: %w", name, err)
		}
	}
	if err := os.RemoveAll(filepath.Join(o.sessionDir, DiffDir)); err != nil {
		return fmt.Errorf("clearing stale %s: %w", DiffDir, err)
	}
	if err := os.RemoveAll(filepath.Join(o.sessionDir, "spec")); err != nil {
		return fmt.Errorf("clearing stale spec snapshot: %w", err)
	}
	entries, err := os.ReadDir(o.sessionDir)
	if err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".diff") {
				_ = os.Remove(filepath.Join(o.sessionDir, e.Name()))
			}
		}
	}
	return nil
}

// Resume continues an existing session from its persisted state.
func (o *Orchestrator) Resume(ctx context.Context, opts ResumeOpts) error {
	if err := o.checkBranch(); err != nil {
		return err
	}
	workerCtx, err := o.workerContext(ctx)
	if err != nil {
		return err
	}
	state, err := LoadState(o.sessionDir)
	if err != nil {
		return fmt.Errorf("cannot resume %s: %w", o.sessionDir, err)
	}
	if state.StartedAt == nil {
		now := time.Now()
		state.StartedAt = &now
	}
	if err := o.checkPlanDrift(state, opts.AcceptPlanChanges); err != nil {
		return err
	}
	switch state.Phase {
	case PhaseDone:
		return fmt.Errorf("session %q is already complete", state.SessionName)
	case PhaseEscalated:
		// Allow re-entering the loop after manual intervention. If every
		// step is approved the escalation was in the final review; re-run
		// that. Otherwise re-review the step that escalated.
		if state.AllStepsApproved() {
			o.progressf("resuming escalated session; re-running final whole-diff review\n")
			state.Phase = PhaseFinalReview
			state.FinalVerified = false
		} else {
			o.progressf("resuming escalated session; re-reviewing step %d\n", state.CurrentStep)
			state.Phase = PhaseReview
		}
	}
	defer o.stopRenderer()
	if err := o.drive(ctx, state, workerCtx); err != nil {
		o.stopRenderer()
		if !errors.Is(err, ErrEscalated) && !errors.Is(err, ErrAwaitingHuman) {
			o.progressf("implement failed: %v\nresume with: %s\n", err, o.ResumeCommand())
		}
		return err
	}
	return nil
}

// checkPlanDrift detects edits to plan.md after acceptance. A changed plan
// escalates: executing an unreviewed plan would bypass the acceptance gate.
// With accept=true the changed plan is re-validated, re-gated, and its steps
// reconciled with the session's progress (number+title+body unchanged steps
// keep their approval; changed or new steps reset to pending; removed steps
// drop).
func (o *Orchestrator) checkPlanDrift(state *State, accept bool) error {
	planPath := filepath.Join(o.sessionDir, "plan.md")
	if state.PlanHash == "" {
		// Session predates drift tracking: nothing to compare against.
		return nil
	}
	raw, err := os.ReadFile(planPath) //nolint:gosec // session dir is harness-managed
	if err != nil {
		if !accept {
			o.stopRenderer()
			state.Phase = PhaseEscalated
			if saveErr := SaveState(o.sessionDir, state); saveErr != nil {
				return saveErr
			}
			o.progressf(
				"plan.md is missing after acceptance — restore it, or review the changes and resume with --accept-plan-changes: %s\n",
				o.ResumeCommand()+" --accept-plan-changes",
			)
			return fmt.Errorf(
				"%w: plan.md is missing (restore %s, or resume with --accept-plan-changes)",
				ErrPlanDrift, planPath,
			)
		}
		return fmt.Errorf("cannot re-accept: %w: plan.md is missing at %s", ErrPlanDrift, planPath)
	}
	current := hashBytes(raw)
	if state.PlanHash == current {
		return nil
	}

	if !accept {
		o.stopRenderer()
		state.Phase = PhaseEscalated
		if saveErr := SaveState(o.sessionDir, state); saveErr != nil {
			return saveErr
		}
		o.progressf(
			"plan.md changed after acceptance — restore it, or review the changes and resume with --accept-plan-changes: %s\n",
			o.ResumeCommand()+" --accept-plan-changes",
		)
		return fmt.Errorf(
			"%w: sha256 differs (restore %s, or resume with --accept-plan-changes)",
			ErrPlanDrift, planPath,
		)
	}

	steps, err := ParseAndValidatePlan(string(raw))
	if err != nil {
		return fmt.Errorf("re-accepting changed plan: %w (edit %s and resume again)", err, planPath)
	}
	if err := ValidateStepAgents(steps, o.mode); err != nil {
		return fmt.Errorf("re-accepting changed plan: %w", err)
	}
	accepted, err := o.runPlanGate(raw, steps)
	if err != nil {
		return err
	}
	if !accepted {
		return fmt.Errorf("changed plan rejected: edit %s and resume again", planPath)
	}

	state.Steps = ReconcileSteps(state.Steps, steps)
	state.PlanHash = current
	if verify := ParseVerification(string(raw)); !verify.Empty() {
		state.Verification = verify
	} else if !o.cfg.Verification.Empty() {
		state.Verification = o.cfg.Verification
	}
	if next := firstPendingStep(state.Steps); next != 0 {
		state.CurrentStep = next
	}
	o.progressf("changed plan re-accepted: %d steps\n", len(state.Steps))
	return SaveState(o.sessionDir, state)
}

// ReconcileSteps merges re-accepted plan steps into a session's progress:
// a step whose number, title, and body are unchanged keeps its runtime
// state; a changed step resets to pending; new steps are pending; removed
// steps drop.
func ReconcileSteps(oldSteps, newSteps []Step) []Step {
	byKey := make(map[string]Step, len(oldSteps))
	for _, s := range oldSteps {
		byKey[fmt.Sprintf("%d\x00%s", s.Number, s.Title)] = s
	}
	out := make([]Step, 0, len(newSteps))
	for _, s := range newSteps {
		if old, ok := byKey[fmt.Sprintf("%d\x00%s", s.Number, s.Title)]; ok && old.Body == s.Body {
			s = old
		} else {
			s.Status = StepPending
		}
		out = append(out, s)
	}
	return out
}

// firstPendingStep returns the number of the first non-approved step, or 0
// when all are approved (or there are none).
func firstPendingStep(steps []Step) int {
	for _, s := range steps {
		if s.Status != StepApproved {
			return s.Number
		}
	}
	return 0
}

// drive runs the state machine until done or escalated. workerCtx is the
// context_command output inlined into worker prompts.
func (o *Orchestrator) drive(ctx context.Context, state *State, workerCtx string) error {
	for state.Phase != PhaseDone && state.Phase != PhaseEscalated {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch state.Phase {
		case PhaseWork:
			if err := o.workPhase(ctx, state, workerCtx); err != nil {
				return err
			}
		case PhaseReview:
			if err := o.reviewPhase(ctx, state); err != nil {
				return err
			}
		case PhaseFinalReview:
			if err := o.finalReviewPhase(ctx, state); err != nil {
				return err
			}
		case PhaseFinalHuman:
			if err := o.finalHumanPhase(state); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown phase %q", state.Phase)
		}
	}
	if state.Phase == PhaseDone {
		if state.FinishedAt == nil {
			now := time.Now()
			state.FinishedAt = &now
			_ = SaveState(o.sessionDir, state)
		}
		o.archiveSpec(state)
		return o.writeSummary(state)
	}
	return ErrEscalated
}

// workerSpecFor resolves the worker for a step: the step's **Worker**
// selection when set, else the mode default, with the step's **Effort**
// override applied last.
func (o *Orchestrator) workerSpecFor(step *Step) (RoleSpec, error) {
	spec := o.mode.Worker
	if step != nil && step.Worker != "" {
		found := false
		for _, c := range o.mode.WorkerChoices {
			if c.Name == step.Worker {
				spec = c.Spec
				found = true
				break
			}
		}
		if !found {
			allowed := make([]string, 0, len(o.mode.WorkerChoices))
			for _, c := range o.mode.WorkerChoices {
				allowed = append(allowed, c.Name)
			}
			return RoleSpec{}, fmt.Errorf(
				"step %d %q: unknown **Worker**: %q (want one of: %s)",
				step.Number, step.Title, step.Worker, strings.Join(allowed, ", "),
			)
		}
	}
	if step != nil && step.Effort != "" {
		spec.Effort = step.Effort
	}
	return spec, nil
}

// reviewerSpecFor is the same for the reviewer. ok is false when the
// mode has no reviewer stage.
func (o *Orchestrator) reviewerSpecFor(step *Step) (spec RoleSpec, ok bool, err error) {
	if o.mode.Reviewer == nil {
		if step != nil && step.Reviewer != "" {
			return RoleSpec{}, false, fmt.Errorf(
				"step %d %q: cannot select **Reviewer**: %q (mode has no reviewer stage)",
				step.Number, step.Title, step.Reviewer,
			)
		}
		return RoleSpec{}, false, nil
	}
	if step != nil && step.Reviewer != "" {
		for _, c := range o.mode.ReviewerChoices {
			if c.Name == step.Reviewer {
				return c.Spec, true, nil
			}
		}
		allowed := make([]string, 0, len(o.mode.ReviewerChoices))
		for _, c := range o.mode.ReviewerChoices {
			allowed = append(allowed, c.Name)
		}
		return RoleSpec{}, false, fmt.Errorf(
			"step %d %q: unknown **Reviewer**: %q (want one of: %s)",
			step.Number, step.Title, step.Reviewer, strings.Join(allowed, ", "),
		)
	}
	return *o.mode.Reviewer, true, nil
}

func (o *Orchestrator) currentState(state *State) (*Step, error) {
	for i := range state.Steps {
		if state.Steps[i].Number == state.CurrentStep {
			return &state.Steps[i], nil
		}
	}
	return nil, fmt.Errorf("state references step %d but plan has steps up to %d", state.CurrentStep, len(state.Steps))
}

// runPlanGate prints the imported plan summary and asks the user to accept
// it before any step runs. Non-interactive contexts auto-accept.
func (o *Orchestrator) runPlanGate(raw []byte, steps []Step) (bool, error) {
	title := ParsePlanTitle(string(raw))
	o.progressf("%s", FormatPlanSummary(title, steps))

	if o.confirmer == nil || !o.confirmer.Interactive() {
		o.progressf("non-interactive: skipping plan acceptance gate\n")
		return true, nil
	}

	prompt := fmt.Sprintf("Start work on %d steps? (y/n)", len(steps))
	confirmed, err := o.confirmer.Confirm(prompt)
	if err != nil {
		return false, fmt.Errorf("plan confirmation: %w", err)
	}
	return confirmed, nil
}

func (o *Orchestrator) workPhase(ctx context.Context, state *State, workerCtx string) error {
	step, err := o.currentState(state)
	if err != nil {
		return err
	}
	spec, err := o.workerSpecFor(step)
	if err != nil {
		return err
	}
	h, err := harnessFor(o.harnesses, spec)
	if err != nil {
		return err
	}

	stepDir := filepath.Join(o.sessionDir, step.StepDir())
	_ = os.MkdirAll(stepDir, 0o750) //nolint:gosec // session dir is harness-managed

	var prev []string
	for _, s := range state.Steps {
		if s.Number < step.Number && s.Status == StepApproved {
			prev = append(prev, filepath.Join(o.sessionDir, s.WorkFile()))
		}
	}

	// Record (and persist) the step start before the worker runs so elapsed
	// time survives a crash mid-step.
	runStart := time.Now()
	if step.StartedAt == nil {
		step.StartedAt = &runStart
		if err := SaveState(o.sessionDir, state); err != nil {
			return err
		}
	}

	o.setView(state, "working", 0, h.Name())
	o.progressf(
		"step %d/%d (%s): working with %s (%s)\n",
		step.Number,
		len(state.Steps),
		step.Title,
		h.Name(),
		spec.Model,
	)
	prompt := WorkerPrompt(o.sessionDir, *step, prev, workerCtx, state.Verification)
	sid, runUsage, err := o.runHeadless(
		ctx, spec, h, "worker", prompt,
		step.WorkSessionID, step.StreamLogFile("worker"), o.liveUsage(state, "worker"),
	)
	if err != nil {
		return fmt.Errorf("worker step %d: %w", step.Number, err)
	}
	if sid != "" && step.WorkSessionID == "" {
		step.WorkSessionID = sid
	}
	if sid == "" && step.WorkSessionID == "" {
		o.log.Warn().
			Int("step", step.Number).
			Msg("no session ID captured from worker stream; fix-round resume will start a fresh session")
	}
	// Replace, not add: a resumed worker conversation reports cumulative
	// usage, so the latest run's report is the whole story.
	step.WorkerUsage = &runUsage

	workPath := filepath.Join(o.sessionDir, step.WorkFile())
	if _, err := os.Stat(workPath); err != nil {
		// Persist the captured session ID (and usage) before failing so a
		// --resume re-enters the worker conversation instead of redoing the
		// step from scratch.
		if saveErr := SaveState(o.sessionDir, state); saveErr != nil {
			return fmt.Errorf(
				"%w: %s (saving state: %v)",
				ErrWorkSummaryMissing,
				workPath,
				saveErr,
			)
		}
		if step.WorkSessionID != "" {
			o.progressf(
				"step %d: worker context saved; resume re-enters it, or attach: %s\n",
				step.Number, h.Attach(step.WorkSessionID),
			)
		}
		return fmt.Errorf("%w: %s", ErrWorkSummaryMissing, workPath)
	}

	workData, err := os.ReadFile(workPath) //nolint:gosec // session dir is harness-managed
	if err != nil {
		return fmt.Errorf("reading work summary: %w", err)
	}
	questions := ParseEscalations(string(workData))

	runFinished := time.Now()
	workerRun := RoleRun{
		Role:       "worker",
		Round:      1,
		Harness:    h.Name(),
		Model:      spec.Model,
		Effort:     spec.Effort,
		StartedAt:  &runStart,
		FinishedAt: &runFinished,
		Duration:   runFinished.Sub(runStart),
		Usage:      runUsage,
		Status:     "approved",
	}

	if len(questions) > 0 {
		workerRunIdx := len(step.Runs)
		step.Runs = append(step.Runs, workerRun)
		if err := o.askLoop(ctx, state, step, spec, h, questions); err != nil {
			return err
		}
		// writeStepDiff is run before verifyGate for crash-debugging,
		// but verifyGate's post-pass regeneration is the authoritative one.
		if err := o.writeStepDiff(step, 1); err != nil {
			return fmt.Errorf("step %d diff: %w", step.Number, err)
		}
		step.Runs[workerRunIdx].DiffStat = step.DiffStat
		if err := SaveState(o.sessionDir, state); err != nil {
			return err
		}
	} else {
		// writeStepDiff is run before verifyGate for crash-debugging,
		// but verifyGate's post-pass regeneration is the authoritative one.
		if err := o.writeStepDiff(step, 1); err != nil {
			return fmt.Errorf("step %d diff: %w", step.Number, err)
		}
		workerRun.DiffStat = step.DiffStat
		step.Runs = append(step.Runs, workerRun)
		if err := SaveState(o.sessionDir, state); err != nil {
			return err
		}
	}

	// Machine-checked gate before a model ever looks at the diff: the
	// reviewer's time is wasted on changes that do not build or pass.
	if err := o.verifyGate(ctx, state, step, spec, h, 1); err != nil {
		return err
	}

	state.Phase = PhaseReview
	return SaveState(o.sessionDir, state)
}

func (o *Orchestrator) askLoop(
	ctx context.Context,
	state *State,
	step *Step,
	spec RoleSpec,
	h Harness,
	questions []string,
) error {
	asker, hasAsker := o.confirmer.(Asker)
	if !hasAsker || !o.confirmer.Interactive() {
		now := time.Now()
		for _, q := range questions {
			step.Escalations = append(step.Escalations, Escalation{
				Question: q,
				Answer:   "",
				AskedAt:  &now,
			})
		}
		o.stopRenderer()
		o.progressf("step %d: ESCALATED — worker asked %d question(s):\n", step.Number, len(questions))
		for _, q := range questions {
			o.progressf("  - %s\n", q)
		}
		o.progressf("resume with: %s\n", o.ResumeCommand())
		state.Phase = PhaseEscalated
		if err := SaveState(o.sessionDir, state); err != nil {
			return err
		}
		return fmt.Errorf(
			"step %d worker escalated with %d question(s): %s",
			step.Number,
			len(questions),
			strings.Join(questions, "; "),
		)
	}

	maxAskRounds := o.AskMaxRounds()
	askRound := 0
	workPath := filepath.Join(o.sessionDir, step.WorkFile())

	for len(questions) > 0 {
		askRound++
		if askRound > maxAskRounds {
			o.stopRenderer()
			o.progressf(
				"step %d: ESCALATED — exceeded max ask rounds (%d); resume with: %s\n",
				step.Number, maxAskRounds, o.ResumeCommand(),
			)
			state.Phase = PhaseEscalated
			if err := SaveState(o.sessionDir, state); err != nil {
				return err
			}
			return fmt.Errorf("step %d exceeded max ask rounds (%d)", step.Number, maxAskRounds)
		}

		o.stopRenderer()
		for _, q := range questions {
			o.progressf("step %d: worker asked: %s\n", step.Number, q)
			ans, err := asker.Ask(q)
			if err != nil {
				return fmt.Errorf("asking question: %w", err)
			}
			if strings.TrimSpace(ans) == "" {
				ans = "no preference — use your judgement"
			}
			now := time.Now()
			step.Escalations = append(step.Escalations, Escalation{
				Question: q,
				Answer:   ans,
				AskedAt:  &now,
			})
		}
		if err := SaveState(o.sessionDir, state); err != nil {
			return err
		}

		askStart := time.Now()
		o.setView(state, "working", askRound, h.Name())
		o.progressf("step %d: resuming worker with answer(s) (round %d/%d)\n", step.Number, askRound, maxAskRounds)

		answerPrompt := EscalationAnswerPrompt(o.sessionDir, *step, step.Escalations)
		sid, askUsage, err := o.runHeadless(
			ctx, spec, h, fmt.Sprintf("ask-%d", askRound), answerPrompt,
			step.WorkSessionID, step.StreamLogFile("worker-ask", askRound), o.liveUsage(state, "worker"),
		)
		if err != nil {
			return fmt.Errorf("worker ask resume step %d: %w", step.Number, err)
		}
		if sid != "" && step.WorkSessionID == "" {
			step.WorkSessionID = sid
		}
		step.WorkerUsage = &askUsage

		askFinished := time.Now()
		step.Runs = append(step.Runs, RoleRun{
			Role:       fmt.Sprintf("ask (a%d)", askRound),
			Round:      askRound,
			Harness:    h.Name(),
			Model:      spec.Model,
			Effort:     spec.Effort,
			StartedAt:  &askStart,
			FinishedAt: &askFinished,
			Duration:   askFinished.Sub(askStart),
			Usage:      askUsage,
			Status:     "approved",
		})
		if err := SaveState(o.sessionDir, state); err != nil {
			return err
		}

		workData, err := os.ReadFile(workPath) //nolint:gosec // session dir is harness-managed
		if err != nil {
			return fmt.Errorf("reading work summary after ask round %d: %w", askRound, err)
		}
		questions = ParseEscalations(string(workData))
	}
	return nil
}

// writeStepDiff generates the filtered unified diff for a step, writes it to
// its round diff file (e.g. "1-slug/r1.diff"), updates the worker summary to
// point to it (and archives a round snapshot of the summary), and sets
// step.DiffStat.
func (o *Orchestrator) writeStepDiff(step *Step, round ...int) error {
	r := 1
	if len(round) > 0 && round[0] > 0 {
		r = round[0]
	}
	rawDiff, err := o.git.WorktreeDiff(o.repoDir)
	if err != nil {
		return fmt.Errorf("generating step diff: %w", err)
	}

	repoAttr, _ := LoadRepoGitattributes(o.repoDir)

	chunks := SplitUnifiedDiff(rawDiff)
	var paths []string
	for _, chunk := range chunks {
		if chunk.Path != "" {
			paths = append(paths, chunk.Path)
		}
	}

	var gitAttrs map[string]map[string]string
	if len(paths) > 0 {
		gitAttrs, _ = o.git.CheckAttr(
			o.repoDir,
			[]string{"diff", "linguist-generated", "gitlab-generated", "generated"},
			paths,
		)
	}

	filtered := FilterUnifiedDiff(rawDiff, func(p string) bool {
		if IsLockfile(p) {
			return true
		}
		if repoAttr != nil && repoAttr.IsGeneratedOrNoDiff(p) {
			return true
		}
		if attrs, ok := gitAttrs[cleanGitPath(p)]; ok {
			if attrs["diff"] == "unset" {
				return true
			}
			for _, key := range []string{"linguist-generated", "gitlab-generated", "generated"} {
				val := strings.ToLower(attrs[key])
				if val == "set" || val == "true" || val == "yes" || val == "1" {
					return true
				}
			}
		}
		return false
	})

	roundDiffPath := filepath.Join(o.sessionDir, step.DiffFile(r))

	if err := os.MkdirAll(
		filepath.Dir(roundDiffPath),
		0o750,
	); err != nil { //nolint:gosec // session dir is harness-managed
		return fmt.Errorf("creating %s: %w", filepath.Dir(roundDiffPath), err)
	}
	//nolint:gosec // session dir is harness-managed
	if err := os.WriteFile(roundDiffPath, []byte(filtered), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", roundDiffPath, err)
	}

	stat := ParseDiffStat(filtered)
	step.DiffStat = &stat

	workPath := filepath.Join(o.sessionDir, step.WorkFile())
	roundWorkPath := filepath.Join(o.sessionDir, step.RoundWorkFile(r))

	if rawSummary, err := os.ReadFile(workPath); err == nil { //nolint:gosec // session dir is harness-managed
		updatedSummary := InjectDiffReference(string(rawSummary), roundDiffPath)
		//nolint:gosec // session dir is harness-managed
		if err := os.WriteFile(workPath, []byte(updatedSummary), 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", workPath, err)
		}
	}
	return snapshotFile(workPath, roundWorkPath)
}

func (o *Orchestrator) reviewPhase(ctx context.Context, state *State) error {
	step, err := o.currentState(state)
	if err != nil {
		return err
	}

	if o.mode.Reviewer == nil {
		o.progressf("step %d: no reviewer configured, auto-approving\n", step.Number)
		return o.advance(state, step)
	}

	// A session resumed after a verification escalation re-enters here with
	// an unverified tree; re-run the gate rather than review changes that
	// may still not build.
	if !step.Verified {
		wSpec, wErr := o.workerSpecFor(step)
		if wErr != nil {
			return wErr
		}
		wh, wErr := harnessFor(o.harnesses, wSpec)
		if wErr != nil {
			return wErr
		}
		if err := o.verifyGate(ctx, state, step, wSpec, wh, step.ReviewRounds+1); err != nil {
			return err
		}
	}

	revSpec, ok, err := o.reviewerSpecFor(step)
	if err != nil {
		return err
	}
	if !ok {
		o.progressf("step %d: no reviewer configured, auto-approving\n", step.Number)
		return o.advance(state, step)
	}

	h, err := harnessFor(o.harnesses, revSpec)
	if err != nil {
		return err
	}

	r := step.ReviewRounds + 1
	revStart := time.Now()
	o.setView(state, "reviewing", r, h.Name(), revStart)
	o.progressf(
		"step %d: reviewing with %s (%s), round %d\n",
		step.Number,
		h.Name(),
		revSpec.Model,
		r,
	)
	prompt := ReviewerPrompt(o.sessionDir, *step, r)
	_, revUsage, err := o.runHeadless(
		ctx, revSpec, h, "reviewer", prompt,
		"", step.StreamLogFile("reviewer", r), o.liveUsage(state, "reviewer"),
	)
	if err != nil {
		return fmt.Errorf("reviewer step %d: %w", step.Number, err)
	}
	revFinished := time.Now()
	// Reviewer rounds are fresh conversations: usage adds across rounds.
	if step.ReviewerUsage == nil {
		step.ReviewerUsage = &revUsage
	} else {
		step.ReviewerUsage.Add(revUsage)
	}

	reviewPath := filepath.Join(o.sessionDir, step.ReviewFile(r))
	raw, err := os.ReadFile(reviewPath) //nolint:gosec // session dir is harness-managed
	if err != nil {
		return fmt.Errorf("%w: %s", ErrReviewMissing, reviewPath)
	}
	verdict, err := ParseVerdict(string(raw))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrReviewMissing, err)
	}

	switch verdict {
	case VerdictApprove:
		step.Runs = append(step.Runs, RoleRun{
			Role:       fmt.Sprintf("reviewer (r%d)", r),
			Round:      r,
			Harness:    h.Name(),
			Model:      revSpec.Model,
			Effort:     revSpec.Effort,
			StartedAt:  &revStart,
			FinishedAt: &revFinished,
			Duration:   revFinished.Sub(revStart),
			Usage:      revUsage,
			Status:     "approved",
		})
		o.progressf("step %d: APPROVED\n", step.Number)
		return o.advance(state, step)

	case VerdictRequestChanges:
		step.Runs = append(step.Runs, RoleRun{
			Role:       fmt.Sprintf("reviewer (r%d)", r),
			Round:      r,
			Harness:    h.Name(),
			Model:      revSpec.Model,
			Effort:     revSpec.Effort,
			StartedAt:  &revStart,
			FinishedAt: &revFinished,
			Duration:   revFinished.Sub(revStart),
			Usage:      revUsage,
			Status:     "rejected",
		})
		step.ReviewRounds++
		if step.ReviewRounds > o.cfg.ReviewMaxRounds {
			state.Phase = PhaseEscalated
			if saveErr := SaveState(o.sessionDir, state); saveErr != nil {
				return saveErr
			}
			o.stopRenderer()
			o.progressf("step %d: ESCALATED after %d fix rounds — review at %s; resume with: %s\n",
				step.Number, o.cfg.ReviewMaxRounds, reviewPath, o.ResumeCommand())
			return ErrEscalated
		}

		fixSpec, err := o.workerSpecFor(step)
		if err != nil {
			return err
		}
		wh, err := harnessFor(o.harnesses, fixSpec)
		if err != nil {
			return err
		}
		fixStart := time.Now()
		o.setView(state, "fixing", step.ReviewRounds, wh.Name(), fixStart)
		o.progressf(
			"step %d: REQUEST_CHANGES — worker fixing (round %d/%d)\n",
			step.Number,
			step.ReviewRounds,
			o.cfg.ReviewMaxRounds,
		)
		fixPrompt := FixPrompt(o.sessionDir, *step, r)
		_, fixUsage, err := o.runHeadless(
			ctx, fixSpec, wh, "worker", fixPrompt,
			step.WorkSessionID, step.StreamLogFile("worker", r+1), o.liveUsage(state, "worker"),
		)
		if err != nil {
			return fmt.Errorf("worker fix step %d: %w", step.Number, err)
		}
		fixFinished := time.Now()
		// Replace: the resumed conversation's result usage is cumulative.
		step.WorkerUsage = &fixUsage

		fixWorkPath := filepath.Join(o.sessionDir, step.WorkFile())
		if _, err := os.Stat(fixWorkPath); err != nil {
			// Persist before failing so --resume re-enters the fix conversation.
			if saveErr := SaveState(o.sessionDir, state); saveErr != nil {
				return fmt.Errorf("%w after fix round (saving state: %v)", ErrWorkSummaryMissing, saveErr)
			}
			return fmt.Errorf("%w after fix round", ErrWorkSummaryMissing)
		}
		if err := o.writeStepDiff(step, r+1); err != nil {
			return fmt.Errorf("step %d fix diff: %w", step.Number, err)
		}
		if err := o.verifyGate(ctx, state, step, fixSpec, wh, r+1); err != nil {
			return err
		}

		step.Runs = append(step.Runs, RoleRun{
			Role:       fmt.Sprintf("fix (r%d)", r),
			Round:      r,
			Harness:    wh.Name(),
			Model:      fixSpec.Model,
			Effort:     fixSpec.Effort,
			StartedAt:  &fixStart,
			FinishedAt: &fixFinished,
			Duration:   fixFinished.Sub(fixStart),
			Usage:      fixUsage,
			Status:     "approved",
			DiffStat:   step.DiffStat,
		})

		// Stay in review phase; loop re-reviews.
		return SaveState(o.sessionDir, state)
	}
	return fmt.Errorf("unknown verdict %q", verdict)
}

func (o *Orchestrator) advance(state *State, step *Step) error {
	step.Status = StepApproved
	if step.StartedAt != nil && step.FinishedAt == nil {
		now := time.Now()
		step.FinishedAt = &now
	}
	if err := o.commitStep(state, step); err != nil {
		return err
	}
	if step.Status != StepApproved {
		return nil
	}
	next := 0
	for _, s := range state.Steps {
		if s.Status != StepApproved && (next == 0 || s.Number < next) {
			next = s.Number
		}
	}
	if next == 0 {
		// All steps approved: the final whole-diff review is next.
		state.Phase = PhaseFinalReview
		state.CurrentStep = 0
	} else {
		state.Phase = PhaseWork
		state.CurrentStep = next
	}
	return SaveState(o.sessionDir, state)
}

// commitStep commits one approved step's changes using the worker's
// proposed message (the COMMIT: line in the work summary), falling back to a
// generated message. A step that produced nothing to commit is tolerated
// with a warning.
func (o *Orchestrator) commitStep(state *State, step *Step) error {
	if len(state.Verification.Commands()) > 0 && !step.Verified {
		return fmt.Errorf("step %d: cannot commit unverified changes", step.Number)
	}
	message := fmt.Sprintf("implement(%s): step %d: %s", state.SessionName, step.Number, step.Title)
	workPath := filepath.Join(o.sessionDir, step.WorkFile())
	if raw, err := os.ReadFile(workPath); err == nil { //nolint:gosec // session dir is harness-managed
		if proposed, pErr := ParseCommitMessage(string(raw)); pErr == nil {
			message = proposed
		} else {
			o.progressf("step %d: no valid COMMIT line in work summary; using generated message\n", step.Number)
		}
	}
	o.setView(state, "committing", 0, "")
	hash, err := o.git.CommitAll(o.repoDir, message)
	if err != nil {
		return o.handleCommitError(state, step, message, err)
	}
	if hash == "" {
		if step.CommitHash == "" {
			o.progressf("step %d: approved with nothing to commit\n", step.Number)
		}
		return nil
	}
	step.CommitHash = hash
	o.progressf("step %d: committed %s (%s)\n", step.Number, shortHash(hash), message)
	return nil
}

func (o *Orchestrator) handleCommitError(state *State, step *Step, message string, commitErr error) error {
	var hookOutput string
	if gitErr, ok := errors.AsType[*GitError](commitErr); ok {
		hookOutput = gitErr.Output
	} else {
		hookOutput = commitErr.Error()
	}

	prompter, isPrompter := o.confirmer.(HookPrompter)
	if !isPrompter || o.confirmer == nil || !o.confirmer.Interactive() {
		o.stopRenderer()
		o.progressf("\n%s\n", FormatCommitError(step.Title, commitErr))
		return fmt.Errorf("committing step %d: %w", step.Number, commitErr)
	}

	o.stopRenderer()
	action, err := prompter.PromptHookAction(step.Title, hookOutput)
	if err != nil {
		return fmt.Errorf("prompting hook action: %w", err)
	}

	switch action {
	case HookActionNoVerify:
		hash, cErr := o.git.CommitAllOpts(o.repoDir, message, CommitOpts{NoVerify: true})
		if cErr != nil {
			return fmt.Errorf("committing step %d with --no-verify: %w", step.Number, cErr)
		}
		if hash != "" {
			step.CommitHash = hash
			o.progressf("step %d: committed %s (%s) [--no-verify]\n", step.Number, shortHash(hash), message)
		}
		return nil

	case HookActionRetry:
		hash, cErr := o.git.CommitAll(o.repoDir, message)
		if cErr != nil {
			return o.handleCommitError(state, step, message, cErr)
		}
		if hash != "" {
			step.CommitHash = hash
			o.progressf("step %d: committed %s (%s)\n", step.Number, shortHash(hash), message)
		}
		return nil

	case HookActionAgentFix:
		step.Status = StepPending
		step.FinishedAt = nil
		reviewPath := filepath.Join(o.sessionDir, step.ReviewFile(step.ReviewRounds+1))
		reviewContent := fmt.Sprintf(
			"# Pre-Commit Hook Failure\n\nPre-commit hooks failed with output:\n```\n%s\n```\n\nVERDICT: REQUEST_CHANGES\n",
			strings.TrimSpace(hookOutput),
		)
		//nolint:gosec // session dir is harness-managed
		if wErr := os.WriteFile(
			reviewPath,
			[]byte(reviewContent),
			0o600,
		); wErr != nil {
			return fmt.Errorf("writing hook fix review file: %w", wErr)
		}
		state.Phase = PhaseReview
		if sErr := SaveState(o.sessionDir, state); sErr != nil {
			return sErr
		}
		o.progressf("step %d: pre-commit hook failure routed to worker agent for fix\n", step.Number)
		return nil

	case HookActionEscalate:
		fallthrough
	default:
		state.Phase = PhaseEscalated
		if sErr := SaveState(o.sessionDir, state); sErr != nil {
			return sErr
		}
		o.stopRenderer()
		o.progressf("step %d: pre-commit failed; session escalated; resume with: %s\n", step.Number, o.ResumeCommand())
		return ErrEscalated
	}
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// finalReviewPhase runs the whole-diff agent review after all steps are
// approved, then either commits final fixes and moves to the human gate or
// runs bounded fix rounds.
func (o *Orchestrator) finalReviewPhase(ctx context.Context, state *State) error {
	if o.mode.Reviewer == nil {
		o.progressf("no reviewer configured; skipping agent final review\n")
		state.Phase = PhaseFinalHuman
		return SaveState(o.sessionDir, state)
	}
	if state.BaseCommit == "" {
		return errors.New("state has no base commit (session predates final-review support); start a new session")
	}
	h, err := harnessFor(o.harnesses, *o.mode.Reviewer)
	if err != nil {
		return err
	}

	revStart := time.Now()
	o.setView(state, "reviewing", state.FinalReviewRounds+1, h.Name(), revStart)
	o.progressf(
		"final review: whole-diff review with %s (%s), round %d\n",
		h.Name(),
		o.mode.Reviewer.Model,
		state.FinalReviewRounds+1,
	)
	prompt := FinalReviewerPrompt(o.sessionDir, state.Task, state.BaseCommit)
	_, revUsage, err := o.runHeadless(
		ctx, *o.mode.Reviewer, h, "reviewer", prompt, "", FinalReviewerLog, nil,
	)
	if err != nil {
		return fmt.Errorf("final reviewer: %w", err)
	}
	// Final reviewer rounds are fresh conversations: usage adds across rounds.
	if state.FinalReviewerUsage == nil {
		state.FinalReviewerUsage = &revUsage
	} else {
		state.FinalReviewerUsage.Add(revUsage)
	}

	reviewPath := filepath.Join(o.sessionDir, FinalReviewFile)
	raw, err := os.ReadFile(reviewPath) //nolint:gosec // session dir is harness-managed
	if err != nil {
		return fmt.Errorf("%w: %s", ErrReviewMissing, reviewPath)
	}
	verdict, err := ParseVerdict(string(raw))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrReviewMissing, err)
	}

	switch verdict {
	case VerdictApprove:
		o.progressf("final review: APPROVED\n")
		if state.FinalReviewRounds > 0 && !state.FinalVerified {
			if err := o.finalVerifyGate(ctx, state); err != nil {
				return err
			}
		}
		if err := o.finalCommit(state); err != nil {
			return err
		}
		state.Phase = PhaseFinalHuman
		return SaveState(o.sessionDir, state)

	case VerdictRequestChanges:
		state.FinalReviewRounds++
		state.FinalVerified = false
		if state.FinalReviewRounds > o.cfg.ReviewMaxRounds {
			state.Phase = PhaseEscalated
			if saveErr := SaveState(o.sessionDir, state); saveErr != nil {
				return saveErr
			}
			o.stopRenderer()
			o.progressf(
				"final review: ESCALATED after %d fix rounds — review at %s; resume with: %s\n",
				o.cfg.ReviewMaxRounds, reviewPath, o.ResumeCommand(),
			)
			return ErrEscalated
		}

		wh, err := harnessFor(o.harnesses, o.mode.Worker)
		if err != nil {
			return err
		}
		fixStart := time.Now()
		o.setView(state, "fixing", state.FinalReviewRounds, wh.Name(), fixStart)
		o.progressf(
			"final review: REQUEST_CHANGES — worker fixing (round %d/%d)\n",
			state.FinalReviewRounds,
			o.cfg.ReviewMaxRounds,
		)
		fixPrompt := FinalFixPrompt(o.sessionDir, state.FinalReviewRounds)
		sid, fixUsage, err := o.runHeadless(
			ctx, o.mode.Worker, wh, "worker", fixPrompt,
			state.FinalWorkSessionID, FinalWorkerLog, nil,
		)
		if err != nil {
			return fmt.Errorf("final fix round: %w", err)
		}
		if sid != "" && state.FinalWorkSessionID == "" {
			state.FinalWorkSessionID = sid
		}
		// Replace: the resumed conversation's result usage is cumulative.
		state.FinalWorkerUsage = &fixUsage
		if _, err := os.Stat(filepath.Join(o.sessionDir, FinalWorkFile)); err != nil {
			// Persist before failing so --resume re-enters the fix conversation.
			if saveErr := SaveState(o.sessionDir, state); saveErr != nil {
				return fmt.Errorf("%w after final fix round (saving state: %v)", ErrWorkSummaryMissing, saveErr)
			}
			return fmt.Errorf("%w after final fix round", ErrWorkSummaryMissing)
		}
		if err := o.finalVerifyGate(ctx, state); err != nil {
			return err
		}
		// Stay in final review; loop re-reviews.
		return SaveState(o.sessionDir, state)
	}
	return fmt.Errorf("unknown verdict %q", verdict)
}

// finalCommit commits final-fix changes once the whole-diff review approves,
// using the fix summary's proposed message. No fix round means no commit.
func (o *Orchestrator) finalCommit(state *State) error {
	if len(state.Verification.Commands()) > 0 && state.FinalReviewRounds > 0 && !state.FinalVerified {
		return errors.New("cannot commit unverified final fixes")
	}
	finalWorkPath := filepath.Join(o.sessionDir, FinalWorkFile)
	raw, err := os.ReadFile(finalWorkPath) //nolint:gosec // session dir is harness-managed
	if err != nil {
		return nil
	}
	message := "fix(implement): final review findings"
	if proposed, pErr := ParseCommitMessage(string(raw)); pErr == nil {
		message = proposed
	}
	o.setView(state, "committing", 0, "")
	hash, err := o.git.CommitAll(o.repoDir, message)
	if err != nil {
		prompter, isPrompter := o.confirmer.(HookPrompter)
		if isPrompter && o.confirmer != nil && o.confirmer.Interactive() {
			action, pErr := prompter.PromptHookAction("Final Review Fixes", err.Error())
			if pErr == nil && action == HookActionNoVerify {
				noVerifyHash, cErr := o.git.CommitAllOpts(o.repoDir, message, CommitOpts{NoVerify: true})
				if cErr == nil && noVerifyHash != "" {
					state.FinalCommitHash = noVerifyHash
					o.progressf("final review: committed %s (%s) [--no-verify]\n", shortHash(noVerifyHash), message)
					return nil
				}
			}
		}
		o.progressf("\n%s\n", FormatCommitError("Final Review Fixes", err))
		return fmt.Errorf("committing final fixes: %w", err)
	}
	if hash == "" {
		o.progressf("final review: fixes already committed; nothing to commit\n")
		return nil
	}
	state.FinalCommitHash = hash
	o.progressf("final review: committed %s (%s)\n", shortHash(hash), message)
	return nil
}

// finalHumanPhase presents the whole diff to the human and gates completion
// on their approval. Non-interactive contexts park the session
// (ErrAwaitingHuman) for a later --resume.
func (o *Orchestrator) finalHumanPhase(state *State) error {
	if state.BaseCommit == "" {
		return errors.New("state has no base commit (session predates final-review support); start a new session")
	}
	// The human owns the terminal from here.
	o.stopRenderer()

	// Archive base and head trees so the diff view compares exactly the
	// tracked files, ignoring local build output and session artifacts.
	diffDir := filepath.Join(o.sessionDir, DiffDir)
	if err := os.RemoveAll(diffDir); err != nil {
		return fmt.Errorf("clearing %s: %w", diffDir, err)
	}
	baseDir := filepath.Join(diffDir, "base")
	headDir := filepath.Join(diffDir, "head")
	if err := o.git.ArchiveTree(o.repoDir, state.BaseCommit, baseDir); err != nil {
		return fmt.Errorf("archiving base tree: %w", err)
	}
	head, err := o.git.Head(o.repoDir)
	if err != nil {
		return fmt.Errorf("reading HEAD: %w", err)
	}
	if err := o.git.ArchiveTree(o.repoDir, head, headDir); err != nil {
		return fmt.Errorf("archiving head tree: %w", err)
	}

	// final.diff is always written: a portable record of the whole change
	// set and the fallback view when no diff tool opens.
	diff, err := o.git.Diff(o.repoDir, state.BaseCommit, head)
	if err != nil {
		return fmt.Errorf("generating final diff: %w", err)
	}
	diffPath := filepath.Join(o.sessionDir, FinalDiffFile)
	if err := os.WriteFile(diffPath, []byte(diff), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", diffPath, err)
	}

	if o.openDiff != nil {
		if err := o.openDiff(baseDir, headDir); err != nil {
			o.progressf("diff view unavailable (%v); using %s\n", err, diffPath)
		}
	} else {
		o.progressf("final diff: %s\n", diffPath)
	}

	if o.confirmer == nil || !o.confirmer.Interactive() {
		o.progressf(
			"final review awaiting human: inspect %s (or the %s trees); resume with: %s\n",
			diffPath,
			diffDir,
			o.ResumeCommand(),
		)
		return ErrAwaitingHuman
	}
	approved, err := o.confirmer.Confirm("Final human review: approve the complete change set? [y/n]:")
	if err != nil {
		return fmt.Errorf("human review: %w", err)
	}
	if !approved {
		state.Phase = PhaseEscalated
		if saveErr := SaveState(o.sessionDir, state); saveErr != nil {
			return saveErr
		}
		o.progressf("final review rejected: edit the code or leave notes; resume with: %s\n", o.ResumeCommand())
		return ErrEscalated
	}
	state.Phase = PhaseDone
	state.CurrentStep = 0
	if err := SaveState(o.sessionDir, state); err != nil {
		return err
	}
	o.progressf("final review: human APPROVED\n")
	if state.BaseCommit != "" {
		o.progressf("\nto GPG-sign session commits:\n  %s\n", SignCommitsCommand(state.BaseCommit))
	}
	return nil
}

// runHeadless executes one headless agent turn, teeing stream events to a
// raw jsonl log plus a human-readable markdown digest, and returning the
// captured session/conversation ID plus the run's accumulated usage.
func (o *Orchestrator) runHeadless(
	ctx context.Context,
	spec RoleSpec,
	h Harness,
	role, prompt, sessionID, logName string,
	onUsage func(StepUsage),
) (string, StepUsage, error) {
	logPath := filepath.Join(o.sessionDir, LogsDir, logName)
	//nolint:gosec // session dir is harness-managed
	logFile, err := os.OpenFile(
		logPath,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
	if err != nil {
		return "", StepUsage{}, fmt.Errorf("opening stream log %s: %w", logPath, err)
	}
	defer logFile.Close() //nolint:errcheck // best-effort log close

	digestPath := filepath.Join(o.sessionDir, LogsDir, strings.TrimSuffix(logName, ".jsonl")+".md")
	//nolint:gosec // session dir is harness-managed
	digestFile, err := os.OpenFile(
		digestPath,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
	if err != nil {
		return "", StepUsage{}, fmt.Errorf("opening digest log %s: %w", digestPath, err)
	}
	defer digestFile.Close() //nolint:errcheck // best-effort log close
	//nolint:errcheck // best-effort digest header: run metadata + the exact
	// prompt the agent received (the stream never echoes it back).
	fmt.Fprintf(
		digestFile,
		"\n# %s run %s (%s)\n\n## prompt\n\n%s\n",
		role,
		time.Now().Format(time.RFC3339),
		spec.Model,
		prompt,
	)

	captured := sessionID
	var runUsage StepUsage
	var cumulativeUsage bool
	onLine := func(line []byte) {
		if _, wErr := logFile.Write(append(bytesClone(line), '\n')); wErr != nil {
			o.log.Warn().Err(wErr).Msg("writing stream log")
		}
		if frag := EventDigest(line); frag != "" {
			if _, wErr := digestFile.WriteString(frag); wErr != nil {
				o.log.Warn().Err(wErr).Msg("writing digest log")
			}
		}
		if sid := h.SessionIDFromEvent(line); sid != "" && captured == "" {
			captured = sid
		}
		if txt := h.TextFromEvent(line); txt != "" {
			o.log.Debug().Str("role", role).Str("text", truncate(txt, 200)).Msg("agent text")
		}
		if o.render != nil {
			if label := h.ActivityFromEvent(line); label != "" {
				o.render.Activity(label)
			}
		}
		if u, ok := h.CumulativeUsageFromEvent(line); ok {
			// The final event reports cumulative totals for the whole run;
			// replace the running per-turn sum, which double-counts the
			// shared context.
			runUsage = u.ToStepUsage()
			cumulativeUsage = true
			if onUsage != nil {
				onUsage(runUsage)
			}
			return
		}
		if cumulativeUsage {
			// Per-turn events after the cumulative report are not additive.
			return
		}
		if u, ok := h.UsageFromEvent(line); ok {
			runUsage.Add(u.ToStepUsage())
			if onUsage != nil {
				onUsage(runUsage)
			}
		}
	}

	args := h.RunArgs(spec, prompt, sessionID)
	runErr := o.runner.RunStream(ctx, h.Name(), args, o.repoDir, onLine)

	// agy never reports a dollar cost on its own (see pricing.go); fill in
	// an estimate from its token counts when we have a rate for the model.
	if h.Name() == HarnessAgy && !runUsage.HasCost && runUsage.HasTokens {
		if cost, ok := EstimateAgyCost(
			spec.Model,
			runUsage.InputTokens,
			runUsage.CacheRead,
			runUsage.OutputTokens,
		); ok {
			runUsage.CostUSD = cost
			runUsage.HasCost = true
		}
	}

	return captured, runUsage, runErr
}

// liveUsage returns a callback pushing live usage for the current step to
// the renderer. Worker runs report conversation-cumulative usage (resumed
// fix rounds), so the live run supersedes the persisted worker total;
// reviewer rounds are separate conversations and stack on top.
func (o *Orchestrator) liveUsage(state *State, role string) func(StepUsage) {
	if o.render == nil {
		return nil
	}
	return func(run StepUsage) {
		step, err := o.currentState(state)
		if err != nil {
			return
		}
		shown := run
		if role == "reviewer" {
			if step.WorkerUsage != nil {
				shown = step.WorkerUsage.Plus(run)
			}
			if step.ReviewerUsage != nil {
				shown = step.ReviewerUsage.Plus(shown)
			}
		} else if step.ReviewerUsage != nil {
			shown = step.ReviewerUsage.Plus(run)
		}
		o.render.Usage(shown)
	}
}

func (o *Orchestrator) writeSummary(state *State) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Implement summary: %s\n\n", state.SessionName)
	fmt.Fprintf(&b, "- Mode: %s\n", state.Mode)
	fmt.Fprintf(&b, "- Task: %s\n", state.Task)
	fmt.Fprintf(&b, "- Result: complete\n")
	if state.BaseCommit != "" {
		fmt.Fprintf(&b, "- Base commit: %s\n", state.BaseCommit)
		fmt.Fprintf(&b, "- Sign commits: `%s`\n", SignCommitsCommand(state.BaseCommit))
	}
	fmt.Fprintf(&b, "\n## Steps\n\n")
	var total StepUsage
	for _, s := range state.Steps {
		fmt.Fprintf(&b, "- Step %d: %s [%s] (review rounds: %d)", s.Number, s.Title, s.Status, s.ReviewRounds)
		if s.CommitHash != "" {
			fmt.Fprintf(&b, " (commit %s)", shortHash(s.CommitHash))
		}
		var segs []string
		if s.WorkerUsage != nil && (s.WorkerUsage.HasTokens || s.WorkerUsage.HasCost) {
			wu := *s.WorkerUsage
			segs = append(
				segs,
				"worker: "+FormatUsage(wu.InputTokens, wu.OutputTokens, wu.CostUSD, wu.HasTokens, wu.HasCost),
			)
			total.Add(wu)
		}
		if s.ReviewerUsage != nil && (s.ReviewerUsage.HasTokens || s.ReviewerUsage.HasCost) {
			ru := *s.ReviewerUsage
			segs = append(
				segs,
				"reviewer: "+FormatUsage(ru.InputTokens, ru.OutputTokens, ru.CostUSD, ru.HasTokens, ru.HasCost),
			)
			total.Add(ru)
		}
		if len(segs) > 0 {
			b.WriteString(" — " + strings.Join(segs, " · "))
		}
		b.WriteByte('\n')
		if len(s.Escalations) > 0 {
			for _, esc := range s.Escalations {
				fmt.Fprintf(&b, "  - Q: %s\n    A: %s\n", esc.Question, esc.Answer)
			}
		}
	}

	if state.FinalReviewRounds > 0 || state.FinalCommitHash != "" ||
		state.FinalReviewerUsage != nil || state.FinalWorkerUsage != nil {
		fmt.Fprintf(&b, "\n## Final review\n\n")
		fmt.Fprintf(&b, "- Whole-diff fix rounds: %d\n", state.FinalReviewRounds)
		if state.FinalCommitHash != "" {
			fmt.Fprintf(&b, "- Final fix commit: %s\n", shortHash(state.FinalCommitHash))
		}
		var segs []string
		if state.FinalWorkerUsage != nil && (state.FinalWorkerUsage.HasTokens || state.FinalWorkerUsage.HasCost) {
			fu := *state.FinalWorkerUsage
			segs = append(
				segs,
				"worker: "+FormatUsage(fu.InputTokens, fu.OutputTokens, fu.CostUSD, fu.HasTokens, fu.HasCost),
			)
			total.Add(fu)
		}
		if state.FinalReviewerUsage != nil && (state.FinalReviewerUsage.HasTokens || state.FinalReviewerUsage.HasCost) {
			fu := *state.FinalReviewerUsage
			segs = append(
				segs,
				"reviewer: "+FormatUsage(fu.InputTokens, fu.OutputTokens, fu.CostUSD, fu.HasTokens, fu.HasCost),
			)
			total.Add(fu)
		}
		if len(segs) > 0 {
			fmt.Fprintf(&b, "- Usage: %s\n", strings.Join(segs, " · "))
		}
	}

	if total.HasTokens || total.HasCost {
		fmt.Fprintf(&b, "\n- Total: %s in %s out", FormatTokens(total.InputTokens), FormatTokens(total.OutputTokens))
		if total.HasCost {
			fmt.Fprintf(&b, ", cost %s", FormatCost(total.CostUSD))
		}
		b.WriteByte('\n')
	}

	// Execution tree in verbose mode for session debugging
	view := buildView(state, "", 0, "")
	view.Verbose = true
	treeLines := BuildFrameStyled(view, "✓", 200, 10000, false)
	fmt.Fprintf(&b, "\n## Execution Tree\n\n```text\n%s\n```\n", strings.Join(treeLines, "\n"))

	b.WriteString(
		"\nArtifacts: plan.md (imported execution plan), spec/ (source spec snapshot, when imported), " +
			"<N>-slug/work.md (live per-step summary), " +
			"<N>-slug/rR.{work,review}.md and rR.diff (per-round archive), " +
			"final-review.md, final.diff, logs/ (raw stream + markdown digests) in this directory.\n",
	)
	return os.WriteFile(filepath.Join(o.sessionDir, "summary.md"), []byte(b.String()), 0o600)
}

func bytesClone(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
