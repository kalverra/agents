package plugin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvRoundTrip(t *testing.T) {
	t.Parallel()
	want := Env{
		AIOutput:   true,
		LogLevel:   "debug",
		ConfigDir:  "/c",
		DataDir:    "/d",
		StateDir:   "/s",
		PluginRoot: "/p",
		PluginData: "/d/plugin-data/p",
		AgentsBin:  "/bin/agents",
	}
	got := FromEnviron(lookup(want.Environ(nil)))
	assert.Equal(t, want, got)
}

func TestEnvironReplacesInheritedContractVars(t *testing.T) {
	t.Parallel()
	base := []string{
		"PATH=/usr/bin",
		"AGENTS_CACHE_TTL=10m",
		EnvPluginRoot + "=/stale",
		EnvLogLevel + "=warn",
	}
	got := Env{LogLevel: "debug"}.Environ(base)
	assert.ElementsMatch(t, []string{"PATH=/usr/bin", "AGENTS_CACHE_TTL=10m", EnvLogLevel + "=debug"}, got)
}

//nolint:paralleltest // t.Setenv mutates process env
func TestFromEnvironDefaultsDirs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")

	got := FromEnviron(lookup(nil))
	assert.Equal(t, Env{
		ConfigDir: "/xdg/config/agents",
		DataDir:   "/xdg/data/agents",
		StateDir:  "/xdg/state/agents",
	}, got)
}

func lookup(environ []string) func(string) string {
	m := map[string]string{}
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return func(k string) string { return m[k] }
}
