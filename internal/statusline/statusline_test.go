package statusline

import (
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatModelDisplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		payload  statuslineInputPayload
		expected string
	}{
		{
			name: "display name only",
			payload: statuslineInputPayload{
				Model: statuslineModel{DisplayName: "Sonnet 4.6"},
			},
			expected: "Sonnet 4.6",
		},
		{
			name: "claude effort level",
			payload: statuslineInputPayload{
				Model:  statuslineModel{DisplayName: "Sonnet 4.6"},
				Effort: statuslineEffort{Level: "high"},
			},
			expected: "Sonnet 4.6 (High)",
		},
		{
			name: "xhigh effort level",
			payload: statuslineInputPayload{
				Model:  statuslineModel{DisplayName: "Opus 4.6"},
				Effort: statuslineEffort{Level: "xhigh"},
			},
			expected: "Opus 4.6 (XHigh)",
		},
		{
			name: "empty effort omitted",
			payload: statuslineInputPayload{
				Model:  statuslineModel{DisplayName: "Gemini 3.5 Flash"},
				Effort: statuslineEffort{Level: ""},
			},
			expected: "Gemini 3.5 Flash",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatModelDisplay(tt.payload))
		})
	}
}

func TestGetContextColor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		usedPct  float64
		expected string
	}{
		{"0%", 0.0, "\033[38;2;166;227;161m"},
		{"50%", 50.0, "\033[38;2;166;227;161m"},
		{"75%", 75.0, "\033[38;2;249;226;175m"},
		{"90%", 90.0, "\033[38;2;243;139;168m"},
		{"95%", 95.0, "\033[38;2;243;139;168m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := getContextColor(tt.usedPct)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestRenderContextBar(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		ratio         float64
		width         int
		expectedFill  string
		expectedTrack string
	}{
		{
			name:          "0%",
			ratio:         0.0,
			width:         11,
			expectedFill:  "",
			expectedTrack: "···········",
		},
		{
			name:          "26%",
			ratio:         0.26,
			width:         11,
			expectedFill:  "██▉",
			expectedTrack: "········",
		},
		{
			name:          "57%",
			ratio:         0.57,
			width:         11,
			expectedFill:  "██████▎",
			expectedTrack: "····",
		},
		{
			name:          "100%",
			ratio:         1.0,
			width:         11,
			expectedFill:  "███████████",
			expectedTrack: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fill, track := renderContextBar(tt.ratio, tt.width)
			assert.Equal(t, tt.expectedFill, fill)
			assert.Equal(t, tt.expectedTrack, track)
		})
	}
}

func TestFormatSessionDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ms       int64
		expected string
	}{
		{0, ""},
		{45000, "45s"},
		{125000, "2m05s"},
		{3431000, "57m11s"},
		{7620000, "2h07m"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatSessionDuration(tt.ms))
		})
	}
}

func TestParseGitDiffStat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name:     "both insertions and deletions",
			input:    " 38 files changed, 5271 insertions(+), 808 deletions(-)",
			contains: []string{"+5271", "-808"},
		},
		{
			name:     "insertions only",
			input:    " 1 file changed, 5 insertions(+)",
			contains: []string{"+5"},
		},
		{
			name:     "deletions only",
			input:    " 1 file changed, 3 deletions(-)",
			contains: []string{"-3"},
		},
		{
			name:     "empty",
			input:    "",
			contains: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseGitDiffStat(tt.input)
			if len(tt.contains) == 0 {
				assert.Empty(t, got)
			} else {
				for _, c := range tt.contains {
					assert.Contains(t, got, c)
				}
			}
		})
	}
}

func TestFormatTokenCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		n        *int
		expected string
	}{
		{nil, ""},
		{new(500), "500"},
		{new(88244), "88.2k"},
		{new(61074), "61.1k"},
		{new(1250000), "1.2M"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v", tt.n), func(t *testing.T) {
			t.Parallel()
			got := formatTokenCount(tt.n)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestGetContextDisplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ctxData  statuslineContextWindow
		expected string
	}{
		{
			name: "Percentage Only",
			ctxData: statuslineContextWindow{
				UsedPercentage: 50.0,
			},
			expected: "█████▌·····▏ 50% — 0",
		},
		{
			name: "With Tokens",
			ctxData: statuslineContextWindow{
				UsedPercentage:    50.0,
				TotalInputTokens:  new(88244),
				TotalOutputTokens: new(61074),
			},
			expected: "█████▌·····▏ 50% — 149.3k",
		},
		{
			name: "With Cache Reads",
			ctxData: statuslineContextWindow{
				UsedPercentage:       50.0,
				TotalInputTokens:     new(88244),
				TotalOutputTokens:    new(61074),
				CacheReadInputTokens: new(1024),
			},
			expected: "█████▌·····▏ 50% — 149.3k",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := getContextDisplay(tt.ctxData)
			assert.Equal(t, stripAnsi(tt.expected), stripAnsi(got))
		})
	}
}

func stripAnsi(str string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return re.ReplaceAllString(str, "")
}

//nolint:paralleltest // mutates package-level statuslineTrackRoot
func TestGetBackgroundTasksDisplay(t *testing.T) {
	root := t.TempDir()
	oldRoot := statuslineTrackRoot
	statuslineTrackRoot = func() string { return root }
	defer func() { statuslineTrackRoot = oldRoot }()

	start := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)
	later := start.Add(2*time.Minute + 15*time.Second)

	tests := []struct {
		name     string
		payload  statuslineInputPayload
		now      time.Time
		expected []string
	}{
		{
			name:     "empty",
			payload:  statuslineInputPayload{},
			now:      start,
			expected: nil,
		},
		{
			name: "named terminals with tracked duration",
			payload: statuslineInputPayload{
				ConversationID: "conv-1",
				BackgroundTasks: []statuslineTask{
					{Status: "running", Name: "go test ./...", Index: new(0)},
					{Status: "running", Name: "npm run build", Index: new(1)},
					{Status: "completed", Name: "done"},
				},
			},
			now: start,
			expected: []string{
				"🛠️ `go test ./...` (0s) · `npm run build` (0s)",
			},
		},
		{
			name: "named terminals after elapsed time",
			payload: statuslineInputPayload{
				ConversationID: "conv-1",
				BackgroundTasks: []statuslineTask{
					{Status: "running", Name: "go test ./...", Index: new(0)},
					{Status: "running", Name: "npm run build", Index: new(1)},
				},
			},
			now: later,
			expected: []string{
				"🛠️ `go test ./...` (2m15s) · `npm run build` (2m15s)",
			},
		},
		{
			name: "task_count fallback without task details",
			payload: statuslineInputPayload{
				TaskCount: new(3),
			},
			now:      start,
			expected: []string{"🛠️ 3 terminals"},
		},
		{
			name: "active subagents",
			payload: statuslineInputPayload{
				Subagents: []statuslineSubagent{
					{Status: "running"},
					{Status: "working"},
				},
			},
			now:      start,
			expected: []string{"🤖 2 agents"},
		},
		{
			name: "terminals and agents",
			payload: statuslineInputPayload{
				ConversationID: "conv-2",
				BackgroundTasks: []statuslineTask{
					{Status: "running", Name: "go test ./...", Index: new(0)},
				},
				Subagents: []statuslineSubagent{{Status: "running"}},
			},
			now:      start,
			expected: []string{"🛠️ `go test ./...` (0s)", "🤖 1 agent"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, getBackgroundTasksDisplayAt(tt.payload, tt.now))
		})
	}
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m30s"},
		{5 * time.Minute, "5m"},
		{2*time.Hour + 15*time.Minute, "2h15m"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatDuration(tt.d))
		})
	}
}

