package skills

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/commit"
	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/git"
	"github.com/kalverra/agents/internal/llm"
	"github.com/kalverra/agents/internal/output"
	"github.com/kalverra/agents/internal/ticket"
)

// CommitMsgCmd is the command to generate commit messages or install git hooks.
var CommitMsgCmd = &cobra.Command{
	Use:   "commit-msg [commit_msg_file] [source] [sha]",
	Short: "Generate an AI-assisted Conventional Commit message from staged diff",
	RunE: func(cmd *cobra.Command, args []string) error {
		installHook, _ := cmd.Flags().GetBool("install-hook")
		globalHook, _ := cmd.Flags().GetBool("global")
		evalMode, _ := cmd.Flags().GetBool("eval")
		finalizeEval, _ := cmd.Flags().GetBool("finalize-eval")
		dir, _ := cmd.Flags().GetString("dir")
		if dir == "" {
			dir = "."
		}

		var commitMsgFile, commitSource, commitSHA string
		if len(args) > 0 {
			commitMsgFile = args[0]
		}
		if len(args) > 1 {
			commitSource = args[1]
		}
		if len(args) > 2 {
			commitSHA = args[2]
		}

		if finalizeEval {
			evalDir, _ := cmd.Flags().GetString("eval-dir")
			_, err := commit.FinalizeEval(commitMsgFile, evalDir)
			return err
		}

		if installHook {
			return installGitHook(cmd.Context(), globalHook, dir, evalMode)
		}

		ctx := cmd.Context()
		logger := zerolog.Ctx(ctx)

		cfg, err := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		provider, _ := cmd.Flags().GetString("provider")
		model, _ := cmd.Flags().GetString("model")
		llmCfg := llmConfigFrom(cfg, provider, model)
		llmRunner := llm.NewRunner(llmCfg)

		var ticketFetcher commit.TicketFetcher
		if cfg.JiraEmail != "" && cfg.JiraAPIToken != "" && cfg.JiraDomain != "" {
			ticketFetcher = ticket.NewJira(*logger, ticket.JiraConfig{
				Email:    cfg.JiraEmail,
				APIToken: cfg.JiraAPIToken,
				Domain:   cfg.JiraDomain,
			})
		}

		runner := &commit.Runner{
			LLM:           llmRunner,
			TicketFetcher: ticketFetcher,
			Logger:        *logger,
		}

		res, err := runner.Run(ctx, commit.Options{
			Dir:           dir,
			CommitMsgFile: commitMsgFile,
			CommitSource:  commitSource,
			CommitSHA:     commitSHA,
			EvalMode:      evalMode,
			Provider:      llmCfg.Provider,
			Model:         llmCfg.Model,
		})
		if err != nil {
			return err
		}

		if res.Skipped {
			if output.JSON() {
				output.Write("commit-msg", map[string]any{"skipped": true, "reason": res.Reason}, nil)
				return nil
			}
			fmt.Printf("Skipping commit msg generation: %s\n", res.Reason)
			return nil
		}

		if output.JSON() {
			output.Write("commit-msg", map[string]any{
				"message":         res.Message,
				"commit_msg_file": commitMsgFile,
				"files_changed":   res.Diff.Stats.FilesChanged,
			}, nil)
			return nil
		}

		if !evalMode {
			if commitMsgFile != "" {
				fmt.Printf("Generated commit message written to %s:\n\n%s\n", commitMsgFile, res.Message)
			} else {
				fmt.Println(res.Message)
			}
		}

		return nil
	},
}

func installGitHook(ctx context.Context, global bool, dir string, evalMode bool) error {
	exe, err := os.Executable()
	if err != nil {
		home, hErr := os.UserHomeDir()
		if hErr != nil {
			return fmt.Errorf("getting home dir: %w", hErr)
		}
		exe = filepath.Join(home, ".agents", "agents")
	}

	evalFlag := ""
	if evalMode {
		evalFlag = " --eval"
	}

	prepHookScript := fmt.Sprintf(`#!/bin/sh
if [ -c /dev/tty ]; then
  exec < /dev/tty
fi
%q commit-msg "$1" "$2" "$3"%s
status=$?
if [ $status -ne 0 ]; then
  exit $status
fi

if command -v lefthook >/dev/null 2>&1; then
  if [ -f "lefthook.yml" ] || [ -f ".lefthook.yml" ] || [ -f "lefthook-local.yml" ] || [ -f ".lefthook-local.yml" ]; then
    lefthook run prepare-commit-msg "$@"
  fi
fi
`, exe, evalFlag)

	postHookScript := fmt.Sprintf(`#!/bin/sh
%q commit-msg --finalize-eval "$1"
`, exe)

	var hooksDir string
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("getting home dir: %w", err)
		}
		hooksDir = filepath.Join(home, ".config", "git", "hooks")
		if err := os.MkdirAll(hooksDir, 0755); err != nil { //nolint:gosec
			return fmt.Errorf("creating global hooks dir: %w", err)
		}

		// Run git config --global core.hooksPath <hooksDir>
		cmd := exec.CommandContext(ctx, "git", "config", "--global", "core.hooksPath", hooksDir) //nolint:gosec
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("setting git global core.hooksPath: %w", err)
		}
	} else {
		repo, err := git.DetectRepo(dir)
		if err != nil {
			return fmt.Errorf("detecting git repo: %w", err)
		}
		_ = repo
		hooksDir = filepath.Join(dir, ".git", "hooks")
		if err := os.MkdirAll(hooksDir, 0755); err != nil { //nolint:gosec
			return fmt.Errorf("creating repo hooks dir: %w", err)
		}
	}

	prepHookPath := filepath.Join(hooksDir, "prepare-commit-msg")
	if err := os.WriteFile(prepHookPath, []byte(prepHookScript), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("writing prepare-commit-msg hook: %w", err)
	}

	postHookPath := filepath.Join(hooksDir, "commit-msg")
	if err := os.WriteFile(postHookPath, []byte(postHookScript), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("writing commit-msg hook: %w", err)
	}

	if output.JSON() {
		output.Write("commit-msg-install", map[string]any{"hook_path": prepHookPath, "global": global}, nil)
		return nil
	}

	if global {
		fmt.Printf("Installed global prepare-commit-msg and commit-msg hooks at %s\n", hooksDir)
	} else {
		fmt.Printf("Installed local prepare-commit-msg and commit-msg hooks at %s\n", hooksDir)
	}

	return nil
}

func init() {
	CommitMsgCmd.Flags().Bool("install-hook", false, "Install prepare-commit-msg git hook script")
	CommitMsgCmd.Flags().Bool("global", false, "Install prepare-commit-msg hook globally (configures core.hooksPath)")
	CommitMsgCmd.Flags().String("dir", "", "Working directory for git repo")
	CommitMsgCmd.Flags().String("provider", "", "LLM provider override (agy, llama, ollama, auto)")
	CommitMsgCmd.Flags().String("model", "", "LLM model override")
	CommitMsgCmd.Flags().Bool("eval", false, "Prompt for interactive rating and record live evaluation feedback")
	CommitMsgCmd.Flags().Bool("finalize-eval", false, "Finalize pending live evaluation feedback after editor closes")
	CommitMsgCmd.Flags().String("eval-dir", "", "Directory to store live evaluation feedback")
}
