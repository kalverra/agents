package activity_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/prompts/activity"
)

func TestLoadDefault(t *testing.T) {
	t.Parallel()

	p, err := activity.LoadDefault("daily")
	require.NoError(t, err)
	assert.Equal(t, "daily", p.Mode)
	assert.Contains(t, p.Body, "{{ACTIVITY_XML}}")
	assert.Contains(t, p.Body, "standup")

	p, err = activity.LoadDefault("weekly")
	require.NoError(t, err)
	assert.Equal(t, "weekly", p.Mode)
	assert.Contains(t, p.Body, "Epic")

	_, err = activity.LoadDefault("nope")
	require.Error(t, err)
}

func TestLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	customPath := filepath.Join(dir, "daily.md")
	require.NoError(t, os.WriteFile(customPath, []byte("custom daily prompt\n{{ACTIVITY_XML}}\n"), 0600))

	p, err := activity.Load("daily", dir)
	require.NoError(t, err)
	assert.Contains(t, p.Body, "custom daily prompt")
	assert.NotContains(t, p.Body, "standup")

	p, err = activity.Load("weekly", dir)
	require.NoError(t, err)
	assert.Equal(t, "weekly", p.Mode)
}

func TestPrompt_Render(t *testing.T) {
	t.Parallel()

	p := activity.Prompt{Mode: "daily", Body: "before {{ACTIVITY_XML}} after"}
	got := p.Render("<x/>")
	assert.Equal(t, "before <x/> after", got)
}
