package implement

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUsageFromEvents(t *testing.T) {
	t.Parallel()
	claudeFull := `{"type":"result","subtype":"success","session_id":"s1",` +
		`"usage":{"input_tokens":300,"output_tokens":40,"cache_read_input_tokens":10,"cache_creation_input_tokens":5},` +
		`"total_cost_usd":0.0123}`
	claudeNoCost := `{"type":"result","usage":{"input_tokens":5,"output_tokens":6}}`
	opencodeFull := `{"type":"step_finish","part":{"type":"step-finish",` +
		`"tokens":{"input":2000,"output":300,"reasoning":100,"cache":{"read":0,"write":0}},"cost":0.05}}`
	opencodeNoCost := `{"type":"step_finish","part":{"type":"step-finish",` +
		`"tokens":{"input":50,"output":10,"reasoning":0,"cache":{"read":5,"write":5}}}}`

	cases := []struct {
		name    string
		h       Harness
		line    string
		want    Usage
		present bool
	}{
		{
			"claude result with cost",
			ClaudeHarness{},
			claudeFull,
			Usage{In: 315, Out: 40, CostUSD: 0.0123, HasCost: true},
			true,
		},
		{
			"claude result without cost",
			ClaudeHarness{},
			claudeNoCost,
			Usage{In: 5, Out: 6},
			true,
		},
		{"claude result zero usage", ClaudeHarness{}, `{"type":"result","usage":{}}`, Usage{}, false},
		{
			"claude non-result ignored",
			ClaudeHarness{},
			`{"type":"assistant","message":{"usage":{"input_tokens":9,"output_tokens":9}}}`,
			Usage{},
			false,
		},
		{"claude invalid json", ClaudeHarness{}, `garbage`, Usage{}, false},
		{
			"opencode usage with cost",
			OpenCodeHarness{},
			opencodeFull,
			Usage{In: 2000, Out: 400, CostUSD: 0.05, HasCost: true},
			true,
		},
		{
			"opencode usage without cost",
			OpenCodeHarness{},
			opencodeNoCost,
			Usage{In: 60, Out: 10},
			true,
		},
		{"opencode no usage", OpenCodeHarness{}, `{"type":"step-start"}`, Usage{}, false},
		{
			// opencode must not misread claude-shaped usage (different keys).
			"opencode claude-shaped ignored",
			OpenCodeHarness{},
			claudeNoCost,
			Usage{},
			false,
		},
		{
			"agy claude-style result",
			AgyHarness{},
			claudeFull,
			Usage{In: 315, Out: 40, CostUSD: 0.0123, HasCost: true},
			true,
		},
		{
			"agy opencode-style usage",
			AgyHarness{},
			opencodeFull,
			Usage{In: 2000, Out: 400, CostUSD: 0.05, HasCost: true},
			true,
		},
		{
			"agy step_update usage",
			AgyHarness{},
			`{"event":"step_update","step_update":{"step_type":"agent_response","usage":{"input_tokens":10302,"output_tokens":582,"thinking_tokens":551,"cache_read_tokens":8113,"total_tokens":10884}}}`,
			Usage{In: 18415, Out: 582, CacheRead: 8113},
			true,
		},
		{
			"agy result usage",
			AgyHarness{},
			`{"event":"result","result":{"status":"SUCCESS","usage":{"input_tokens":10418,"output_tokens":589,"thinking_tokens":551,"cache_read_tokens":8113,"total_tokens":11007}}}`,
			Usage{In: 18531, Out: 589, CacheRead: 8113},
			true,
		},
		{"agy unknown event", AgyHarness{}, `{"type":"status","status":"thinking"}`, Usage{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := tc.h.UsageFromEvent([]byte(tc.line))
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.present, ok)
		})
	}
}

func TestCumulativeUsageFromEvents(t *testing.T) {
	t.Parallel()
	agyStep := `{"event":"step_update","step_update":{"step_type":"agent_response",` +
		`"usage":{"input_tokens":10302,"output_tokens":582,"thinking_tokens":551,` +
		`"cache_read_tokens":8113,"total_tokens":10884}}}`
	agyResult := `{"event":"result","result":{"status":"SUCCESS",` +
		`"usage":{"input_tokens":10418,"output_tokens":589,"thinking_tokens":551,` +
		`"cache_read_tokens":8113,"total_tokens":11007}}}`
	agyResultNoUsage := `{"event":"result","result":{"status":"SUCCESS"}}`
	claudeFull := `{"type":"result","subtype":"success","session_id":"s1",` +
		`"usage":{"input_tokens":300,"output_tokens":40,"cache_read_input_tokens":10,` +
		`"cache_creation_input_tokens":5},"total_cost_usd":0.0123}`
	opencodeFinish := `{"type":"step_finish","part":{"type":"step-finish",` +
		`"tokens":{"input":2000,"output":300,"reasoning":100,"cache":{"read":0,"write":0}},"cost":0.05}}`

	cases := []struct {
		name    string
		h       Harness
		line    string
		want    Usage
		present bool
	}{
		{
			// agy's result event reports conversation-cumulative totals;
			// it is the authoritative accounting report for the run.
			"agy result is cumulative",
			AgyHarness{},
			agyResult,
			Usage{In: 18531, Out: 589, CacheRead: 8113},
			true,
		},
		{
			// Per-turn step_update usage is NOT cumulative: each turn
			// re-counts the whole shared context.
			"agy step_update is not cumulative",
			AgyHarness{},
			agyStep,
			Usage{},
			false,
		},
		{"agy result without usage", AgyHarness{}, agyResultNoUsage, Usage{}, false},
		{
			// agy sometimes emits claude-shaped streams; their result
			// events carry the same cumulative semantics.
			"agy claude-style result is cumulative",
			AgyHarness{},
			claudeFull,
			Usage{In: 315, Out: 40, CostUSD: 0.0123, HasCost: true},
			true,
		},
		{
			"claude result is cumulative",
			ClaudeHarness{},
			claudeFull,
			Usage{In: 315, Out: 40, CostUSD: 0.0123, HasCost: true},
			true,
		},
		{
			"claude non-result is not cumulative",
			ClaudeHarness{},
			`{"type":"assistant","message":{"usage":{"input_tokens":9,"output_tokens":9}}}`,
			Usage{},
			false,
		},
		{
			// opencode only reports per-step usage; no cumulative event.
			"opencode step_finish is not cumulative",
			OpenCodeHarness{},
			opencodeFinish,
			Usage{},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := tc.h.CumulativeUsageFromEvent([]byte(tc.line))
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.present, ok)
		})
	}
}
