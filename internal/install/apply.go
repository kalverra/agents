package install

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/kalverra/agents/internal/ghskill"
	"github.com/kalverra/agents/internal/harness"
	"github.com/kalverra/agents/internal/rules"
	"github.com/kalverra/agents/internal/settings"
)

// Result is what Apply or Uninstall did.
type Result struct {
	Harnesses []string `json:"harnesses,omitempty"`
	Removed   []string `json:"removed,omitempty"`
	// SkillCommands are gh commands the user must run because gh is missing
	// or too old.
	SkillCommands []string `json:"skill_commands,omitempty"`
	// LeftoverSkills are skill dirs agents no longer manages. gh skill has no
	// remove command, so the user deletes them.
	LeftoverSkills []string `json:"leftover_skills,omitempty"`
}

// Apply carries out p and updates the record. On error the record still
// describes what was written, so the caller should save it either way. gh
// output goes to out.
func (i *Installer) Apply(p *Plan, out io.Writer) (*Result, error) {
	res := &Result{}
	for _, name := range p.Remove {
		if err := i.uninstallHarness(name, res); err != nil {
			return res, err
		}
	}
	block := rules.Render(p.rules)
	for _, hp := range p.Harnesses {
		if err := i.applyHarness(p, hp, block, out, res); err != nil {
			return res, fmt.Errorf("%s: %w", hp.Name, err)
		}
		res.Harnesses = append(res.Harnesses, hp.Name)
	}
	return res, nil
}

func (i *Installer) applyHarness(p *Plan, hp HarnessPlan, block string, out io.Writer, res *Result) error {
	rec := i.Record.harness(hp.Name)

	if rec.Instructions != "" && rec.Instructions != hp.Instructions {
		if err := rules.RemoveBlock(rec.Instructions); err != nil {
			return err
		}
	}
	if len(p.rules) == 0 {
		if err := rules.RemoveBlock(hp.Instructions); err != nil {
			return err
		}
		rec.Instructions = ""
	} else {
		if err := rules.WriteBlock(hp.Instructions, block); err != nil {
			return err
		}
		rec.Instructions = hp.Instructions
	}

	if p.permissions {
		owned, err := settings.Apply(hp.entries, rec.Permissions, harness.Migrate([]harness.Harness{hp.harness}))
		if err != nil {
			return err
		}
		rec.Permissions = owned
	}

	if p.skills {
		return i.applySkills(p, hp, rec, out, res)
	}
	return nil
}

func (i *Installer) applySkills(p *Plan, hp HarnessPlan, rec *HarnessRecord, out io.Writer, res *Result) error {
	skillsDir := hp.harness.SkillsDir()
	if hp.SkillAgent == "" {
		res.LeftoverSkills = append(res.LeftoverSkills, recordedSkillDirs(rec)...)
		rec.Skills, rec.SkillsDir = nil, ""
		return nil
	}
	if rec.SkillsDir != skillsDir {
		res.LeftoverSkills = append(res.LeftoverSkills, recordedSkillDirs(rec)...)
		rec.Skills = nil
	}
	rec.SkillsDir = skillsDir
	for _, name := range slices.Sorted(maps.Keys(rec.Skills)) {
		if !slices.Contains(p.Skills, name) {
			res.LeftoverSkills = append(res.LeftoverSkills, filepath.Join(skillsDir, name))
			delete(rec.Skills, name)
		}
	}

	for _, name := range hp.InstallSkills {
		s := p.skill(name)
		repo, pin := p.origin(s)
		args := ghskill.InstallArgs(repo, name, hp.SkillAgent, pin)
		if s.Dir != "" {
			args = ghskill.InstallLocalArgs(s.Dir, name, hp.SkillAgent)
		}
		if p.GhMissing {
			res.SkillCommands = append(res.SkillCommands, ghskill.Command(args))
			continue
		}
		if err := i.GH.Run(args, out, out); err != nil {
			return fmt.Errorf("%s: %w", ghskill.Command(args), err)
		}
		if rec.Skills == nil {
			rec.Skills = map[string]string{}
		}
		rec.Skills[name] = p.source(s)
	}
	if len(rec.Skills) == 0 {
		rec.SkillsDir = ""
	}
	return nil
}

// Uninstall removes everything the record lists from every harness.
func (i *Installer) Uninstall() (*Result, error) {
	res := &Result{}
	for _, name := range slices.Sorted(maps.Keys(i.Record.Harnesses)) {
		if err := i.uninstallHarness(name, res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// uninstallHarness removes name's rules block and permissions and drops it
// from the record. Its skills are reported as leftovers.
func (i *Installer) uninstallHarness(name string, res *Result) error {
	rec := i.Record.Harnesses[name]
	if rec.Instructions != "" {
		if err := rules.RemoveBlock(rec.Instructions); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if err := settings.Remove(rec.Permissions); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	res.LeftoverSkills = append(res.LeftoverSkills, recordedSkillDirs(rec)...)
	delete(i.Record.Harnesses, name)
	res.Removed = append(res.Removed, name)
	return nil
}

func recordedSkillDirs(rec *HarnessRecord) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(rec.Skills)) {
		out = append(out, filepath.Join(rec.SkillsDir, name))
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
