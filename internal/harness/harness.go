// Package harness describes the AI coding tools agents installs into: where
// each keeps its instructions, skills and settings, and how agents
// permissions map onto its settings format.
package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/settings"
)

// Harness is one supported AI coding tool. Paths are at user scope.
type Harness interface {
	// Name is the ID used in --targets and config.toml: "claude",
	// "antigravity" or "opencode".
	Name() string
	// GhSkillAgent is the gh skill install --agent value.
	GhSkillAgent() string
	// Detect reports whether the tool looks installed on this machine.
	Detect() bool
	// InstructionsPath is the global instructions file that holds the rules.
	InstructionsPath() string
	// SkillsDir is where gh skill installs this tool's user-scope skills.
	SkillsDir() string
	// PermissionEntries maps p onto this tool's settings files.
	PermissionEntries(p config.Permissions) []settings.Entry
}

// Migrator is implemented by harnesses whose settings files may need a
// format upgrade before agents adds entries.
type Migrator interface {
	MigrateSettings(file string, m map[string]any)
}

// All returns every supported harness.
func All() []Harness {
	return []Harness{Claude{}, Antigravity{}, OpenCode{}}
}

// Names returns the names of hs, in order.
func Names(hs []Harness) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Name()
	}
	return out
}

// ByNames returns the harnesses with the given names, in All() order. It
// errors on an unknown name.
func ByNames(names []string) ([]Harness, error) {
	all := All()
	for _, n := range names {
		if !slices.Contains(Names(all), n) {
			return nil, fmt.Errorf("unknown harness %q (supported: %s)", n, strings.Join(Names(all), ", "))
		}
	}
	return slices.DeleteFunc(all, func(h Harness) bool { return !slices.Contains(names, h.Name()) }), nil
}

// Detected returns the harnesses that look installed.
func Detected() []Harness {
	return slices.DeleteFunc(All(), func(h Harness) bool { return !h.Detect() })
}

// Migrate returns a settings.Migrate that runs every Migrator in hs.
func Migrate(hs []Harness) settings.Migrate {
	return func(file string, m map[string]any) {
		for _, h := range hs {
			if mig, ok := h.(Migrator); ok {
				mig.MigrateSettings(file, m)
			}
		}
	}
}

// detectAny reports whether any binary is on PATH or any dir exists, logging
// the first signal found.
func detectAny(name string, bins, dirs []string) bool {
	for _, b := range bins {
		if p, err := exec.LookPath(b); err == nil {
			log.Debug().Str("harness", name).Str("bin", p).Msg("Detected")
			return true
		}
	}
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			log.Debug().Str("harness", name).Str("dir", d).Msg("Detected")
			return true
		}
	}
	return false
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// listEntries returns one KindList entry per value.
func listEntries(file string, path []string, values ...string) []settings.Entry {
	out := make([]settings.Entry, len(values))
	for i, v := range values {
		out[i] = settings.Entry{File: file, Path: path, Kind: settings.KindList, Value: v}
	}
	return out
}

// globAll is the glob that matches everything under dir.
func globAll(dir string) string {
	return filepath.Join(dir, "**")
}
