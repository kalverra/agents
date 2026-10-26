package implement

// OpenCodeHarness drives opencode.
//
// Headless: opencode run <msg> --format json
// Resume:   opencode run <msg> --session <id> --format json
type OpenCodeHarness struct{}

// Name implements Harness.
func (OpenCodeHarness) Name() string { return HarnessOpenCode }

// RunArgs implements Harness.
func (OpenCodeHarness) RunArgs(spec RoleSpec, prompt, sessionID string) []string {
	args := []string{"run", prompt, "--format", "json", "--auto", "--model", spec.Model}
	if spec.Effort != "" {
		args = append(args, "--variant", spec.Effort)
	}
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}
	return args
}

// SessionIDFromEvent implements Harness.
// opencode JSON events carry sessionID (camelCase).
func (OpenCodeHarness) SessionIDFromEvent(line []byte) string {
	return firstEventField(line, "sessionID", "session_id")
}

// TextFromEvent implements Harness.
// opencode emits text via message part updates: {"part":{"type":"text","text":...}}.
func (OpenCodeHarness) TextFromEvent(line []byte) string {
	if txt := eventNestedText(line, "part", "text"); txt != "" {
		return txt
	}
	return extractResultText(line)
}

// ActivityFromEvent implements Harness.
// opencode emits tool activity via part updates: {"part":{"type":"tool",...}}.
func (OpenCodeHarness) ActivityFromEvent(line []byte) string {
	return opencodeToolLabel(line)
}

// UsageFromEvent implements Harness.
// opencode emits usage and cost on step-finish events.
func (OpenCodeHarness) UsageFromEvent(line []byte) (Usage, bool) {
	return opencodeUsage(line)
}

// CumulativeUsageFromEvent implements Harness.
// opencode reports only per-step usage on step-finish events; there is no
// cumulative final event, so per-step sums stand as the run totals.
func (OpenCodeHarness) CumulativeUsageFromEvent(_ []byte) (Usage, bool) {
	return Usage{}, false
}

// Attach implements Harness.
func (OpenCodeHarness) Attach(sessionID string) string {
	return "opencode --session " + sessionID
}
