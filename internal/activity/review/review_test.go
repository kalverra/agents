package review_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/activity"
	review "github.com/kalverra/agents/internal/activity/review"
	"github.com/kalverra/agents/internal/ticket"
)

func TestRunner_Run(t *testing.T) {
	t.Parallel()

	reportsDir := t.TempDir()
	promptsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "daily.md"), []byte("Daily\n{{ACTIVITY_XML}}"), 0600))

	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	poster := &fakePoster{}
	runner := &review.Runner{
		Fetcher:    &fakeFetcher{report: &activity.Report{Timeframe: "1d"}},
		LLM:        &fakeLLM{response: "generated report"},
		Poster:     poster,
		ReportsDir: reportsDir,
		PromptsDir: promptsDir,
		Now:        func() time.Time { return now },
		Logger:     zerolog.New(nil),
	}

	res, err := runner.Run(context.Background(), "daily", "", "Work", false)
	require.NoError(t, err)
	assert.Equal(t, "daily", res.Mode)
	assert.Equal(t, "generated report", res.Generated)
	assert.Equal(t, "yesterday", res.Timeframe)
	assert.FileExists(t, res.ReportPath)

	content, err := os.ReadFile(res.ReportPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "generated report")

	require.NotNil(t, poster.req)
	assert.Equal(t, "Daily Standup — 2026-08-06", poster.req.Title)
	assert.Contains(t, poster.req.Description, "vscode://file/")
	assert.NotContains(t, poster.req.Description, "generated report")
	assert.Equal(t, "today at 10:00", poster.req.DueString)
}

func TestRunner_Run_TaskDueDate(t *testing.T) {
	t.Parallel()

	reportsDir := t.TempDir()
	promptsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "weekly.md"), []byte("Weekly\n{{ACTIVITY_XML}}"), 0600))

	poster := &fakePoster{}
	runner := &review.Runner{
		Fetcher:    &fakeFetcher{report: &activity.Report{Timeframe: "7d"}},
		LLM:        &fakeLLM{response: "report"},
		Poster:     poster,
		ReportsDir: reportsDir,
		PromptsDir: promptsDir,
		Now:        func() time.Time { return time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC) },
		Logger:     zerolog.New(nil),
	}

	_, err := runner.Run(context.Background(), "weekly", "", "Work", false)
	require.NoError(t, err)
	require.NotNil(t, poster.req)
	assert.Equal(t, "today at 16:00", poster.req.DueString)
}

func TestRunner_Run_DryRun(t *testing.T) {
	t.Parallel()

	reportsDir := t.TempDir()
	promptsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "weekly.md"), []byte("Weekly\n{{ACTIVITY_XML}}"), 0600))

	poster := &fakePoster{}
	runner := &review.Runner{
		Fetcher:    &fakeFetcher{report: &activity.Report{Timeframe: "7d"}},
		LLM:        &fakeLLM{response: "report"},
		Poster:     poster,
		ReportsDir: reportsDir,
		PromptsDir: promptsDir,
		Now:        func() time.Time { return time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC) },
		Logger:     zerolog.New(nil),
	}

	_, err := runner.Run(context.Background(), "weekly", "7d", "Work", true)
	require.NoError(t, err)
	assert.Nil(t, poster.req)
}

func TestRunner_Run_DefaultTimeframe(t *testing.T) {
	t.Parallel()

	reportsDir := t.TempDir()
	promptsDir := t.TempDir()
	for _, mode := range []string{"daily", "weekly", "monthly", "quarterly"} {
		require.NoError(t, os.WriteFile(filepath.Join(promptsDir, mode+".md"), []byte(mode+"\n{{ACTIVITY_XML}}"), 0600))
	}

	tests := map[string]string{
		"daily":     "yesterday",
		"weekly":    "week",
		"monthly":   "month",
		"quarterly": "quarter",
	}

	for mode, expected := range tests {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ff := &fakeFetcher{report: &activity.Report{Timeframe: expected}}
			runner := &review.Runner{
				Fetcher:    ff,
				LLM:        &fakeLLM{response: "r"},
				ReportsDir: reportsDir,
				PromptsDir: promptsDir,
				Now:        func() time.Time { return time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC) },
				Logger:     zerolog.New(nil),
			}
			res, err := runner.Run(context.Background(), mode, "", "Work", true)
			require.NoError(t, err)
			assert.Equal(t, expected, res.Timeframe)
			assert.Equal(t, expected, ff.gotTimeframe)
		})
	}
}

