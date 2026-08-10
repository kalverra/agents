// Package activity provides prompt templates for activity reports.
package activity

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed *.md
var defaults embed.FS

var modes = []string{"daily", "weekly", "monthly", "quarterly"}

// Prompt is a loaded prompt template.
type Prompt struct {
	Mode string
	Body string
}

// Render inserts the activity XML into the prompt.
func (p Prompt) Render(activityXML string) string {
	return strings.Replace(p.Body, "{{ACTIVITY_XML}}", activityXML, 1)
}

// LoadDefault returns the embedded prompt for mode.
func LoadDefault(mode string) (Prompt, error) {
	name := fmt.Sprintf("%s.md", mode)
	data, err := defaults.ReadFile(name)
	if err != nil {
		return Prompt{}, fmt.Errorf("no default prompt for mode %q: %w", mode, err)
	}
	return Prompt{Mode: mode, Body: string(data)}, nil
}

// Load returns the prompt for mode, preferring a file in overrideDir if present.
func Load(mode, overrideDir string) (Prompt, error) {
	if overrideDir != "" {
		path := filepath.Join(overrideDir, fmt.Sprintf("%s.md", mode))
		if data, err := os.ReadFile(path); err == nil { //nolint:gosec
			return Prompt{Mode: mode, Body: string(data)}, nil
		} else if !os.IsNotExist(err) {
			return Prompt{}, fmt.Errorf("reading override prompt %s: %w", path, err)
		}
	}
	return LoadDefault(mode)
}

// List returns supported modes.
func List() []string {
	return append([]string{}, modes...)
}
