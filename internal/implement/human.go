package implement

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"charm.land/huh/v2"
	"golang.org/x/term"
)

// HookAction represents the user's chosen action when a pre-commit hook fails.
type HookAction string

// Available remediation choices when pre-commit hooks fail.
const (
	// HookActionAgentFix routes the failure output back to the worker agent for an auto-fix round.
	HookActionAgentFix HookAction = "fix"
	// HookActionNoVerify bypasses pre-commit hooks with git commit --no-verify.
	HookActionNoVerify HookAction = "no-verify"
	// HookActionRetry retries the commit after manual user edits.
	HookActionRetry HookAction = "retry"
	// HookActionEscalate halts the session requiring human intervention before resuming.
	HookActionEscalate HookAction = "escalate"
)

// HookPrompter is an optional interface a Confirmer can implement to interactively
// prompt the user when pre-commit hooks fail.
type HookPrompter interface {
	PromptHookAction(title, output string) (HookAction, error)
}

// VerifyBaselineAction is the user's choice when a declared verification
// command fails against the clean baseline tree.
type VerifyBaselineAction string

// Available remediation choices when a verification command fails at baseline.
const (
	// VerifyBaselineDrop degrades the failing verification check to worker-run.
	VerifyBaselineDrop VerifyBaselineAction = "drop"
	// VerifyBaselineKeep keeps the failing command, requiring workers to fix it.
	VerifyBaselineKeep VerifyBaselineAction = "keep"
	// VerifyBaselineAbort halts the session requiring human resolution.
	VerifyBaselineAbort VerifyBaselineAction = "abort"
)

// VerifyPrompter is an optional interface a Confirmer can implement to
// ask what to do about a command that fails at baseline.
type VerifyPrompter interface {
	PromptVerifyBaseline(name, command, output string, notFound bool) (VerifyBaselineAction, error)
}

// Asker is an optional interface a Confirmer can implement to collect a
// free-text answer to a worker's ESCALATE: question.
type Asker interface {
	Ask(question string) (string, error)
}

// Confirmer gates the flow on a human decision (the final whole-diff
// review). Tests inject a fake.
type Confirmer interface {
	// Interactive reports whether Confirm can prompt a human right now.
	Interactive() bool
	// Confirm asks one yes/no question.
	Confirm(prompt string) (bool, error)
}

// TerminalConfirmer reads y/n answers from a TTY stdin.
type TerminalConfirmer struct {
	In  io.Reader
	Out io.Writer
}

// NewTerminalConfirmer builds the default confirmer wired to stdio.
func NewTerminalConfirmer() *TerminalConfirmer {
	return &TerminalConfirmer{In: os.Stdin, Out: os.Stdout}
}

// Interactive reports whether stdin is a terminal.
func (c *TerminalConfirmer) Interactive() bool {
	f, ok := c.In.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// Confirm prompts for yes/no approval, using an interactive huh form on TTYs and
// falling back to bufio line reading on non-terminal streams.
func (c *TerminalConfirmer) Confirm(prompt string) (bool, error) {
	if c.Interactive() {
		var proceed bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(prompt).
					Affirmative("Yes").
					Negative("No").
					Value(&proceed),
			),
		).WithTheme(huh.ThemeFunc(huh.ThemeCharm))
		if err := form.Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return false, nil
			}
			return false, err
		}
		return proceed, nil
	}

	if c.Out != nil {
		_, _ = fmt.Fprintf(c.Out, "%s ", prompt)
	}
	r := bufio.NewReader(c.In)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		if c.Out != nil {
			_, _ = fmt.Fprint(c.Out, "answer y or n: ")
		}
	}
}

