package implement

// ClaudeHarness drives Claude Code (claude).
//
// Headless: claude -p <prompt> --output-format stream-json
// Resume:   claude -p <prompt> --resume <id> --output-format stream-json
// TUI:      claude "<prompt>" (positional prompt opens interactive session)
type ClaudeHarness struct {
	HomeDir string // optional override for testing
}

// Name implements Harness.
func (ClaudeHarness) Name() string { return HarnessClaude }

// RunArgs implements Harness.
func (ClaudeHarness) RunArgs(spec RoleSpec, prompt, sessionID string) []string {
	args := []string{"-p", prompt, "--output-format", "stream-json"}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	args = append(args, "--model", spec.Model)
	if spec.Effort != "" {
		args = append(args, "--effort", spec.Effort)
	}
	return append(args, "--permission-mode", "bypassPermissions")
}

// SessionIDFromEvent implements Harness.
// claude stream-json carries session_id on system/init and result events.
func (ClaudeHarness) SessionIDFromEvent(line []byte) string {
	return firstEventField(line, "session_id", "sessionID")
}

// TextFromEvent implements Harness.
func (ClaudeHarness) TextFromEvent(line []byte) string {
	return extractResultText(line)
}

// ActivityFromEvent implements Harness.
// claude emits tool calls as assistant events with tool_use content blocks.
func (ClaudeHarness) ActivityFromEvent(line []byte) string {
	return claudeToolName(line)
}

// UsageFromEvent implements Harness.
// claude reports session usage on result events (authoritative for the run).
func (ClaudeHarness) UsageFromEvent(line []byte) (Usage, bool) {
	return claudeUsage(line)
}

// CumulativeUsageFromEvent implements Harness.
// claude's result event reports session-cumulative usage; it supersedes
// any summed per-event usage for accounting.
func (ClaudeHarness) CumulativeUsageFromEvent(line []byte) (Usage, bool) {
	return claudeUsage(line)
}

// Attach implements Harness.
func (ClaudeHarness) Attach(sessionID string) string {
	return "claude --resume " + sessionID
}
