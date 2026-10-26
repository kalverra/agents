package github

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	jiraRegex        = regexp.MustCompile(`\[([^\]]+)\]\(https?://[^)]*(?:atlassian\.net|jira)[^)]*\)`)
	htmlCommentRegex = regexp.MustCompile(`<!--[\s\S]*?-->`)
	mdImageRegex     = regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`)
	htmlImageRegex   = regexp.MustCompile(`(?i)<img[^>]+>`)
)

// StripBloat removes JIRA links, HTML comments, and image tags from markdown.
func StripBloat(body string) string {
	body = jiraRegex.ReplaceAllString(body, "$1")
	body = htmlCommentRegex.ReplaceAllString(body, "")
	body = mdImageRegex.ReplaceAllString(body, "")
	body = htmlImageRegex.ReplaceAllString(body, "")
	return strings.TrimSpace(body)
}

func isBot(author string) bool {
	lower := strings.ToLower(author)
	if strings.HasSuffix(lower, "[bot]") || strings.HasSuffix(lower, "-bot") ||
		lower == "github-actions" || lower == "cl-sonarqube-production" || lower == "trunk-io" || lower == "sonarcloud" || strings.Contains(lower, "bot") {
		return true
	}
	return false
}

// WritePRHeader writes the PR title and metadata block.
func WritePRHeader(b *strings.Builder, pr *PR) {
	fmt.Fprintf(b, "# PR #%d: %s\n\n", pr.Number, pr.Title)
	fmt.Fprintf(b, "- **URL:** %s\n", pr.URL)
	fmt.Fprintf(b, "- **Branch:** %s -> %s\n", pr.HeadRef, pr.BaseRef)
	fmt.Fprintf(b, "- **Author:** %s\n", pr.Author)
	if pr.ReviewDecision != "" {
		fmt.Fprintf(b, "- **Review Decision:** %s\n", pr.ReviewDecision)
	}
}

// WritePRReviewSections writes description, reviews, and comment threads.
func WritePRReviewSections(b *strings.Builder, pr *PR, _ bool) {
	b.WriteString("\n## PR Description\n\n")
	if pr.Body != "" {
		b.WriteString(StripBloat(pr.Body))
		b.WriteString("\n")
	} else {
		b.WriteString("*(no description)*\n")
	}

	WriteReviews(b, pr.Reviews)

	// Skip printing threads here since they will be interleaved in the diff by review.FormatBundle
}

// WriteReviews appends formatted PR review summaries.
func WriteReviews(b *strings.Builder, reviews []Review) {
	var filtered []Review
	for _, r := range reviews {
		if isBot(r.Author) {
			continue
		}
		if strings.TrimSpace(r.Body) == "" && r.State == "COMMENTED" {
			continue
		}
		filtered = append(filtered, r)
	}

	if len(filtered) == 0 {
		return
	}
	b.WriteString("\n## Reviews\n\n")
	for _, r := range filtered {
		date := r.CreatedAt.Format("2006-01-02")
		fmt.Fprintf(b, "### @%s -- %s (%s)\n", r.Author, r.State, date)
		body := StripBloat(r.Body)
		if body != "" {
			fmt.Fprintf(b, "%s\n", body)
		}
		b.WriteString("\n")
	}
}

// WriteUnresolvedThreads appends unresolved inline review threads.
func WriteUnresolvedThreads(b *strings.Builder, threads []ReviewThread) {
	fmt.Fprintf(b, "\n## Unresolved Review Threads (%d)\n\n", len(threads))
	if len(threads) == 0 {
		b.WriteString("No unresolved threads.\n")
		return
	}
	for i, t := range threads {
		formatThread(b, t)
		if i < len(threads)-1 {
			b.WriteString("\n---\n\n")
		}
	}
}

// WriteGeneralComments appends top-level PR conversation comments.
func WriteGeneralComments(b *strings.Builder, comments []Comment) {
	var filtered []Comment
	for _, c := range comments {
		if !isBot(c.Author) {
			filtered = append(filtered, c)
		}
	}

	if len(filtered) == 0 {
		return
	}
	b.WriteString("\n## General Comments\n\n")
	for _, c := range filtered {
		date := c.CreatedAt.Format("2006-01-02")
		fmt.Fprintf(b, "**@%s** (%s):\n", c.Author, date)
		writeQuoted(b, StripBloat(c.Body))
		b.WriteString("\n")
	}
}

// WriteResolvedThreads appends resolved inline review threads.
func WriteResolvedThreads(b *strings.Builder, threads []ReviewThread, full bool) {
	if len(threads) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Resolved Threads (%d)\n\n", len(threads))
	if !full {
		b.WriteString("*(pass --include-resolved to expand)*\n")
		return
	}
	for i, t := range threads {
		formatThread(b, t)
		if i < len(threads)-1 {
			b.WriteString("\n---\n\n")
		}
	}
}

func formatThread(b *strings.Builder, t ReviewThread) {
	loc := t.Path
	if t.IsFileLevel() {
		loc = fmt.Sprintf("%s (file comment)", t.Path)
	} else if t.Line > 0 {
		if t.StartLine > 0 && t.StartLine != t.Line {
			loc = fmt.Sprintf("%s:%d-%d", t.Path, t.StartLine, t.Line)
		} else {
			loc = fmt.Sprintf("%s:%d", t.Path, t.Line)
		}
	}
	label := "Thread"
	if t.IsOutdated {
		label = "Thread (outdated)"
	}
	fmt.Fprintf(b, "### %s: %s\n", label, loc)
	for _, c := range t.Comments {
		date := c.CreatedAt.Format("2006-01-02")
		fmt.Fprintf(b, "**@%s** (%s):\n", c.Author, date)
		writeQuoted(b, StripBloat(c.Body))
		writeCommentSuggestions(b, c)
		b.WriteString("\n")
	}
	if hunk := threadDiffHunkHuman(t); hunk != "" {
		b.WriteString("\n**Diff context:**\n")
		fmt.Fprintf(b, "```diff\n%s\n```\n", strings.TrimRight(hunk, "\n"))
	}
}

func threadDiffHunkHuman(t ReviewThread) string {
	for _, c := range t.Comments {
		if c.DiffHunk != "" {
			return c.DiffHunk
		}
	}
	return ""
}

func writeCommentSuggestions(b *strings.Builder, c Comment) {
	for i, s := range c.Suggestions {
		label := "Suggested change"
		if len(c.Suggestions) > 1 {
			label = fmt.Sprintf("Suggested change %d", i+1)
		}
		if s.Source == SuggestionSourceAutomated {
			label += " (Copilot)"
		}
		fmt.Fprintf(b, "\n**%s:**\n", label)
		if s.Path != "" {
			fmt.Fprintf(b, "Path: `%s`", s.Path)
			if s.StartLine > 0 {
				if s.EndLine > 0 && s.EndLine != s.StartLine {
					fmt.Fprintf(b, ":%d-%d", s.StartLine, s.EndLine)
				} else {
					fmt.Fprintf(b, ":%d", s.StartLine)
				}
			}
			b.WriteString("\n")
		}
		fmt.Fprintf(b, "```\n%s\n```\n", s.Code)
	}
}

func writeQuoted(b *strings.Builder, body string) {
	for line := range strings.SplitSeq(body, "\n") {
		fmt.Fprintf(b, "> %s\n", line)
	}
}

// FormatPR formats a PR into a readable string.
func FormatPR(pr *PR, includeResolved bool) string {
	var b strings.Builder
	WritePRHeader(&b, pr)
	WritePRReviewSections(&b, pr, includeResolved)

	var resolved, unresolved []ReviewThread
	for _, t := range pr.Threads {
		if t.IsResolved {
			resolved = append(resolved, t)
		} else {
			unresolved = append(unresolved, t)
		}
	}
	WriteUnresolvedThreads(&b, unresolved)
	WriteGeneralComments(&b, pr.Comments)
	WriteResolvedThreads(&b, resolved, includeResolved)

	return b.String()
}
