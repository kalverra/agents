package commit_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/commit"
	"github.com/kalverra/agents/internal/eval"
)

func TestRunner_Run_EvalModeCapturesFeedback(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello"), 0600))
	runGit(t, dir, "add", "foo.txt")

	evalDir := t.TempDir()
	inReader := bytes.NewBufferString("5\nLooks great and concise\n")
	outWriter := &bytes.Buffer{}

	r := &commit.Runner{
		LLM: &mockLLM{response: "feat(foo): add foo.txt"},
	}

	res, err := r.Run(context.Background(), commit.Options{
		Dir:            dir,
		EvalMode:       true,
		EvalDir:        evalDir,
		FeedbackReader: inReader,
		FeedbackWriter: outWriter,
		Provider:       "agy",
		Model:          "gemini-3.6-flash-high",
	})
	require.NoError(t, err)
	assert.Equal(t, "feat(foo): add foo.txt", res.Message)

	entries, err := os.ReadDir(evalDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	filePath := filepath.Join(evalDir, entries[0].Name())
	rec, err := eval.LoadLiveFeedback(filePath)
	require.NoError(t, err)

	assert.Equal(t, "commit", rec.Command)
	assert.Equal(t, 5, rec.UserScore)
	assert.Equal(t, "Looks great and concise", rec.UserReason)
	assert.Equal(t, "feat(foo): add foo.txt", rec.Response)
	assert.Equal(t, "gemini-3.6-flash-high", rec.Model)
	assert.Equal(t, "agy", rec.Provider)
}

func TestRunner_Run_EvalModePendingAndFinalize(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bar.txt"), []byte("bar content"), 0600))
	runGit(t, dir, "add", "bar.txt")

	msgFile := filepath.Join(dir, ".git", "COMMIT_EDITMSG")
	evalDir := filepath.Join(dir, "eval", "live_feedback")
	inReader := bytes.NewBufferString("4\nGood start, added details in editor\n")
	outWriter := &bytes.Buffer{}

	r := &commit.Runner{
		LLM: &mockLLM{response: "feat: add bar.txt"},
	}

	res, err := r.Run(context.Background(), commit.Options{
		Dir:            dir,
		CommitMsgFile:  msgFile,
		EvalMode:       true,
		EvalDir:        evalDir,
		FeedbackReader: inReader,
		FeedbackWriter: outWriter,
		Provider:       "agy",
		Model:          "gemini-3.6-flash-high",
	})
	require.NoError(t, err)
	assert.Equal(t, "feat: add bar.txt", res.Message)

	// Simulate user editing commit message in editor
	editedMsg := "feat(bar): add bar.txt with detailed description\n\n- add bar.txt file"
	require.NoError(t, os.WriteFile(msgFile, []byte(editedMsg), 0600))

	// Finalize eval after editor closes
	finalRec, err := commit.FinalizeEval(msgFile, evalDir)
	require.NoError(t, err)

	assert.Equal(t, "feat: add bar.txt", finalRec.Response)
	assert.Equal(t, editedMsg, finalRec.CorrectedResponse)
	assert.Equal(t, 4, finalRec.UserScore)
	assert.Equal(t, "Good start, added details in editor", finalRec.UserReason)
}
