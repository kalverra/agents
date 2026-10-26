package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test temp file
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func allow(file string, v string) Entry {
	return Entry{File: file, Path: []string{"permissions", "allow"}, Kind: KindList, Value: v}
}

func TestApply_OwnsOnlyWhatItAdds(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "settings.json")
	writeJSON(t, file, `{"theme":"dark","permissions":{"allow":["Bash(ls)"]}}`)

	owned, err := Apply([]Entry{allow(file, "Bash(ls)"), allow(file, "Bash(cat)")}, nil, nil)
	require.NoError(t, err)

	assert.Equal(t, []Entry{allow(file, "Bash(cat)")}, owned)
	m := readJSON(t, file)
	assert.Equal(t, "dark", m["theme"])
	assert.Equal(t, []any{"Bash(ls)", "Bash(cat)"}, m["permissions"].(map[string]any)["allow"])
}

func TestApply_ConvergesOnDesired(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "settings.json")
	owned, err := Apply([]Entry{allow(file, "a"), allow(file, "b")}, nil, nil)
	require.NoError(t, err)

	owned, err = Apply([]Entry{allow(file, "b"), allow(file, "c")}, owned, nil)
	require.NoError(t, err)

	assert.Equal(t, []Entry{allow(file, "b"), allow(file, "c")}, owned, "keeps owning b")
	assert.Equal(t, []any{"b", "c"}, readJSON(t, file)["permissions"].(map[string]any)["allow"])
}

func TestApply_NeverOverwritesUserKey(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "trusted.json")
	writeJSON(t, file, `{"/x":"TRUST_FOLDER"}`)
	key := func(path, v string) Entry { return Entry{File: file, Path: []string{path}, Kind: KindKey, Value: v} }

	owned, err := Apply([]Entry{key("/x", "TRUST_PARENT"), key("/y", "TRUST_PARENT")}, nil, nil)
	require.NoError(t, err)

	assert.Equal(t, []Entry{key("/y", "TRUST_PARENT")}, owned)
	assert.Equal(t, map[string]any{"/x": "TRUST_FOLDER", "/y": "TRUST_PARENT"}, readJSON(t, file))
}

func TestRemove_RestoresStartingState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	userFile := filepath.Join(dir, "user.json")
	writeJSON(t, userFile, `{"permissions":{"allow":["mine"]}}`)
	newFile := filepath.Join(dir, "new.json")

	owned, err := Apply([]Entry{
		allow(userFile, "mine"), allow(userFile, "ours"),
		{File: newFile, Path: []string{"sandbox", "filesystem", "allowRead"}, Kind: KindList, Value: "/x/**"},
		{File: newFile, Path: []string{"$schema"}, Kind: KindKey, Value: "s"},
	}, nil, nil)
	require.NoError(t, err)
	require.FileExists(t, newFile)

	require.NoError(t, Remove(owned))

	assert.Equal(t, map[string]any{"permissions": map[string]any{"allow": []any{"mine"}}}, readJSON(t, userFile))
	assert.NoFileExists(t, newFile, "a file left empty is deleted")
}

func TestRemove_SkipsChangedValues(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "s.json")
	entry := Entry{File: file, Path: []string{"k"}, Kind: KindKey, Value: "ours"}
	owned, err := Apply([]Entry{entry}, nil, nil)
	require.NoError(t, err)
	writeJSON(t, file, `{"k":"user changed it"}`)

	require.NoError(t, Remove(owned))
	assert.Equal(t, "user changed it", readJSON(t, file)["k"])
}

func TestApply_Migrate(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "s.json")
	writeJSON(t, file, `{"old":true}`)
	migrate := func(_ string, m map[string]any) {
		if _, ok := m["old"]; ok {
			delete(m, "old")
			m["new"] = true
		}
	}

	_, err := Apply([]Entry{allow(file, "a")}, nil, migrate)
	require.NoError(t, err)

	m := readJSON(t, file)
	assert.NotContains(t, m, "old")
	assert.Equal(t, true, m["new"])
}

func TestApply_WrongShapeErrors(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "s.json")
	writeJSON(t, file, `{"permissions":{"allow":"not a list"}}`)

	_, err := Apply([]Entry{allow(file, "a")}, nil, nil)
	assert.ErrorContains(t, err, "not a list")
}
