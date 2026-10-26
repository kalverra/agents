// Package install sets up harnesses: it writes the selected rules into each
// harness's instructions file, merges permissions into its settings, and
// installs skills through gh skill. It records what it wrote in
// installed.json, so re-running install converges on the current choices
// and uninstall removes exactly what install added.
package install

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/ghskill"
	"github.com/kalverra/agents/internal/harness"
	"github.com/kalverra/agents/internal/plugins"
	"github.com/kalverra/agents/internal/rules"
	"github.com/kalverra/agents/internal/settings"
)

// Request holds the install command's choices. Nil lists mean "not given".
type Request struct {
	Targets       []string // harness names; nil uses config, else asks or uses detected
	Skills        []string // skill names or remote IDs; nil uses config, else asks
	AllSkills     bool     // every built-in skill
	NoSkills      bool     // leave skills alone
	NoPermissions bool     // leave permissions alone
	Reconfigure   bool     // ask again even when config has choices
}

// Option is one choice offered by a Chooser.
type Option struct {
	Value string
	Label string
}

// Chooser asks the user to pick any of options, with selected checked at the
// start. It returns the picked values.
type Chooser func(title string, options []Option, selected []string) ([]string, error)

// Installer plans and applies installs. Config holds the saved choices; Plan
// updates it with new ones and the caller saves it after Apply. Choose nil
// means non-interactive: missing choices fall back to defaults.
type Installer struct {
	Config   *config.Config
	Record   *Record
	Choose   Chooser
	GH       ghskill.Client
	Pin      string           // built-in skills version; "" for gh's default
	RulesDir string           // the user's own rules
	Plugins  []plugins.Plugin // add their rules and skills to the choices
}

// Plan is what Apply will do.
type Plan struct {
	Harnesses  []HarnessPlan `json:"harnesses"`
	Remove     []string      `json:"remove,omitempty"`     // recorded harnesses no longer selected
	Undetected []string      `json:"undetected,omitempty"` // selected but not detected
	Rules      []string      `json:"rules"`
	Skills     []string      `json:"skills,omitempty"`
	SkillsPin  string        `json:"skills_pin,omitempty"`
	GhMissing  bool          `json:"gh_missing,omitempty"` // print gh commands instead of running them
	// Dropped are saved choices that no longer exist, such as the rules of a
	// removed plugin. They are taken out of the config.
	Dropped []string `json:"dropped,omitempty"`

	rules       []rules.Rule
	skillSet    []Skill
	permissions bool
	skills      bool
}

// HarnessPlan is what Apply will do in one harness.
type HarnessPlan struct {
	Name            string   `json:"name"`
	Instructions    string   `json:"instructions"`
	PermissionFiles []string `json:"permission_files,omitempty"`
	SkillAgent      string   `json:"skill_agent,omitempty"`
	InstallSkills   []string `json:"install_skills,omitempty"`
	// SkillsFrom names the harness whose skills this one already reads.
	SkillsFrom string `json:"skills_from,omitempty"`

	harness harness.Harness
	entries []settings.Entry
}

// Plan resolves req against the saved config into a Plan, asking the user
// for missing choices when Choose is set. It writes nothing to disk.
func (i *Installer) Plan(req Request) (*Plan, error) {
	p := &Plan{permissions: !req.NoPermissions, skills: !req.NoSkills}
	hs, err := i.resolveHarnesses(p, req)
	if err != nil {
		return nil, err
	}
	if p.rules, err = i.resolveRules(p, req); err != nil {
		return nil, err
	}
	p.Rules = rules.Names(p.rules)
	if p.skills {
		if p.skillSet, err = i.resolveSkills(p, req); err != nil {
			return nil, err
		}
		for _, s := range p.skillSet {
			p.Skills = append(p.Skills, s.Name)
		}
		p.SkillsPin = i.Pin
		p.GhMissing = len(p.Skills) > 0 && !i.GH.Available()
	}

	names := harness.Names(hs)
	for _, h := range hs {
		if !h.Detect() {
			p.Undetected = append(p.Undetected, h.Name())
		}
		p.Harnesses = append(p.Harnesses, i.planHarness(p, h, names))
	}
	for name := range i.Record.Harnesses {
		if !slices.Contains(names, name) {
			p.Remove = append(p.Remove, name)
		}
	}
	slices.Sort(p.Remove)
	return p, nil
}

