// Package commit provides prompt templates for commit message generation.
package commit

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed commit.md
var defaultPrompt string

// Prompt is a loaded commit prompt template.
type Prompt struct {
	Body string
}

// Render inserts the commit XML into the prompt.
func (p Prompt) Render(commitXML string) string {
	return strings.Replace(p.Body, "{{COMMIT_XML}}", commitXML, 1)
}

// LoadDefault returns the embedded prompt template.
func LoadDefault() (Prompt, error) {
	if defaultPrompt == "" {
		return Prompt{}, fmt.Errorf("embedded commit prompt is empty")
	}
	return Prompt{Body: defaultPrompt}, nil
}

// Load returns the commit prompt, preferring a file in overrideDir if present.
func Load(overrideDir string) (Prompt, error) {
	if overrideDir != "" {
		path := filepath.Join(overrideDir, "commit.md")
		if data, err := os.ReadFile(path); err == nil { //nolint:gosec
			return Prompt{Body: string(data)}, nil
		} else if !os.IsNotExist(err) {
			return Prompt{}, fmt.Errorf("reading override prompt %s: %w", path, err)
		}
	}
	return LoadDefault()
}
