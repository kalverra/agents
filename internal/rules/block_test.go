package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test temp file
	require.NoError(t, err)
	return string(data)
}

func TestWriteBlock_NewFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sub", "CLAUDE.md")
	require.NoError(t, WriteBlock(path, "rules v1"))

	got := readFile(t, path)
	assert.Contains(t, got, BeginMarker)
	assert.Contains(t, got, "rules v1")
	assert.Contains(t, got, EndMarker)
}

func TestWriteBlock_KeepsUserTextAndReplacesInPlace(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "AGENTS.md")
	require.NoError(t, os.WriteFile(path, []byte("# Mine\n\nabove\n"), 0o600))

	require.NoError(t, WriteBlock(path, "rules v1"))
	got := readFile(t, path)
	require.NoError(t, os.WriteFile(path, []byte(got+"\nbelow\n"), 0o600))

	require.NoError(t, WriteBlock(path, "rules v2"))
	got = readFile(t, path)
	assert.NotContains(t, got, "rules v1")
	assert.Contains(t, got, "rules v2")
	assert.Regexp(t, `(?s)^# Mine\n\nabove\n\n<!-- agents:begin -->.*rules v2.*<!-- agents:end -->\n\nbelow\n$`, got)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "keeps the file mode")
}

func TestWriteBlock_ReplacesSymlinkWithoutTouchingTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "GLOBAL_AGENTS.md")
	link := filepath.Join(dir, "GEMINI.md")
	require.NoError(t, os.WriteFile(target, []byte("old global"), 0o600))
	require.NoError(t, os.Symlink(target, link))

	require.NoError(t, WriteBlock(link, "rules"))

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular())
	assert.NotContains(t, readFile(t, link), "old global")
	assert.Equal(t, "old global", readFile(t, target))
}

func TestRemoveBlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	userPath := filepath.Join(dir, "user.md")
	require.NoError(t, os.WriteFile(userPath, []byte("above\n"), 0o600))
	require.NoError(t, WriteBlock(userPath, "rules"))
	require.NoError(t, RemoveBlock(userPath))
	assert.Equal(t, "above\n", readFile(t, userPath), "restores the user's text")

	onlyBlock := filepath.Join(dir, "only.md")
	require.NoError(t, WriteBlock(onlyBlock, "rules"))
	require.NoError(t, RemoveBlock(onlyBlock))
	assert.NoFileExists(t, onlyBlock, "a file left empty is deleted")

	require.NoError(t, RemoveBlock(filepath.Join(dir, "missing.md")))

	noBlock := filepath.Join(dir, "plain.md")
	require.NoError(t, os.WriteFile(noBlock, []byte("plain"), 0o600))
	require.NoError(t, RemoveBlock(noBlock))
	assert.Equal(t, "plain", readFile(t, noBlock))
}

func TestBlock_BadMarkers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for name, content := range map[string]string{
		"end-first.md": EndMarker + "\n" + BeginMarker + "\n",
		"no-end.md":    BeginMarker + "\n",
		"two.md":       BeginMarker + EndMarker + BeginMarker + EndMarker,
	} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		require.Error(t, WriteBlock(path, "x"), name)
		assert.Error(t, RemoveBlock(path), name)
	}
}
