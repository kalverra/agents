package paths

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

//nolint:paralleltest // t.Setenv mutates process env
func TestDirs(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()

	tests := []struct {
		name     string
		env      string
		envValue string
		dir      func() string
		expected string
	}{
		{"config from XDG", "XDG_CONFIG_HOME", xdg, ConfigDir, filepath.Join(xdg, "agents")},
		{"config default", "XDG_CONFIG_HOME", "", ConfigDir, filepath.Join(home, ".config", "agents")},
		{"config ignores relative XDG", "XDG_CONFIG_HOME", "rel", ConfigDir, filepath.Join(home, ".config", "agents")},
		{"data from XDG", "XDG_DATA_HOME", xdg, DataDir, filepath.Join(xdg, "agents")},
		{"data default", "XDG_DATA_HOME", "", DataDir, filepath.Join(home, ".local", "share", "agents")},
		{"state from XDG", "XDG_STATE_HOME", xdg, StateDir, filepath.Join(xdg, "agents")},
		{"state default", "XDG_STATE_HOME", "", StateDir, filepath.Join(home, ".local", "state", "agents")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv(tt.env, tt.envValue)
			assert.Equal(t, tt.expected, tt.dir())
		})
	}
}
