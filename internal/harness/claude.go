package harness

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/settings"
)

// Claude is Claude Code.
type Claude struct{}

// Name implements Harness.
func (Claude) Name() string { return "claude" }

// GhSkillAgent implements Harness.
func (Claude) GhSkillAgent() string { return "claude-code" }

// Detect implements Harness.
func (c Claude) Detect() bool {
	return detectAny(c.Name(), []string{"claude"}, []string{c.dir()})
}

// InstructionsPath implements Harness.
func (c Claude) InstructionsPath() string { return filepath.Join(c.dir(), "CLAUDE.md") }

// SkillsDir implements Harness.
func (c Claude) SkillsDir() string { return filepath.Join(c.dir(), "skills") }

// SettingsPath is Claude Code's user settings.json.
func (c Claude) SettingsPath() string { return filepath.Join(c.dir(), "settings.json") }

func (Claude) dir() string { return filepath.Join(home(), ".claude") }

// PermissionEntries implements Harness. Commands become Bash rules, also
// covering the rtk-wrapped form. Directories become additional directories,
// Read/Edit rules and sandbox filesystem grants.
func (c Claude) PermissionEntries(p config.Permissions) []settings.Entry {
	file := c.SettingsPath()
	allow := []string{"permissions", "allow"}
	var out []settings.Entry

	cmds := p.ResolvedCommands()
	for _, cmd := range cmds.Allow {
		out = append(out, listEntries(file, allow, claudeCommandRules(cmd)...)...)
	}
	for _, cmd := range cmds.Deny {
		out = append(out, listEntries(file, []string{"permissions", "deny"}, claudeCommandRules(cmd)...)...)
	}

	for _, d := range p.ResolvedDirectories() {
		glob := globAll(d.Path)
		out = append(out, listEntries(file, []string{"permissions", "additionalDirectories"}, d.Path)...)
		// Claude Code marks an absolute path in a rule with a leading "//".
		out = append(out, listEntries(file, allow, fmt.Sprintf("Read(/%s)", glob))...)
		out = append(out, listEntries(file, []string{"sandbox", "filesystem", "allowRead"}, glob)...)
		if d.Access == config.AccessReadWrite {
			out = append(out, listEntries(file, allow, fmt.Sprintf("Edit(/%s)", glob))...)
			out = append(out, listEntries(file, []string{"sandbox", "filesystem", "allowWrite"}, glob)...)
		}
	}
	return out
}

// claudeCommandRules returns the Bash rules for cmd and its arguments, plus
// the rtk-wrapped pair unless cmd already starts with rtk.
func claudeCommandRules(cmd string) []string {
	rules := []string{fmt.Sprintf("Bash(%s)", cmd), fmt.Sprintf("Bash(%s *)", cmd)}
	if cmd == "rtk" || strings.HasPrefix(cmd, "rtk ") {
		return rules
	}
	return append(rules, fmt.Sprintf("Bash(rtk %s)", cmd), fmt.Sprintf("Bash(rtk %s *)", cmd))
}