func (i *Installer) planHarness(p *Plan, h harness.Harness, selected []string) HarnessPlan {
	hp := HarnessPlan{Name: h.Name(), Instructions: h.InstructionsPath(), harness: h}
	if p.permissions {
		cfg := i.Config.Permissions
		hp.entries = h.PermissionEntries(cfg)
		hp.PermissionFiles = settings.Files(hp.entries)
	}
	if !p.skills {
		return hp
	}
	// OpenCode also reads ~/.claude/skills, so installing for both would
	// show every skill twice.
	if h.Name() == (harness.OpenCode{}).Name() && slices.Contains(selected, (harness.Claude{}).Name()) {
		hp.SkillsFrom = (harness.Claude{}).Name()
		return hp
	}
	hp.SkillAgent = h.GhSkillAgent()
	prev := i.Record.Harnesses[h.Name()]
	for _, s := range p.skillSet {
		// A built-in or remote skill from the same source is already current.
		// Plugin skills are local copies that may have changed, so they
		// always reinstall.
		if s.Dir == "" && prev != nil && prev.SkillsDir == h.SkillsDir() &&
			prev.Skills[s.Name] == p.source(s) &&
			fileExists(filepath.Join(h.SkillsDir(), s.Name, "SKILL.md")) {
			continue
		}
		hp.InstallSkills = append(hp.InstallSkills, s.Name)
	}
	return hp
}

func (i *Installer) resolveHarnesses(p *Plan, req Request) ([]harness.Harness, error) {
	all := harness.All()
	options := make([]Option, len(all))
	for k, h := range all {
		options[k] = Option{Value: h.Name(), Label: h.Name()}
	}
	names, err := i.resolve(p, choice{
		kind:       "harness",
		title:      "Install into which harnesses?",
		flag:       req.Targets,
		saved:      &i.Config.Harnesses,
		options:    options,
		reconfig:   req.Reconfigure,
		fallback:   harness.Names(harness.Detected()),
		saveAlways: true,
	})
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no harnesses selected or detected; pass --targets %s",
			"claude,antigravity,opencode")
	}
	return harness.ByNames(names)
}

func (i *Installer) resolveRules(p *Plan, req Request) ([]rules.Rule, error) {
	pluginDirs := make([]string, len(i.Plugins))
	for k, pl := range i.Plugins {
		pluginDirs[k] = pl.RulesDir()
	}
	all, err := rules.Load(i.RulesDir, pluginDirs...)
	if err != nil {
		return nil, err
	}
	options := make([]Option, len(all))
	for k, r := range all {
		options[k] = Option{Value: r.Name, Label: r.Name + i.ruleOrigin(r)}
	}
	names, err := i.resolve(p, choice{
		kind:       "rule",
		title:      "Which rules go into each harness's instructions?",
		saved:      &i.Config.Rules,
		options:    options,
		reconfig:   req.Reconfigure,
		fallback:   rules.Names(all),
		saveAlways: true,
	})
	if err != nil {
		return nil, err
	}
	return rules.Select(all, names)
}

// ruleOrigin labels a rule that is not built in with where it comes from.
func (i *Installer) ruleOrigin(r rules.Rule) string {
	switch {
	case r.Source == rules.SourceBuiltin:
		return ""
	case filepath.Dir(r.Source) == i.RulesDir:
		return " (yours)"
	}
	for _, pl := range i.Plugins {
		if filepath.Dir(r.Source) == pl.RulesDir() {
			return " (plugin " + pl.Name + ")"
		}
	}
	return ""
}

func (i *Installer) resolveSkills(p *Plan, req Request) ([]Skill, error) {
	all, err := i.availableSkills()
	if err != nil {
		return nil, err
	}
	// Remote skills are offered only once named, by --skills or config.toml.
	named := req.Skills
	if named == nil && i.Config.Skills != nil {
		named = *i.Config.Skills
	}
	all = withRemoteSkills(all, named)

	options := make([]Option, len(all))
	var builtinNames []string
	for k, s := range all {
		label := s.Name + ": " + s.Description
		switch {
		case s.Plugin != "":
			label = s.Name + " (plugin " + s.Plugin + "): " + s.Description
		case s.Repo != "":
			label = s.Name + " (" + s.Repo + ")"
		default:
			builtinNames = append(builtinNames, s.Name)
		}
		options[k] = Option{Value: s.ID(), Label: label}
	}
	flag := req.Skills
	if req.AllSkills {
		flag = builtinNames
	}
	names, err := i.resolve(p, choice{
		kind:     "skill",
		title:    "Which skills should gh skill install?",
		flag:     flag,
		saved:    &i.Config.Skills,
		options:  options,
		reconfig: req.Reconfigure,
	})
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, s := range all {
		if slices.Contains(names, s.ID()) {
			out = append(out, s)
		}
	}
	return out, nil
}

