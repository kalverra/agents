package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kalverra/agents/internal/plugins"
	builtin "github.com/kalverra/agents/skills"
)

// Skill is a skill agents install can offer: a built-in one, which gh skill
// installs from ghskill.Repo; a plugin's, which it installs from Dir; or a
// remote one, which it installs from Repo.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Plugin and Dir are set for plugin skills. Dir holds the SKILL.md.
	Plugin string `json:"plugin,omitempty"`
	Dir    string `json:"dir,omitempty"`
	// Repo ("owner/repo") and Ref are set for remote skills. An empty Ref
	// installs gh's default: the latest release, else the default branch.
	Repo string `json:"repo,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

// ID is how config.toml and --skills name s: "owner/repo/name[@ref]" for a
// remote skill, else its name.
func (s Skill) ID() string {
	if s.Repo == "" {
		return s.Name
	}
	id := s.Repo + "/" + s.Name
	if s.Ref != "" {
		id += "@" + s.Ref
	}
	return id
}

// ParseRemoteSkill parses a remote skill ID, "owner/repo/name[@ref]". It
// returns false for any other form, such as a plain skill name.
func ParseRemoteSkill(id string) (Skill, bool) {
	path, ref, hasRef := strings.Cut(id, "@")
	parts := strings.Split(path, "/")
	if len(parts) != 3 || slices.Contains(parts, "") || (hasRef && ref == "") {
		return Skill{}, false
	}
	return Skill{Name: parts[2], Repo: parts[0] + "/" + parts[1], Ref: ref}, true
}

// withRemoteSkills adds the remote skills named in ids to all. A remote skill
// replaces any skill of the same name; of two remote skills with one name,
// the first wins.
func withRemoteSkills(all []Skill, ids []string) []Skill {
	var remote []Skill
	for _, id := range ids {
		s, ok := ParseRemoteSkill(id)
		if !ok || slices.ContainsFunc(remote, func(o Skill) bool { return o.Name == s.Name }) {
			continue
		}
		remote = append(remote, s)
	}
	out := slices.DeleteFunc(slices.Clone(all), func(s Skill) bool {
		return slices.ContainsFunc(remote, func(o Skill) bool { return o.Name == s.Name })
	})
	return append(out, remote...)
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
