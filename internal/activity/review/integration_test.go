//go:build integration

package review_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/activity"
	review "github.com/kalverra/agents/internal/activity/review"
	"github.com/kalverra/agents/internal/eval"
	"github.com/kalverra/agents/internal/llm"
	"github.com/kalverra/agents/internal/ticket"
)

// capturePoster captures CreateTask calls without hitting Todoist
type capturePoster struct {
	lastReq *ticket.CreateTaskRequest
}

func (c *capturePoster) CreateTask(_ context.Context, req ticket.CreateTaskRequest) (*ticket.Ticket, error) {
	c.lastReq = &req
	return &ticket.Ticket{ID: "test-task", URL: "https://todoist.com/test"}, nil
}

// minimalReport returns a small activity.Report with enough data for LLM generation
func minimalReport(now time.Time) *activity.Report {
	return &activity.Report{
		Timeframe: "1d",
		Since:     now.Add(-24 * time.Hour),
		Until:     now,
		Project:   "Work",
		Todoist: []activity.TodoistTask{
			{
				ID:           "123",
				Title:        "Implement feature X",
				CompletedAt:  now.Add(-12 * time.Hour).Format(time.RFC3339),
				IsJiraLinked: true,
				JiraKey:      "PROJ-123",
			},
		},
		GitHub: activity.GitHubUserActivity{
			PRsOpened: []activity.PullRequestInfo{
				{
					Number:     42,
					Title:      "Add feature X",
					Repository: "kalverra/repo",
					URL:        "https://github.com/kalverra/repo/pull/42",
					JiraKey:    "PROJ-123",
				},
			},
		},
	}
}

// loadEnvSecrets loads ACTIVITY_OLLAMA_HOST and ACTIVITY_WINDOWS_MAC from env or .env
func loadEnvSecrets() (string, string) {
	host := os.Getenv("ACTIVITY_OLLAMA_HOST")
	mac := os.Getenv("ACTIVITY_WINDOWS_MAC")
	if host != "" && mac != "" {
		return host, mac
	}

	dirsToSearch := []string{"./", "../", "../../", "../../../", "../../../../"}
	if home, err := os.UserHomeDir(); err == nil {
		dirsToSearch = append(dirsToSearch, filepath.Join(home, ".agents"))
	}
	for _, dir := range dirsToSearch {
		envPath := filepath.Join(dir, ".env")
		data, err := os.ReadFile(envPath)
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			key := strings.TrimSpace(parts[0])
			val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			if key == "ACTIVITY_OLLAMA_HOST" && host == "" {
				host = val
			}
			if key == "ACTIVITY_WINDOWS_MAC" && mac == "" {
				mac = val
			}
		}
	}
	return host, mac
}

// findFiles returns file paths matching a glob pattern in dir
func findFiles(t *testing.T, dir, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	require.NoError(t, err)
	return matches
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestActivityEvalFlow_E2E(t *testing.T) {
	host, mac := loadEnvSecrets()
	if host == "" || mac == "" {
		t.Skip("ACTIVITY_OLLAMA_HOST and ACTIVITY_WINDOWS_MAC required")
	}

	reportsDir := t.TempDir()
	evalDir := t.TempDir()
	promptsDir := t.TempDir()

	promptContent := `Generate a report.
{{ACTIVITY_XML}}
`
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "daily.md"), []byte(promptContent), 0600))

	now := time.Now().Truncate(time.Second)

	model := envOrDefault("ACTIVITY_MODEL", "gemma4:31b")
	llmRunner := llm.NewRunner(llm.Config{
		Provider:   llm.ProviderAuto,
		Model:      model,
		OllamaHost: host,
		WindowsMAC: mac,
		WoLTimeout: 2 * time.Minute,
	})

	poster := &capturePoster{}

	// Phase 1: Generate report with eval mode
	runner := &review.Runner{
		Fetcher:    &fakeFetcher{report: minimalReport(now)},
		LLM:        llmRunner,
		Poster:     poster,
		ReportsDir: reportsDir,
		PromptsDir: promptsDir,
		EvalDir:    evalDir,
		EvalMode:   true,
		Now:        func() time.Time { return now },
		Logger:     zerolog.New(os.Stdout),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	res, err := runner.Run(ctx, "daily", "", "Work", false)
	require.NoError(t, err)

	require.NotNil(t, poster.lastReq)
	assert.Contains(t, poster.lastReq.Description, "vscode://file/")
	assert.Equal(t, "today at 10:00", poster.lastReq.DueString)

	// Assert: report file has eval_status: pending frontmatter
	reportBytes, err := os.ReadFile(res.ReportPath)
	require.NoError(t, err)
	reportStr := string(reportBytes)

	fm, body, err := review.ParseReportFrontmatter(reportStr)
	require.NoError(t, err)
	require.NotNil(t, fm)
	assert.Equal(t, "pending", fm.EvalStatus)
	assert.Equal(t, res.Generated, body)

	// Assert: pending_*.json exists
	pendingFiles := findFiles(t, evalDir, "pending_*.json")
	require.Len(t, pendingFiles, 1)
	pendingRecordPath := pendingFiles[0]

	// Phase 2: Simulate user editing
	newBody := "CORRECTED: " + body
	newFmStr := fmt.Sprintf("---\neval_status: reviewed\nscore: 4\nreason: \"Good but verbose\"\n---\n%s", newBody)
	err = os.WriteFile(res.ReportPath, []byte(newFmStr), 0600)
	require.NoError(t, err)

	// Phase 3: Run finalizer
	finalizer := &review.Finalizer{
		ReportsDir: reportsDir,
		EvalDir:    evalDir,
		Logger:     zerolog.New(os.Stdout),
	}

	finalCtx := context.Background()
	results, err := finalizer.Run(finalCtx)
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, 4, results[0].Score)
	assert.Equal(t, "Good but verbose", results[0].Reason)

	// Assert: live_*.json exists with CorrectedResponse containing "CORRECTED:"
	liveFiles := findFiles(t, evalDir, "live_*.json")
	require.Len(t, liveFiles, 1)

	liveRec, err := eval.LoadLiveFeedback(liveFiles[0])
	require.NoError(t, err)
	assert.Equal(t, 4, liveRec.UserScore)
	assert.Equal(t, "Good but verbose", liveRec.UserReason)
	assert.Contains(t, liveRec.CorrectedResponse, "CORRECTED:")

	// Assert: pending_*.json removed
	_, err = os.Stat(pendingRecordPath)
	assert.True(t, os.IsNotExist(err))

	// Assert: report frontmatter now eval_status: finalized
	reportBytes, err = os.ReadFile(res.ReportPath)
	require.NoError(t, err)
	fm, _, err = review.ParseReportFrontmatter(string(reportBytes))
	require.NoError(t, err)
	require.NotNil(t, fm)
	assert.Equal(t, "finalized", fm.EvalStatus)

	// Assert: running again returns empty results (idempotent)
	results, err = finalizer.Run(finalCtx)
	require.NoError(t, err)
	assert.Empty(t, results)
}
