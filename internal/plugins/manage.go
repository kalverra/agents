package plugins

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kalverra/agents/internal/config"
)

// Manager adds, removes and updates the plugins listed in a Config. The
// caller saves the Config afterwards.
type Manager struct {
	Config *config.Config
	// ConfirmBuild is called before a plugin's build command runs. Returning
	// an error stops the add or update. Plugins run with the user's
	// permissions, so the user should see the command first.
	ConfirmBuild func(p Plugin) error
	// Out receives git and build output.
	Out io.Writer
}

// ParseSource turns a `plugin add` argument into a config entry without a
// name. An existing directory is a local plugin. Anything else is a git URL,
// with an optional "@ref" (branch or tag) after the repo path, such as
// git@github.com:me/plugin.git@v1.
func ParseSource(arg string) (config.Plugin, error) {
	if info, err := os.Stat(arg); err == nil && info.IsDir() {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return config.Plugin{}, err
		}
		return config.Plugin{Path: abs}, nil
	}
	if !strings.Contains(arg, ":") { // every git URL form has a colon
		return config.Plugin{}, fmt.Errorf("%s is neither a directory nor a git URL", arg)
	}
	url, ref := arg, ""
	// The "@" in git@host is before the repo path; a ref's "@" is after it.
	if at := strings.LastIndex(arg, "@"); at > strings.LastIndexAny(arg, "/:") {
		url, ref = arg[:at], arg[at+1:]
		if ref == "" {
			return config.Plugin{}, fmt.Errorf("empty ref in %q", arg)
		}
	}
	if url == "" {
		return config.Plugin{}, fmt.Errorf("invalid plugin source %q", arg)
	}
	return config.Plugin{URL: url, Ref: ref}, nil
}

// Add adds the plugin at arg (see ParseSource) to the end of the plugin
// list: it clones a git URL into ClonesDir, then runs the plugin's build.
// On error nothing is added and any clone is removed.
func (m *Manager) Add(arg string) (Plugin, error) {
	ref, err := ParseSource(arg)
	if err != nil {
		return Plugin{}, err
	}
	var p Plugin
	if ref.URL != "" {
		p, err = m.clone(ref)
	} else {
		p, err = m.local(ref.Path)
	}
	if err != nil {
		return Plugin{}, err
	}

	if err := m.build(p); err != nil {
		m.discard(p)
		return Plugin{}, err
	}
	m.Config.Plugins = append(m.Config.Plugins, p.Plugin)
	return p, nil
}

// local reads the plugin at path, which is used in place.
func (m *Manager) local(path string) (Plugin, error) {
	p, err := read(path)
	if err != nil {
		return Plugin{}, err
	}
	if m.index(p.Name) >= 0 {
		return Plugin{}, fmt.Errorf("plugin %s is already added", p.Name)
	}
	p.Path = path
	return p, nil
}

// clone clones ref into a temp dir, reads the plugin's name, and moves the
// clone to ClonesDir()/<name>.
func (m *Manager) clone(ref config.Plugin) (Plugin, error) {
	if err := os.MkdirAll(ClonesDir(), 0o750); err != nil {
		return Plugin{}, err
	}
	tmp, err := os.MkdirTemp(ClonesDir(), ".add-")
	if err != nil {
		return Plugin{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	args := []string{"clone"}
	if ref.Ref != "" {
		args = append(args, "--branch", ref.Ref)
	}
	if err := m.git(tmp, append(args, "--", ref.URL, "repo")...); err != nil {
		return Plugin{}, err
	}
	p, err := read(filepath.Join(tmp, "repo"))
	if err != nil {
		return Plugin{}, err
	}
	if m.index(p.Name) >= 0 {
		return Plugin{}, fmt.Errorf("plugin %s is already added", p.Name)
	}
	dest := filepath.Join(ClonesDir(), p.Name)
	if _, err := os.Stat(dest); err == nil {
		return Plugin{}, fmt.Errorf("%s already exists; remove it or the plugin using it", dest)
	}
	if err := os.Rename(p.Root, dest); err != nil {
		return Plugin{}, err
	}
	p.Root, p.URL, p.Ref = dest, ref.URL, ref.Ref
	return p, nil
}

// Remove drops the plugin name from the list and deletes its clone, if
// agents made one. Its data dir is kept.
func (m *Manager) Remove(name string) (config.Plugin, error) {
	i := m.index(name)
	if i < 0 {
		return config.Plugin{}, fmt.Errorf("no plugin named %s", name)
	}
	ref := m.Config.Plugins[i]
	if ref.URL != "" {
		if err := os.RemoveAll(filepath.Join(ClonesDir(), ref.Name)); err != nil {
			return config.Plugin{}, err
		}
	}
	m.Config.Plugins = slices.Delete(m.Config.Plugins, i, i+1)
	return ref, nil
}

// Update pulls each named plugin's clone (all plugins when names is empty)
// and re-runs its build. A clone checked out at a tag is not pulled. Local
// plugins are only rebuilt.
func (m *Manager) Update(names []string) ([]Plugin, error) {
	if len(names) == 0 {
		for _, ref := range m.Config.Plugins {
			names = append(names, ref.Name)
		}
	}
	var out []Plugin
	for _, name := range names {
		i := m.index(name)
		if i < 0 {
			return out, fmt.Errorf("no plugin named %s", name)
		}
		p, err := m.update(m.Config.Plugins[i])
		if err != nil {
			return out, fmt.Errorf("plugin %s: %w", name, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func (m *Manager) update(ref config.Plugin) (Plugin, error) {
	p, err := Open(ref)
	if err != nil {
		return Plugin{}, err
	}
	// symbolic-ref fails on a detached HEAD, which is how a tag is checked out.
	if ref.URL != "" && m.git(p.Root, "symbolic-ref", "-q", "HEAD") == nil {
		if err := m.git(p.Root, "pull", "--ff-only"); err != nil {
			return Plugin{}, err
		}
		// The pull may have changed plugin.json.
		if p, err = Open(ref); err != nil {
			return Plugin{}, err
		}
	}
	return p, m.build(p)
}

// build runs p's build command after ConfirmBuild allows it.
func (m *Manager) build(p Plugin) error {
	if p.Build == "" {
		return nil
	}
	if m.ConfirmBuild == nil {
		return errors.New("no way to confirm the build command")
	}
	if err := m.ConfirmBuild(p); err != nil {
		return err
	}
	cmd := exec.Command("sh", "-c", p.Build) //nolint:gosec // G204: the user confirmed the plugin's build command
	cmd.Dir, cmd.Stdout, cmd.Stderr = p.Root, m.out(), m.out()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build %q: %w", p.Build, err)
	}
	return nil
}

// discard removes p's clone after a failed add. Local plugins are left alone.
func (m *Manager) discard(p Plugin) {
	if p.URL != "" {
		_ = os.RemoveAll(p.Root)
	}
}

func (m *Manager) git(dir string, args ...string) error {
	cmd := exec.Command("git", args...) //nolint:gosec // G204: args are built here
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, m.out(), m.out()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (m *Manager) out() io.Writer {
	if m.Out == nil {
		return io.Discard
	}
	return m.Out
}

func (m *Manager) index(name string) int {
	return slices.IndexFunc(m.Config.Plugins, func(r config.Plugin) bool { return r.Name == name })
}
