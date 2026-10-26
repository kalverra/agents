package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/kalverra/agents/internal/implement"
	"github.com/kalverra/agents/pkg/output"
)

// ImplementCmd executes an existing plan or spec through a worker/reviewer agent pipeline.
var ImplementCmd = newImplementCmd()

func newImplementCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "implement",
		Short: "Implement an existing plan or spec (openspec / spec-kit) with worker/reviewer agents (agy, claude, opencode)",
		Long: `Implement an existing plan or spec through a multi-agent pipeline.

The plan is never written here: it already exists — a flow-native plan.md
(--plan), an openspec change dir (openspec/changes/<name>/), or a spec-kit
spec dir (specs/<name>/ with spec.md + tasks.md). With neither --plan nor
--spec, the repo is scanned for an in-flight spec; when several are found
they are listed and --spec picks one. The spec is snapshotted into the
session and compiled to a plan.md, which the human accepts before any step
runs. Editing plan.md after acceptance escalates on resume (restore it, or
--resume --accept-plan-changes to re-gate and reconcile step status).

Requires an implement.toml (repo .agents/implement.toml or
~/.config/agents/implement.toml) defining modes and per-role harness/model
assignments. The worktree must be clean: each approved step is committed
(message from the worker's proposed COMMIT line), and after all steps the
whole diff is agent-reviewed, then opened for your final human approval.
Session artifacts are written to .agents/local/<session>/ in the current
repo:

  plan.md            imported execution plan (the frozen contract)
  spec/              snapshot of the imported spec dir
  <N>-slug/work.md   worker summary per step (ends with a COMMIT line)
  <N>-slug/          per-round rR.work.md / rR.review.md / rR.diff archive
  final-review.md    whole-diff agent review (ends with a VERDICT line)
  final-work.md      final-review fix summary (when fixes were needed)
  final.diff         full unified diff presented for human review
  diff/              archived base/head trees opened in the diff viewer
  state.json         resumable session state
  logs/              raw agent stream logs (step-N-*.jsonl) + markdown digests (step-N-*.md)`,
		RunE: runImplement,
	}

	cmd.Flags().String("mode", "", "Mode defined in implement.toml (e.g. simple, medium, hard, extreme)")
	cmd.Flags().String("session", "", "Session name; artifacts go to .agents/local/<session>/")
	cmd.Flags().String("plan", "", "Adopt an existing plan.md (flow-native format) instead of a spec dir")
	cmd.Flags().String("spec", "", "Spec dir to implement (openspec change or spec-kit specs/<name>/)")
	cmd.Flags().String("task", "", "Optional task title override (default: derived from the spec)")
	cmd.Flags().Bool("verbose", false, "Print each agent tool event as it streams")
	cmd.Flags().Bool("resume", false, "Resume an existing session from its state.json")
	cmd.Flags().Bool("accept-plan-changes", false, "With --resume: re-accept a plan.md edited after acceptance")
	cmd.Flags().Bool("list", false, "List modes defined in implement.toml and exit")
	cmd.Flags().
		String("config", "", "Explicit path to implement.toml (default: .agents/implement.toml then ~/.config/agents/implement.toml)")
	return cmd
}

