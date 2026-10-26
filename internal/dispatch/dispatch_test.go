package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/pkg/plugin"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		argv   []string
		want   Invocation
		wantOK bool
	}{
		{
			name:   "plain",
			argv:   []string{"hello", "x", "--y"},
			want:   Invocation{Name: "hello", Args: []string{"x", "--y"}},
			wantOK: true,
		},
		{
			name: "root flags before name, short and long",
			argv: []string{"-a", "--log-level", "debug", "hello", "x"},
			want: Invocation{
				Name: "hello", Args: []string{"x"},
				AIOutput: true, AIOutputSet: true, LogLevel: "debug",
			},
			wantOK: true,
		},
		{
			name: "long root flags after name are taken out",
			argv: []string{"ticket", "status", "--ai-output", "--log-level=warn"},
			want: Invocation{
				Name: "ticket", Args: []string{"status"},
				AIOutput: true, AIOutputSet: true, LogLevel: "warn",
			},
			wantOK: true,
		},
		{
			name:   "short flags after name belong to the plugin",
			argv:   []string{"hello", "-a", "-l", "x"},
			want:   Invocation{Name: "hello", Args: []string{"-a", "-l", "x"}},
			wantOK: true,
		},
		{
			name:   "nothing after -- is touched",
			argv:   []string{"hello", "--", "--ai-output"},
			want:   Invocation{Name: "hello", Args: []string{"--", "--ai-output"}},
			wantOK: true,
		},
		{
			name:   "explicit false",
			argv:   []string{"--ai-output=false", "hello"},
			want:   Invocation{Name: "hello", AIOutputSet: true},
			wantOK: true,
		},
		{name: "empty", argv: nil},
		{name: "only root flags", argv: []string{"--ai-output"}},
		{name: "unknown flag before name", argv: []string{"--help", "hello"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := Parse(tt.argv)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

// writeScript puts an executable agents-<name> shell script in a new dir and
// returns the dir.
func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, Prefix+name)
	script := []byte("#!/bin/sh\n" + body)
	require.NoError(t, os.WriteFile(path, script, 0o700)) //nolint:gosec // test script must be executable
	return dir
}

//nolint:paralleltest // t.Setenv mutates PATH
func TestLookup(t *testing.T) {
	dir := writeScript(t, "hello", "exit 0\n")
	t.Setenv("PATH", dir)

	path, ok := Lookup("hello")
	require.True(t, ok)
	assert.Equal(t, filepath.Join(dir, "agents-hello"), path)

	_, ok = Lookup("missing")
	assert.False(t, ok)
	_, ok = Lookup("../hello")
	assert.False(t, ok, "names with path separators are rejected")
}

//nolint:paralleltest // Run changes process-wide signal handling
func TestRun(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	dir := writeScript(t, "hello", `printf '%s\n' "$@" > "`+out+`"
env | grep '^AGENTS_' | sort >> "`+out+`"
exit 7
`)
	t.Setenv(plugin.EnvPluginRoot, "/stale")

	code, err := Run(filepath.Join(dir, "agents-hello"), []string{"x", "--y"}, plugin.Env{
		AIOutput:  true,
		LogLevel:  "debug",
		AgentsBin: "/bin/agents",
	})
	require.NoError(t, err)
	assert.Equal(t, 7, code, "exit code passes through")

	data, err := os.ReadFile(out) //nolint:gosec // test file
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Equal(t, []string{
		"x", "--y",
		"AGENTS_AI_OUTPUT=1",
		"AGENTS_BIN=/bin/agents",
		"AGENTS_LOG_LEVEL=debug",
	}, lines)
}

//nolint:paralleltest // Run changes process-wide signal handling
func TestRunSignaledExitCode(t *testing.T) {
	dir := writeScript(t, "die", "kill -TERM $$\n")
	code, err := Run(filepath.Join(dir, "agents-die"), nil, plugin.Env{})
	require.NoError(t, err)
	assert.Equal(t, 128+15, code)
}

func TestRunMissingExecutable(t *testing.T) {
	t.Parallel()
	code, err := Run(filepath.Join(t.TempDir(), "agents-nope"), nil, plugin.Env{})
	require.Error(t, err)
	assert.Equal(t, 1, code)
}
