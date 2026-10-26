package skills

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/implement"
	"github.com/kalverra/agents/pkg/output"
)

func writeTestImplementYAML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "implement.yaml")
	content := `agents:
  fast:
    harness: agy
    model: gemini-3.8-flash-medium
  strong:
    harness: agy
    model: gemini-3.8-flash-high
  deep:
    harness: opencode
    model: fireworks-ai/accounts/fireworks/models/glm-5p3

modes:
  test:
    worker: fast
  multi:
    worker: fast
    worker_options: [fast, strong]
    reviewer: fast
    reviewer_options: [fast, deep]
review_max_rounds: 1
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestImplementCmdListModes(t *testing.T) { //nolint:paralleltest // mutates global output mode
	output.SetJSON(false)
	t.Cleanup(func() { output.SetJSON(false) })

	cfgPath := writeTestImplementYAML(t)

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	cmd := newImplementCmd()
	require.NoError(t, cmd.Flags().Set("list", "true"))
	require.NoError(t, cmd.Flags().Set("config", cfgPath))

	runErr := cmd.RunE(cmd, nil)

	require.NoError(t, w.Close())
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()

	require.NoError(t, runErr)
	out := buf.String()
	assert.Contains(t, out, "Available modes:")
	assert.Contains(t, out, "worker_options=[fast,strong]")
	assert.Contains(t, out, "reviewer_options=[fast,deep]")
	assert.NotContains(t, out, "planner=")
}

func TestImplementCmdListModesJSON(t *testing.T) { //nolint:paralleltest // mutates global output mode
	output.SetJSON(true)
	t.Cleanup(func() { output.SetJSON(false) })

	cfgPath := writeTestImplementYAML(t)

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	cmd := newImplementCmd()
	require.NoError(t, cmd.Flags().Set("list", "true"))
	require.NoError(t, cmd.Flags().Set("config", cfgPath))

	runErr := cmd.RunE(cmd, nil)

	require.NoError(t, w.Close())
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()

	require.NoError(t, runErr)
	out := buf.String()
	assert.Contains(t, out, "<implement_modes>")
	assert.Contains(t, out, `worker_options="fast,strong"`)
	assert.Contains(t, out, `reviewer_options="fast,deep"`)
	assert.Contains(t, out, `<mode name="test" worker="agy":"gemini-3.8-flash-medium"/>`)
}

func TestImplementCmdRequiresSession(t *testing.T) {
	t.Parallel()

	cmd := newImplementCmd()
	require.NoError(t, cmd.Flags().Set("list", "false"))
	require.NoError(t, cmd.Flags().Set("config", filepath.Join("testdata", "implement.yaml")))

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--session is required")
}

func TestImplementCmdAcceptPlanChangesRequiresResume(t *testing.T) {
	t.Parallel()

	cmd := newImplementCmd()
	require.NoError(t, cmd.Flags().Set("list", "false"))
	require.NoError(t, cmd.Flags().Set("config", filepath.Join("testdata", "implement.yaml")))
	require.NoError(t, cmd.Flags().Set("session", "unit-test-session"))
	require.NoError(t, cmd.Flags().Set("mode", "test"))
	require.NoError(t, cmd.Flags().Set("accept-plan-changes", "true"))

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--accept-plan-changes only applies with --resume")
}

func TestWriteImplementResultFailureHuman(t *testing.T) { //nolint:paralleltest // mutates global output mode
	output.SetJSON(false)
	t.Cleanup(func() { output.SetJSON(false) })

	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".agents", "local", "test-fail")
	require.NoError(t, os.MkdirAll(sessionDir, 0o750))
	require.NoError(t, implement.SaveState(sessionDir, &implement.State{
		SessionName: "test-fail",
		Mode:        "simple",
		Steps:       []implement.Step{{Number: 1, Title: "Step 1", Status: implement.StepPending}},
	}))

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	runErr := errors.New("simulated agent crash")
	resErr := writeImplementResult(sessionDir, runErr)

	require.NoError(t, w.Close())
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()

	require.ErrorIs(t, resErr, runErr)
	out := buf.String()
	assert.Contains(t, out, "Implement failed: simulated agent crash")
	assert.Contains(t, out, "resume with: agents implement --mode simple --session test-fail --resume")
}

func TestWriteImplementResultFailureJSON(t *testing.T) { //nolint:paralleltest // mutates global output mode
	output.SetJSON(true)
	t.Cleanup(func() { output.SetJSON(false) })

	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".agents", "local", "test-fail-json")
	require.NoError(t, os.MkdirAll(sessionDir, 0o750))
	require.NoError(t, implement.SaveState(sessionDir, &implement.State{
		SessionName: "test-fail-json",
		Mode:        "full",
		Steps:       []implement.Step{{Number: 1, Title: "Step 1", Status: implement.StepPending}},
	}))

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	runErr := errors.New("step 1 worker died")
	resErr := writeImplementResult(sessionDir, runErr)

	require.NoError(t, w.Close())
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()

	require.ErrorIs(t, resErr, runErr)
	out := buf.String()
	assert.Contains(t, out, `<implement session="test-fail-json" mode="full" status="failed"`)
	assert.Contains(t, out, `resume_command="agents implement --mode full --session test-fail-json --resume"`)
	assert.Contains(t, out, `error="step 1 worker died"`)
}

func TestWriteImplementResultEscalated(t *testing.T) { //nolint:paralleltest // mutates global output mode
	output.SetJSON(false)
	t.Cleanup(func() { output.SetJSON(false) })

	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".agents", "local", "test-esc")
	require.NoError(t, os.MkdirAll(sessionDir, 0o750))
	require.NoError(t, implement.SaveState(sessionDir, &implement.State{
		SessionName: "test-esc",
		Mode:        "hard",
		Steps:       []implement.Step{{Number: 1, Title: "Step 1", Status: implement.StepPending}},
	}))

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	resErr := writeImplementResult(sessionDir, implement.ErrEscalated)

	require.NoError(t, w.Close())
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()

	require.NoError(t, resErr)
	out := buf.String()
	assert.Contains(t, out, "Implement escalated: review fixes exceeded max rounds")
	assert.Contains(t, out, "resume with: agents implement --mode hard --session test-esc --resume")
}

func TestWriteImplementResultBaselineAborted(t *testing.T) { //nolint:paralleltest // mutates global output mode
	output.SetJSON(false)
	t.Cleanup(func() { output.SetJSON(false) })

	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".agents", "local", "test-abort")
	require.NoError(t, os.MkdirAll(sessionDir, 0o750))
	require.NoError(t, implement.SaveState(sessionDir, &implement.State{
		SessionName: "test-abort",
		Mode:        "hard",
		Steps:       []implement.Step{{Number: 1, Title: "Step 1", Status: implement.StepPending}},
	}))

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	resErr := writeImplementResult(sessionDir, implement.ErrVerifyBaseline)

	require.NoError(t, w.Close())
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()

	require.NoError(t, resErr)
	out := buf.String()
	assert.Contains(t, out, "Implement aborted: baseline verification failed and session was aborted by user")
	assert.Contains(t, out, "resume with: agents implement --mode hard --session test-abort --resume")
}

func TestWriteImplementResultPlanDrift(t *testing.T) { //nolint:paralleltest // mutates global output mode
	output.SetJSON(false)
	t.Cleanup(func() { output.SetJSON(false) })

	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".agents", "local", "test-drift")
	require.NoError(t, os.MkdirAll(sessionDir, 0o750))
	require.NoError(t, implement.SaveState(sessionDir, &implement.State{
		SessionName: "test-drift",
		Mode:        "hard",
		Phase:       implement.PhaseEscalated,
		PlanSource:  implement.PlanSource{Kind: implement.SpecKindOpenSpec, Path: "openspec/changes/test-drift"},
		Steps:       []implement.Step{{Number: 1, Title: "Step 1", Status: implement.StepPending}},
	}))

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	resErr := writeImplementResult(sessionDir, implement.ErrPlanDrift)

	require.NoError(t, w.Close())
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()

	require.NoError(t, resErr)
	out := buf.String()
	assert.Contains(t, out, "Implement halted: plan.md changed after acceptance")
	assert.Contains(
		t,
		out,
		"resume with: agents implement --mode hard --session test-drift --resume --accept-plan-changes",
	)
}
