package implement

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shipped example must always parse and contain the documented modes.
func TestExampleConfigLoads(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "implement.example.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("example config not found at %s", path)
	}
	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	for _, mode := range []string{"standard", "intense", "extreme"} {
		_, err := cfg.Mode(mode)
		require.NoError(t, err, "mode %q missing from example", mode)
	}

	standard, err := cfg.Mode("standard")
	require.NoError(t, err)
	assert.Equal(t, "fast", standard.WorkerName)
	assert.Equal(t, HarnessAgy, standard.Worker.Harness)
	assert.Equal(t, "gemini-3.8-flash-medium", standard.Worker.Model)
	require.Len(t, standard.WorkerChoices, 2)
	assert.Equal(t, "fast", standard.WorkerChoices[0].Name)
	assert.Equal(t, "strong", standard.WorkerChoices[1].Name)

	intense, err := cfg.Mode("intense")
	require.NoError(t, err)
	assert.Equal(t, "strong", intense.WorkerName)
	assert.Equal(t, HarnessAgy, intense.Worker.Harness)
	assert.Equal(t, "gemini-3.8-flash-high", intense.Worker.Model)
	require.Len(t, intense.WorkerChoices, 3)
	assert.Equal(t, "fast", intense.WorkerChoices[0].Name)
	assert.Equal(t, "strong", intense.WorkerChoices[1].Name)
	assert.Equal(t, "opus", intense.WorkerChoices[2].Name)

	require.NotNil(t, intense.Reviewer)
	assert.Equal(t, "review-fast", intense.ReviewerName)
	assert.Equal(t, HarnessOpenCode, intense.Reviewer.Harness)
	assert.Equal(t, "fireworks-ai/accounts/fireworks/models/glm-5p3-flash", intense.Reviewer.Model)
	require.Len(t, intense.ReviewerChoices, 2)
	assert.Equal(t, "review-fast", intense.ReviewerChoices[0].Name)
	assert.Equal(t, "review-deep", intense.ReviewerChoices[1].Name)

	// Verification commands are declared at repo level, preserving order.
	require.Len(t, cfg.Verification.Commands(), 3)
	assert.Equal(t, "build", cfg.Verification.Commands()[0].Name)
	assert.Equal(t, "test", cfg.Verification.Commands()[1].Name)
	assert.Equal(t, "lint", cfg.Verification.Commands()[2].Name)

	// archive_on_done defaults to true when unset.
	assert.True(t, cfg.ArchiveSpecsOnDone())
}