func TestRunner_Run_InvalidMode(t *testing.T) {
	t.Parallel()

	runner := &review.Runner{
		Fetcher:    &fakeFetcher{},
		LLM:        &fakeLLM{},
		ReportsDir: t.TempDir(),
		PromptsDir: t.TempDir(),
		Now:        time.Now,
		Logger:     zerolog.New(nil),
	}

	_, err := runner.Run(context.Background(), "yearly", "", "Work", true)
	require.Error(t, err)
}

type fakeFetcher struct {
	report       *activity.Report
	gotTimeframe string
}

func (f *fakeFetcher) Fetch(
	_ context.Context,
	_ time.Time,
	timeframe, _ string,
) (*activity.Report, error) {
	f.gotTimeframe = timeframe
	if f.report == nil {
		return &activity.Report{Timeframe: timeframe}, nil
	}
	f.report.Timeframe = timeframe
	return f.report, nil
}

type fakeLLM struct {
	response string
}

func (f *fakeLLM) Run(_ context.Context, _, _ string) (string, error) {
	return f.response, nil
}

type fakePoster struct {
	req *ticket.CreateTaskRequest
}

func (f *fakePoster) CreateTask(_ context.Context, req ticket.CreateTaskRequest) (*ticket.Ticket, error) {
	f.req = &req
	return &ticket.Ticket{ID: "task-1", URL: "https://todoist.com/task-1"}, nil
}

func TestParseReportFrontmatter(t *testing.T) {
	t.Parallel()

	content := "---\neval_status: pending\nscore: 0\nreason: \"\"\n---\n# Title\nBody"
	fm, body, err := review.ParseReportFrontmatter(content)
	require.NoError(t, err)
	require.NotNil(t, fm)
	assert.Equal(t, "pending", fm.EvalStatus)
	assert.Equal(t, 0, fm.Score)
	assert.Empty(t, fm.Reason)
	assert.Equal(t, "# Title\nBody", body)

	contentNoFm := "# Title\nBody"
	fm, body, err = review.ParseReportFrontmatter(contentNoFm)
	require.NoError(t, err)
	assert.Nil(t, fm)
	assert.Equal(t, "# Title\nBody", body)

	contentEmptyBody := "---\neval_status: pending\nscore: 0\nreason: \"\"\n---\n"
	fm, body, err = review.ParseReportFrontmatter(contentEmptyBody)
	require.NoError(t, err)
	require.NotNil(t, fm)
	assert.Empty(t, body)
}

func TestStripFrontmatter(t *testing.T) {
	t.Parallel()

	content := "---\neval_status: pending\nscore: 0\nreason: \"\"\n---\n# Title\nBody"
	body := review.StripFrontmatter(content)
	assert.Equal(t, "# Title\nBody", body)

	contentNoFm := "# Title\nBody"
	body = review.StripFrontmatter(contentNoFm)
	assert.Equal(t, "# Title\nBody", body)
}

func TestRunner_Run_DeferredEval(t *testing.T) {
	t.Parallel()

	reportsDir := t.TempDir()
	promptsDir := t.TempDir()
	evalDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "daily.md"), []byte("Daily\n{{ACTIVITY_XML}}"), 0600))

	poster := &fakePoster{}
	runner := &review.Runner{
		Fetcher:    &fakeFetcher{report: &activity.Report{Timeframe: "1d"}},
		LLM:        &fakeLLM{response: "generated report"},
		Poster:     poster,
		ReportsDir: reportsDir,
		PromptsDir: promptsDir,
		Now:        func() time.Time { return time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC) },
		Logger:     zerolog.New(nil),
		EvalMode:   true,
		EvalDir:    evalDir,
	}

	_, err := runner.Run(context.Background(), "daily", "", "Work", false)
	require.NoError(t, err)

	entries, err := os.ReadDir(evalDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
}
