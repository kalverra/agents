package implement

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgyHarnessArgs(t *testing.T) {
	t.Parallel()
	h := AgyHarness{}
	spec := RoleSpec{Harness: "agy", Model: "gemini-3.8-flash-medium"}

	assert.Equal(t, "agy", h.Name())

	wantRun := []string{
		"--print", "do work", "--output-format", "stream-json",
		"--model", "gemini-3.8-flash-medium",
		"--print-timeout", "30m",
		"--dangerously-skip-permissions",
	}
	assert.Equal(t, wantRun, h.RunArgs(spec, "do work", ""))

	wantResume := []string{
		"--print", "fix it", "--output-format", "stream-json",
		"--conversation", "abc123",
		"--model", "gemini-3.8-flash-medium",
		"--print-timeout", "30m",
		"--dangerously-skip-permissions",
	}
	assert.Equal(t, wantResume, h.RunArgs(spec, "fix it", "abc123"))

	specEffort := RoleSpec{Harness: "agy", Model: "gemini-3.8-flash-high", Effort: "high"}
	assert.Contains(t, strings.Join(h.RunArgs(specEffort, "x", ""), " "), "--effort high")

	// Explicit timeout overrides the default; agy's own default (5m) kills
	// mid-loop worker runs, so the harness must always pin one.
	specTimeout := RoleSpec{Harness: "agy", Model: "gemini-3.8-flash-medium", Timeout: "45m"}
	assert.Contains(t, h.RunArgs(specTimeout, "x", ""), "--print-timeout")
	timeoutIdx := indexOfArg(t, h.RunArgs(specTimeout, "x", ""), "--print-timeout")
	assert.Equal(t, "45m", h.RunArgs(specTimeout, "x", "")[timeoutIdx+1])
}

func indexOfArg(t *testing.T, args []string, flag string) int {
	t.Helper()
	for i, a := range args {
		if a == flag {
			return i
		}
	}
	t.Fatalf("flag %q not found in %v", flag, args)
	return -1
}

func TestClaudeHarnessArgs(t *testing.T) {
	t.Parallel()
	h := ClaudeHarness{}
	spec := RoleSpec{Harness: "claude", Model: "opus-5", Effort: "high"}

	assert.Equal(t, "claude", h.Name())

	wantRun := []string{
		"-p", "do work", "--output-format", "stream-json",
		"--model", "opus-5", "--effort", "high",
		"--permission-mode", "bypassPermissions",
	}
	assert.Equal(t, wantRun, h.RunArgs(spec, "do work", ""))

	wantResume := []string{
		"-p", "fix it", "--output-format", "stream-json",
		"--resume", "sess-42",
		"--model", "opus-5", "--effort", "high",
		"--permission-mode", "bypassPermissions",
	}
	assert.Equal(t, wantResume, h.RunArgs(spec, "fix it", "sess-42"))
}

func TestOpenCodeHarnessArgs(t *testing.T) {
	t.Parallel()
	h := OpenCodeHarness{}
	spec := RoleSpec{Harness: "opencode", Model: "fireworks-ai/accounts/fireworks/models/glm-5p3", Effort: "high"}

	assert.Equal(t, "opencode", h.Name())

	wantRun := []string{
		"run", "do work", "--format", "json", "--auto",
		"--model", "fireworks-ai/accounts/fireworks/models/glm-5p3",
		"--variant", "high",
	}
	assert.Equal(t, wantRun, h.RunArgs(spec, "do work", ""))

	wantResume := []string{
		"run", "fix it", "--format", "json", "--auto",
		"--model", "fireworks-ai/accounts/fireworks/models/glm-5p3",
		"--variant", "high",
		"--session", "s-1",
	}
	assert.Equal(t, wantResume, h.RunArgs(spec, "fix it", "s-1"))
}

func TestAttachStrings(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "agy --conversation c1", AgyHarness{}.Attach("c1"))
	assert.Equal(t, "claude --resume c1", ClaudeHarness{}.Attach("c1"))
	assert.Equal(t, "opencode --session c1", OpenCodeHarness{}.Attach("c1"))
}

func TestHarnessRegistry(t *testing.T) {
	t.Parallel()
	reg := harnessRegistry()
	for _, name := range ValidHarnesses {
		assert.Contains(t, reg, name)
	}
	_, err := harnessFor(reg, RoleSpec{Harness: "nope"})
	require.Error(t, err)
}

func TestSessionIDFromEvents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		h    Harness
		line string
		want string
	}{
		{
			"claude init",
			ClaudeHarness{},
			`{"type":"system","subtype":"init","session_id":"abc-1","model":"opus-5"}`,
			"abc-1",
		},
		{
			"claude result",
			ClaudeHarness{},
			`{"type":"result","subtype":"success","session_id":"abc-2","result":"done"}`,
			"abc-2",
		},
		{"claude none", ClaudeHarness{}, `{"type":"assistant","message":{"role":"assistant"}}`, ""},
		{
			"opencode session",
			OpenCodeHarness{},
			`{"type":"session.updated","sessionID":"ses_123","title":"wip"}`,
			"ses_123",
		},
		{"opencode none", OpenCodeHarness{}, `{"type":"message.part.updated","part":{"type":"text","text":"hi"}}`, ""},
		{"agy conversation id", AgyHarness{}, `{"type":"system","conversation_id":"conv-9"}`, "conv-9"},
		{"agy camelcase", AgyHarness{}, `{"type":"system","conversationId":"conv-10"}`, "conv-10"},
		{"agy session id fallback", AgyHarness{}, `{"type":"init","session_id":"s-77"}`, "s-77"},
		{"agy none", AgyHarness{}, `{"type":"text","text":"hello"}`, ""},
		{"invalid json", ClaudeHarness{}, `not json at all`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.h.SessionIDFromEvent([]byte(tc.line)))
		})
	}
}

func TestTextFromEvents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		h    Harness
		line string
		want string
	}{
		{
			"claude result text",
			ClaudeHarness{},
			`{"type":"result","subtype":"success","result":"all done"}`,
			"all done",
		},
		{"claude non-result", ClaudeHarness{}, `{"type":"system","result":"nope"}`, ""},
		{
			"opencode text part",
			OpenCodeHarness{},
			`{"type":"message.part.updated","part":{"type":"text","text":"working on it"}}`,
			"working on it",
		},
		{
			"opencode non-text part",
			OpenCodeHarness{},
			`{"type":"message.part.updated","part":{"type":"tool","tool":"bash"}}`,
			"",
		},
		{"agy result text", AgyHarness{}, `{"type":"result","result":"finished step"}`, "finished step"},
		{"invalid json", ClaudeHarness{}, `garbage`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.h.TextFromEvent([]byte(tc.line)))
		})
	}
}
