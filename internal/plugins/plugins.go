// Package plugins reads and manages agents plugins. A plugin is a directory in
// the Claude Code plugin layout:
//
//	.claude-plugin/plugin.json   optional: name, description, version, metadata
//	bin/agents-<cmd>             commands, run as `agents <cmd>`
//	rules/<name>.md              rules offered by agents install
//	skills/<name>/SKILL.md       skills offered by agents install
//
// agents reads two optional keys from plugin.json's metadata map:
// "agents-build" is a shell command that builds bin/, and "agents-commands"
// describes the commands as "name=description;name=description".
package plugins

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/dispatch"
	"github.com/kalverra/agents/internal/paths"
)

// Plugin is an added plugin, read from disk.
type Plugin struct {
	config.Plugin

	Root        string `json:"root"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	// Build is the metadata.agents-build command; empty for no build step.
	Build string `json:"build,omitempty"`

	commandDocs map[string]string
}

// Command is an executable a plugin provides.
type Command struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	Plugin      string `json:"plugin"`

	plugin *Plugin
}

// manifest is .claude-plugin/plugin.json.
type manifest struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	Metadata    map[string]string `json:"metadata"`
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ClonesDir is where agents clones URL plugins, one dir per plugin name.
func ClonesDir() string {
	return filepath.Join(paths.DataDir(), "plugins")
}

// Load opens every plugin in refs, in order. It returns the plugins it could
// open along with an error naming the ones it could not.
func Load(refs []config.Plugin) ([]Plugin, error) {
	out := make([]Plugin, 0, len(refs))
	var errs []error
	for _, ref := range refs {
		p, err := Open(ref)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, p)
	}
	return out, errors.Join(errs...)
}

// Open reads the plugin ref points to.
func Open(ref config.Plugin) (Plugin, error) {
	root := ref.Path
	if root == "" {
		root = filepath.Join(ClonesDir(), ref.Name)
	}
	p, err := read(root)
	if err != nil {
		return Plugin{}, fmt.Errorf("plugin %s: %w", ref.Name, err)
	}
	p.Plugin = ref
	return p, nil
}

// read reads the plugin at root. Its Name comes from plugin.json, else the
// dir name.
func read(root string) (Plugin, error) {
	info, err := os.Stat(root)
	if err != nil {
		return Plugin{}, err
	}
	if !info.IsDir() {
		return Plugin{}, fmt.Errorf("%s is not a directory", root)
	}
	m, err := readManifest(root)
	if err != nil {
		return Plugin{}, err
	}
	p := Plugin{
		Root:        root,
		Description: m.Description,
		Version:     m.Version,
		Build:       strings.TrimSpace(m.Metadata["agents-build"]),
		commandDocs: parseCommandDocs(m.Metadata["agents-commands"]),

		Name: cmp.Or(m.Name, filepath.Base(root))}
	if !validName.MatchString(p.Name) {
		return Plugin{}, fmt.Errorf("invalid plugin name %q", p.Name)
	}
	return p, nil
}

// readManifest reads root's plugin.json. A missing file is an empty manifest.
func readManifest(root string) (manifest, error) {
	var m manifest
	path := filepath.Join(root, ".claude-plugin", "plugin.json")
	data, err := os.ReadFile(path) //nolint:gosec // plugin dir chosen by the user
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parsing plugin.json: %w", err)
	}
	return m, nil
}

// parseCommandDocs parses "name=description;name=description".
func parseCommandDocs(s string) map[string]string {
	out := map[string]string{}
	for item := range strings.SplitSeq(s, ";") {
		name, desc, _ := strings.Cut(item, "=")
		if name = strings.TrimSpace(name); name != "" {
			out[name] = strings.TrimSpace(desc)
		}
	}
	return out
}

// RulesDir is the plugin's rules/ dir. It may not exist.
func (p Plugin) RulesDir() string { return filepath.Join(p.Root, "rules") }

// SkillsDir is the plugin's skills/ dir. It may not exist.
func (p Plugin) SkillsDir() string { return filepath.Join(p.Root, "skills") }

// DataDir is where the plugin keeps its data. It may not exist yet.
func (p Plugin) DataDir() string {
	return filepath.Join(paths.DataDir(), "plugin-data", p.Name)
}

// Commands lists the executables in the plugin's bin/, by name.
func (p *Plugin) Commands() ([]Command, error) {
	entries, err := os.ReadDir(filepath.Join(p.Root, "bin"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Command
	for _, e := range entries {
		name, ok := strings.CutPrefix(e.Name(), dispatch.Prefix)
		if !ok || name == "" {
			continue
		}
		if c, ok := p.command(name); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// command returns bin/agents-<name> when it is an executable file.
func (p *Plugin) command(name string) (Command, bool) {
	path := filepath.Join(p.Root, "bin", dispatch.Prefix+name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return Command{}, false
	}
	return Command{Name: name, Path: path, Description: p.commandDocs[name], Plugin: p.Name, plugin: p}, true
}

// Owner returns the plugin that provides c.
func (c Command) Owner() Plugin { return *c.plugin }

// Find returns the command name from the first plugin that has it.
func Find(ps []Plugin, name string) (Command, bool) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return Command{}, false
	}
	for i := range ps {
		if c, ok := ps[i].command(name); ok {
			return c, true
		}
	}
	return Command{}, false
}

// AllCommands lists every plugin command, sorted by name. When plugins share
// a name, the first plugin's command is in cmds and the rest in shadowed.
func AllCommands(ps []Plugin) (cmds, shadowed []Command, err error) {
	seen := map[string]bool{}
	for i := range ps {
		list, err := ps[i].Commands()
		if err != nil {
			return nil, nil, fmt.Errorf("plugin %s: %w", ps[i].Name, err)
		}
		for _, c := range list {
			if seen[c.Name] {
				shadowed = append(shadowed, c)
				continue
			}
			seen[c.Name] = true
			cmds = append(cmds, c)
		}
	}
	slices.SortFunc(cmds, func(a, b Command) int { return strings.Compare(a.Name, b.Name) })
	return cmds, shadowed, nil
}