//nolint:paralleltest // cannot run in parallel as it modifies package-level getGitInfo/statuslineTrackRoot/getGitDiffStat
func TestRender(t *testing.T) {
	oldGetGitInfo := getGitInfo
	defer func() { getGitInfo = oldGetGitInfo }()
	getGitInfo = func(_ string) (string, bool) {
		return "mock-git-branch", true
	}

	oldGetGitDiffStat := getGitDiffStat
	defer func() { getGitDiffStat = oldGetGitDiffStat }()
	getGitDiffStat = func(_ string) string {
		return ""
	}

	oldRoot := statuslineTrackRoot
	statuslineTrackRoot = func() string { return t.TempDir() }
	defer func() { statuslineTrackRoot = oldRoot }()

	fixedNow := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)
	oldNow := statuslineNow
	defer func() { statuslineNow = oldNow }()
	statuslineNow = func() time.Time { return fixedNow }

	t.Setenv("HOME", "/home/tester")
	wd, err := os.Getwd()
	require.NoError(t, err)

	tests := []struct {
		name      string
		jsonInput string
		expected  string
	}{
		{
			name: "Antigravity Payload",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/home/tester/Projects/testrig",
				"vcs": {
					"type": "git",
					"branch": "main",
					"dirty": true
				},
				"context_window": {
					"used_percentage": 15.5,
					"total_input_tokens": 15000,
					"total_output_tokens": 500,
					"thinking_tokens": 200
				},
				"agent_state": "working"
			}`,
			expected: "Gemini 3.5 Flash │ █▊·········▏ 16% — 15.7k │ ~/Projects/testrig (main*) │ working",
		},
		{
			name: "Claude Payload",
			jsonInput: `{
				"model": {
					"id": "claude-3-5-sonnet",
					"display_name": "Sonnet 3.5"
				},
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 42.0,
					"total_input_tokens": 42000,
					"total_output_tokens": 1200
				},
				"cost": {
					"total_cost_usd": 0.0543,
					"total_duration_ms": 125000
				}
			}`,
			expected: "Sonnet 3.5 │ ████▋······▏ 42% — 43.2k │ $0.05 │ 2m05s │ ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Claude Payload with effort level",
			jsonInput: `{
				"model": {
					"id": "claude-sonnet-4-6",
					"display_name": "Sonnet 4.6"
				},
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 42.0,
					"total_input_tokens": 42000,
					"total_output_tokens": 1200
				},
				"effort": {
					"level": "high"
				},
				"cost": {
					"total_cost_usd": 0.0543,
					"total_duration_ms": 125000
				}
			}`,
			expected: "Sonnet 4.6 (High) │ ████▋······▏ 42% — 43.2k │ $0.05 │ 2m05s │ ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Claude Payload with total_thinking_tokens",
			jsonInput: `{
				"model": {
					"id": "claude-3-5-sonnet",
					"display_name": "Sonnet 3.5"
				},
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 42.0,
					"total_input_tokens": 42000,
					"total_output_tokens": 1200,
					"total_thinking_tokens": 800
				},
				"cost": {
					"total_cost_usd": 0.0543,
					"total_duration_ms": 125000
				}
			}`,
			expected: "Sonnet 3.5 │ ████▋······▏ 42% — 44.0k │ $0.05 │ 2m05s │ ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Idle State Payload",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 10.0
				},
				"agent_state": "idle"
			}`,
			expected: "Gemini 3.5 Flash │ █▏·········▏ 10% — 0 │ ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Antigravity thoughts_token_count",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 15.5,
					"total_input_tokens": 15000,
					"total_output_tokens": 500,
					"thoughts_token_count": 300
				}
			}`,
			expected: "Gemini 3.5 Flash │ █▊·········▏ 16% — 15.8k │ ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Antigravity nested thoughts_token_count",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 15.5,
					"total_input_tokens": 15000,
					"total_output_tokens": 500,
					"current_usage": {
						"thoughts_token_count": 400
					}
				}
			}`,
			expected: "Gemini 3.5 Flash │ █▊·········▏ 16% — 15.9k │ ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Antigravity Cache Reads in context_window",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 15.5,
					"total_input_tokens": 15000,
					"total_output_tokens": 500,
					"cache_read_input_tokens": 8000
				}
			}`,
			expected: "Gemini 3.5 Flash │ █▊·········▏ 16% — 15.5k │ ⧉ 51.6% — 8.0k │ ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Antigravity Cache Reads in current_usage",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 15.5,
					"total_input_tokens": 15000,
					"total_output_tokens": 500,
					"current_usage": {
						"cache_read_input_tokens": 9000
					}
				}
			}`,
			expected: "Gemini 3.5 Flash │ █▊·········▏ 16% — 15.5k │ ⧉ 58.1% — 9.0k │ ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Background terminals and subagents",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/home/tester/Projects/testrig",
				"conversation_id": "conv-parse-1",
				"context_window": {
					"used_percentage": 12.3
				},
				"agent_state": "tool_use",
				"background_tasks": [
					{"status": "running", "name": "go test ./...", "index": 0},
					{"status": "running", "name": "npm run build", "index": 1}
				],
				"subagents": [
					{"status": "running"}
				]
			}`,
			expected: "Gemini 3.5 Flash │ █▍·········▏ 12% — 0 │ ~/Projects/testrig (mock-git-branch*) │ tool_use │ 🛠️ `go test ./...` (0s) · `npm run build` (0s) │ 🤖 1 agent",
		},
		{
			name: "Task count field",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 12.3
				},
				"task_count": 4
			}`,
			expected: "Gemini 3.5 Flash │ █▍·········▏ 12% — 0 │ ~/Projects/testrig (mock-git-branch*) │ 🛠️ 4 terminals",
		},
		{
			name: "Antigravity Payload with execution_mode",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 15.5
				},
				"agent_state": "working",
				"execution_mode": "plan"
			}`,
			expected: "Gemini 3.5 Flash │ █▊·········▏ 16% — 0 │ ~/Projects/testrig (mock-git-branch*) │ working │ plan",
		},
		{
			name: "Payload with permission_mode fallback",
			jsonInput: `{
				"model": {
					"id": "claude-3-5-sonnet",
					"display_name": "Sonnet 3.5"
				},
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 10.0
				},
				"permission_mode": "auto"
			}`,
			expected: "Sonnet 3.5 │ █▏·········▏ 10% — 0 │ ~/Projects/testrig (mock-git-branch*) │ auto",
		},
		{
			name: "Antigravity Doc Example Payload",
			jsonInput: `{
				"cwd": "/home/tester/Projects/testrig",
				"session_id": "12345678-abcd-ef01-2345-6789abcdef01",
				"conversation_id": "12345678-abcd-ef01-2345-6789abcdef01",
				"model": {
					"id": "Gemini 3.5 Flash (High)",
					"display_name": "Gemini 3.5 Flash (High)"
				},
				"workspace": {
					"current_dir": "/home/tester/Projects/testrig",
					"project_dir": "/home/tester/Projects/testrig"
				},
				"version": "1.0.13",
				"context_window": {
					"total_input_tokens": 88244,
					"total_output_tokens": 61074,
					"context_window_size": 1048576,
					"used_percentage": 14.24,
					"remaining_percentage": 85.76,
					"current_usage": {
						"input_tokens": 63382,
						"output_tokens": 346,
						"cache_creation_input_tokens": 0,
						"cache_read_input_tokens": 20857
					}
				},
				"vcs": {
					"type": "git",
					"branch": "main",
					"dirty": false
				},
				"task_count": 1,
				"execution_mode": "planning"
			}`,
			expected: "Gemini 3.5 Flash (High) │ █▋·········▏ 14% — 149.3k │ ⧉ 14.0% — 20.9k (cold 2:05pm) │ ~/Projects/testrig (main*) │ planning │ 🛠️ 1 terminal",
		},
		{
			name: "Antigravity Workspace Fallback When CWD Missing",
			jsonInput: `{
				"workspace": {
					"current_dir": "/home/tester/Projects/testrig"
				},
				"context_window": {
					"used_percentage": 5.0
				}
			}`,
			expected: "No Model │ ▌··········▏ 5% — 0 │ ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Antigravity Workspace ProjectDir Fallback When CurrentDir Missing",
			jsonInput: `{
				"workspace": {
					"project_dir": "/home/tester/Projects/testrig"
				},
				"context_window": {
					"used_percentage": 5.0
				}
			}`,
			expected: "No Model │ ▌··········▏ 5% — 0 │ ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Antigravity Context Window Top-Level Tokens and Vim Mode",
			jsonInput: `{
				"model": "Gemini 3.8 Flash (High)",
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 24.5,
					"input_tokens": 12000,
					"output_tokens": 800
				},
				"vim": {
					"mode": "NORMAL"
				}
			}`,
			expected: "Gemini 3.8 Flash (High) │ ██▊········▏ 25% — 12.8k │ ~/Projects/testrig (mock-git-branch*) │ NORMAL",
		},
		{
			name: "String Model Deserialization",
			jsonInput: `{
				"model": "Gemini 3.8 Flash (High)",
				"cwd": "/home/tester/Projects/testrig",
				"context_window": {
					"used_percentage": 10.0
				}
			}`,
			expected: "Gemini 3.8 Flash (High) │ █▏·········▏ 10% — 0 │ ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name:      "Empty JSON Input",
			jsonInput: `{}`,
			expected:  "No Model │ ···········▏ 0% — 0 │ " + wd + " (mock-git-branch*)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render([]byte(tt.jsonInput))
			require.NoError(t, err)
			assert.Equal(t, tt.expected, stripAnsi(got))
		})
	}
}

//nolint:paralleltest // mutates package-level getGitDiffStat
func TestRender_WithGitDiff(t *testing.T) {
	oldGetGitDiffStat := getGitDiffStat
	defer func() { getGitDiffStat = oldGetGitDiffStat }()
	getGitDiffStat = func(_ string) string {
		return "+42 -7"
	}
	t.Setenv("HOME", "/home/tester")

	jsonInput := `{
		"model": {"display_name": "Sonnet 3.5"},
		"cwd": "/home/tester/Projects/testrig",
		"vcs": {
			"type": "git",
			"branch": "main",
			"dirty": true
		},
		"context_window": {"used_percentage": 20.0}
	}`

	got, err := Render([]byte(jsonInput))
	require.NoError(t, err)
	assert.Contains(t, stripAnsi(got), "~/Projects/testrig (main* +42 -7)")
}

func TestGetExecutionMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		payload  statuslineInputPayload
		expected string
	}{
		{
			name: "execution_mode takes priority",
			payload: statuslineInputPayload{
				ExecutionMode:  "plan",
				PermissionMode: "auto",
				Mode:           "default",
			},
			expected: "plan",
		},
		{
			name: "permission_mode fallback",
			payload: statuslineInputPayload{
				PermissionMode: "auto",
				Mode:           "default",
			},
			expected: "auto",
		},
		{
			name: "mode fallback",
			payload: statuslineInputPayload{
				Mode: "default",
			},
			expected: "default",
		},
		{
			name:     "empty when none present",
			payload:  statuslineInputPayload{},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, getExecutionMode(tt.payload))
		})
	}
}

func TestRender_WithStack(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	gitDir := tempDir + "/.git"
	require.NoError(t, os.MkdirAll(gitDir, 0o700))

	ghStackJSON := `{
		"schemaVersion": 1,
		"stacks": [
			{
				"trunk": {"branch": "main"},
				"branches": [
					{"branch": "feat-1", "base": "sha1", "pullRequest": {"number": 101, "merged": false}},
					{"branch": "feat-2", "base": "sha2", "pullRequest": {"number": 102, "merged": false}}
				]
			}
		]
	}`
	require.NoError(t, os.WriteFile(gitDir+"/gh-stack", []byte(ghStackJSON), 0o600))
	require.NoError(t, os.WriteFile(gitDir+"/HEAD", []byte("ref: refs/heads/feat-2\n"), 0o600))

	payloadJSON := fmt.Sprintf(`{
		"model": {"display_name": "Opus 4.6"},
		"cwd": "%s",
		"vcs": {"type": "git", "branch": "feat-2", "dirty": false},
		"context_window": {"used_percentage": 10.0}
	}`, tempDir)

	out, err := Render([]byte(payloadJSON))
	require.NoError(t, err)
	assert.Contains(t, out, "(feat-2 · 🥞 2/2 #102)")
}

func TestFormatExpirationClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		expTime  time.Time
		expected string
	}{
		{
			name:     "same day afternoon",
			expTime:  time.Date(2026, 6, 8, 14, 32, 0, 0, time.UTC),
			expected: "2:32pm",
		},
		{
			name:     "same day morning",
			expTime:  time.Date(2026, 6, 8, 9, 5, 0, 0, time.UTC),
			expected: "9:05am",
		},
		{
			name:     "next day across midnight",
			expTime:  time.Date(2026, 6, 9, 1, 15, 0, 0, time.UTC),
			expected: "jun 9 1:15am",
		},
		{
			name:     "previous day",
			expTime:  time.Date(2026, 6, 7, 22, 0, 0, 0, time.UTC),
			expected: "jun 7 10:00pm",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatExpirationClock(tt.expTime, now))
		})
	}
}

func TestGetCacheDisplay_ClaudeCode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 8, 14, 30, 0, 0, time.UTC)
	futureExp := now.Add(5 * time.Minute).Unix() // 14:35
	pastExp := now.Add(-25 * time.Minute).Unix() // 14:05

	tests := []struct {
		name     string
		payload  statuslineInputPayload
		expected string
	}{
		{
			name: "warm with future expires_at",
			payload: statuslineInputPayload{
				ContextWindow: statuslineContextWindow{
					CacheReadInputTokens: new(40000),
				},
				PromptCache: &statuslinePromptCache{
					Warm:            true,
					CachingObserved: true,
					ExpiresAt:       &futureExp,
				},
			},
			expected: "⧉ 100.0% — 40.0k (cold 2:35pm)",
		},
		{
			name: "cold with past expires_at",
			payload: statuslineInputPayload{
				ContextWindow: statuslineContextWindow{
					CacheReadInputTokens: new(40000),
				},
				PromptCache: &statuslinePromptCache{
					Warm:            false,
					CachingObserved: true,
					ExpiresAt:       &pastExp,
				},
			},
			expected: "⚠️ ⧉ 40.0k cold since 2:05pm (start new session)",
		},
		{
			name: "warm without expires_at",
			payload: statuslineInputPayload{
				ContextWindow: statuslineContextWindow{
					CacheReadInputTokens: new(40000),
				},
				PromptCache: &statuslinePromptCache{
					Warm:            true,
					CachingObserved: true,
				},
			},
			expected: "⧉ 100.0% — 40.0k",
		},
		{
			name: "cold without expires_at",
			payload: statuslineInputPayload{
				ContextWindow: statuslineContextWindow{
					CacheReadInputTokens: new(40000),
				},
				PromptCache: &statuslinePromptCache{
					Warm:            false,
					CachingObserved: true,
				},
			},
			expected: "⚠️ ⧉ 40.0k cold (start new session)",
		},
		{
			name: "caching not observed",
			payload: statuslineInputPayload{
				PromptCache: &statuslinePromptCache{
					Warm:            false,
					CachingObserved: false,
				},
			},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := getCacheDisplayAt(tt.payload, now)
			assert.Equal(t, tt.expected, stripAnsi(got))
		})
	}
}

//nolint:paralleltest // mutates statuslineTrackRoot
func TestGetCacheDisplay_AntigravityFallback(t *testing.T) {
	root := t.TempDir()
	oldRoot := statuslineTrackRoot
	statuslineTrackRoot = func() string { return root }
	defer func() { statuslineTrackRoot = oldRoot }()

	start := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)
	later := start.Add(2 * time.Hour) // 16:00

	payload := statuslineInputPayload{
		SessionID:  "agy-sess-1",
		AgentState: "thinking",
		ContextWindow: statuslineContextWindow{
			CurrentUsage: &statuslineCurrentUsage{
				CacheReadInputTokens: new(20857),
			},
		},
	}

	// 1. Initial turn: active at 14:00, cold at 14:05
	got1 := getCacheDisplayAt(payload, start)
	assert.Equal(t, "⧉ 100.0% — 20.9k (cold 2:05pm)", stripAnsi(got1))

	// 2. 2 hours later: idle check at 16:00, cold since 14:05
	idlePayload := payload
	idlePayload.AgentState = "idle"
	got2 := getCacheDisplayAt(idlePayload, later)
	assert.Equal(t, "⚠️ ⧉ 20.9k cold since 2:05pm (start new session)", stripAnsi(got2))
}