// Ask prompts the user for a free-text answer to an escalation question,
// using a multi-line huh text form on TTYs and falling back to bufio line
// reading on non-terminal streams.
func (c *TerminalConfirmer) Ask(question string) (string, error) {
	if c.Interactive() {
		var answer string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewText().
					Title(question).
					Value(&answer),
			),
		).WithTheme(huh.ThemeFunc(huh.ThemeCharm))
		if err := form.Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return "", nil
			}
			return "", err
		}
		return strings.TrimSpace(answer), nil
	}

	if c.Out != nil {
		_, _ = fmt.Fprintf(c.Out, "%s\n> ", question)
	}
	r := bufio.NewReader(c.In)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// PromptHookAction displays the pre-commit failure and prompts the user using huh.
func (c *TerminalConfirmer) PromptHookAction(title, output string) (HookAction, error) {
	if !c.Interactive() {
		return HookActionEscalate, nil
	}

	if c.Out != nil {
		_, _ = fmt.Fprintln(c.Out, FormatCommitError(title, errors.New(output)))
	}

	var action HookAction
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[HookAction]().
				Title("Pre-commit hooks failed. How do you want to proceed?").
				Options(
					huh.NewOption("Feed output to Worker Agent (Auto-fix round)", HookActionAgentFix),
					huh.NewOption("Commit anyway with --no-verify", HookActionNoVerify),
					huh.NewOption("Retry commit (after manual fix)", HookActionRetry),
					huh.NewOption("Pause session (Escalate)", HookActionEscalate),
				).
				Value(&action),
		),
	).WithTheme(huh.ThemeFunc(huh.ThemeCharm))

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return HookActionEscalate, nil
		}
		return HookActionEscalate, err
	}
	return action, nil
}

// PromptVerifyBaseline displays the baseline failure and prompts the user using huh.
func (c *TerminalConfirmer) PromptVerifyBaseline(
	name, command, output string,
	notFound bool,
) (VerifyBaselineAction, error) {
	if !c.Interactive() {
		return VerifyBaselineDrop, nil
	}

	bin := command
	if parts := strings.Fields(command); len(parts) > 0 {
		bin = parts[0]
	}

	if c.Out != nil {
		if notFound {
			_, _ = fmt.Fprintf(
				c.Out,
				"\nVerification command %q (%s) failed at baseline: %s is not installed or not on PATH\n",
				name,
				command,
				bin,
			)
		} else {
			_, _ = fmt.Fprintf(c.Out, "\nVerification command %q (%s) failed at baseline:\n", name, command)
		}
		if output != "" {
			_, _ = fmt.Fprintln(c.Out, output)
		}
	}

	title := fmt.Sprintf("Verification command %q failed at baseline. How do you want to proceed?", name)
	if notFound {
		title = fmt.Sprintf(
			"Verification command %q failed (%s is not installed or not on PATH). How do you want to proceed?",
			name,
			bin,
		)
	}

	var action VerifyBaselineAction
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[VerifyBaselineAction]().
				Title(title).
				Options(
					huh.NewOption("Drop this check (worker verifies it instead)", VerifyBaselineDrop),
					huh.NewOption("Keep it anyway (workers will be asked to fix it)", VerifyBaselineKeep),
					huh.NewOption("Abort the session", VerifyBaselineAbort),
				).
				Value(&action),
		),
	).WithTheme(huh.ThemeFunc(huh.ThemeCharm))

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return VerifyBaselineAbort, nil
		}
		return VerifyBaselineAbort, err
	}
	return action, nil
}

// zedDiffOpener opens a split multi-file diff view in Zed via
// `zed --diff <old-dir> <new-dir>` (single multi-diff view of all changed
// files). Errors when zed is not installed; the caller falls back to the
// written final.diff file.
func zedDiffOpener(baseDir, headDir string) error {
	zed, err := exec.LookPath("zed")
	if err != nil {
		return fmt.Errorf("zed not found on PATH: %w", err)
	}
	cmd := exec.Command(zed, "--diff", baseDir, headDir) //nolint:gosec // session-dir paths
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting zed diff: %w", err)
	}
	go func() { _ = cmd.Wait() }() //nolint:errcheck // fire-and-forget: zed returns once the window opens
	return nil
}
