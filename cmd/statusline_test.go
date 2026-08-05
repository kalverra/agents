package cmd

import (
	"fmt"
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
		{"0%", 0.0, "\033[38;2;0;255;0m"},
		{"50%", 50.0, "\033[38;2;255;60;0m"},
		{"90%", 90.0, "\033[38;2;255;0;0m"},
		{"95%", 95.0, "\033[38;2;255;0;0m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := getContextColor(tt.usedPct)
			assert.Equal(t, tt.expected, got)
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
			expected: "\033[38;2;255;60;0m(50.0%)",
		},
		{
			name: "With Tokens",
			ctxData: statuslineContextWindow{
				UsedPercentage:    50.0,
				TotalInputTokens:  new(88244),
				TotalOutputTokens: new(61074),
			},
			expected: "\033[38;2;255;60;0m▲ 88.2k ▼ 61.1k (50.0%)",
		},
		{
			name: "With Cache Reads",
			ctxData: statuslineContextWindow{
				UsedPercentage:       50.0,
				TotalInputTokens:     new(88244),
				TotalOutputTokens:    new(61074),
				CacheReadInputTokens: new(1024),
			},
			expected: "\033[38;2;255;60;0m▲ 88.2k ▼ 61.1k (50.0%) 💾 1.0k",
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

//nolint:paralleltest // cannot run in parallel as it modifies package-level getGitInfo/statuslineTrackRoot
func TestParseAndBuildStatusline(t *testing.T) {
	oldGetGitInfo := getGitInfo
	defer func() { getGitInfo = oldGetGitInfo }()
	getGitInfo = func(_ string) (string, bool) {
		return "mock-git-branch", true
	}

	oldRoot := statuslineTrackRoot
	statuslineTrackRoot = func() string { return t.TempDir() }
	defer func() { statuslineTrackRoot = oldRoot }()

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
				"cwd": "/Users/adamhamrick/Projects/testrig",
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
			expected: "Gemini 3.5 Flash | ▲ 15.0k ▼ 500 🧠 200 (15.5%) | ~/Projects/testrig (main*) | working",
		},
		{
			name: "Claude Payload",
			jsonInput: `{
				"model": {
					"id": "claude-3-5-sonnet",
					"display_name": "Sonnet 3.5"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
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
			expected: "Sonnet 3.5 | ▲ 42.0k ▼ 1.2k (42.0%) | ~/Projects/testrig (mock-git-branch*) | 💰 $0.05",
		},
		{
			name: "Claude Payload with effort level",
			jsonInput: `{
				"model": {
					"id": "claude-sonnet-4-6",
					"display_name": "Sonnet 4.6"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
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
			expected: "Sonnet 4.6 (High) | ▲ 42.0k ▼ 1.2k (42.0%) | ~/Projects/testrig (mock-git-branch*) | 💰 $0.05",
		},
		{
			name: "Claude Payload with total_thinking_tokens",
			jsonInput: `{
				"model": {
					"id": "claude-3-5-sonnet",
					"display_name": "Sonnet 3.5"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
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
			expected: "Sonnet 3.5 | ▲ 42.0k ▼ 1.2k 🧠 800 (42.0%) | ~/Projects/testrig (mock-git-branch*) | 💰 $0.05",
		},
		{
			name: "Idle State Payload",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
				"context_window": {
					"used_percentage": 10.0
				},
				"agent_state": "idle"
			}`,
			expected: "Gemini 3.5 Flash | (10.0%) | ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Antigravity thoughts_token_count",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
				"context_window": {
					"used_percentage": 15.5,
					"total_input_tokens": 15000,
					"total_output_tokens": 500,
					"thoughts_token_count": 300
				}
			}`,
			expected: "Gemini 3.5 Flash | ▲ 15.0k ▼ 500 🧠 300 (15.5%) | ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Antigravity nested thoughts_token_count",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
				"context_window": {
					"used_percentage": 15.5,
					"total_input_tokens": 15000,
					"total_output_tokens": 500,
					"current_usage": {
						"thoughts_token_count": 400
					}
				}
			}`,
			expected: "Gemini 3.5 Flash | ▲ 15.0k ▼ 500 🧠 400 (15.5%) | ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Antigravity Cache Reads in context_window",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
				"context_window": {
					"used_percentage": 15.5,
					"total_input_tokens": 15000,
					"total_output_tokens": 500,
					"cache_read_input_tokens": 8000
				}
			}`,
			expected: "Gemini 3.5 Flash | ▲ 15.0k ▼ 500 (15.5%) 💾 8.0k | ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Antigravity Cache Reads in current_usage",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
				"context_window": {
					"used_percentage": 15.5,
					"total_input_tokens": 15000,
					"total_output_tokens": 500,
					"current_usage": {
						"cache_read_input_tokens": 9000
					}
				}
			}`,
			expected: "Gemini 3.5 Flash | ▲ 15.0k ▼ 500 (15.5%) 💾 9.0k | ~/Projects/testrig (mock-git-branch*)",
		},
		{
			name: "Background terminals and subagents",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
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
			expected: "Gemini 3.5 Flash | (12.3%) | ~/Projects/testrig (mock-git-branch*) | tool_use | 🛠️ `go test ./...` (0s) · `npm run build` (0s) | 🤖 1 agent",
		},
		{
			name: "Task count field",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
				"context_window": {
					"used_percentage": 12.3
				},
				"task_count": 4
			}`,
			expected: "Gemini 3.5 Flash | (12.3%) | ~/Projects/testrig (mock-git-branch*) | 🛠️ 4 terminals",
		},
		{
			name: "Antigravity Payload with execution_mode",
			jsonInput: `{
				"model": {
					"id": "gemini-3.5-flash",
					"display_name": "Gemini 3.5 Flash"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
				"context_window": {
					"used_percentage": 15.5
				},
				"agent_state": "working",
				"execution_mode": "plan"
			}`,
			expected: "Gemini 3.5 Flash | (15.5%) | ~/Projects/testrig (mock-git-branch*) | working | plan",
		},
		{
			name: "Payload with permission_mode fallback",
			jsonInput: `{
				"model": {
					"id": "claude-3-5-sonnet",
					"display_name": "Sonnet 3.5"
				},
				"cwd": "/Users/adamhamrick/Projects/testrig",
				"context_window": {
					"used_percentage": 10.0
				},
				"permission_mode": "auto"
			}`,
			expected: "Sonnet 3.5 | (10.0%) | ~/Projects/testrig (mock-git-branch*) | auto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAndBuildStatusline([]byte(tt.jsonInput))
			require.NoError(t, err)
			assert.Equal(t, tt.expected, stripAnsi(got))
		})
	}
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
