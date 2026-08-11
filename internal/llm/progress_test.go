package llm_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/kalverra/agents/internal/llm"
)

func TestProgress_TreeFormatting(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	now := time.Now()
	currentTime := now

	p := llm.NewProgress(
		&buf,
		"daily report",
		"gemma4",
		false,
	) // false = disable ANSI colors for clean string assertion

	p.Now = func() time.Time { return currentTime }

	s1 := p.AddStep("Send Wake-on-LAN (AA:BB:CC:DD:EE:FF)")
	currentTime = currentTime.Add(2 * time.Second)
	s1.Complete("Magic packet sent")

	s2 := p.AddStep("Connect to Ollama (ollama-server:11434)")
	currentTime = currentTime.Add(5 * time.Second)
	s2.Complete("Host awake")

	s3 := p.AddStep("Generate Prompt")
	currentTime = currentTime.Add(3 * time.Second)
	s3.Fail(errors.New("connection reset"))

	p.Finish(time.Second * 10)

	out := buf.String()
	assert.Contains(t, out, "Generating daily report with gemma4")
	assert.Contains(t, out, "✓ Send Wake-on-LAN (AA:BB:CC:DD:EE:FF) (Magic packet sent) (2s)")
	assert.Contains(t, out, "✓ Connect to Ollama (ollama-server:11434) (Host awake) (5s)")
	assert.Contains(t, out, "✗ Generate Prompt (connection reset) (3s)")
	assert.Contains(t, out, "Finished daily report with gemma4 (10s)")
}

func TestProgress_TTY_AnimationAndTreeFormatting(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	now := time.Now()
	currentTime := now

	p := llm.NewProgress(&buf, "commit message", "gemma4:31b", false)
	p.Now = func() time.Time { return currentTime }
	p.IsTTY = true

	s1 := p.AddStep("Wake-on-LAN (10-FF-E0-33-DF-DE)")
	currentTime = currentTime.Add(1 * time.Second)
	p.Tick() // manual tick for spinner frame advance

	s1.Complete("Host woken up")

	s2 := p.AddStep("Connect to Ollama (192.168.1.176:11434)")
	currentTime = currentTime.Add(2 * time.Second)
	s2.Complete("Connected")

	p.Finish(3 * time.Second)

	out := buf.String()
	// Should contain ANSI cursor up escape sequence to overwrite header (2 lines)
	assert.Contains(t, out, ansi.CursorUp(2))
	// Step 1 becomes ├── when step 2 is added
	assert.Contains(t, out, "├── ✓ Wake-on-LAN (10-FF-E0-33-DF-DE) (Host woken up)")
	// Step 2 is └── as final step
	assert.Contains(t, out, "└── ✓ Connect to Ollama (192.168.1.176:11434) (Connected)")
	// Should show animated spinner frame tick
	assert.Contains(t, out, "⠙ Wake-on-LAN")
}

func TestProgress_TTY_Coloring(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := llm.NewProgress(&buf, "commit message", "gemma4:31b", true)
	p.IsTTY = true

	s1 := p.AddStep("Wake-on-LAN")
	s1.Complete("Host woken up")
	p.Finish(1 * time.Second)

	out := buf.String()
	// Lipgloss ANSI color sequences should be present when useColor and IsTTY are true
	assert.Contains(t, out, "\033[")
}

func TestProgress_TTY_HeaderCountupTimer(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	now := time.Now()
	currentTime := now

	p := llm.NewProgress(&buf, "commit message", "gemma4:31b", false)
	p.Now = func() time.Time { return currentTime }
	p.IsTTY = true

	s1 := p.AddStep("Wake-on-LAN")
	currentTime = currentTime.Add(5 * time.Second)
	p.Tick()

	out := buf.String()
	assert.Contains(t, out, "Generating commit message with gemma4:31b... (5s)")

	s1.Complete("Host woken up")
	p.Finish(5 * time.Second)
}

func TestProgress_TTY_SingleHeaderLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	now := time.Now()
	currentTime := now

	p := llm.NewProgress(&buf, "commit message", "gemma4:31b", false)
	p.Now = func() time.Time { return currentTime }
	p.IsTTY = true

	_ = p.AddStep("Wake-on-LAN")
	currentTime = currentTime.Add(1 * time.Second)
	p.Tick()

	out := buf.String()
	// Adding step 1 after renderHeader (2 lines printed) must move up 2 lines to overwrite header
	assert.Contains(t, out, "\033[2A")
}

func TestProgress_TTY_CorrectHeaderCursorMovement(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p := llm.NewProgress(&buf, "commit message", "gemma4:31b", false)
	p.IsTTY = true

	_ = p.AddStep("Step 1")
	_ = p.AddStep("Step 2")

	out := buf.String()
	// Step 2 has 3 lines above it (Header, │, Step 1), so it must move up 3 lines (\033[3A)
	assert.Contains(t, out, "\033[3A")
}

func TestStep_Complete_PreservesOriginalEndAt(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	now := time.Now()
	currentTime := now

	p := llm.NewProgress(&buf, "commit message", "gemma4:31b", false)
	p.Now = func() time.Time { return currentTime }

	s := p.AddStep("Evaluate Prompt")
	currentTime = currentTime.Add(1 * time.Second)
	s.Complete("Prompt evaluated")

	assert.Equal(t, currentTime, s.EndAt)

	// Subsquent call to Complete (e.g. to update details with metrics) must not overwrite EndAt
	currentTime = currentTime.Add(30 * time.Second)
	s.Complete("891 prompt tokens, 1124.5 tok/s")

	assert.Equal(t, now.Add(1*time.Second), s.EndAt)
	p.Finish(31 * time.Second)

	out := buf.String()
	assert.Contains(t, out, "✓ Evaluate Prompt (891 prompt tokens, 1124.5 tok/s) (1s)")
}
