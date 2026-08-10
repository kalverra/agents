package commit

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/kalverra/agents/internal/eval"
	"github.com/kalverra/agents/internal/git"
	"github.com/kalverra/agents/internal/ticket"
	prompts "github.com/kalverra/agents/prompts/commit"
)

// LLM runs a prompt and returns generated text.
type LLM interface {
	Run(ctx context.Context, target, prompt string) (string, error)
}

// TicketFetcher retrieves ticket details by key or ID.
type TicketFetcher interface {
	Fetch(ctx context.Context, ref string) (*ticket.Ticket, error)
}

// Options configures a commit message generation run.
type Options struct {
	Dir            string
	CommitMsgFile  string
	CommitSource   string
	CommitSHA      string
	PromptsDir     string
	ContextLines   int
	Ticket         *ticket.Ticket
	EvalMode       bool
	EvalDir        string
	FeedbackReader io.Reader
	FeedbackWriter io.Writer
	Provider       string
	Model          string
}

// Result holds the output of a commit generation run.
type Result struct {
	Message string
	Skipped bool
	Reason  string
	Diff    *git.BranchDiffResult
}

// Runner orchestrates commit message generation.
type Runner struct {
	LLM           LLM
	TicketFetcher TicketFetcher
	Logger        zerolog.Logger
}

// Run executes the commit message generation workflow.
func (r *Runner) Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Dir == "" {
		opts.Dir = "."
	}

	if opts.CommitSource == "message" {
		existing := ""
		if opts.CommitMsgFile != "" {
			if data, err := os.ReadFile(opts.CommitMsgFile); err == nil {
				existing = string(data)
			}
		}
		return &Result{
			Message: existing,
			Skipped: true,
			Reason:  "explicit message provided via -m",
		}, nil
	}

	diff, err := git.StagedDiff(opts.Dir, git.BranchDiffOptions{
		ContextLines: opts.ContextLines,
	})
	if err != nil {
		return nil, fmt.Errorf("reading staged diff: %w", err)
	}

	if len(diff.Files) == 0 {
		return nil, fmt.Errorf("no staged changes to commit")
	}

	tk := opts.Ticket
	if tk == nil && r.TicketFetcher != nil {
		if key := ticket.ExtractJiraKey(diff.CurrentBranch); key != "" {
			fetched, fErr := r.TicketFetcher.Fetch(ctx, key)
			if fErr == nil {
				tk = fetched
			}
		}
	}

	xmlBundle := FormatBundle(diff, tk)

	promptTemplate, err := prompts.Load(opts.PromptsDir)
	if err != nil {
		return nil, fmt.Errorf("loading commit prompt: %w", err)
	}

	renderedPrompt := promptTemplate.Render(xmlBundle)

	generated, err := r.LLM.Run(ctx, "commit message", renderedPrompt)
	if err != nil {
		return nil, fmt.Errorf("generating commit message: %w", err)
	}

	generated = cleanCommitMessage(generated)

	if opts.CommitMsgFile != "" {
		gitComments := ""
		if data, readErr := os.ReadFile(opts.CommitMsgFile); readErr == nil {
			gitComments = extractGitComments(string(data))
		}

		outMsg := generated
		if gitComments != "" {
			outMsg = generated + "\n\n" + gitComments
		}

		if err := os.WriteFile(opts.CommitMsgFile, []byte(outMsg), 0600); err != nil {
			return nil, fmt.Errorf("writing commit msg file: %w", err)
		}
	}

	if opts.EvalMode {
		in := opts.FeedbackReader
		if in == nil {
			in = os.Stdin
		}
		out := opts.FeedbackWriter
		if out == nil {
			out = os.Stdout
		}
		evalDir := opts.EvalDir
		if evalDir == "" {
			evalDir = filepath.Join("eval", "live_feedback")
		}

		score, reason, promptErr := eval.PromptUserFeedback(in, out)
		if promptErr != nil {
			return nil, fmt.Errorf("prompting for live feedback: %w", promptErr)
		}

		rec := eval.LiveFeedbackRecord{
			ID:         fmt.Sprintf("commit-%s", time.Now().UTC().Format("20060102-150405")),
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
			Command:    "commit",
			FullPrompt: renderedPrompt,
			PromptFile: "prompts/commit/commit.md",
			PromptHash: eval.ComputePromptHash(promptTemplate.Body),
			Model:      opts.Model,
			Provider:   opts.Provider,
			Response:   generated,
			UserScore:  score,
			UserReason: reason,
			Metadata: map[string]string{
				"branch":        diff.CurrentBranch,
				"files_changed": fmt.Sprintf("%d", diff.Stats.FilesChanged),
			},
		}

		if opts.CommitMsgFile != "" {
			if _, saveErr := eval.SavePendingLiveFeedback(evalDir, rec); saveErr != nil {
				return nil, fmt.Errorf("saving pending live feedback: %w", saveErr)
			}
		} else {
			if _, saveErr := eval.SaveLiveFeedback(evalDir, rec); saveErr != nil {
				return nil, fmt.Errorf("saving live feedback: %w", saveErr)
			}
		}
	}

	return &Result{
		Message: generated,
		Diff:    diff,
	}, nil
}

// FinalizeEval completes pending live evaluation after user closes editor.
func FinalizeEval(commitMsgFile, evalDir string) (*eval.LiveFeedbackRecord, error) {
	if evalDir == "" {
		evalDir = filepath.Join("eval", "live_feedback")
	}

	pendingPath, err := findPendingEvalFile(evalDir)
	if err != nil {
		return nil, err
	}
	if pendingPath == "" {
		return nil, nil
	}

	return eval.FinalizeLiveFeedback(evalDir, pendingPath, commitMsgFile)
}

func findPendingEvalFile(evalDir string) (string, error) {
	entries, err := os.ReadDir(evalDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading eval dir: %w", err)
	}

	var latestPath string
	var latestTime time.Time

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "pending_") && strings.HasSuffix(entry.Name(), ".json") {
			info, iErr := entry.Info()
			if iErr != nil {
				continue
			}
			if latestPath == "" || info.ModTime().After(latestTime) {
				latestTime = info.ModTime()
				latestPath = filepath.Join(evalDir, entry.Name())
			}
		}
	}

	return latestPath, nil
}

func extractGitComments(s string) string {
	lines := strings.Split(s, "\n")
	var comments []string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			comments = append(comments, line)
		}
	}
	if len(comments) == 0 {
		return ""
	}
	res := strings.Join(comments, "\n")
	if strings.HasSuffix(s, "\n") {
		res += "\n"
	}
	return res
}

func cleanCommitMessage(s string) string {
	s = trimSurroundingQuotes(s)
	return s
}

func trimSurroundingQuotes(s string) string {
	if len(s) >= 6 && s[:3] == "```" && s[len(s)-3:] == "```" {
		s = s[3 : len(s)-3]
		if len(s) > 0 && s[0] == '\n' {
			s = s[1:]
		}
	}
	return s
}
