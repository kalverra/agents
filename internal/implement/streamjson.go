package implement

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
)

// eventField returns a top-level string field from a JSON event line.
// Returns "" if the line is not JSON, or the field is absent/non-string.
func eventField(line []byte, key string) string {
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		return ""
	}
	v, ok := obj[key].(string)
	if !ok {
		return ""
	}
	return v
}

// eventNestedText returns obj[field][sub] as a string, for nested event
// payloads like opencode's {"part":{"type":"text","text":"..."}}.
func eventNestedText(line []byte, field, sub string) string {
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		return ""
	}
	nested, ok := obj[field].(map[string]any)
	if !ok {
		return ""
	}
	v, ok := nested[sub].(string)
	if !ok {
		return ""
	}
	return v
}

// firstEventField returns the first non-empty value among candidate keys.
func firstEventField(line []byte, keys ...string) string {
	for _, k := range keys {
		if v := eventField(line, k); v != "" {
			return v
		}
	}
	return ""
}

// extractResultText pulls the final assistant text from a claude/agy-style
// result event: {"type":"result","result":"..."}.
func extractResultText(line []byte) string {
	if eventField(line, "type") != "result" {
		return ""
	}
	if txt := eventField(line, "result"); txt != "" {
		return txt
	}
	return eventField(line, "text")
}

// opencodePartEvent is the subset of opencode's part events that carries
// tool activity: {"part":{"type":"tool","tool":"read","state":{"title":...}}}.
type opencodePartEvent struct {
	Part struct {
		Type  string `json:"type"`
		Tool  string `json:"tool"`
		State struct {
			Title string `json:"title"`
		} `json:"state"`
	} `json:"part"`
}

// opencodeToolLabel returns the activity label from an opencode tool part
// event, preferring the human-readable state title. Returns "" otherwise.
func opencodeToolLabel(line []byte) string {
	var ev opencodePartEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return ""
	}
	if ev.Part.Type != "tool" {
		return ""
	}
	if ev.Part.State.Title != "" {
		return ev.Part.State.Title
	}
	return ev.Part.Tool
}

// claudeAssistantEvent is the subset of claude-style assistant events that
// carries tool calls: {"type":"assistant","message":{"content":[...]}}.
type claudeAssistantEvent struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type  string         `json:"type"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input,omitempty"`
		} `json:"content"`
	} `json:"message"`
}

// Usage is provider-reported token usage and cost from one stream event.
// In folds input + cache read + cache write tokens; Out folds output +
// reasoning/thinking tokens. Cost is provider-reported only, never computed.
// CacheRead is the subset of In served from cache (only agy populates it;
// it exists so agy cost estimation can bill cache reads at their own,
// cheaper rate instead of the full input rate - see pricing.go).
type Usage struct {
	In        int64
	Out       int64
	CacheRead int64
	CostUSD   float64
	HasCost   bool
}

// ToStepUsage converts one event report for accumulation into a step total.
func (u Usage) ToStepUsage() StepUsage {
	return StepUsage{
		InputTokens:  u.In,
		OutputTokens: u.Out,
		CacheRead:    u.CacheRead,
		CostUSD:      u.CostUSD,
		HasTokens:    u.In > 0 || u.Out > 0,
		HasCost:      u.HasCost,
	}
}

// claudeUsageEvent is the subset of claude's result events that carries
// session usage. Result usage is authoritative for the run; per-message
// usage events are ignored to avoid double-counting.
type claudeUsageEvent struct {
	Type         string           `json:"type"`
	Usage        claudeUsageField `json:"usage"`
	TotalCostUSD *float64         `json:"total_cost_usd"`
}

type claudeUsageField struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CacheRead    int64 `json:"cache_read_input_tokens"`
	CacheWrite   int64 `json:"cache_creation_input_tokens"`
}

// claudeUsage extracts usage from a claude-style result event.
func claudeUsage(line []byte) (Usage, bool) {
	var ev claudeUsageEvent
	if err := json.Unmarshal(line, &ev); err != nil || ev.Type != "result" {
		return Usage{}, false
	}
	u := Usage{
		In:  ev.Usage.InputTokens + ev.Usage.CacheRead + ev.Usage.CacheWrite,
		Out: ev.Usage.OutputTokens,
	}
	if ev.TotalCostUSD != nil {
		u.CostUSD = *ev.TotalCostUSD
		u.HasCost = true
	}
	if u.In == 0 && u.Out == 0 && !u.HasCost {
		return Usage{}, false
	}
	return u, true
}

// opencodeUsageEvent is the subset of opencode's events that carries usage
// and cost. Real opencode output nests both under "part" on a top-level
// "step_finish" event ("part.type" is the hyphenated "step-finish"):
// {"type":"step_finish","part":{"type":"step-finish","cost":...,"tokens":{...}}}.
type opencodeUsageEvent struct {
	Type string `json:"type"`
	Part *struct {
		Cost   *float64           `json:"cost"`
		Tokens opencodeUsageField `json:"tokens"`
	} `json:"part"`
}

type opencodeUsageField struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cache     struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

