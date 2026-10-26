package plugins

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/config"
)

// writePlugin makes a plugin dir with the given plugin.json (none when empty)
// and executables bin/agents-<cmd> for each cmd.
func writePlugin(t *testing.T, dir, manifest string, cmds ...string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o750))
	if manifest != "" {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), []byte(manifest), 0o600))
	}
	for _, c := range cmds {
		//nolint:gosec // test command must be executable
		require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", "agents-"+c), []byte("#!/bin/sh\n"), 0o700))
	}
	return dir
}

func TestOpen(t *testing.T) {
	t.Parallel()

	dir := writePlugin(t, t.TempDir(), `{
		"name": "mine",
		"description": "My tools",
		"metadata": {"agents-build": " make ", "agents-commands": "hello=Say hi; bye = Say bye"}
	}`, "hello", "bye")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", "agents-notexec"), []byte("x"), 0o600))
	other := filepath.Join(dir, "bin", "other")
	require.NoError(t, os.WriteFile(other, []byte("#!/bin/sh\n"), 0o700)) //nolint:gosec // must be executable

	p, err := Open(config.Plugin{Name: "mine", Path: dir})
	require.NoError(t, err)
	assert.Equal(t, "mine", p.Name)
	assert.Equal(t, "My tools", p.Description)
	assert.Equal(t, "make", p.Build)

	cmds, err := p.Commands()
	require.NoError(t, err)
	require.Len(t, cmds, 2, "only executable agents-* files are commands")
	assert.Equal(t, "bye", cmds[0].Name)
	assert.Equal(t, "Say bye", cmds[0].Description)
	assert.Equal(t, "hello", cmds[1].Name)
	assert.Equal(t, filepath.Join(dir, "bin", "agents-hello"), cmds[1].Path)
}

func TestRead_NameFromDir(t *testing.T) {
	t.Parallel()

	p, err := read(writePlugin(t, filepath.Join(t.TempDir(), "plain"), ""))
	require.NoError(t, err)
	assert.Equal(t, "plain", p.Name)
	assert.Empty(t, p.Build)

	_, err = read(writePlugin(t, t.TempDir(), `{"name": "../escape"}`))
	require.ErrorContains(t, err, "invalid plugin name")
}

func TestLoad_SkipsBroken(t *testing.T) {
	t.Parallel()

	good := writePlugin(t, filepath.Join(t.TempDir(), "good"), "")
	got, err := Load([]config.Plugin{
		{Name: "missing", Path: filepath.Join(t.TempDir(), "missing")},
		{Name: "good", Path: good},
	})
	require.ErrorContains(t, err, "plugin missing")
	require.Len(t, got, 1)
	assert.Equal(t, "good", got[0].Name)
}

func TestFindAndAllCommands_FirstPluginWins(t *testing.T) {
	t.Parallel()

	first := writePlugin(t, filepath.Join(t.TempDir(), "first"), "", "shared", "a")
	second := writePlugin(t, filepath.Join(t.TempDir(), "second"), "", "shared", "b")
	ps, err := Load([]config.Plugin{{Name: "first", Path: first}, {Name: "second", Path: second}})
	require.NoError(t, err)

	c, ok := Find(ps, "shared")
	require.True(t, ok)
	assert.Equal(t, "first", c.Plugin)
	assert.Equal(t, first, c.Owner().Root)

	c, ok = Find(ps, "b")
	require.True(t, ok)
	assert.Equal(t, "second", c.Owner().Name)

	for _, name := range []string{"missing", "", "../first/bin/agents-a"} {
		_, ok = Find(ps, name)
		assert.False(t, ok, name)
	}

	cmds, shadowed, err := AllCommands(ps)
	require.NoError(t, err)
	names := []string{}
	for _, c := range cmds {
		names = append(names, c.Plugin+"/"+c.Name)
	}
	assert.Equal(t, []string{"first/a", "second/b", "first/shared"}, names)
	require.Len(t, shadowed, 1)
	assert.Equal(t, "second", shadowed[0].Plugin)
}

func TestParseSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tests := []struct {
		arg     string
		want    config.Plugin
		wantErr bool
	}{
		{arg: dir, want: config.Plugin{Path: dir}},
		{arg: "git@github.com:me/plug.git", want: config.Plugin{URL: "git@github.com:me/plug.git"}},
		{arg: "git@github.com:me/plug.git@v1.2", want: config.Plugin{URL: "git@github.com:me/plug.git", Ref: "v1.2"}},
		{arg: "https://github.com/me/plug@main", want: config.Plugin{URL: "https://github.com/me/plug", Ref: "main"}},
		{arg: "https://github.com/me/plug", want: config.Plugin{URL: "https://github.com/me/plug"}},
		{arg: "https://github.com/me/plug@", wantErr: true},
		{arg: "not-a-dir", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseSource(tt.arg)
		if tt.wantErr {
			require.Error(t, err, tt.arg)
			continue
		}
		require.NoError(t, err, tt.arg)
		assert.Equal(t, tt.want, got, tt.arg)
	}
}

func TestManager_AddLocal(t *testing.T) {
	t.Parallel()

	dir := writePlugin(t, t.TempDir(), `{"name": "mine", "metadata": {"agents-build": "touch built"}}`)
	var confirmed []string
	m := &Manager{Config: &config.Config{}, ConfirmBuild: func(p Plugin) error {
		confirmed = append(confirmed, p.Name)
		return nil
	}}

	p, err := m.Add(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"mine"}, confirmed)
	assert.FileExists(t, filepath.Join(dir, "built"), "the build runs in the plugin dir")
	assert.Equal(t, []config.Plugin{{Name: "mine", Path: dir}}, m.Config.Plugins)
	assert.Equal(t, dir, p.Root)

	_, err = m.Add(dir)
	require.ErrorContains(t, err, "already added")
	assert.Len(t, m.Config.Plugins, 1)
}

func TestManager_AddLocal_BuildDeclined(t *testing.T) {
	t.Parallel()

	dir := writePlugin(t, t.TempDir(), `{"name": "mine", "metadata": {"agents-build": "touch built"}}`)
	m := &Manager{Config: &config.Config{}, ConfirmBuild: func(Plugin) error { return assert.AnError }}

	_, err := m.Add(dir)
	require.ErrorIs(t, err, assert.AnError)
	assert.Empty(t, m.Config.Plugins)
	assert.NoFileExists(t, filepath.Join(dir, "built"))
	assert.DirExists(t, dir, "a local plugin is never deleted")
}

// git runs git in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitArgs := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", gitArgs...).CombinedOutput() //nolint:gosec // G204: test args
	require.NoError(t, err, string(out))
}

// gitRepo makes a git repo holding a plugin called name and returns its
// file:// URL.
func gitRepo(t *testing.T, name string) (dir, url string) {
	t.Helper()
	dir = writePlugin(t, t.TempDir(), `{"name": "`+name+`"}`, "hello")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "commit", "-qm", "init"},
	} {
		git(t, dir, args...)
	}
	return dir, "file://" + dir
}

// Not parallel: it sets XDG_DATA_HOME.
func TestManager_GitLifecycle(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	src, url := gitRepo(t, "remote")
	m := &Manager{Config: &config.Config{}, ConfirmBuild: func(Plugin) error { return nil }}

	p, err := m.Add(url)
	require.NoError(t, err)
	clone := filepath.Join(ClonesDir(), "remote")
	assert.Equal(t, clone, p.Root)
	assert.Equal(t, []config.Plugin{{Name: "remote", URL: url}}, m.Config.Plugins)
	entries, err := os.ReadDir(ClonesDir())
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the temp clone dir is cleaned up")

	_, err = m.Add(url)
	require.ErrorContains(t, err, "already added")

	// A new command upstream arrives with update.
	writePlugin(t, src, "", "new")
	git(t, src, "add", ".")
	git(t, src, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "commit", "-qm", "new")
	_, err = m.Update(nil)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(clone, "bin", "agents-new"))

	ref, err := m.Remove("remote")
	require.NoError(t, err)
	assert.Equal(t, "remote", ref.Name)
	assert.Empty(t, m.Config.Plugins)
	assert.NoDirExists(t, clone)

	_, err = m.Remove("remote")
	require.Error(t, err)
}
