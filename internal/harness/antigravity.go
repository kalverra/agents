package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/settings"
)

// Antigravity is the Antigravity CLI (agy) and IDE.
type Antigravity struct{}

// Name implements Harness.
func (Antigravity) Name() string { return "antigravity" }

// GhSkillAgent implements Harness.
func (Antigravity) GhSkillAgent() string { return "antigravity-cli" }

// Detect implements Harness.
func (a Antigravity) Detect() bool {
	dirs := []string{filepath.Join(a.dir(), "antigravity-cli"), filepath.Join(home(), ".antigravity-server")}
	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs, filepath.Join(home(), "Library", "Application Support", "Antigravity"))
	case "linux":
		dirs = append(dirs, filepath.Join(home(), ".config", "Antigravity"))
	}
	return detectAny(a.Name(), []string{"antigravity", "agy"}, dirs)
}

// InstructionsPath implements Harness.
func (a Antigravity) InstructionsPath() string { return filepath.Join(a.dir(), "GEMINI.md") }

// SkillsDir implements Harness.
func (a Antigravity) SkillsDir() string { return filepath.Join(a.dir(), "antigravity-cli", "skills") }

// CLISettingsPath is the Antigravity CLI's settings.json.
func (a Antigravity) CLISettingsPath() string {
	return filepath.Join(a.dir(), "antigravity-cli", "settings.json")
}

// IDESettingsPath is the Antigravity IDE's settings.json.
func (a Antigravity) IDESettingsPath() string { return filepath.Join(a.dir(), "settings.json") }

// TrustedFoldersPath is Antigravity's trustedFolders.json.
func (a Antigravity) TrustedFoldersPath() string {
	return filepath.Join(a.dir(), "trustedFolders.json")
}

// dir is the Antigravity config dir: $ANTIGRAVITY_CONFIG_DIR, else ~/.gemini.
func (Antigravity) dir() string {
	if env := os.Getenv("ANTIGRAVITY_CONFIG_DIR"); env != "" {
		return env
	}
	return filepath.Join(home(), ".gemini")
}

// PermissionEntries implements Harness. Entries go into the CLI settings,
// and into the IDE settings when that file exists. Commands become command
// rules. Directories become sandbox dirs and read_file or write_file rules;
// trusted ones also go into trustedWorkspaces and trustedFolders.json.
func (a Antigravity) PermissionEntries(p config.Permissions) []settings.Entry {
	files := []string{a.CLISettingsPath()}
	if fileExists(a.IDESettingsPath()) {
		files = append(files, a.IDESettingsPath())
	}

	cmds := p.ResolvedCommands()
	dirs := p.ResolvedDirectories()
	var out []settings.Entry
	for _, file := range files {
		allow := []string{"permissions", "allow"}
		for _, cmd := range cmds.Allow {
			out = append(out, listEntries(file, allow, antigravityCommandRules(cmd)...)...)
		}
		for _, cmd := range cmds.Deny {
			out = append(out, listEntries(file, []string{"permissions", "deny"}, antigravityCommandRules(cmd)...)...)
		}
		for _, d := range dirs {
			out = append(out, listEntries(file, []string{"sandbox", "allowedDirectories"}, d.Path)...)
			tool := "read_file"
			if d.Access == config.AccessReadWrite {
				tool = "write_file"
			}
			out = append(out, listEntries(file, allow, fmt.Sprintf("%s(%s)", tool, d.Path))...)
			if trustValue(d.Trust) != "" {
				out = append(out, listEntries(file, []string{"trustedWorkspaces"}, d.Path)...)
			}
		}
	}

	for _, d := range dirs {
		if v := trustValue(d.Trust); v != "" {
			out = append(out, settings.Entry{
				File: a.TrustedFoldersPath(), Path: []string{d.Path}, Kind: settings.KindKey, Value: v,
			})
		}
	}
	return out
}

// trustValue is trustedFolders.json's value for t, or "" for no trust.
func trustValue(t config.Trust) string {
	switch t {
	case config.TrustParent:
		return "TRUST_PARENT"
	case config.TrustFolder:
		return "TRUST_FOLDER"
	default:
		return ""
	}
}

// antigravityCommandRules returns the command rules for cmd, plus the
// rtk-wrapped rule unless cmd already starts with rtk.
func antigravityCommandRules(cmd string) []string {
	rules := []string{fmt.Sprintf("command(%s)", cmd)}
	if cmd == "rtk" || strings.HasPrefix(cmd, "rtk ") {
		return rules
	}
	return append(rules, fmt.Sprintf("command(rtk %s)", cmd))
}
