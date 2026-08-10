// Package eval implements the prompt evaluation harness.
package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kalverra/agents/internal/markdown"
)

// Case represents a single eval test case loaded from YAML.
type Case struct {
	Name              string   `yaml:"name"`
	Tags              []string `yaml:"tags"`
	Description       string   `yaml:"description"`
	SystemPrompt      string   `yaml:"system_prompt"`
	SystemPromptFile  string   `yaml:"system_prompt_file"`
	SystemPromptFiles []string `yaml:"system_prompt_files"`
	UserMessage       string   `yaml:"user_message"`
	ReferenceAnswer   string   `yaml:"reference_answer"`
	Criteria          Criteria `yaml:"criteria"`
	FileName          string   `yaml:"-"` // populated at load time
}

// Criteria defines the 5-point rubric for judging.
type Criteria struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Score1      string `yaml:"score_1"`
	Score2      string `yaml:"score_2"`
	Score3      string `yaml:"score_3"`
	Score4      string `yaml:"score_4"`
	Score5      string `yaml:"score_5"`
}

// LoadCases reads all YAML test cases and JSON live feedback records from dir, optionally filtering by tag.
func LoadCases(dir, tagFilter string) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading cases dir: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".json")) {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)

	var cases []Case
	for _, f := range files {
		var c Case
		if strings.HasSuffix(f, ".json") {
			rec, err := LoadLiveFeedback(f)
			if err != nil {
				return nil, fmt.Errorf("parsing live feedback %s: %w", f, err)
			}
			c = ConvertLiveToCase(rec)
		} else {
			data, err := os.ReadFile(f) //nolint:gosec
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", f, err)
			}
			if err := yaml.Unmarshal(data, &c); err != nil {
				return nil, fmt.Errorf("parsing %s: %w", f, err)
			}
		}

		c.FileName = filepath.Base(f)
		if c.Name == "" {
			c.Name = c.FileName
		}

		if tagFilter != "" && !containsTag(c.Tags, tagFilter) {
			continue
		}
		cases = append(cases, c)
	}

	if len(cases) == 0 {
		msg := fmt.Sprintf("no cases found in %s", dir)
		if tagFilter != "" {
			msg += fmt.Sprintf(" with tag %q", tagFilter)
		}
		return nil, fmt.Errorf("%s", msg)
	}

	return cases, nil
}

// LoadSystemPrompt resolves the system prompt from inline text or file references.
func LoadSystemPrompt(c Case, repoRoot string) (string, error) {
	if c.SystemPrompt != "" {
		return strings.TrimSpace(c.SystemPrompt), nil
	}

	if len(c.SystemPromptFiles) > 0 {
		var parts []string
		for _, rel := range c.SystemPromptFiles {
			content, err := loadAndMerge(filepath.Join(repoRoot, rel), repoRoot)
			if err != nil {
				return "", err
			}
			parts = append(parts, strings.TrimSpace(content))
		}
		return strings.Join(parts, "\n\n---\n\n"), nil
	}

	if c.SystemPromptFile != "" {
		content, err := loadAndMerge(filepath.Join(repoRoot, c.SystemPromptFile), repoRoot)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(content), nil
	}

	return "", nil
}

func loadAndMerge(path, repoRoot string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("system_prompt_file not found: %s", path)
	}
	userSrc := filepath.Join(repoRoot, "USER_AGENTS.md")
	return markdown.MergeUserAgents(string(data), userSrc)
}

func containsTag(tags []string, tag string) bool {
	return slices.Contains(tags, tag)
}
