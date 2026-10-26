package implement

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalConfirmerFallback(t *testing.T) {
	t.Parallel()

	// "y" -> true
	in := strings.NewReader("y\n")
	out := &bytes.Buffer{}
	c := &TerminalConfirmer{In: in, Out: out}
	assert.False(t, c.Interactive())

	ans, err := c.Confirm("Approve?")
	require.NoError(t, err)
	assert.True(t, ans)
	assert.Contains(t, out.String(), "Approve?")

	// "n" -> false
	in = strings.NewReader("n\n")
	out = &bytes.Buffer{}
	c = &TerminalConfirmer{In: in, Out: out}
	ans, err = c.Confirm("Approve?")
	require.NoError(t, err)
	assert.False(t, ans)
}

func TestHookPrompterNonInteractive(t *testing.T) {
	t.Parallel()

	in := strings.NewReader("")
	out := &bytes.Buffer{}
	c := &TerminalConfirmer{In: in, Out: out}

	var prompter HookPrompter = c
	action, err := prompter.PromptHookAction("Step 1", "hook error")
	require.NoError(t, err)
	assert.Equal(t, HookActionEscalate, action)
}

func TestHookActionConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, HookActionAgentFix, HookAction("fix"))
	assert.Equal(t, HookActionNoVerify, HookAction("no-verify"))
	assert.Equal(t, HookActionRetry, HookAction("retry"))
	assert.Equal(t, HookActionEscalate, HookAction("escalate"))
}

func TestVerifyBaselineActionConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, VerifyBaselineDrop, VerifyBaselineAction("drop"))
	assert.Equal(t, VerifyBaselineKeep, VerifyBaselineAction("keep"))
	assert.Equal(t, VerifyBaselineAbort, VerifyBaselineAction("abort"))
}

func TestVerifyPrompterNonInteractive(t *testing.T) {
	t.Parallel()

	in := strings.NewReader("")
	out := &bytes.Buffer{}
	c := &TerminalConfirmer{In: in, Out: out}

	var prompter VerifyPrompter = c
	action, err := prompter.PromptVerifyBaseline("lint", "golangci-lint run ./...", "not found", true)
	require.NoError(t, err)
	assert.Equal(t, VerifyBaselineDrop, action)
}

func TestTerminalConfirmerAsk(t *testing.T) {
	t.Parallel()

	in := strings.NewReader("use postgresql\n")
	out := &bytes.Buffer{}
	c := &TerminalConfirmer{In: in, Out: out}
	assert.False(t, c.Interactive())

	var asker Asker = c
	ans, err := asker.Ask("Which database driver?")
	require.NoError(t, err)
	assert.Equal(t, "use postgresql", ans)
	assert.Contains(t, out.String(), "Which database driver?")

	// Empty answer
	in = strings.NewReader("\n")
	out = &bytes.Buffer{}
	c = &TerminalConfirmer{In: in, Out: out}
	ans, err = c.Ask("Any preference?")
	require.NoError(t, err)
	assert.Empty(t, ans)
}