// opencodeUsage extracts usage from an opencode-style event.
func opencodeUsage(line []byte) (Usage, bool) {
	var ev opencodeUsageEvent
	if err := json.Unmarshal(line, &ev); err != nil || ev.Type != "step_finish" || ev.Part == nil {
		return Usage{}, false
	}
	tokens := ev.Part.Tokens
	u := Usage{
		In:  tokens.Input + tokens.Cache.Read + tokens.Cache.Write,
		Out: tokens.Output + tokens.Reasoning,
	}
	if ev.Part.Cost != nil {
		u.CostUSD = *ev.Part.Cost
		u.HasCost = true
	}
	if u.In == 0 && u.Out == 0 && !u.HasCost {
		return Usage{}, false
	}
	return u, true
}

// extractToolTarget extracts a concise target descriptor (e.g. file name or command)
// from a tool input parameter map.
func extractToolTarget(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	for _, k := range []string{"CommandLine", "command", "cmd"} {
		if v, ok := params[k].(string); ok && v != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, k := range []string{"TargetFile", "AbsolutePath", "file_path", "path", "file"} {
		if v, ok := params[k].(string); ok && v != "" {
			return filepath.Base(v)
		}
	}
	return ""
}

// claudeToolName returns the last tool name from a claude-style assistant
// event, or "". If a target file or command is available, it is appended.
func claudeToolName(line []byte) string {
	var ev claudeAssistantEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return ""
	}
	if ev.Type != "assistant" {
		return ""
	}
	for _, v := range slices.Backward(ev.Message.Content) {
		if v.Type == "tool_use" {
			target := extractToolTarget(v.Input)
			if target != "" {
				return v.Name + " " + target
			}
			return v.Name
		}
	}
	return ""
}

type agyStreamEvent struct {
	Event      string `json:"event"`
	StepUpdate *struct {
		StepType string `json:"step_type"`
		ToolName string `json:"tool_name"`
		ToolInfo *struct {
			Name       string         `json:"name"`
			Parameters map[string]any `json:"parameters"`
		} `json:"tool_info"`
		Usage     *agyUsagePayload `json:"usage"`
		TextDelta string           `json:"text_delta"`
	} `json:"step_update"`
	Result *struct {
		ConversationID string           `json:"conversation_id"`
		Status         string           `json:"status"`
		Response       string           `json:"response"`
		Usage          *agyUsagePayload `json:"usage"`
	} `json:"result"`
}

type agyUsagePayload struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ThinkingTokens  int64 `json:"thinking_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
}

// agyUsageFromPayload converts one agy usage payload into a Usage.
func agyUsageFromPayload(uField *agyUsagePayload) (Usage, bool) {
	u := Usage{
		In:        uField.InputTokens + uField.CacheReadTokens,
		Out:       uField.OutputTokens,
		CacheRead: uField.CacheReadTokens,
	}
	if u.In == 0 && u.Out == 0 {
		return Usage{}, false
	}
	return u, true
}

// agyUsage extracts usage from an agy step_update or result event.
func agyUsage(line []byte) (Usage, bool) {
	var ev agyStreamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return Usage{}, false
	}
	var uField *agyUsagePayload
	if ev.Result != nil && ev.Result.Usage != nil {
		uField = ev.Result.Usage
	} else if ev.StepUpdate != nil && ev.StepUpdate.Usage != nil {
		uField = ev.StepUpdate.Usage
	}
	if uField == nil {
		return Usage{}, false
	}
	return agyUsageFromPayload(uField)
}

// agyResultUsage extracts usage from agy's final result event only. The
// result event reports conversation-cumulative totals for the whole run;
// per-turn step_update events each re-count the whole shared context, so
// only the result event is authoritative for run accounting.
func agyResultUsage(line []byte) (Usage, bool) {
	var ev agyStreamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return Usage{}, false
	}
	if ev.Result == nil || ev.Result.Usage == nil {
		return Usage{}, false
	}
	return agyUsageFromPayload(ev.Result.Usage)
}

// agyToolLabel extracts a tool activity label from an agy step_update event.
func agyToolLabel(line []byte) string {
	var ev agyStreamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return ""
	}
	if ev.StepUpdate == nil || ev.StepUpdate.StepType != "tool" {
		return ""
	}
	name := ev.StepUpdate.ToolName
	var params map[string]any
	if ev.StepUpdate.ToolInfo != nil {
		if ev.StepUpdate.ToolInfo.Name != "" {
			name = ev.StepUpdate.ToolInfo.Name
		}
		params = ev.StepUpdate.ToolInfo.Parameters
	}
	target := extractToolTarget(params)
	if target != "" {
		return name + " " + target
	}
	return name
}

// agySessionID extracts a conversation_id from an agy event.
func agySessionID(line []byte) string {
	var ev struct {
		ConversationID string `json:"conversation_id"`
		StepUpdate     *struct {
			ConversationID string `json:"conversation_id"`
		} `json:"step_update"`
		Result *struct {
			ConversationID string `json:"conversation_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		return ""
	}
	if ev.ConversationID != "" {
		return ev.ConversationID
	}
	if ev.Result != nil && ev.Result.ConversationID != "" {
		return ev.Result.ConversationID
	}
	if ev.StepUpdate != nil && ev.StepUpdate.ConversationID != "" {
		return ev.StepUpdate.ConversationID
	}
	return ""
}

// agyText extracts response text or delta from an agy event.
func agyText(line []byte) string {
	var ev agyStreamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return ""
	}
	if ev.Result != nil && ev.Result.Response != "" {
		return ev.Result.Response
	}
	if ev.StepUpdate != nil && ev.StepUpdate.TextDelta != "" {
		return ev.StepUpdate.TextDelta
	}
	return ""
}
