package implement

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActivityFromEvents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		h    Harness
		line string
		want string
	}{
		{
			"opencode tool with title",
			OpenCodeHarness{},
			`{"type":"part-start","part":{"type":"tool","tool":"read","state":{"title":"Read file.go","status":"pending"}}}`,
			"Read file.go",
		},
		{
			"opencode tool without title",
			OpenCodeHarness{},
			`{"type":"part-start","part":{"type":"tool","tool":"bash"}}`,
			"bash",
		},
		{"opencode text part", OpenCodeHarness{}, `{"type":"part-start","part":{"type":"text","text":"hi"}}`, ""},
		{"opencode result", OpenCodeHarness{}, `{"type":"result","result":"done"}`, ""},
		{"opencode invalid json", OpenCodeHarness{}, `not json`, ""},
		{
			"claude tool use",
			ClaudeHarness{},
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{}}]}}`,
			"Edit",
		},
		{
			"claude text block",
			ClaudeHarness{},
			`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
			"",
		},
		{"claude result", ClaudeHarness{}, `{"type":"result","subtype":"success","result":"done"}`, ""},
		{"claude invalid json", ClaudeHarness{}, `garbage`, ""},
		{
			"agy claude-style tool use",
			AgyHarness{},
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`,
			"Read",
		},
		{
			"agy opencode-style tool part",
			AgyHarness{},
			`{"type":"part-start","part":{"type":"tool","tool":"read","state":{"title":"Read file.go"}}}`,
			"Read file.go",
		},
		{
			"claude tool with target",
			ClaudeHarness{},
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"render.go"}}]}}`,
			"Read render.go",
		},
		{
			"agy native tool step",
			AgyHarness{},
			`{"event":"step_update","step_update":{"step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"echo hello"}}}}`,
			"run_command echo hello",
		},
		{
			"agy native tool with file",
			AgyHarness{},
			`{"event":"step_update","step_update":{"step_type":"tool","tool_name":"write_to_file","tool_info":{"name":"write_to_file","parameters":{"TargetFile":"internal/flow/render.go"}}}}`,
			"write_to_file render.go",
		},
		{"agy unknown event", AgyHarness{}, `{"type":"status","status":"thinking"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.h.ActivityFromEvent([]byte(tc.line)))
		})
	}
}
