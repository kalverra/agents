// Package commit formats staged git diffs and ticket context for LLM commit message generation.
package commit

import (
	"fmt"
	"strings"

	"github.com/kalverra/agents/internal/git"
	"github.com/kalverra/agents/internal/ticket"
)

// FormatBundle renders staged diff and optional ticket details as an XML document.
func FormatBundle(diff *git.BranchDiffResult, tk *ticket.Ticket) string {
	var b strings.Builder

	if diff != nil && diff.CurrentBranch != "" {
		fmt.Fprintf(&b, "<commit_context branch=%q>\n", diff.CurrentBranch)
	} else {
		b.WriteString("<commit_context>\n")
	}

	if tk != nil {
		fmt.Fprintf(&b, "  <ticket id=%q", tk.ID)
		if tk.Status != "" {
			fmt.Fprintf(&b, " status=%q", tk.Status)
		}
		b.WriteString(">\n")
		fmt.Fprintf(&b, "    <title>%s</title>\n", xmlEscape(tk.Title))
		if tk.Description != "" {
			fmt.Fprintf(&b, "    <description>%s</description>\n", xmlEscape(tk.Description))
		}
		if tk.ParentID != "" || tk.ParentTitle != "" {
			fmt.Fprintf(&b, "    <epic id=%q title=%q/>\n", tk.ParentID, tk.ParentTitle)
		}
		b.WriteString("  </ticket>\n")
	}

	if diff != nil {
		var codeAdd, codeDel, codeCount int
		var toolAdd, toolDel, toolCount int
		for _, f := range diff.Files {
			if git.IsCodeFile(f.Path) {
				codeCount++
				codeAdd += f.Additions
				codeDel += f.Deletions
			} else {
				toolCount++
				toolAdd += f.Additions
				toolDel += f.Deletions
			}
		}

		b.WriteString("  <stats ")
		if codeCount > 0 {
			fmt.Fprintf(&b, "code=\"+%d -%d (%d files)\" ", codeAdd, codeDel, codeCount)
		}
		if toolCount > 0 {
			fmt.Fprintf(&b, "tooling=\"+%d -%d (%d files)\" ", toolAdd, toolDel, toolCount)
		}
		b.WriteString("/>\n\n")

		b.WriteString("  <diff>\n")
		for _, f := range diff.Files {
			fmt.Fprintf(
				&b,
				"    <file path=%q status=%q additions=\"%d\" deletions=\"%d\">\n",
				f.Path,
				f.Status,
				f.Additions,
				f.Deletions,
			)
			if f.Patch != "" {
				switch {
				case git.IsDependencyOrLockfile(f.Path):
					b.WriteString("      <skipped reason=\"dependency/lockfile/generated\" />\n")
				case !git.IsCodeFile(f.Path) && f.Status == "added" && f.Additions > 40:
					fmt.Fprintf(&b, "      <skipped reason=\"%d-line config file\" />\n", f.Additions)
				default:
					b.WriteString(f.Patch)
					b.WriteByte('\n')
				}
			}
			b.WriteString("    </file>\n")
		}
		b.WriteString("  </diff>\n")
	}

	b.WriteString("</commit_context>\n")
	return b.String()
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
