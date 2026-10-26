package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:paralleltest // t.Setenv mutates process env
func TestLoadSave(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, err := Load()
	require.NoError(t, err)
	assert.Nil(t, cfg.Rules, "missing file means nothing chosen")

	empty := []string{}
	harnesses := []string{"claude"}
	cfg.Rules, cfg.Harnesses = &empty, &harnesses
	require.NoError(t, cfg.Save())

	got, err := Load()
	require.NoError(t, err)
	require.NotNil(t, got.Rules, "an empty choice survives a round trip")
	assert.Empty(t, *got.Rules)
	assert.Equal(t, []string{"claude"}, *got.Harnesses)
	assert.Nil(t, got.Skills)
}

//nolint:paralleltest // t.Setenv mutates process env
func TestLoad_BadYAML(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Dir(Path()), 0o750))
	require.NoError(t, os.WriteFile(Path(), []byte("rules: {"), 0o600))

	_, err := Load()
	assert.ErrorContains(t, err, "parsing")
}

func TestResolvedCommands(t *testing.T) {
	t.Parallel()

	assert.Equal(t, DefaultCommands.Allow, Permissions{}.ResolvedCommands().Allow)

	p := Permissions{Commands: &Commands{Allow: []string{" ls ", ""}}}
	assert.Equal(t, Commands{Allow: []string{"ls"}, Deny: []string{}}, p.ResolvedCommands())
}

//nolint:paralleltest // t.Setenv mutates process env
func TestResolvedDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := Permissions{Directories: []Directory{
		{Path: "~/Projects", Trust: TrustParent},
		{Path: "$HOME/go/", Access: AccessReadOnly},
		{Path: "~"},
		{Path: "  "},
	}}

	assert.Equal(t, []Directory{
		{Path: filepath.Join(home, "Projects"), Access: AccessReadWrite, Trust: TrustParent},
		{Path: filepath.Join(home, "go"), Access: AccessReadOnly, Trust: TrustNone},
		{Path: home, Access: AccessReadWrite, Trust: TrustNone},
	}, p.ResolvedDirectories())
}
