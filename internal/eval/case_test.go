package eval_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/eval"
)

func TestLoadCases_IncludesJSONLiveFeedback(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	rec := eval.LiveFeedbackRecord{
		ID:         "live-001",
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Command:    "commit",
		FullPrompt: "Commit Prompt",
		Response:   "feat: test commit",
		UserScore:  5,
		UserReason: "Good response",
	}

	_, err := eval.SaveLiveFeedback(tempDir, rec)
	require.NoError(t, err)

	cases, err := eval.LoadCases(tempDir, "")
	require.NoError(t, err)
	require.Len(t, cases, 1)

	assert.Equal(t, "live-001", cases[0].Name)
	assert.Equal(t, "feat: test commit", cases[0].ReferenceAnswer)
}
