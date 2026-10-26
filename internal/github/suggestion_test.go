package github

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractSuggestions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		wantBody    string
		wantSuggest []Suggestion
	}{
		{
			name:        "no suggestion",
			body:        "please fix this",
			wantBody:    "please fix this",
			wantSuggest: nil,
		},
		{
			name:     "single suggestion",
			body:     "drop the prefix\n```suggestion\nfoo()\n```",
			wantBody: "drop the prefix",
			wantSuggest: []Suggestion{
				{Code: "foo()", Source: SuggestionSourceBody},
			},
		},
		{
			name:     "multiline suggestion preserves indentation",
			body:     "update secrets\n```suggestion\n      SLACK_TOKEN: ${{ secrets.FOO }}\n      SLACK_CHANNEL_ID: ${{ secrets.BAR }}\n```",
			wantBody: "update secrets",
			wantSuggest: []Suggestion{
				{
					Code:   "      SLACK_TOKEN: ${{ secrets.FOO }}\n      SLACK_CHANNEL_ID: ${{ secrets.BAR }}",
					Source: SuggestionSourceBody,
				},
			},
		},
		{
			name:     "multiple suggestions",
			body:     "two fixes\n```suggestion\na\n```\n```suggestion\nb\n```",
			wantBody: "two fixes",
			wantSuggest: []Suggestion{
				{Code: "a", Source: SuggestionSourceBody},
				{Code: "b", Source: SuggestionSourceBody},
			},
		},
		{
			name:     "suggestion only",
			body:     "```suggestion\nx := 1\n```",
			wantBody: "",
			wantSuggest: []Suggestion{
				{Code: "x := 1", Source: SuggestionSourceBody},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotBody, gotSuggest := ExtractSuggestions(tt.body)
			assert.Equal(t, tt.wantBody, gotBody)
			require.Equal(t, tt.wantSuggest, gotSuggest)
		})
	}
}
