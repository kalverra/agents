package harness

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/settings"
)

// OpenCode is OpenCode.
type OpenCode struct{}

// Name implements Harness.
func (OpenCode) Name() string { return "opencode" }

// GhSkillAgent implements Harness.
func (OpenCode) GhSkillAgent() string { return "opencode" }

// Detect implements Harness.
func (o OpenCode) Detect() bool {
	return detectAny(o.Name(), []string{"opencode"}, []string{o.dir()})
}

// InstructionsPath implements Harness.
func (o OpenCode) InstructionsPath() string { return filepath.Join(o.dir(), "AGENTS.md") }

// SkillsDir implements Harness.
func (o OpenCode) SkillsDir() string { return filepath.Join(o.dir(), "skills") }

// SettingsPath is OpenCode's global opencode.json.
func (o OpenCode) SettingsPath() string { return filepath.Join(o.dir(), "opencode.json") }

func (OpenCode) dir() string { return filepath.Join(home(), ".config", "opencode") }

const openCodeSchema = "https://opencode.ai/config.json"

// PermissionEntries implements Harness. Commands become V2 "shell" rules,
// also covering the rtk-wrapped form. Directories become
// "external_directory" rules.
func (o OpenCode) PermissionEntries(p config.Permissions) []settings.Entry {
	file := o.SettingsPath()
	var rules []settings.Entry
	addRule := func(action, resource, effect string) {
		rules = append(rules, settings.Entry{
			File: file, Path: []string{"permissions"}, Kind: settings.KindList,
			Value: openCodeRule(action, resource, effect),
		})
	}

	cmds := p.ResolvedCommands()
	for _, cmd := range cmds.Allow {
		for _, r := range openCodeCommandResources(cmd) {
			addRule("shell", r, "allow")
		}
	}
	for _, cmd := range cmds.Deny {
		for _, r := range openCodeCommandResources(cmd) {
			addRule("shell", r, "deny")
		}
	}
	for _, d := range p.ResolvedDirectories() {
		addRule("external_directory", globAll(d.Path), "allow")
	}

	if len(rules) == 0 {
		return nil
	}
	schema := settings.Entry{File: file, Path: []string{"$schema"}, Kind: settings.KindKey, Value: openCodeSchema}
	return append([]settings.Entry{schema}, rules...)
}

// MigrateSettings implements Migrator. It replaces a legacy V1 "permission"
// map in opencode.json with the equivalent V2 "permissions" rules. Converted
// rules come first so explicit V2 rules keep precedence: V2 matching takes
// the last matching rule.
func (o OpenCode) MigrateSettings(file string, m map[string]any) {
	if file != o.SettingsPath() {
		return
	}
	legacy, ok := m["permission"].(map[string]any)
	if !ok {
		return
	}
	rules, _ := m["permissions"].([]any)
	m["permissions"] = append(legacyOpenCodeRules(legacy), rules...)
	delete(m, "permission")
}

// openCodeLegacyActions maps V1 OpenCode permission tool names to V2 actions.
var openCodeLegacyActions = map[string]string{
	"bash":  "shell",
	"task":  "subagent",
	"write": "edit",
	"patch": "edit",
}

// legacyOpenCodeRules converts a V1 "permission" map into V2 rules. Keys are
// visited in sorted order because decoded JSON objects have no order.
func legacyOpenCodeRules(legacy map[string]any) []any {
	var rules []any
	for _, tool := range sortedKeys(legacy) {
		action, known := openCodeLegacyActions[tool]
		if !known {
			action = tool
		}
		switch value := legacy[tool].(type) {
		case string:
			rules = append(rules, openCodeRule(action, "*", value))
		case map[string]any:
			for _, pattern := range sortedKeys(value) {
				if effect, ok := value[pattern].(string); ok {
					rules = append(rules, openCodeRule(action, pattern, effect))
				}
			}
		}
	}
	return rules
}

func openCodeRule(action, resource, effect string) map[string]any {
	return map[string]any{"action": action, "resource": resource, "effect": effect}
}

// openCodeCommandResources returns the shell patterns cmd covers: the bare
// command and its wildcard form, plus the rtk-wrapped pair unless cmd
// already starts with rtk.
func openCodeCommandResources(cmd string) []string {
	if cmd == "rtk" || strings.HasPrefix(cmd, "rtk ") {
		return []string{cmd, cmd + " *"}
	}
	return []string{cmd, cmd + " *", "rtk " + cmd, "rtk " + cmd + " *"}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