// availableSkills lists the built-in skills, then each plugin's. When names
// clash, the built-in skill or the earlier plugin's wins.
func (i *Installer) availableSkills() ([]Skill, error) {
	all, err := BuiltinSkills()
	if err != nil {
		return nil, err
	}
	for _, pl := range i.Plugins {
		list, err := PluginSkills(pl)
		if err != nil {
			return nil, err
		}
		for _, s := range list {
			if !slices.ContainsFunc(all, func(o Skill) bool { return o.Name == s.Name }) {
				all = append(all, s)
			}
		}
	}
	return all, nil
}

// choice is one list the user picks: harnesses, rules or skills.
type choice struct {
	kind     string // what is being picked, for errors
	title    string
	flag     []string   // from a command flag; wins when set
	saved    **[]string // the config field
	options  []Option
	reconfig bool
	// fallback is used when nothing is saved and nobody can be asked.
	fallback []string
	// saveAlways saves the fallback too. Without it, a non-interactive run
	// that falls back leaves the config unset, so a later run still asks.
	saveAlways bool
}

// resolve picks the values for c: the flag, else the saved config (unless
// reconfiguring), else the user's answer, else the fallback. It saves the
// result into the config field. A flag value that is not an option is an
// error; a saved one is dropped and added to p.Dropped.
func (i *Installer) resolve(p *Plan, c choice) ([]string, error) {
	saved := *c.saved
	var values []string
	switch {
	case c.flag != nil:
		if unknown := c.unknown(c.flag); len(unknown) > 0 {
			return nil, fmt.Errorf("unknown %s %q (available: %s)",
				c.kind, unknown[0], strings.Join(c.values(), ", "))
		}
		values = c.flag
	case saved != nil && !c.reconfig:
		unknown := c.unknown(*saved)
		if len(unknown) == 0 {
			return *saved, nil
		}
		for _, v := range unknown {
			p.Dropped = append(p.Dropped, c.kind+" "+v)
		}
		values = slices.DeleteFunc(slices.Clone(*saved), func(v string) bool { return slices.Contains(unknown, v) })
	case i.Choose != nil:
		selected := c.fallback
		if saved != nil {
			selected = *saved
		}
		picked, err := i.Choose(c.title, c.options, selected)
		if err != nil {
			return nil, err
		}
		values = picked
	default:
		if !c.saveAlways {
			return c.fallback, nil
		}
		values = c.fallback
	}
	values = append([]string{}, values...)
	*c.saved = &values
	return values, nil
}

func (c choice) values() []string {
	out := make([]string, len(c.options))
	for k, o := range c.options {
		out[k] = o.Value
	}
	return out
}

// unknown returns the values that are not options.
func (c choice) unknown(values []string) []string {
	known := c.values()
	var out []string
	for _, v := range values {
		if !slices.Contains(known, v) {
			out = append(out, v)
		}
	}
	return out
}

// origin returns the repo and pin gh installs s from: its own for a remote
// skill, else the built-in repo at the plan's pin.
func (p *Plan) origin(s Skill) (repo, pin string) {
	if s.Repo != "" {
		return s.Repo, s.Ref
	}
	return ghskill.Repo, p.SkillsPin
}

// source identifies where s is installed from: its local dir for a plugin
// skill, else its repo and pin. The record keeps it, so a later plan can tell
// whether an installed skill is current.
func (p *Plan) source(s Skill) string {
	if s.Dir != "" {
		return s.Dir
	}
	repo, pin := p.origin(s)
	if pin == "" {
		return repo
	}
	return repo + "@" + pin
}

// skill returns the planned skill called name.
func (p *Plan) skill(name string) Skill {
	i := slices.IndexFunc(p.skillSet, func(s Skill) bool { return s.Name == name })
	return p.skillSet[i]
}
