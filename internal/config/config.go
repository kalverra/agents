// Package config reads and writes the user's agents config.yaml, which holds
// the install choices (harnesses, rules, skills) and the permissions to merge
// into each harness.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kalverra/agents/internal/paths"
)

// FileName is the config file's name inside paths.ConfigDir().
const FileName = "config.yaml"

// Config is the content of config.yaml. A nil choice list means the user has
// not chosen yet, so install falls back to a default or asks. An empty list
// means the user chose nothing.
type Config struct {
	Harnesses   *[]string   `yaml:"harnesses,omitempty"`
	Rules       *[]string   `yaml:"rules,omitempty"`
	Skills      *[]string   `yaml:"skills,omitempty"`
	Permissions Permissions `yaml:"permissions,omitempty"`
	// Plugins are the added plugins. Order matters: when two provide the same
	// command, rule or skill, the earlier one wins.
	Plugins []Plugin `yaml:"plugins,omitempty"`
}

// Plugin is an added plugin. Exactly one of Path and URL is set: a Path
// plugin is used in place; a URL plugin is a git clone agents manages.
type Plugin struct {
	Name string `yaml:"name"           json:"name"`
	Path string `yaml:"path,omitempty" json:"path,omitempty"`
	URL  string `yaml:"url,omitempty"  json:"url,omitempty"`
	// Ref is the branch or tag cloned from URL; empty for the default branch.
	Ref string `yaml:"ref,omitempty" json:"ref,omitempty"`
}

// Permissions are merged into every selected harness's settings.
type Permissions struct {
	// Commands are shell commands to allow or deny. Nil means DefaultCommands.
	Commands    *Commands   `yaml:"commands,omitempty"`
	Directories []Directory `yaml:"directories,omitempty"`
}

// Commands lists shell command prefixes, such as "git status".
type Commands struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

// Access is the access level a harness gets to a directory.
type Access string

// Supported access levels.
const (
	AccessReadWrite Access = "read-write"
	AccessReadOnly  Access = "read-only"
)

// Trust is the folder trust level for harnesses that have one (Antigravity).
type Trust string

// Supported trust levels.
const (
	TrustNone   Trust = "none"
	TrustFolder Trust = "folder"
	TrustParent Trust = "parent"
)

// Directory is a directory the harnesses may use outside the current project.
type Directory struct {
	Path   string `yaml:"path"`
	Access Access `yaml:"access,omitempty"`
	Trust  Trust  `yaml:"trust,omitempty"`
}

// DefaultCommands are read-only commands every harness may run without
// asking. They apply when config.yaml has no permissions.commands.
var DefaultCommands = Commands{
	Allow: []string{
		"cat", "sed", "head", "tail", "less", "grep", "rg", "wc", "diff", "find", "awk",
		"stat", "file", "ls", "pwd", "which",
		"git status", "git diff", "git log", "git show", "git branch", "git rev-parse",
		"git ls-files", "git remote", "git tag", "git stash list", "git describe",
		"git blame", "git check-ref-format", "git merge-base", "git shortlog",
		"rtk",
	},
}

// Path returns where config.yaml lives.
func Path() string {
	return filepath.Join(paths.ConfigDir(), FileName)
}

// Load reads config.yaml. A missing file is an empty Config.
func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", Path(), err)
	}
	return &cfg, nil
}

// Save writes config.yaml, creating the config dir if needed.
func (c *Config) Save() error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path()), 0o750); err != nil {
		return err
	}
	return os.WriteFile(Path(), data, 0o600)
}

// ResolvedCommands returns the configured commands, or DefaultCommands when
// none are configured. Blank entries are dropped.
func (p Permissions) ResolvedCommands() Commands {
	c := DefaultCommands
	if p.Commands != nil {
		c = *p.Commands
	}
	return Commands{Allow: nonBlank(c.Allow), Deny: nonBlank(c.Deny)}
}

// ResolvedDirectories returns the configured directories with "~" and "$HOME"
// expanded, paths cleaned, and blank access and trust set to their defaults
// (read-write, none). Blank paths are dropped.
func (p Permissions) ResolvedDirectories() []Directory {
	home, _ := os.UserHomeDir()
	out := make([]Directory, 0, len(p.Directories))
	for _, d := range p.Directories {
		path := strings.TrimSpace(d.Path)
		if path == "" {
			continue
		}
		switch {
		case path == "~":
			path = home
		case strings.HasPrefix(path, "~/"):
			path = filepath.Join(home, path[2:])
		case strings.HasPrefix(path, "$HOME/"):
			path = filepath.Join(home, path[len("$HOME/"):])
		}
		d.Path = filepath.Clean(path)
		if d.Access == "" {
			d.Access = AccessReadWrite
		}
		if d.Trust == "" {
			d.Trust = TrustNone
		}
		out = append(out, d)
	}
	return out
}

func nonBlank(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
