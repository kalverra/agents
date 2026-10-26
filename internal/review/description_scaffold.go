package review

import (
	"strings"

	"github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/pkg/git"
)

// ScaffoldDescription returns a template markdown string for description.md.
// Guidance lives in HTML comments, which update-description strips before publishing.
func ScaffoldDescription(pr *github.PR, diff *git.BranchDiffResult) string {
	var b strings.Builder

	if pr != nil && strings.TrimSpace(pr.Body) != "" {
		b.WriteString("<!-- Existing PR description context:\n")
		b.WriteString(github.StripBloat(pr.Body))
		b.WriteString("\n-->\n\n")
	}

	b.WriteString("## Why\n\n")
	b.WriteString("<!-- ONE sentence: problem + how this PR fixes it. -->\n\n")

	b.WriteString("## Notes\n\n")
	b.WriteString(
		"<!-- 0-3 bullets: breaking contracts, migrations, risky paths, decisions reviewers must know. " +
			"Omit section if none. -->\n\n",
	)

	b.WriteString("## Changes\n\n")
	b.WriteString(
		"<!-- 0-2 simple mermaid charts (1-line caption each) only when logic or flow changes. Omit otherwise. -->\n\n",
	)
	b.WriteString("```mermaid\nflowchart LR\n```\n\n")
	b.WriteString("<!-- Then one collapsed <details> per smaller change: <summary> = 1-line caption,\n")
	b.WriteString(
		"     body = 1-3 short lines with [label](diff:path/to/file.go#L10-L20) links. No code or diff blocks.\n",
	)
	b.WriteString("     Inside <summary> use HTML only: <a href=\"diff:path/to/file.go#L10-L20\">, <code>, <b>.\n")
	b.WriteString("     Skip trivial/plumbing files. -->\n\n")
	b.WriteString("<details>\n<summary>Caption</summary>\n\n- \n\n</details>\n")
	if diff != nil {
		var paths []string
		for _, f := range diff.Files {
			if f.Status == "added" || f.Status == "modified" || f.Status == "deleted" {
				paths = append(paths, f.Path)
			}
		}
		if len(paths) > 0 {
			b.WriteString("<!-- Changed files: ")
			b.WriteString(strings.Join(paths, ", "))
			b.WriteString(" -->\n")
		}
	}
	b.WriteString("\n")

	b.WriteString("## Merge Danger\n\n")
	b.WriteString("<!-- Badges: 🟢 trivial/small · 🟡 minor/medium · 🟠 large · 🔴 major/extreme -->\n\n")
	b.WriteString("| | Rating | Why |\n")
	b.WriteString("| --- | --- | --- |\n")
	b.WriteString("| **Revert** | 🟢 **trivial** / 🟡 **minor** / 🔴 **major** | <rationale> |\n")
	b.WriteString("| **Blast radius** | 🟢 **small** / 🟡 **medium** / 🟠 **large** / 🔴 **extreme** | <rationale> |\n")

	return b.String()
}
