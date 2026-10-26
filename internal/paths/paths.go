// Package paths resolves where agents keeps its own files. It follows the XDG
// Base Directory spec on every OS, so macOS uses ~/.config rather than
// ~/Library. Callers create the directories when they first write to them.
package paths

import (
	"os"
	"path/filepath"
)

const appName = "agents"

// ConfigDir returns the directory for user-edited config (config.yaml,
// implement.yaml, rules): $XDG_CONFIG_HOME/agents, else ~/.config/agents.
func ConfigDir() string {
	return xdgDir("XDG_CONFIG_HOME", ".config")
}

// DataDir returns the directory for data agents manages (plugin clones,
// plugin data): $XDG_DATA_HOME/agents, else ~/.local/share/agents.
func DataDir() string {
	return xdgDir("XDG_DATA_HOME", filepath.Join(".local", "share"))
}

// StateDir returns the directory for state and caches that are safe to lose
// (install records, statusline tracking): $XDG_STATE_HOME/agents, else
// ~/.local/state/agents.
func StateDir() string {
	return xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state"))
}

// xdgDir returns $env/agents when env holds an absolute path, else
// <home>/<homeRel>/agents. The spec says to ignore relative values. With no
// home dir, it falls back to the temp dir so callers never handle an error.
func xdgDir(env, homeRel string) string {
	if base := os.Getenv(env); filepath.IsAbs(base) {
		return filepath.Join(base, appName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, homeRel, appName)
}
