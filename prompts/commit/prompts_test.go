package commit_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/prompts/commit"
)

func TestLoadDefault(t *testing.T) {
	t.Parallel()

	p, err := commit.LoadDefault()
	require.NoError(t, err)
	assert.Contains(t, p.Body, "{{COMMIT_XML}}")
	assert.Contains(t, p.Body, "Conventional Commits")
}

func TestLoadOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	customPath := filepath.Join(dir, "commit.md")
	require.NoError(t, os.WriteFile(customPath, []byte("custom commit prompt\n{{COMMIT_XML}}\n"), 0600))

	p, err := commit.Load(dir)
	require.NoError(t, err)
	assert.Contains(t, p.Body, "custom commit prompt")
}

func TestPrompt_Render(t *testing.T) {
	t.Parallel()

	p := commit.Prompt{Body: "prefix {{COMMIT_XML}} suffix"}
	got := p.Render("<commit_context/>")
	assert.Equal(t, "prefix <commit_context/> suffix", got)
}
