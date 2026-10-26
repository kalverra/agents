package implement

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Harness abstracts one agent CLI (agy, claude, opencode).
// Implementations are pure argv/event builders; process execution goes
// through Runner so tests never spawn real agents.
type Harness interface {
	// Name is the harness identifier used in implement.yaml.
	Name() string
	// RunArgs builds the headless stream-JSON argv for worker/reviewer.
	// sessionID empty starts a new conversation; non-empty resumes it.
	RunArgs(spec RoleSpec, prompt, sessionID string) []string
	// SessionIDFromEvent extracts the session/conversation ID from one
	// NDJSON stream event line, or "" if the line carries none.
	SessionIDFromEvent(line []byte) string
	// TextFromEvent extracts assistant text from an event line, or "".
	TextFromEvent(line []byte) string
	// ActivityFromEvent extracts a short tool/activity label from one
	// NDJSON stream event line, or "" if the line carries none.
	ActivityFromEvent(line []byte) string
	// UsageFromEvent extracts provider-reported token usage and cost from
	// one NDJSON stream event line. ok is false when the line carries none.
	UsageFromEvent(line []byte) (Usage, bool)
	// CumulativeUsageFromEvent extracts usage from a final event that
	// reports conversation-cumulative totals for the whole run. When a
	// run emits one, it supersedes summed per-event usage for accounting;
	// per-turn events would double-count the shared context.
	CumulativeUsageFromEvent(line []byte) (Usage, bool)
	// Attach returns a human-runnable command to attach a TUI to a session.
	Attach(sessionID string) string
}

// Runner abstracts process execution. RunStream tees each stdout line to
// onLine (NDJSON stream events).
type Runner interface {
	RunStream(ctx context.Context, name string, args []string, dir string, onLine func(line []byte)) error
}

// ExecRunner is the real process runner.
type ExecRunner struct{}

// RunStream executes the agent CLI headless, streaming stdout lines.
func (ExecRunner) RunStream(
	ctx context.Context,
	name string,
	args []string,
	dir string,
	onLine func(line []byte),
) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // agent CLIs are user-configured binaries
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe for %s: %w", name, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}

	buf := make([]byte, 0, 64*1024)
	chunk := make([]byte, 32*1024)
	for {
		n, readErr := stdout.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			for {
				idx := bytes.IndexByte(buf, '\n')
				if idx < 0 {
					break
				}
				line := buf[:idx]
				buf = buf[idx+1:]
				if onLine != nil && len(bytes.TrimSpace(line)) > 0 {
					onLine(line)
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	// Flush any trailing line without newline.
	if rest := bytes.TrimSpace(buf); len(rest) > 0 && onLine != nil {
		onLine(rest)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s exited: %w (stderr: %s)", name, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// harnessRegistry returns the built-in harnesses keyed by name.
func harnessRegistry() map[string]Harness {
	return map[string]Harness{
		HarnessAgy:      AgyHarness{},
		HarnessClaude:   ClaudeHarness{},
		HarnessOpenCode: OpenCodeHarness{},
	}
}

// harnessFor resolves a RoleSpec's harness.
func harnessFor(registry map[string]Harness, spec RoleSpec) (Harness, error) {
	h, ok := registry[spec.Harness]
	if !ok {
		return nil, fmt.Errorf("no harness registered for %q", spec.Harness)
	}
	return h, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
