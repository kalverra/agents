package eval_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/eval"
)

func TestImportLiveFeedbackToCases(t *testing.T) {
	t.Parallel()

	liveDir := t.TempDir()
	casesDir := t.TempDir()

	rec := eval.LiveFeedbackRecord{
		ID:         "live-commit-123",
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Command:    "commit",
		FullPrompt: "Staged Diff Prompt",
		Response:   "fix(auth): fix token expiry check",
		UserScore:  5,
		UserReason: "Exact fix",
	}

	_, err := eval.SaveLiveFeedback(liveDir, rec)
	require.NoError(t, err)

	count, err := eval.ImportLiveFeedbackToCases(liveDir, casesDir)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	yamlPath := filepath.Join(casesDir, "live-commit-123.yaml")
	assert.FileExists(t, yamlPath)

	importedCases, err := eval.LoadCases(casesDir, "")
	require.NoError(t, err)
	require.Len(t, importedCases, 1)

	assert.Equal(t, "live-commit-123", importedCases[0].Name)
	assert.Equal(t, "fix(auth): fix token expiry check", importedCases[0].ReferenceAnswer)
}
