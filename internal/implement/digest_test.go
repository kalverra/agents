package implement

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Real event shapes observed from agy stream-json runs; claude and opencode
// shapes mirror the probes in streamjson.go.
func TestEventDigest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		line    string
		want    []string // substrings the fragment must contain
		notWant []string
		empty   bool
	}{
		{
			"agy tool done with command",
			`{"event":"step_update","step_update":{"step_index":2,"state":"DONE","step_type":"tool","tool_name":"run_command","duration_seconds":1.018459,"tool_info":{"name":"run_command","parameters":{"CommandLine":"rtk gh pr view 114"},"output":"PR body here"}}}`,
			[]string{"### run_command", "rtk gh pr view 114", "PR body here"},
			nil,
			false,
		},
		{
			"agy tool done with target file",
			`{"event":"step_update","step_update":{"step_index":5,"state":"DONE","step_type":"tool","tool_name":"write_to_file","duration_seconds":0.023,"tool_info":{"name":"write_to_file","parameters":{"TargetFile":"/tmp/x.go"}}}}`,
			[]string{"### write_to_file", "/tmp/x.go"},
			nil,
			false,
		},
		{
			"agy tool active skipped",
			`{"event":"step_update","step_update":{"step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"ls"}}}}`,
			nil,
			nil,
			true,
		},
		{
			"agy malformed line skipped",
			`{"event":"step_update","step_update":{"state":"DONE","step_type":"tool","tool_name":"run_command","tool_info":{"parameters":{"CommandLine":"cat big"},"output":"AAAA"}}`,
			// malformed JSON (unclosed) must not render
			nil,
			nil,
			true,
		},
		{
			"agy agent response text",
			`{"event":"step_update","step_update":{"step_index":2,"state":"DONE","step_type":"agent_response","text_delta":"tests pass now"}}`,
			[]string{"agent: tests pass now"},
			nil,
			false,
		},
		{
			"agy agent response without text skipped",
			`{"event":"step_update","step_update":{"step_index":1,"state":"DONE","step_type":"agent_response","duration_seconds":5.36}}`,
			nil,
			nil,
			true,
		},
		{
			"agy user input skipped",
			`{"event":"step_update","step_update":{"step_index":0,"state":"DONE","step_type":"user_input"}}`,
			nil,
			nil,
			true,
		},
		{
			"agy result",
			`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"did the thing","duration_seconds":294.2,"num_turns":1,"usage":{"input_tokens":241109,"output_tokens":18276}}}`,
			[]string{"## result: SUCCESS", "241109 in / 18276 out", "did the thing"},
			nil,
			false,
		},
		{
			"agy empty response not shown",
			`{"event":"result","result":{"status":"SUCCESS","response":"","usage":{"input_tokens":10,"output_tokens":2}}}`,
			[]string{"## result: SUCCESS"},
			[]string{"response:"},
			false,
		},
		{
			"claude assistant tool use",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`,
			[]string{"### Read"},
			nil,
			false,
		},
		{
			"claude result",
			`{"type":"result","subtype":"success","result":"all done","usage":{"input_tokens":300,"output_tokens":40,"cache_read_input_tokens":10,"cache_creation_input_tokens":5},"total_cost_usd":0.0123}`,
			[]string{"## result: success", "all done", "315 in / 40 out"},
			nil,
			false,
		},
		{
			"opencode tool part",
			`{"type":"part-start","part":{"type":"tool","tool":"read","state":{"title":"Read step file"}}}`,
			[]string{"### Read step file"},
			nil,
			false,
		},
		{
			"opencode text part",
			`{"type":"message.part.updated","part":{"type":"text","text":"working on it"}}`,
			[]string{"agent: working on it"},
			nil,
			false,
		},
		{
			"opencode tool part without title falls back to tool name",
			`{"type":"message.part.updated","part":{"type":"tool","tool":"bash"}}`,
			[]string{"### bash"},
			nil,
			false,
		},
		{"invalid json", `not json at all`, nil, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EventDigest([]byte(tc.line))
			if tc.empty {
				assert.Empty(t, got)
				return
			}
			for _, want := range tc.want {
				assert.Contains(t, got, want)
			}
			for _, notWant := range tc.notWant {
				assert.NotContains(t, got, notWant)
			}
		})
	}
}

func TestEventDigestTruncatesLongOutput(t *testing.T) {
	t.Parallel()
	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'A'
	}
	line := `{"event":"step_update","step_update":{"state":"DONE","step_type":"tool","tool_name":"run_command","tool_info":{"parameters":{"CommandLine":"cat"},"output":"` + string(
		long,
	) + `"}}}`

	got := EventDigest([]byte(line))
	assert.Less(t, len(got), 2000, "long tool output must be truncated")
	assert.Contains(t, got, "### run_command")
}

func TestEventDigestParamFallbackJSON(t *testing.T) {
	t.Parallel()
	// Unknown parameter names render as compact JSON instead of nothing.
	line := `{"event":"step_update","step_update":{"state":"DONE","step_type":"tool","tool_name":"custom_tool","tool_info":{"parameters":{"Widget":"zap"}}}}`

	got := EventDigest([]byte(line))
	assert.Contains(t, got, "### custom_tool")
	assert.Contains(t, got, "Widget")
}
