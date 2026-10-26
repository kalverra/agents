package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/settings"
)

// values returns the values of the entries at file and path.
func values(entries []settings.Entry, file string, path ...string) []any {
	var out []any
	for _, e := range entries {
		if e.File == file && assert.ObjectsAreEqual(path, e.Path) {
			out = append(out, e.Value)
		}
	}
	return out
}

func perms(dirs ...config.Directory) config.Permissions {
	return config.Permissions{
		Commands:    &config.Commands{Allow: []string{"git status", "rtk"}, Deny: []string{"rm"}},
		Directories: dirs,
	}
}

//nolint:paralleltest // t.Setenv mutates process env
func TestClaude_PermissionEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := Claude{}

	got := c.PermissionEntries(perms(
		config.Directory{Path: "/rw", Access: config.AccessReadWrite},
		config.Directory{Path: "/ro", Access: config.AccessReadOnly},
	))

	file := c.SettingsPath()
	assert.Equal(t, []any{
		"Bash(git status)", "Bash(git status *)", "Bash(rtk git status)", "Bash(rtk git status *)",
		"Bash(rtk)", "Bash(rtk *)",
		"Read(//rw/**)", "Edit(//rw/**)", "Read(//ro/**)",
	}, values(got, file, "permissions", "allow"))
	assert.Equal(t, []any{"Bash(rm)", "Bash(rm *)", "Bash(rtk rm)", "Bash(rtk rm *)"},
		values(got, file, "permissions", "deny"))
	assert.Equal(t, []any{"/rw", "/ro"}, values(got, file, "permissions", "additionalDirectories"))
	assert.Equal(t, []any{"/rw/**"}, values(got, file, "sandbox", "filesystem", "allowWrite"))
	assert.Equal(t, []any{"/rw/**", "/ro/**"}, values(got, file, "sandbox", "filesystem", "allowRead"))
}

//nolint:paralleltest // t.Setenv mutates process env
func TestAntigravity_PermissionEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTIGRAVITY_CONFIG_DIR", "")
	a := Antigravity{}
	p := perms(
		config.Directory{Path: "/p", Access: config.AccessReadWrite, Trust: config.TrustParent},
		config.Directory{Path: "/r", Access: config.AccessReadOnly, Trust: config.TrustNone},
	)

	got := a.PermissionEntries(p)
	cli := a.CLISettingsPath()
	assert.Equal(t, []any{
		"command(git status)", "command(rtk git status)", "command(rtk)", "write_file(/p)", "read_file(/r)",
	}, values(got, cli, "permissions", "allow"))
	assert.Equal(t, []any{"/p", "/r"}, values(got, cli, "sandbox", "allowedDirectories"))
	assert.Equal(t, []any{"/p"}, values(got, cli, "trustedWorkspaces"))
	assert.Equal(t, []any{"TRUST_PARENT"}, values(got, a.TrustedFoldersPath(), "/p"))
	assert.Empty(t, values(got, a.IDESettingsPath(), "permissions", "allow"), "no IDE settings file yet")

	require.NoError(t, os.MkdirAll(filepath.Dir(a.IDESettingsPath()), 0o750))
	require.NoError(t, os.WriteFile(a.IDESettingsPath(), []byte("{}"), 0o600))
	got = a.PermissionEntries(p)
	assert.Contains(t, values(got, a.IDESettingsPath(), "permissions", "allow"), "write_file(/p)")
}

//nolint:paralleltest // t.Setenv mutates process env
func TestOpenCode_PermissionEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := OpenCode{}

	got := o.PermissionEntries(perms(config.Directory{Path: "/d"}))
	file := o.SettingsPath()

	assert.Equal(t, []any{openCodeSchema}, values(got, file, "$schema"))
	rules := values(got, file, "permissions")
	assert.Contains(t, rules, openCodeRule("shell", "rtk git status *", "allow"))
	assert.Contains(t, rules, openCodeRule("shell", "rtk", "allow"))
	assert.NotContains(t, rules, openCodeRule("shell", "rtk rtk", "allow"))
	assert.Contains(t, rules, openCodeRule("shell", "rm *", "deny"))
	assert.Contains(t, rules, openCodeRule("external_directory", "/d/**", "allow"))

	assert.Nil(t, o.PermissionEntries(config.Permissions{Commands: &config.Commands{}}),
		"no permissions means no $schema either")
}

//nolint:paralleltest // t.Setenv mutates process env
func TestOpenCode_MigrateSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := OpenCode{}
	m := map[string]any{
		"permission":  map[string]any{"bash": map[string]any{"ls": "allow"}, "edit": "ask"},
		"permissions": []any{openCodeRule("shell", "rm", "deny")},
	}

	o.MigrateSettings("/other/file.json", m)
	assert.Contains(t, m, "permission", "only touches opencode.json")

	o.MigrateSettings(o.SettingsPath(), m)
	assert.NotContains(t, m, "permission")
	assert.Equal(t, []any{
		openCodeRule("shell", "ls", "allow"),
		openCodeRule("edit", "*", "ask"),
		openCodeRule("shell", "rm", "deny"),
	}, m["permissions"])
}

func TestByNames(t *testing.T) {
	t.Parallel()

	got, err := ByNames([]string{"opencode", "claude"})
	require.NoError(t, err)
	assert.Equal(t, []string{"claude", "opencode"}, Names(got))

	_, err = ByNames([]string{"cursor"})
	assert.ErrorContains(t, err, `unknown harness "cursor"`)
}
