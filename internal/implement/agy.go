package implement

import (
	"strings"
)

// AgyHarness drives the Antigravity CLI (agy).
//
// Headless: agy --print <prompt> --output-format stream-json
// Resume:   agy --print <prompt> --conversation <id> --output-format stream-json
type AgyHarness struct{}

// DefaultAgyPrintTimeout overrides agy's own --print-timeout default of 5m,
// which kills mid-agentic-loop worker runs (the turn ends with an empty
// response before the model finishes its task).
const DefaultAgyPrintTimeout = "30m"

// agyModelEffort reconciles a spec's model with its effort. agy encodes
// reasoning effort as a model-name suffix (gemini-3.8-flash-high) and rejects
// a --effort flag that contradicts it:
//
//	invalid model selection (--model "gemini-3.8-flash-high" --effort "medium"):
//	--model gemini-3.8-flash-high conflicts with --effort=medium
//
// A per-step "**Effort**:" override sets Effort on a spec whose model already
// carries a suffix, which is exactly that conflict. An explicit effort wins:
// strip the suffix and let --effort carry it. With no explicit effort the
// model is passed through untouched, so the suffix keeps meaning what it says.
func agyModelEffort(spec RoleSpec) (model, effort string) {
	model, effort = spec.Model, spec.Effort
	if effort == "" {
		return model, ""
	}
	for _, suffix := range agyEffortSuffixes {
		if stripped, ok := strings.CutSuffix(model, suffix); ok {
			return stripped, effort
		}
	}
	return model, effort
}

// Name implements Harness.
func (AgyHarness) Name() string { return HarnessAgy }

// RunArgs implements Harness.
func (AgyHarness) RunArgs(spec RoleSpec, prompt, sessionID string) []string {
	args := []string{"--print", prompt, "--output-format", "stream-json"}
	if sessionID != "" {
		args = append(args, "--conversation", sessionID)
	}
	model, effort := agyModelEffort(spec)
	args = append(args, "--model", model)
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	timeout := spec.Timeout
	if timeout == "" {
		timeout = DefaultAgyPrintTimeout
	}
	args = append(args, "--print-timeout", timeout)
	return append(args, "--dangerously-skip-permissions")
}

// SessionIDFromEvent implements Harness.
func (AgyHarness) SessionIDFromEvent(line []byte) string {
	if sid := agySessionID(line); sid != "" {
		return sid
	}
	return firstEventField(line, "conversation_id", "conversationId", "session_id", "sessionID")
}

// TextFromEvent implements Harness.
func (AgyHarness) TextFromEvent(line []byte) string {
	if txt := agyText(line); txt != "" {
		return txt
	}
	return extractResultText(line)
}

// ActivityFromEvent implements Harness.
func (AgyHarness) ActivityFromEvent(line []byte) string {
	if label := agyToolLabel(line); label != "" {
		return label
	}
	if name := claudeToolName(line); name != "" {
		return name
	}
	return opencodeToolLabel(line)
}

// UsageFromEvent implements Harness.
func (AgyHarness) UsageFromEvent(line []byte) (Usage, bool) {
	if u, ok := agyUsage(line); ok {
		return u, true
	}
	if u, ok := claudeUsage(line); ok {
		return u, true
	}
	return opencodeUsage(line)
}

// CumulativeUsageFromEvent implements Harness.
// agy's final result event reports conversation-cumulative usage; per-turn
// step_update events each re-count the whole shared context, so the result
// event supersedes them for accounting. claude-style result events carry
// the same semantics when agy emits that stream shape.
func (AgyHarness) CumulativeUsageFromEvent(line []byte) (Usage, bool) {
	if u, ok := agyResultUsage(line); ok {
		return u, true
	}
	return claudeUsage(line)
}

// Attach implements Harness.
func (AgyHarness) Attach(sessionID string) string {
	return "agy --conversation " + sessionID
}
