package review

import (
	"fmt"
	"strings"
)

// TOCEntry represents an entry in the Table of Contents table.
type TOCEntry struct {
	Title     string
	Anchor    string
	Tier      FileTier
	Changes   string
	Threads   int
	DiffPath  string
	StartLine int
	EndLine   int
}

// RenderTOC formats the slice of TOC entries into a Markdown table.
func RenderTOC(entries []TOCEntry) string {
	if len(entries) == 0 {
		return ""
	}

	hasThreads := false
	hasDiffs := false
	for _, e := range entries {
		if e.Threads > 0 {
			hasThreads = true
		}
		if e.DiffPath != "" {
			hasDiffs = true
		}
	}

	var b strings.Builder
	b.WriteString("## Table of Contents\n\n")
	switch {
	case hasThreads && hasDiffs:
		b.WriteString("| File / Section | Tier | Changes | Threads | Diff | Bundle Lines |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	case hasThreads && !hasDiffs:
		b.WriteString("| File / Section | Tier | Changes | Threads | Bundle Lines |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
	case !hasThreads && hasDiffs:
		b.WriteString("| File / Section | Tier | Changes | Diff | Bundle Lines |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
	default:
		b.WriteString("| File / Section | Tier | Changes | Bundle Lines |\n")
		b.WriteString("| --- | --- | --- | --- |\n")
	}

	for _, e := range entries {
		titleLink := e.Title
		if e.Anchor != "" {
			titleLink = fmt.Sprintf("[%s](%s)", e.Title, e.Anchor)
		}

		tierName := string(e.Tier)
		switch e.Tier {
		case TierSemantic:
			tierName = "Semantic"
		case TierFormatting:
			tierName = "Formatting"
		case TierDeletion:
			tierName = "Deletion"
		case TierUnreadable:
			tierName = "Unreadable"
		}

		linesStr := "-"
		if e.StartLine > 0 && e.EndLine >= e.StartLine {
			linesStr = fmt.Sprintf("L%d-L%d", e.StartLine, e.EndLine)
		} else if e.StartLine > 0 {
			linesStr = fmt.Sprintf("L%d", e.StartLine)
		}

		threadsStr := "-"
		if e.Threads > 0 {
			threadsStr = fmt.Sprintf("%d unres.", e.Threads)
		}

		diffStr := "-"
		if e.DiffPath != "" {
			diffStr = fmt.Sprintf("[`diff`](%s)", e.DiffPath)
		}

		switch {
		case hasThreads && hasDiffs:
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
				titleLink, tierName, e.Changes, threadsStr, diffStr, linesStr)
		case hasThreads && !hasDiffs:
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
				titleLink, tierName, e.Changes, threadsStr, linesStr)
		case !hasThreads && hasDiffs:
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
				titleLink, tierName, e.Changes, diffStr, linesStr)
		default:
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
				titleLink, tierName, e.Changes, linesStr)
		}
	}

	return b.String()
}

// AssembleBundleWithTOC replaces the {{TOC_PLACEHOLDER}} in the template with a TOC
// whose line numbers are computed accurately via a two-pass calculation.
func AssembleBundleWithTOC(
	docWithPlaceholder string,
	initialEntries []TOCEntry,
	measureFn func(doc string) []TOCEntry,
) string {
	placeholder := "{{TOC_PLACEHOLDER}}"
	if !strings.Contains(docWithPlaceholder, placeholder) {
		return docWithPlaceholder
	}

	// First pass: render TOC with initial entries to establish baseline size
	firstPassTOC := RenderTOC(initialEntries)
	tempDoc := strings.Replace(docWithPlaceholder, placeholder, firstPassTOC, 1)

	// Measure exact line offsets on tempDoc
	updatedEntries := measureFn(tempDoc)

	// Second pass: render TOC with measured exact offsets
	finalTOC := RenderTOC(updatedEntries)
	return strings.Replace(docWithPlaceholder, placeholder, finalTOC, 1)
}
