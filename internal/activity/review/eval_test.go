package review_test

import (
	"bytes"
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
	"github.com/kalverra/agents/internal/eval"
)

func TestRunner_Run_EvalModeCapturesFeedback(t *testing.T) {
	t.Parallel()

	reportsDir := t.TempDir()
	promptsDir := t.TempDir()
	evalDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "daily.md"), []byte("Daily\n{{ACTIVITY_XML}}"), 0600))

	inReader := bytes.NewBufferString("4\nDetailed but a bit verbose\n")
	outWriter := &bytes.Buffer{}

	runner := &review.Runner{
		Fetcher:        &fakeFetcher{report: &activity.Report{Timeframe: "1d"}},
		LLM:            &fakeLLM{response: "daily activity summary"},
		ReportsDir:     reportsDir,
		PromptsDir:     promptsDir,
		Now:            func() time.Time { return time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC) },
		Logger:         zerolog.New(nil),
		EvalMode:       true,
		EvalDir:        evalDir,
		FeedbackReader: inReader,
		FeedbackWriter: outWriter,
		Provider:       "agy",
		Model:          "gemini-3.6-flash-high",
	}

	res, err := runner.Run(context.Background(), "daily", "", "Work", true)
	require.NoError(t, err)
	assert.Equal(t, "daily activity summary", res.Generated)

	entries, err := os.ReadDir(evalDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	rec, err := eval.LoadLiveFeedback(filepath.Join(evalDir, entries[0].Name()))
	require.NoError(t, err)

	assert.Equal(t, "activity", rec.Command)
	assert.Equal(t, "daily", rec.Mode)
	assert.Equal(t, 4, rec.UserScore)
	assert.Equal(t, "Detailed but a bit verbose", rec.UserReason)
	assert.Equal(t, "daily activity summary", rec.Response)
	assert.Equal(t, "gemini-3.6-flash-high", rec.Model)
	assert.Equal(t, "agy", rec.Provider)
}