func runImplement(cmd *cobra.Command, args []string) error {
	mode, _ := cmd.Flags().GetString("mode")
	session, _ := cmd.Flags().GetString("session")
	planPath, _ := cmd.Flags().GetString("plan")
	specPath, _ := cmd.Flags().GetString("spec")
	task, _ := cmd.Flags().GetString("task")
	resume, _ := cmd.Flags().GetBool("resume")
	acceptChanges, _ := cmd.Flags().GetBool("accept-plan-changes")
	list, _ := cmd.Flags().GetBool("list")
	verbose, _ := cmd.Flags().GetBool("verbose")
	configPath, _ := cmd.Flags().GetString("config")

	configPaths := []string{configPath}
	if configPath == "" {
		configPaths = implement.DefaultConfigPaths()
	}
	implCfg, err := implement.LoadConfig(configPaths...)
	if err != nil {
		return fmt.Errorf("loading implement config: %w (see implement.example.toml in the agents repo)", err)
	}

	if list {
		return listModes(implCfg)
	}

	if session == "" {
		return errors.New("--session is required")
	}

	repoDir, err := os.Getwd()
	if err != nil {
		return err
	}
	sessionDir := filepath.Join(repoDir, ".agents", "local", session)

	if resume {
		state, sErr := implement.LoadState(sessionDir)
		if sErr != nil {
			return fmt.Errorf("--resume: %w", sErr)
		}
		if mode == "" {
			mode = state.Mode
		} else if mode != state.Mode {
			return fmt.Errorf("--mode %q does not match session mode %q", mode, state.Mode)
		}
	} else if acceptChanges {
		return errors.New("--accept-plan-changes only applies with --resume")
	}

	if mode == "" {
		return fmt.Errorf("--mode is required (available: %s)", strings.Join(implCfg.ModeNames(), ", "))
	}
	if task == "" && len(args) > 0 {
		task = strings.Join(args, " ")
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	// Route SIGINT/SIGTERM through context so a mid-session interrupt still
	// tears the renderer down and restores the terminal cursor.
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	log := zerolog.Nop()
	if l := zerolog.Ctx(ctx); l != nil {
		log = *l
	}
	out := io.Writer(os.Stdout)
	if output.JSON() {
		out = io.Discard
	}

	orch, err := implement.NewOrchestrator(implCfg, mode, repoDir, session, nil, log, out)
	if err != nil {
		return err
	}
	orch.SetRenderer(newImplementRenderer(out, verbose))

	if !resume {
		if _, sErr := os.Stat(filepath.Join(sessionDir, "state.json")); sErr == nil {
			return fmt.Errorf("session %q already exists; use --resume to continue it", session)
		}
		err = orch.Run(ctx, implement.RunOpts{
			Task:     task,
			PlanPath: planPath,
			SpecPath: specPath,
		})
	} else {
		err = orch.Resume(ctx, implement.ResumeOpts{
			AcceptPlanChanges: acceptChanges,
		})
	}

	return writeImplementResult(sessionDir, err)
}

// newImplementRenderer builds the progress renderer. In-place frame
// rendering is enabled only on color-capable TTYs; pipes and AI-output mode
// get plain lines (plus the verbose event tail when requested).
func newImplementRenderer(out io.Writer, verbose bool) *implement.Renderer {
	fd := int(os.Stdout.Fd())
	tty := !output.JSON() && os.Getenv("NO_COLOR") == "" && term.IsTerminal(fd)
	opts := implement.RendererOpts{TTY: tty, Color: tty, Verbose: verbose}
	if w, h, err := term.GetSize(fd); err == nil && w > 0 && h > 0 {
		opts.Width, opts.Height = w, h
	}
	return implement.NewRenderer(out, opts)
}

func listModes(implCfg *implement.Config) error {
	if output.JSON() {
		var b strings.Builder
		b.WriteString("<implement_modes>\n")
		for _, name := range implCfg.ModeNames() {
			mode, _ := implCfg.Mode(name)
			fmt.Fprintf(&b, "  <mode name=%q", name)
			fmt.Fprintf(&b, " worker=%q:%q", mode.Worker.Harness, mode.Worker.Model)
			if len(mode.WorkerChoices) > 1 {
				fmt.Fprintf(&b, " worker_options=%q", choiceNames(mode.WorkerChoices))
			}
			if mode.Reviewer != nil {
				fmt.Fprintf(&b, " reviewer=%q:%q", mode.Reviewer.Harness, mode.Reviewer.Model)
			}
			if len(mode.ReviewerChoices) > 1 {
				fmt.Fprintf(&b, " reviewer_options=%q", choiceNames(mode.ReviewerChoices))
			}
			b.WriteString("/>\n")
		}
		b.WriteString("</implement_modes>")
		output.Write("implement-modes", b.String(), nil)
		return nil
	}
	output.Write("implement-modes", nil, func() {
		fmt.Println("Available modes:")
		for _, name := range implCfg.ModeNames() {
			mode, _ := implCfg.Mode(name)
			fmt.Printf("  %s\n", formatModeEntry(name, mode))
		}
	})
	return nil
}

func formatModeEntry(name string, mode implement.ModeConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-8s", name)
	fmt.Fprintf(&b, " worker=%s(%s)", mode.Worker.Harness, mode.Worker.Model)
	if mode.Reviewer != nil {
		fmt.Fprintf(&b, " reviewer=%s(%s)", mode.Reviewer.Harness, mode.Reviewer.Model)
	} else {
		b.WriteString(" reviewer=none")
	}
	if len(mode.WorkerChoices) > 1 {
		fmt.Fprintf(&b, " worker_options=[%s]", choiceNames(mode.WorkerChoices))
	}
	if len(mode.ReviewerChoices) > 1 {
		fmt.Fprintf(&b, " reviewer_options=[%s]", choiceNames(mode.ReviewerChoices))
	}
	return b.String()
}

func choiceNames(choices []implement.NamedSpec) string {
	opts := make([]string, len(choices))
	for i, c := range choices {
		opts[i] = c.Name
	}
	return strings.Join(opts, ",")
}

func writeImplementResult(sessionDir string, runErr error) error {
	state, sErr := implement.LoadState(sessionDir)
	if sErr != nil {
		return runErr
	}

	status := "complete"
	if runErr != nil {
		switch {
		case errors.Is(runErr, implement.ErrEscalated):
			status = "escalated"
		case errors.Is(runErr, implement.ErrAwaitingHuman):
			status = "awaiting_human"
		case errors.Is(runErr, implement.ErrVerifyBaseline):
			status = "baseline_aborted"
		case errors.Is(runErr, implement.ErrPlanDrift):
			status = "plan_drift"
		default:
			status = "failed"
		}
	}

	resumeCmd := state.ResumeCommand()

	if output.JSON() {
		var b strings.Builder
		fmt.Fprintf(&b, "<implement session=%q mode=%q status=%q steps=%d session_dir=%q",
			state.SessionName, state.Mode, status, len(state.Steps), sessionDir)
		if state.PlanSource.Kind != "" {
			fmt.Fprintf(&b, " plan_source=%q:%q", state.PlanSource.Kind, state.PlanSource.Path)
		}
		if status != "complete" {
			fmt.Fprintf(&b, " resume_command=%q", resumeCmd)
		} else if state.BaseCommit != "" {
			fmt.Fprintf(&b, " sign_command=%q", implement.SignCommitsCommand(state.BaseCommit))
		}
		if runErr != nil && status == "failed" {
			fmt.Fprintf(&b, " error=%q", runErr.Error())
		}
		b.WriteString(">\n")
		for _, s := range state.Steps {
			fmt.Fprintf(
				&b,
				"  <step n=%d title=%q status=%q review_rounds=%d/>\n",
				s.Number,
				s.Title,
				s.Status,
				s.ReviewRounds,
			)
		}
		b.WriteString("</implement>")
		output.Write("implement", b.String(), nil)
		if status == "failed" {
			return runErr
		}
		return nil
	}

	output.Write("implement", nil, func() {
		switch status {
		case "escalated":
			fmt.Printf(
				"\nImplement escalated: review fixes exceeded max rounds. intervene, then resume with: %s\n",
				resumeCmd,
			)
		case "awaiting_human":
			fmt.Printf(
				"\nImplement awaiting human review: inspect %s (or the archived trees in %s), then rerun with --resume to approve.\n",
				filepath.Join(sessionDir, implement.FinalDiffFile),
				filepath.Join(sessionDir, implement.DiffDir),
			)
			fmt.Printf("resume with: %s\n", resumeCmd)
		case "baseline_aborted":
			fmt.Printf(
				"\nImplement aborted: baseline verification failed and session was aborted by user.\nresume with: %s\n",
				resumeCmd,
			)
		case "plan_drift":
			fmt.Printf(
				"\nImplement halted: plan.md changed after acceptance.\nrestore it, or review the changes and resume with: %s --accept-plan-changes\n",
				resumeCmd,
			)
		case "failed":
			fmt.Printf("\nImplement failed: %v\nresume with: %s\n", runErr, resumeCmd)

		default:
			fmt.Printf("\nImplement complete. summary: %s\n", filepath.Join(sessionDir, "summary.md"))
			if state.BaseCommit != "" {
				fmt.Printf("sign commits: %s\n", implement.SignCommitsCommand(state.BaseCommit))
			}
		}
		fmt.Printf("state: %s\n", filepath.Join(sessionDir, "state.json"))
	})
	if status == "failed" {
		return runErr
	}
	return nil
}
