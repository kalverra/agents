package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

var (
	diffLinkRe    = regexp.MustCompile(`(\]\()diff:([^)#\s]+)(?:#L(\d+)(?:-L?(\d+))?)?(\))`)
	diffHrefRe    = regexp.MustCompile(`(href=")diff:([^"#\s]+)(?:#L(\d+)(?:-L?(\d+))?)?(")`)
	htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	extraBlankRe  = regexp.MustCompile(`\n{3,}`)
)

// PrepareDescription converts a local description.md body into the published PR body:
// HTML comments are stripped and diff: links are expanded against prURL.
// Fenced code blocks are left untouched.
func PrepareDescription(body, prURL string) string {
	segments := splitFenced(body)
	for i, seg := range segments {
		if seg.fenced {
			continue
		}
		text := htmlCommentRe.ReplaceAllString(seg.text, "")
		segments[i].text = expandDiffLinks(text, prURL)
	}

	var b strings.Builder
	for _, seg := range segments {
		b.WriteString(seg.text)
	}
	return strings.TrimSpace(extraBlankRe.ReplaceAllString(b.String(), "\n\n"))
}

// DiffAnchor returns the GitHub "Files changed" URL for path within a PR,
// optionally targeting a right-side line or line range (end of 0 means single line).
func DiffAnchor(prURL, path string, start, end int) string {
	sum := sha256.Sum256([]byte(path))
	url := fmt.Sprintf("%s/files#diff-%s", strings.TrimRight(prURL, "/"), hex.EncodeToString(sum[:]))
	if start > 0 {
		url += fmt.Sprintf("R%d", start)
		if end > start {
			url += fmt.Sprintf("-R%d", end)
		}
	}
	return url
}

// expandDiffLinks rewrites markdown [x](diff:...) and HTML href="diff:..." references.
// HTML form is needed inside <summary>, where GitHub does not render markdown.
func expandDiffLinks(text, prURL string) string {
	for _, re := range []*regexp.Regexp{diffLinkRe, diffHrefRe} {
		text = re.ReplaceAllStringFunc(text, func(m string) string {
			parts := re.FindStringSubmatch(m)
			var start, end int
			_, _ = fmt.Sscanf(parts[3], "%d", &start)
			_, _ = fmt.Sscanf(parts[4], "%d", &end)
			return parts[1] + DiffAnchor(prURL, parts[2], start, end) + parts[5]
		})
	}
	return text
}

type mdSegment struct {
	text   string
	fenced bool
}

// splitFenced splits markdown into alternating plain and fenced-code segments.
func splitFenced(body string) []mdSegment {
	var (
		segments []mdSegment
		cur      strings.Builder
		fence    string
	)
	flush := func(fenced bool) {
		if cur.Len() > 0 {
			segments = append(segments, mdSegment{text: cur.String(), fenced: fenced})
			cur.Reset()
		}
	}

	for _, line := range strings.SplitAfter(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case fence == "" && fenceMarker(trimmed) != "":
			flush(false)
			fence = fenceMarker(trimmed)
			cur.WriteString(line)
		case fence != "" && strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "":
			cur.WriteString(line)
			flush(true)
			fence = ""
		default:
			cur.WriteString(line)
		}
	}
	flush(fence != "")
	return segments
}

// fenceMarker returns the opening fence run (3+ backticks or tildes) of line, or "".
func fenceMarker(line string) string {
	if line == "" || (line[0] != '`' && line[0] != '~') {
		return ""
	}
	n := len(line) - len(strings.TrimLeft(line, line[:1]))
	if n < 3 {
		return ""
	}
	return line[:n]
}
