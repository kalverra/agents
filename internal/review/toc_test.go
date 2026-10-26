package review

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTOC(t *testing.T) {
	t.Parallel()

	entries := []TOCEntry{
		{
			Title:     "`core/router.go`",
			Anchor:    "#coreroutergo-10--2",
			Tier:      TierSemantic,
			Changes:   "+10 -2",
			Threads:   1,
			StartLine: 45,
			EndLine:   80,
		},
		{
			Title:     "`tools/docker/.env`",
			Anchor:    "#toolsdockerenv",
			Tier:      TierUnreadable,
			Changes:   "-",
			StartLine: 82,
			EndLine:   90,
		},
		{
			Title:     "*Formatting & Imports (43 files)*",
			Anchor:    "#formatting--low-signal-changes",
			Tier:      TierFormatting,
			Changes:   "+45 -45",
			StartLine: 92,
			EndLine:   130,
		},
	}

	toc := RenderTOC(entries)

	assert.Contains(t, toc, "## Table of Contents")
	assert.Contains(t, toc, "| File / Section | Tier | Changes | Threads | Bundle Lines |")
	assert.Contains(t, toc, "| [`core/router.go`](#coreroutergo-10--2) | Semantic | +10 -2 | 1 unres. | L45-L80 |")
	assert.Contains(t, toc, "| [`tools/docker/.env`](#toolsdockerenv) | Unreadable | - | - | L82-L90 |")
	assert.Contains(
		t,
		toc,
		"| [*Formatting & Imports (43 files)*](#formatting--low-signal-changes) | Formatting | +45 -45 | - | L92-L130 |",
	)
}

func TestBuildTOC_NoThreads_OmitsThreadsColumn(t *testing.T) {
	t.Parallel()

	entries := []TOCEntry{
		{
			Title:     "`core/router.go`",
			Anchor:    "#coreroutergo-10--2",
			Tier:      TierSemantic,
			Changes:   "+10 -2",
			Threads:   0,
			StartLine: 45,
			EndLine:   80,
		},
	}

	toc := RenderTOC(entries)

	assert.Contains(t, toc, "| File / Section | Tier | Changes | Bundle Lines |")
	assert.NotContains(t, toc, "Threads")
	assert.Contains(t, toc, "| [`core/router.go`](#coreroutergo-10--2) | Semantic | +10 -2 | L45-L80 |")
}

func TestInsertTOC_AccurateOffsets(t *testing.T) {
	t.Parallel()

	// A document template with placeholder for TOC
	docWithoutTOC := strings.Join([]string{
		"# PR #1: Test",
		"- **Branch**: `feat` -> `main`",
		"",
		"{{TOC_PLACEHOLDER}}",
		"",
		"## Semantic Changes",
		"",
		"### `main.go` (+2 -1)",
		"",
		"```diff",
		"+line1",
		"+line2",
		"-line3",
		"```",
	}, "\n")

	entries := []TOCEntry{
		{
			Title:     "`main.go`",
			Anchor:    "#maingo-2--1",
			Tier:      TierSemantic,
			Changes:   "+2 -1",
			StartLine: 0, // to be calculated
			EndLine:   0,
		},
	}

	finalDoc := AssembleBundleWithTOC(docWithoutTOC, entries, func(doc string) []TOCEntry {
		// Measures line where ### `main.go` starts
		lines := strings.Split(doc, "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "### `main.go`") {
				return []TOCEntry{
					{
						Title:     "`main.go`",
						Anchor:    "#maingo-2--1",
						Tier:      TierSemantic,
						Changes:   "+2 -1",
						StartLine: i + 1,
						EndLine:   len(lines),
					},
				}
			}
		}
		return nil
	})

	assert.NotContains(t, finalDoc, "{{TOC_PLACEHOLDER}}")
	lines := strings.Split(finalDoc, "\n")

	// Find the exact line in finalDoc where ### `main.go` appears
	mainLine := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "### `main.go`") {
			mainLine = i + 1
			break
		}
	}
	require.NotEqual(t, -1, mainLine)

	// Check that TOC mentions L<mainLine>
	assert.Contains(t, finalDoc, fmt.Sprintf("L%d", mainLine))
}
