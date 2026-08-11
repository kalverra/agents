package eval_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/eval"
)

func TestComputePromptHash(t *testing.T) {
	t.Parallel()

	promptBody := "System prompt for commit generation"
	hash1 := eval.ComputePromptHash(promptBody)
	hash2 := eval.ComputePromptHash(promptBody)
	require.NotEmpty(t, hash1)
	assert.Equal(t, hash1, hash2)

	diffHash := eval.ComputePromptHash("Different prompt content")
	assert.NotEqual(t, hash1, diffHash)
}

func TestSaveAndLoadLiveFeedback(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	rec := eval.LiveFeedbackRecord{
		ID:            "live-commit-20260810-152010-a1b2c3",
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Command:       "commit",
		Mode:          "",
		FullPrompt:    "System prompt text\n\nDiff data",
		PromptFile:    "prompts/commit/commit.md",
		PromptHash:    "a1b2c3d4e5f6",
		PromptVersion: "abc1234",
		Model:         "gemini-3.6-flash-high",
		Provider:      "agy",
		Response:      "feat(eval): add live feedback capture",
		UserScore:     5,
		UserReason:    "Accurate Conventional Commit format",
		Metadata: map[string]string{
			"branch":        "main",
			"files_changed": "3",
		},
	}

	savedPath, err := eval.SaveLiveFeedback(tempDir, rec)
	require.NoError(t, err)
	assert.Equal(t, tempDir, filepath.Dir(savedPath))

	transcriptPath := filepath.Join(tempDir, "live_"+rec.ID+".transcript.txt")
	assert.FileExists(t, transcriptPath)
	transcriptBytes, err := os.ReadFile(transcriptPath) //nolint:gosec
	require.NoError(t, err)
	assert.Contains(t, string(transcriptBytes), rec.FullPrompt)
	assert.Contains(t, string(transcriptBytes), rec.Response)

	loaded, err := eval.LoadLiveFeedback(savedPath)
	require.NoError(t, err)

	assert.Equal(t, rec.ID, loaded.ID)
	assert.Equal(t, rec.Command, loaded.Command)
	assert.Equal(t, rec.PromptHash, loaded.PromptHash)
	assert.Equal(t, rec.UserScore, loaded.UserScore)
	assert.Equal(t, rec.UserReason, loaded.UserReason)
	assert.Equal(t, "main", loaded.Metadata["branch"])
}

func TestPromptUserFeedback(t *testing.T) {
	t.Parallel()

	input := "4\nGood summary but missed one ticket\n"
	inReader := bytes.NewBufferString(input)
	outWriter := &bytes.Buffer{}

	score, reason, err := eval.PromptUserFeedback(inReader, outWriter)
	require.NoError(t, err)

	assert.Equal(t, 4, score)
	assert.Equal(t, "Good summary but missed one ticket", reason)
	assert.Contains(t, outWriter.String(), "Score response (1-5, 1=bad, 5=good): ")
}

func TestConvertLiveToCase(t *testing.T) {
	t.Parallel()

	rec := eval.LiveFeedbackRecord{
		ID:         "live-commit-001",
		Command:    "commit",
		FullPrompt: "Commit prompt content",
		Response:   "feat: add feature",
		UserScore:  5,
		UserReason: "Great commit msg",
		PromptFile: "prompts/commit/commit.md",
		Model:      "gemini-3.6-flash-high",
	}

	c := eval.ConvertLiveToCase(&rec)
	assert.Equal(t, rec.ID, c.Name)
	assert.Equal(t, rec.Response, c.ReferenceAnswer)
	assert.Equal(t, rec.FullPrompt, c.UserMessage)
}

func TestSaveAndFinalizeLiveFeedback(t *testing.T) {
	t.Parallel()

	evalDir := t.TempDir()
	commitMsgFile := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	require.NoError(t, os.WriteFile(commitMsgFile, []byte("feat: user edited commit message"), 0600))

	rec := eval.LiveFeedbackRecord{
		ID:         "commit-20260810-160000",
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Command:    "commit",
		Response:   "feat: original llm message",
		UserScore:  4,
		UserReason: "Needs minor edit",
	}

	pendingPath, err := eval.SavePendingLiveFeedback(evalDir, rec)
	require.NoError(t, err)
	assert.FileExists(t, pendingPath)

	finalRec, err := eval.FinalizeLiveFeedback(evalDir, pendingPath, commitMsgFile)
	require.NoError(t, err)

	assert.Equal(t, "feat: original llm message", finalRec.Response)
	assert.Equal(t, "feat: user edited commit message", finalRec.CorrectedResponse)
	assert.NoFileExists(t, pendingPath)

	entries, err := os.ReadDir(evalDir)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	var jsonPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			jsonPath = filepath.Join(evalDir, e.Name())
			break
		}
	}
	require.NotEmpty(t, jsonPath)

	saved, err := eval.LoadLiveFeedback(jsonPath)
	require.NoError(t, err)
	assert.Equal(t, "feat: user edited commit message", saved.CorrectedResponse)
}
