package github

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleAutomatedSuggestionHTML = `<script type="application/json" data-target="react-partial.embeddedData">{"props":{"comment":{"automatedComment":{"suggestion":{"diffEntries":[{"path":"core/capabilities/fakes/gateway/local.go","diffLines":[{"text":"@@ -63,6 +63,8 @@","type":"HUNK","right":62},{"text":"\t\t\tw.WriteHeader(http.StatusOK)","type":"CONTEXT","right":63},{"text":"\t\tcase <-r.Context().Done():","type":"CONTEXT","right":64},{"text":"\t\t\thttp.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)","type":"CONTEXT","right":65},{"text":"\t\tdefault:","type":"ADDITION","right":66},{"text":"\t\t\thttp.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)","type":"ADDITION","right":67},{"text":"\t\t}","type":"CONTEXT","right":68}]}]},"suggestionState":"present"}}}}</script>`

func TestDecodeNodeDatabaseID(t *testing.T) {
	t.Parallel()

	got, err := decodeNodeDatabaseID("PRRT_kwDOBqSue86LblCt")
	require.NoError(t, err)
	assert.Equal(t, int64(2339262637), got)
}

func TestParseAutomatedSuggestionsFromHTML(t *testing.T) {
	t.Parallel()

	got, err := parseAutomatedSuggestionsFromHTML(sampleAutomatedSuggestionHTML)
	require.NoError(t, err)
	require.Len(t, got, 1)

	assert.Equal(t, "core/capabilities/fakes/gateway/local.go", got[0].Path)
	assert.Equal(t, SuggestionSourceAutomated, got[0].Source)
	assert.Contains(t, got[0].Code, "default:")
	assert.Contains(t, got[0].Code, "StatusTooManyRequests")
	assert.Equal(t, 63, got[0].StartLine)
	assert.Equal(t, 68, got[0].EndLine)
}

func TestBuildSuggestionFromDiffLines(t *testing.T) {
	t.Parallel()

	right66 := 66
	right67 := 67
	code, start, end := buildSuggestionFromDiffLines([]automatedDiffLine{
		{Text: "@@ -1 +1 @@", Type: "HUNK"},
		{Text: "\t\tcase <-ctx.Done():", Type: "CONTEXT", Right: &right66},
		{Text: "\t\tdefault:", Type: "ADDITION", Right: &right66},
		{Text: "\t\t\treturn err", Type: "ADDITION", Right: &right67},
	})

	assert.Equal(t, "\t\tcase <-ctx.Done():\n\t\tdefault:\n\t\t\treturn err", code)
	require.NotNil(t, start)
	require.NotNil(t, end)
	assert.Equal(t, 66, *start)
	assert.Equal(t, 67, *end)
}

func TestIsCopilotAuthor(t *testing.T) {
	t.Parallel()

	assert.True(t, isCopilotAuthor("Copilot"))
	assert.True(t, isCopilotAuthor("copilot-pull-request-reviewer"))
	assert.False(t, isCopilotAuthor("kalverra"))
}
