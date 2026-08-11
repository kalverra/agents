// Package review orchestrates fetching activity, rendering a prompt, running an LLM agent,
// saving the report, and delivering a Todoist task.
package review

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/kalverra/agents/internal/activity"
	"github.com/kalverra/agents/internal/eval"
	"github.com/kalverra/agents/internal/ticket"
	prompts "github.com/kalverra/agents/prompts/activity"
)

// Fetcher loads an activity report for a timeframe.
type Fetcher interface {
	Fetch(ctx context.Context, now time.Time, timeframe, projectName string) (*activity.Report, error)
}

// LLM runs a prompt and returns generated text.
type LLM interface {
	Run(ctx context.Context, target, prompt string) (string, error)
}

// Poster creates a Todoist task.
type Poster interface {
	CreateTask(ctx context.Context, req ticket.CreateTaskRequest) (*ticket.Ticket, error)
}

// Runner holds the dependencies for a review run.
type Runner struct {
	Fetcher        Fetcher
	LLM            LLM
	Poster         Poster
	ReportsDir     string
	PromptsDir     string
	ProjectID      string
	Now            func() time.Time
	Logger         zerolog.Logger
	EvalMode       bool
	EvalDir        string
	FeedbackReader io.Reader
	FeedbackWriter io.Writer
	Provider       string
	Model          string
}

// Result is the output of a review run.
type Result struct {
	Mode        string
	Timeframe   string
	ProjectName string
	Prompt      string
	Generated   string
	ReportPath  string
	TaskID      string
	TaskURL     string
}

// Run performs a full review cycle for the requested mode.
func (r *Runner) Run(ctx context.Context, mode, timeframe, projectName string, dryRun bool) (*Result, error) {
	if !isValidMode(mode) {
		return nil, fmt.Errorf("unsupported review mode %q (use daily, weekly, monthly, quarterly)", mode)
	}
	if timeframe == "" {
		timeframe = defaultTimeframe(mode)
	}

	now := r.now()
	report, err := r.Fetcher.Fetch(ctx, now, timeframe, projectName)
	if err != nil {
		return nil, fmt.Errorf("fetching activity: %w", err)
	}

	prompt, err := prompts.Load(mode, r.PromptsDir)
	if err != nil {
		return nil, fmt.Errorf("loading prompt: %w", err)
	}

	rendered := prompt.Render(activity.FormatXMLReport(report))
	target := fmt.Sprintf("%s report", mode)
	generated, err := r.LLM.Run(ctx, target, rendered)
	if err != nil {
		return nil, fmt.Errorf("running LLM: %w", err)
	}

	reportContent := buildReportContent(mode, now, generated)
	reportPath, err := r.writeReport(mode, now, reportContent)
	if err != nil {
		return nil, fmt.Errorf("writing report: %w", err)
	}

	res := &Result{
		Mode:        mode,
		Timeframe:   timeframe,
		ProjectName: projectName,
		Prompt:      rendered,
		Generated:   generated,
		ReportPath:  reportPath,
	}

	if !dryRun && r.Poster != nil {
		title := reportTitle(mode, now)
		task, err := r.Poster.CreateTask(ctx, ticket.CreateTaskRequest{
			Title:       title,
			Description: generated + "\n\nReport: " + reportPath,
			ProjectID:   r.ProjectID,
			DueString:   reportDueString(mode),
		})
		if err != nil {
			return nil, fmt.Errorf("creating Todoist task: %w", err)
		}
		res.TaskID = task.ID
		res.TaskURL = task.URL
	}

	if r.EvalMode {
		in := r.FeedbackReader
		if in == nil {
			in = os.Stdin
		}
		out := r.FeedbackWriter
		if out == nil {
			out = os.Stdout
		}
		evalDir := r.EvalDir
		if evalDir == "" {
			evalDir = filepath.Join("eval", "live_feedback")
		}

		score, reason, promptErr := eval.PromptUserFeedback(in, out)
		if promptErr != nil {
			return nil, fmt.Errorf("prompting for live feedback: %w", promptErr)
		}

		model := r.Model
		if model == "" {
			if m, ok := r.LLM.(interface{ Model() string }); ok {
				model = m.Model()
			}
		}
		provider := r.Provider
		if provider == "" {
			if p, ok := r.LLM.(interface{ Provider() string }); ok {
				provider = p.Provider()
			}
		}

		promptFileName := fmt.Sprintf("prompts/activity/%s.md", mode)
		rec := eval.LiveFeedbackRecord{
			ID:         fmt.Sprintf("activity-%s-%s", mode, now.UTC().Format("20060102-150405")),
			Timestamp:  now.UTC().Format(time.RFC3339),
			Command:    "activity",
			Mode:       mode,
			FullPrompt: rendered,
			PromptFile: promptFileName,
			PromptHash: eval.ComputePromptHash(prompt.Body),
			Model:      model,
			Provider:   provider,
			Response:   generated,
			UserScore:  score,
			UserReason: reason,
			Metadata: map[string]string{
				"timeframe": timeframe,
				"project":   projectName,
			},
		}

		if _, saveErr := eval.SaveLiveFeedback(evalDir, rec); saveErr != nil {
			return nil, fmt.Errorf("saving live feedback: %w", saveErr)
		}
	}

	return res, nil
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func isValidMode(mode string) bool {
	switch mode {
	case "daily", "weekly", "monthly", "quarterly":
		return true
	}
	return false
}

func defaultTimeframe(mode string) string {
	switch mode {
	case "daily":
		return "yesterday"
	case "weekly":
		return "week"
	case "monthly":
		return "month"
	case "quarterly":
		return "quarter"
	}
	return "7d"
}

func reportTitle(mode string, now time.Time) string {
	date := now.Format("2006-01-02")
	switch mode {
	case "daily":
		return "Daily Standup — " + date
	case "weekly":
		return "Weekly Update — " + date
	case "monthly":
		return "Monthly Summary — " + date
	case "quarterly":
		return "Quarterly Summary — " + date
	}
	return "Activity Report — " + date
}

func reportDueString(mode string) string {
	switch mode {
	case "daily":
		return "today at 10:00"
	case "weekly":
		return "today at 16:00"
	case "monthly", "quarterly":
		return "today at 17:00"
	}
	return "today at 10:00"
}

func buildReportContent(mode string, now time.Time, generated string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", reportTitle(mode, now))
	b.WriteString(generated)
	b.WriteString("\n")
	return b.String()
}

func (r *Runner) writeReport(mode string, now time.Time, content string) (string, error) {
	if r.ReportsDir == "" {
		return "", fmt.Errorf("reports directory not configured")
	}
	if err := os.MkdirAll(r.ReportsDir, 0750); err != nil {
		return "", fmt.Errorf("creating reports dir: %w", err)
	}
	name := fmt.Sprintf("%s-%s.md", now.Format("2006-01-02"), mode)
	path := filepath.Join(r.ReportsDir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("writing report: %w", err)
	}
	return path, nil
}
