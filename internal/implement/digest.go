package implement

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Digest rendering limits: tool output and agent text are truncated so one
// verbose run cannot balloon the markdown log.
const (
	digestOutputLimit   = 400
	digestTextLimit     = 2000
	digestResponseLimit = 2000
)

// digestParamOrder lists tool parameter keys rendered verbatim, most
// human-useful first. Parameters with other keys fall back to compact JSON.
var digestParamOrder = []string{
	"CommandLine", "AbsolutePath", "TargetFile",
	"SearchDirectory", "SearchPath", "Pattern", "Query",
}

// EventDigest renders one NDJSON stream event as a markdown fragment for the
// human-readable step-N-<role>.md debug logs. It probes the known event
// shapes (agy step_update/result, claude assistant/result, opencode part
// events) and returns "" for lines that carry nothing worth showing.
func EventDigest(line []byte) string {
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		return ""
	}
	switch event := obj["event"]; event {
	case "step_update":
		if su, ok := obj["step_update"].(map[string]any); ok {
			return digestAgyStep(su)
		}
		return ""
	case "result":
		if r, ok := obj["result"].(map[string]any); ok {
			return digestAgyResult(r)
		}
		return ""
	}
	switch obj["type"] {
	case "assistant":
		if name := claudeToolName(line); name != "" {
			return "### " + name + "\n"
		}
		return ""
	case "result":
		return digestClaudeResult(line)
	case "part-start", "part-updated", "message.part.updated", "message.part-start":
		return digestOpenCodePart(line)
	}
	return ""
}

// digestAgyStep renders agy step_update events: completed tool calls and
// agent text. ACTIVE events and non-text steps carry nothing durable.
func digestAgyStep(su map[string]any) string {
	if state, _ := su["state"].(string); state != "DONE" {
		return ""
	}
	switch stepType, _ := su["step_type"].(string); stepType {
	case "tool":
		return digestAgyTool(su)
	case "agent_response":
		if txt, _ := su["text_delta"].(string); strings.TrimSpace(txt) != "" {
			return "agent: " + truncate(strings.TrimSpace(txt), digestTextLimit) + "\n"
		}
		return ""
	default:
		return ""
	}
}

// digestAgyTool renders one completed agy tool call: name, duration,
// parameter summary, and truncated output.
func digestAgyTool(su map[string]any) string {
	name, _ := su["tool_name"].(string)
	var b strings.Builder
	b.WriteString("### " + name)
	if dur, ok := su["duration_seconds"].(float64); ok {
		fmt.Fprintf(&b, " (%.2fs)", dur)
	}
	b.WriteByte('\n')
	if info, ok := su["tool_info"].(map[string]any); ok {
		if params, ok := info["parameters"].(map[string]any); ok {
			if s := toolParamSummary(params); s != "" {
				fmt.Fprintf(&b, "  args: %s\n", truncate(strings.TrimSpace(s), digestOutputLimit))
			}
		}
		if out, ok := info["output"].(string); ok && strings.TrimSpace(out) != "" {
			fmt.Fprintf(&b, "  out: %s\n", truncate(collapseLines(out), digestOutputLimit))
		}
	}
	return b.String()
}

// digestAgyResult renders agy's terminal result event: status, duration,
// turns, response text, and cumulative usage.
func digestAgyResult(r map[string]any) string {
	status, _ := r["status"].(string)
	var b strings.Builder
	fmt.Fprintf(&b, "## result: %s", status)
	if dur, ok := r["duration_seconds"].(float64); ok {
		fmt.Fprintf(&b, " (%.1fs", dur)
		if turns, ok := r["num_turns"].(float64); ok {
			fmt.Fprintf(&b, ", %d turns", int64(turns))
		}
		b.WriteByte(')')
	}
	b.WriteByte('\n')
	if resp, ok := r["response"].(string); ok && strings.TrimSpace(resp) != "" {
		fmt.Fprintf(&b, "response: %s\n", truncate(collapseLines(resp), digestResponseLimit))
	}
	if usage, ok := r["usage"].(map[string]any); ok {
		if in, ok := usage["input_tokens"].(float64); ok {
			if out, ok := usage["output_tokens"].(float64); ok {
				fmt.Fprintf(&b, "usage: %d in / %d out\n", int64(in), int64(out))
			}
		}
	}
	return b.String()
}

// digestClaudeResult renders claude's terminal result event.
func digestClaudeResult(line []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## result: %s\n", eventField(line, "subtype"))
	if txt := extractResultText(line); txt != "" {
		fmt.Fprintf(&b, "response: %s\n", truncate(collapseLines(txt), digestResponseLimit))
	}
	if u, ok := claudeUsage(line); ok {
		fmt.Fprintf(&b, "usage: %d in / %d out\n", u.In, u.Out)
	}
	return b.String()
}

// digestOpenCodePart renders opencode part events: text as agent output,
// tool parts as a header with the state title or tool name.
func digestOpenCodePart(line []byte) string {
	if txt := eventNestedText(line, "part", "text"); strings.TrimSpace(txt) != "" {
		return "agent: " + truncate(strings.TrimSpace(txt), digestTextLimit) + "\n"
	}
	if label := opencodeToolLabel(line); label != "" {
		return "### " + label + "\n"
	}
	return ""
}

// toolParamSummary picks the most human-useful parameter value to show, or
// falls back to compact JSON for unknown parameter shapes.
func toolParamSummary(params map[string]any) string {
	for _, k := range digestParamOrder {
		if v, ok := params[k].(string); ok && v != "" {
			return v
		}
	}
	if len(params) == 0 {
		return ""
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	return string(raw)
}

// collapseLines flattens multi-line text into a single space-separated line
// so digest entries stay one line each.
func collapseLines(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
