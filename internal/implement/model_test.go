package implement

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatModelName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		model    string
		effort   string
		expected string
	}{
		{
			name:     "agy gemini flash high in model id",
			model:    "gemini-3.8-flash-high",
			expected: "Gemini 3.8 Flash (High)",
		},
		{
			name:     "agy gemini flash medium in model id",
			model:    "gemini-3.8-flash-medium",
			expected: "Gemini 3.8 Flash (Medium)",
		},
		{
			name:     "agy gemini flash low in model id",
			model:    "gemini-3.8-flash-low",
			expected: "Gemini 3.8 Flash (Low)",
		},
		{
			name:     "claude opus 5 with medium effort",
			model:    "opus-5",
			effort:   "medium",
			expected: "Opus 5 (Medium)",
		},
		{
			name:     "claude opus 5 with high effort",
			model:    "opus-5",
			effort:   "high",
			expected: "Opus 5 (High)",
		},
		{
			name:     "claude opus 5 without effort",
			model:    "opus-5",
			expected: "Opus 5",
		},
		{
			name:     "opencode fireworks kimi k3 with high effort",
			model:    "fireworks-ai/accounts/fireworks/models/kimi-k3",
			effort:   "high",
			expected: "Kimi K3 (High)",
		},
		{
			name:     "opencode fireworks glm 5p3 flash",
			model:    "fireworks-ai/accounts/fireworks/models/glm-5p3-flash",
			expected: "GLM 5.3 Flash",
		},
		{
			name:     "opencode fireworks glm 5p3 with high effort",
			model:    "fireworks-ai/accounts/fireworks/models/glm-5p3",
			effort:   "high",
			expected: "GLM 5.3 (High)",
		},
		{
			name:     "claude 3.7 sonnet slug",
			model:    "claude-3-7-sonnet",
			expected: "Claude 3.7 Sonnet",
		},
		{
			name:     "gemini 2.5 pro slug",
			model:    "gemini-2.5-pro",
			expected: "Gemini 2.5 Pro",
		},
		{
			name:     "already formatted with effort",
			model:    "Gemini 3.8 Flash (High)",
			expected: "Gemini 3.8 Flash (High)",
		},
		{
			name:     "already formatted without effort",
			model:    "Opus 5",
			effort:   "Medium",
			expected: "Opus 5 (Medium)",
		},
		{
			name:     "empty model",
			model:    "",
			expected: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, FormatModelName(tc.model, tc.effort))
		})
	}
}

func TestHarnessColors(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "#CC785C", HarnessColor("claude"))
	assert.Equal(t, "#CC785C", HarnessColor("Claude"))
	assert.Equal(t, "#7C3AED", HarnessColor("agy"))
	assert.Equal(t, "#7C3AED", HarnessColor("AGY"))
	assert.Equal(t, "#10B981", HarnessColor("opencode"))
	assert.Equal(t, "#10B981", HarnessColor("Opencode"))
	assert.Empty(t, HarnessColor("unknown"))
}

func TestStyleModel(t *testing.T) {
	t.Parallel()

	// When useColor is false, model name is returned without ANSI codes
	plain := StyleModel("Gemini 3.8 Flash (High)", "agy", false)
	assert.Equal(t, "Gemini 3.8 Flash (High)", plain)

	// When useColor is true, model name contains Lipgloss color
	coloredAgy := StyleModel("Gemini 3.8 Flash (High)", "agy", true)
	assert.Contains(t, coloredAgy, "Gemini 3.8 Flash (High)")
	assert.Contains(t, coloredAgy, "\x1b[")

	coloredClaude := StyleModel("Opus 5 (Medium)", "claude", true)
	assert.Contains(t, coloredClaude, "Opus 5 (Medium)")
	assert.Contains(t, coloredClaude, "\x1b[")

	coloredOpencode := StyleModel("Kimi K3 (High)", "opencode", true)
	assert.Contains(t, coloredOpencode, "Kimi K3 (High)")
	assert.Contains(t, coloredOpencode, "\x1b[")
}
