package review_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/agents/internal/review"
)

const testPRURL = "https://github.com/acme/repo/pull/7"

func pathHash(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}

func TestDiffAnchor(t *testing.T) {
	t.Parallel()

	base := testPRURL + "/files#diff-" + pathHash("pkg/a.go")
	assert.Equal(t, base, review.DiffAnchor(testPRURL, "pkg/a.go", 0, 0))
	assert.Equal(t, base, review.DiffAnchor(testPRURL+"/", "pkg/a.go", 0, 0))
	assert.Equal(t, base+"R10", review.DiffAnchor(testPRURL, "pkg/a.go", 10, 0))
	assert.Equal(t, base+"R10-R20", review.DiffAnchor(testPRURL, "pkg/a.go", 10, 20))
}

func TestPrepareDescription_Links(t *testing.T) {
	t.Parallel()

	a := testPRURL + "/files#diff-" + pathHash("pkg/a.go")
	b := testPRURL + "/files#diff-" + pathHash("cmd/b.go")

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"file only", "[a](diff:pkg/a.go)", "[a](" + a + ")"},
		{"single line", "[a](diff:pkg/a.go#L5)", "[a](" + a + "R5)"},
		{"range", "[a](diff:pkg/a.go#L5-L9)", "[a](" + a + "R5-R9)"},
		{"range short end", "[a](diff:pkg/a.go#L5-9)", "[a](" + a + "R5-R9)"},
		{
			"multiple",
			"see [a](diff:pkg/a.go#L1) and [b](diff:cmd/b.go)",
			"see [a](" + a + "R1) and [b](" + b + ")",
		},
		{
			"html href in summary",
			`<summary><a href="diff:pkg/a.go#L3-L4"><code>a.go</code></a> change</summary>`,
			`<summary><a href="` + a + `R3-R4"><code>a.go</code></a> change</summary>`,
		},
		{"non-diff link untouched", "[x](https://example.com)", "[x](https://example.com)"},
		{
			"fenced code untouched",
			"```md\n[a](diff:pkg/a.go)\n<!-- keep -->\n```",
			"```md\n[a](diff:pkg/a.go)\n<!-- keep -->\n```",
		},
		{
			"nested fence untouched",
			"````md\n```go\n```\n[a](diff:pkg/a.go)\n````\n[a](diff:pkg/a.go)",
			"````md\n```go\n```\n[a](diff:pkg/a.go)\n````\n[a](" + a + ")",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, review.PrepareDescription(tt.in, testPRURL))
		})
	}
}

func TestPrepareDescription_StripsComments(t *testing.T) {
	t.Parallel()

	in := "<!-- old body\nspans lines\n-->\n\n## Why\n\n<!-- guidance -->\n\nFix bug.\n\n## Changes\n\n<!-- a\nb -->\n<!-- files -->\n\nStuff.\n"
	assert.Equal(t, "## Why\n\nFix bug.\n\n## Changes\n\nStuff.", review.PrepareDescription(in, testPRURL))
}
