package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/kalverra/agents/internal/plugins"
	builtin "github.com/kalverra/agents/skills"
)

// Skill is a skill agents install can offer: a built-in one, which gh skill
// installs from ghskill.Repo, or a plugin's, which it installs from Dir.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Plugin and Dir are set for plugin skills. Dir holds the SKILL.md.
	Plugin string `json:"plugin,omitempty"`
	Dir    string `json:"dir,omitempty"`
}

// BuiltinSkills lists the skills embedded in this binary, by name.
func BuiltinSkills() ([]Skill, error) {
	return readSkills(builtin.FS)
}

// PluginSkills lists the skills in p's skills/ dir, by name.
func PluginSkills(p plugins.Plugin) ([]Skill, error) {
	fsys := os.DirFS(p.SkillsDir())
	if _, err := fs.Stat(fsys, "."); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	list, err := readSkills(fsys)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", p.Name, err)
	}
	for k := range list {
		list[k].Plugin = p.Name
		list[k].Dir = filepath.Join(p.SkillsDir(), list[k].Name)
	}
	return list, nil
}

// readSkills reads */SKILL.md in fsys. A skill's name is its dir name.
func readSkills(fsys fs.FS) ([]Skill, error) {
	files, err := fs.Glob(fsys, "*/SKILL.md")
	if err != nil {
		return nil, err
	}
	out := make([]Skill, 0, len(files))
	for _, file := range files {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, err
		}
		s, err := parseFrontmatter(data)
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", file, err)
		}
		s.Name = path.Dir(file)
		out = append(out, s)
	}
	return out, nil
}

func parseFrontmatter(data []byte) (Skill, error) {
	const fence = "---\n"
	if !bytes.HasPrefix(data, []byte(fence)) {
		return Skill{}, nil
	}
	front, _, ok := bytes.Cut(data[len(fence):], []byte("\n"+fence))
	if !ok {
		return Skill{}, fmt.Errorf("unterminated frontmatter")
	}
	var s Skill
	if err := yaml.Unmarshal(front, &s); err != nil {
		return Skill{}, err
	}
	return s, nil
}
