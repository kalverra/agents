package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/eval"
)

func TestFinalizer_Run(t *testing.T) {
	t.Parallel()

	reportsDir := t.TempDir()
	evalDir := t.TempDir()
	logger := zerolog.Nop()

	f := &Finalizer{
		ReportsDir: reportsDir,
		EvalDir:    evalDir,
		Logger:     logger,
	}

	// 1. Create report file with eval_status: reviewed
	reportContent := `---
eval_status: reviewed
score: 4
reason: "good"
---
Corrected body text.`
	reportPath := filepath.Join(reportsDir, "2026-08-11-daily.md")
	err := os.WriteFile(reportPath, []byte(reportContent), 0600)
	require.NoError(t, err)

	// 2. Create matching pending eval record
	rec := eval.LiveFeedbackRecord{
		ID:       "activity-daily-20260811-090000",
		Mode:     "daily",
		Response: "Original response",
	}
	_, err = eval.SavePendingLiveFeedback(evalDir, rec)
	require.NoError(t, err)

	// 3. Run finalizer
	results, err := f.Run(context.Background())
	require.NoError(t, err)

	// 4. Assert
	require.Len(t, results, 1)
	assert.Equal(t, reportPath, results[0].ReportPath)
	assert.Equal(t, "activity-daily-20260811-090000", results[0].RecordID)
	assert.Equal(t, 4, results[0].Score)
	assert.Equal(t, "good", results[0].Reason)

	// Check live_*.json exists and pending is gone
	livePath := filepath.Join(evalDir, "live_activity-daily-20260811-090000.json")
	assert.FileExists(t, livePath)
	pendingPath := filepath.Join(evalDir, "pending_activity-daily-20260811-090000.json")
	assert.NoFileExists(t, pendingPath)

	liveRec, err := eval.LoadLiveFeedback(livePath)
	require.NoError(t, err)
	assert.Equal(t, 4, liveRec.UserScore)
	assert.Equal(t, "good", liveRec.UserReason)
	assert.Equal(t, "Corrected body text.", liveRec.CorrectedResponse)

	// Check report is updated to finalized
	updatedReportBytes, err := os.ReadFile(reportPath) //nolint: gosec // Test
	require.NoError(t, err)
	updatedReportContent := string(updatedReportBytes)
	assert.Contains(t, updatedReportContent, "eval_status: finalized")
	assert.NotContains(t, updatedReportContent, "eval_status: reviewed")
}

func TestFinalizer_Run_SkipsFinalized(t *testing.T) {
	t.Parallel()

	reportsDir := t.TempDir()
	evalDir := t.TempDir()
	logger := zerolog.Nop()

	f := &Finalizer{
		ReportsDir: reportsDir,
		EvalDir:    evalDir,
		Logger:     logger,
	}

	reportContent := `---
eval_status: finalized
score: 4
reason: "good"
---
Corrected body text.`
	reportPath := filepath.Join(reportsDir, "2026-08-11-daily.md")
	err := os.WriteFile(reportPath, []byte(reportContent), 0600)
	require.NoError(t, err)

	results, err := f.Run(context.Background())
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestFinalizer_Run_SkipsPending(t *testing.T) {
	t.Parallel()

	reportsDir := t.TempDir()
	evalDir := t.TempDir()
	logger := zerolog.Nop()

	f := &Finalizer{
		ReportsDir: reportsDir,
		EvalDir:    evalDir,
		Logger:     logger,
	}

	reportContent := `---
eval_status: pending
---
Corrected body text.`
	reportPath := filepath.Join(reportsDir, "2026-08-11-daily.md")
	err := os.WriteFile(reportPath, []byte(reportContent), 0600)
	require.NoError(t, err)

	results, err := f.Run(context.Background())
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestFinalizer_Run_Idempotent(t *testing.T) {
	t.Parallel()

	reportsDir := t.TempDir()
	evalDir := t.TempDir()
	logger := zerolog.Nop()

	f := &Finalizer{
		ReportsDir: reportsDir,
		EvalDir:    evalDir,
		Logger:     logger,
	}

	reportContent := `---
eval_status: reviewed
score: 4
reason: "good"
---
Corrected body text.`
	reportPath := filepath.Join(reportsDir, "2026-08-11-daily.md")
	err := os.WriteFile(reportPath, []byte(reportContent), 0600)
	require.NoError(t, err)

	rec := eval.LiveFeedbackRecord{
		ID:       "activity-daily-20260811-090000",
		Mode:     "daily",
		Response: "Original response",
	}
	_, err = eval.SavePendingLiveFeedback(evalDir, rec)
	require.NoError(t, err)

	results1, err := f.Run(context.Background())
	require.NoError(t, err)
	assert.Len(t, results1, 1)

	results2, err := f.Run(context.Background())
	require.NoError(t, err)
	assert.Empty(t, results2)
}
