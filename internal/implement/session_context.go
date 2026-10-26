package implement

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kalverra/agents/pkg/git"
)

// checkBranch refuses to run on the repo's default branch when implement.yaml
// sets refuse_default_branch, so step commits never land on main by accident.
// A detached HEAD passes.
func (o *Orchestrator) checkBranch() error {
	if o.cfg == nil || !o.cfg.RefuseDefaultBranch {
		return nil
	}
	branch, err := gitOut(o.repoDir, "branch", "--show-current")
	if err != nil {
		return fmt.Errorf("refuse_default_branch: reading current branch: %w", err)
	}
	defaultBranch, err := git.DetectDefaultBranch(o.repoDir)
	if err != nil {
		return fmt.Errorf("refuse_default_branch: %w", err)
	}
	if strings.TrimSpace(branch) == defaultBranch {
		return fmt.Errorf(
			"refusing to run implement on default branch %q (refuse_default_branch is set); check out a work branch",
			defaultBranch,
		)
	}
	return nil
}

// workerContext runs implement.yaml's context_command through sh in the repo
// dir and returns its trimmed stdout, which is inlined into every worker
// prompt. It returns "" when no command is set. A failing command is an error:
// the user asked for this context, so running without it would be a surprise.
func (o *Orchestrator) workerContext(ctx context.Context) (string, error) {
	if o.cfg == nil || o.cfg.ContextCommand == "" {
		return "", nil
	}
	//nolint:gosec // G204: the command comes from the user's own implement.yaml
	cmd := exec.CommandContext(ctx, "sh", "-c", o.cfg.ContextCommand)
	cmd.Dir = o.repoDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf(
			"context_command %q: %w: %s",
			o.cfg.ContextCommand, err, strings.TrimSpace(stderr.String()),
		)
	}
	return strings.TrimSpace(string(out)), nil
}
