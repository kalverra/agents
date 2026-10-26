package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	userDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(userDir, "rg.md"), []byte("my rg"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(userDir, "mine.md"), []byte("my rule"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(userDir, "notes.txt"), []byte("ignored"), 0o600))

	got, err := Load(userDir)
	require.NoError(t, err)

	byName := map[string]Rule{}
	for _, r := range got {
		byName[r.Name] = r
	}
	assert.Equal(t, SourceBuiltin, byName["communication-style"].Source)
	assert.Equal(t, "my rg", byName["rg"].Body, "user rule replaces the built-in one")
	assert.Equal(t, filepath.Join(userDir, "mine.md"), byName["mine"].Source)
	assert.NotContains(t, byName, "notes")
	assert.IsIncreasing(t, Names(got))
}

func TestLoad_MissingUserDir(t *testing.T) {
	t.Parallel()

	got, err := Load(filepath.Join(t.TempDir(), "missing"))
	require.NoError(t, err)
	assert.Contains(t, Names(got), "rg")
}

func TestSelect(t *testing.T) {
	t.Parallel()

	all := []Rule{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	got, err := Select(all, []string{"c", "a"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, Names(got), "keeps rules order")

	_, err = Select(all, []string{"nope"})
	assert.ErrorContains(t, err, `unknown rule "nope"`)
}

func TestRender(t *testing.T) {
	t.Parallel()

	got := Render([]Rule{{Name: "a", Body: "### A\n\nbody a\n\n"}, {Name: "b", Body: "### B"}})
	assert.Contains(t, got, "### A\n\nbody a\n\n### B\n")
}

func TestLoad_PluginDirs(t *testing.T) {
	t.Parallel()

	userDir, first, second := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(dir, name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o600))
	}
	write(first, "shared", "first")
	write(second, "shared", "second")
	write(second, "rg", "plugin rg")
	write(second, "mine", "plugin mine")
	write(userDir, "mine", "user mine")

	got, err := Load(userDir, first, second)
	require.NoError(t, err)

	byName := map[string]Rule{}
	for _, r := range got {
		byName[r.Name] = r
	}
	assert.Equal(t, "first", byName["shared"].Body, "the earlier plugin wins")
	assert.Equal(t, SourceBuiltin, byName["rg"].Source, "a built-in rule beats a plugin rule")
	assert.Equal(t, "user mine", byName["mine"].Body, "a user rule beats a plugin rule")
}
