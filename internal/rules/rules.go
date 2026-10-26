// Package rules loads the rules agents install can write into a harness's
// global instructions file, and manages the marked block that holds them.
package rules

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	builtin "github.com/kalverra/agents/rules"
)

// SourceBuiltin is Rule.Source for rules embedded in the agents binary.
const SourceBuiltin = "builtin"

// Rule is one markdown fragment. Name is the file name without ".md".
type Rule struct {
	Name   string `json:"name"`
	Source string `json:"source"` // SourceBuiltin or the file path
	Body   string `json:"-"`
}

// Load returns the built-in rules plus every <name>.md in userDir and
// pluginDirs, sorted by name. When names clash, a user rule wins, then a
// built-in rule, then the earliest plugin's. Missing dirs add nothing.
func Load(userDir string, pluginDirs ...string) ([]Rule, error) {
	byName := map[string]Rule{}
	// Load from lowest to highest precedence; later loads replace earlier ones.
	for _, dir := range slices.Backward(pluginDirs) {
		if err := loadDir(byName, dir); err != nil {
			return nil, err
		}
	}
	if err := loadFS(byName, builtin.FS, func(string) string { return SourceBuiltin }); err != nil {
		return nil, fmt.Errorf("loading built-in rules: %w", err)
	}
	if err := loadDir(byName, userDir); err != nil {
		return nil, err
	}

	out := make([]Rule, 0, len(byName))
	for _, r := range byName {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Rule) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// loadDir adds every *.md in dir to byName. A missing dir adds nothing.
func loadDir(byName map[string]Rule, dir string) error {
	source := func(file string) string { return filepath.Join(dir, file) }
	err := loadFS(byName, os.DirFS(dir), source)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("loading rules from %s: %w", dir, err)
	}
	return nil
}

// loadFS adds every *.md at the root of fsys to byName. source maps a file
// name to Rule.Source.
func loadFS(byName map[string]Rule, fsys fs.FS, source func(file string) string) error {
	if _, err := fs.Stat(fsys, "."); err != nil {
		return err
	}
	files, err := fs.Glob(fsys, "*.md")
	if err != nil {
		return err
	}
	for _, file := range files {
		body, err := fs.ReadFile(fsys, file)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(file, ".md")
		byName[name] = Rule{Name: name, Source: source(file), Body: string(body)}
	}
	return nil
}

// Names returns the names of rules, in order.
func Names(rules []Rule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.Name
	}
	return out
}

// Select returns the rules whose names are in names, in rules order. It errors
// on a name that matches no rule.
func Select(rules []Rule, names []string) ([]Rule, error) {
	var out []Rule
	for _, name := range names {
		if !slices.ContainsFunc(rules, func(r Rule) bool { return r.Name == name }) {
			return nil, fmt.Errorf("unknown rule %q (available: %s)", name, strings.Join(Names(rules), ", "))
		}
	}
	for _, r := range rules {
		if slices.Contains(names, r.Name) {
			out = append(out, r)
		}
	}
	return out, nil
}

// Render joins the rules into the content of a managed block.
func Render(rules []Rule) string {
	var b strings.Builder
	b.WriteString("## Global rules from agents\n\n")
	b.WriteString("Machine-wide default instructions. Local rules take precedence.\n")
	for _, r := range rules {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(r.Body))
		b.WriteString("\n")
	}
	return b.String()
}
