// Package statusline renders a one-line, ANSI-colored status bar from the JSON
// payload an agent harness pipes to its statusline command.
//
// Payload formats:
//   - antigravity-cli: https://antigravity.google/docs/cli/statusline/
//   - claude-code: https://code.claude.com/docs/en/statusline
package statusline

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kalverra/agents/internal/statuslinetrack"
	"github.com/kalverra/agents/pkg/git"
)

type statuslineModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

func (m *statuslineModel) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.ID = s
		m.DisplayName = s
		return nil
	}
	type rawModel statuslineModel
	var raw rawModel
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = statuslineModel(raw)
	return nil
}

type statuslineVCS struct {
	Type   string `json:"type"`
	Branch string `json:"branch"`
	Dirty  bool   `json:"dirty"`
}
type statuslineCurrentUsage struct {
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	ThinkingTokens           *int `json:"thinking_tokens"`
	ReasoningTokens          *int `json:"reasoning_tokens"`
	ThoughtsTokens           *int `json:"thoughts_tokens"`
	ThoughtsTokenCount       *int `json:"thoughts_token_count"`
	ReasoningTokenCount      *int `json:"reasoning_token_count"`
	ThinkingTokenCount       *int `json:"thinking_token_count"`
	TotalThinkingTokens      *int `json:"total_thinking_tokens"`
	CacheCreationTokens      *int `json:"cache_creation_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	CacheReadTokens          *int `json:"cache_read_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
}

type statuslineContextWindow struct {
	UsedPercentage           float64                 `json:"used_percentage"`
	InputTokens              *int                    `json:"input_tokens"`
	OutputTokens             *int                    `json:"output_tokens"`
	TotalInputTokens         *int                    `json:"total_input_tokens"`
	TotalOutputTokens        *int                    `json:"total_output_tokens"`
	ThinkingTokens           *int                    `json:"thinking_tokens"`
	ReasoningTokens          *int                    `json:"reasoning_tokens"`
	ThoughtsTokens           *int                    `json:"thoughts_tokens"`
	ThoughtsTokenCount       *int                    `json:"thoughts_token_count"`
	ReasoningTokenCount      *int                    `json:"reasoning_token_count"`
	ThinkingTokenCount       *int                    `json:"thinking_token_count"`
	TotalThinkingTokens      *int                    `json:"total_thinking_tokens"`
	CacheReadTokens          *int                    `json:"cache_read_tokens"`
	CacheReadInputTokens     *int                    `json:"cache_read_input_tokens"`
	CacheCreationTokens      *int                    `json:"cache_creation_tokens"`
	CacheCreationInputTokens *int                    `json:"cache_creation_input_tokens"`
	CurrentUsage             *statuslineCurrentUsage `json:"current_usage"`
}

type statuslineTask struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Index  *int   `json:"index"`
}

type statuslineSubagent struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

type statuslineCost struct {
	TotalCostUSD    float64 `json:"total_cost_usd"`
	TotalDurationMS int64   `json:"total_duration_ms"`
}

type statuslinePR struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	ReviewState string `json:"review_state"`
}

type statuslineEffort struct {
	Level string `json:"level"`
}

type statuslinePromptCache struct {
	Warm             bool     `json:"warm"`
	CachingObserved  bool     `json:"caching_observed"`
	TTL              string   `json:"ttl"`
	ExpiresAt        *int64   `json:"expires_at"`
	Requests         int      `json:"requests"`
	Misses           int      `json:"misses"`
	ExpectedRebuilds int      `json:"expected_rebuilds"`
	HitRatio         *float64 `json:"hit_ratio"`
}

type statuslineWorkspace struct {
	CurrentDir string `json:"current_dir"`
	ProjectDir string `json:"project_dir"`
}

type statuslineVim struct {
	Mode string `json:"mode"`
}

type statuslineInputPayload struct {
	Model           statuslineModel         `json:"model"`
	CWD             string                  `json:"cwd"`
	Workspace       statuslineWorkspace     `json:"workspace"`
	SessionID       string                  `json:"session_id"`
	ConversationID  string                  `json:"conversation_id"`
	TranscriptPath  string                  `json:"transcript_path"`
	VCS             statuslineVCS           `json:"vcs"`
	ContextWindow   statuslineContextWindow `json:"context_window"`
	BackgroundTasks []statuslineTask        `json:"background_tasks"`
	Subagents       []statuslineSubagent    `json:"subagents"`
	TaskCount       *int                    `json:"task_count"`
	AgentState      string                  `json:"agent_state"`
	ExecutionMode   string                  `json:"execution_mode"`
	PermissionMode  string                  `json:"permission_mode"`
	Mode            string                  `json:"mode"`
	Cost            statuslineCost          `json:"cost"`
	Effort          statuslineEffort        `json:"effort"`
	PR              *statuslinePR           `json:"pr"`
	PromptCache     *statuslinePromptCache  `json:"prompt_cache"`
	Vim             *statuslineVim          `json:"vim"`
}

func getExecutionMode(payload statuslineInputPayload) string {
	if payload.ExecutionMode != "" {
		return payload.ExecutionMode
	}
	if payload.PermissionMode != "" {
		return payload.PermissionMode
	}
	return payload.Mode
}

var (
	statuslineTrackRoot = statuslinetrack.DefaultRoot
	statuslineNow       = time.Now
)

func runCmd(dir, name string, args ...string) (string, error) {
	//nolint:gosec // G204: subprocess command generated dynamically
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var getGitInfo = func(cwd string) (string, bool) {
	if _, err := runCmd(cwd, "git", "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", false
	}
	branch, err := runCmd(cwd, "git", "branch", "--show-current")
	if err != nil || branch == "" {
		branch, _ = runCmd(cwd, "git", "rev-parse", "--abbrev-ref", "HEAD")
	}
	status, err := runCmd(cwd, "git", "status", "--porcelain")
	dirty := err == nil && status != ""
	return branch, dirty
}

const (
	ansiReset   = "\033[0m"
	ansiMuted   = "\033[38;2;127;132;156m" // Catppuccin Mocha Subtext0/Overlay1 #7f849c
	ansiText    = "\033[38;2;205;214;244m" // Catppuccin Mocha Text #cdd6f4
	ansiSuccess = "\033[38;2;166;227;161m" // Catppuccin Mocha Green #a6e3a1
	ansiWarning = "\033[38;2;249;226;175m" // Catppuccin Mocha Yellow #f9e2af
	ansiError   = "\033[38;2;243;139;168m" // Catppuccin Mocha Red #f38ba8
	ansiModel   = "\033[38;2;137;180;250m" // Catppuccin Mocha Blue #89b4fa
	ansiCWD     = "\033[38;2;249;226;175m" // Catppuccin Mocha Yellow #f9e2af
	ansiSep     = "\033[38;2;127;132;156m │ \033[0m"

	trackChar       = "·"
	edgeEnd         = "▏"
	contextBarWidth = 11
)

var (
	eighths = []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

	reDiffIns = regexp.MustCompile(`(\d+)\s+insertion`)
	reDiffDel = regexp.MustCompile(`(\d+)\s+deletion`)
)

func parseGitDiffStat(out string) string {
	ins := ""
	del := ""
	if m := reDiffIns.FindStringSubmatch(out); len(m) > 1 {
		ins = m[1]
	}
	if m := reDiffDel.FindStringSubmatch(out); len(m) > 1 {
		del = m[1]
	}
	if ins != "" && del != "" {
		return fmt.Sprintf("%s+%s %s-%s%s", ansiSuccess, ins, ansiError, del, ansiCWD)
	}
	if ins != "" {
		return fmt.Sprintf("%s+%s%s", ansiSuccess, ins, ansiCWD)
	}
	if del != "" {
		return fmt.Sprintf("%s-%s%s", ansiError, del, ansiCWD)
	}
	return ""
}

var getGitDiffStat = func(cwd string) string {
	out, err := runCmd(cwd, "git", "diff", "--shortstat", "HEAD")
	if err != nil || out == "" {
		out, err = runCmd(cwd, "git", "diff", "--shortstat")
		if err != nil || out == "" {
			return ""
		}
	}
	return parseGitDiffStat(out)
}

func renderContextBar(ratio float64, width int) (string, string) {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	eighthCount := int(math.Round(ratio * float64(width) * 8.0))
	full := eighthCount / 8
	rest := eighthCount % 8
	if full > width {
		full = width
		rest = 0
	}
	fill := strings.Repeat("█", full)
	if full < width && rest > 0 {
		fill += eighths[rest]
	}
	totalCells := full
	if rest > 0 {
		totalCells++
	}
	trackLen := max(width-totalCells, 0)
	track := strings.Repeat(trackChar, trackLen)
	return fill, track
}

func getPressureColor(ratio float64) string {
	if ratio >= 0.90 {
		return ansiError
	}
	if ratio >= 0.70 {
		return ansiWarning
	}
	return ansiSuccess
}

func formatSessionDuration(ms int64) string {
	if ms <= 0 {
		return ""
	}
	secs := ms / 1000
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	remSecs := secs % 60
	if mins < 60 {
		return fmt.Sprintf("%dm%02ds", mins, remSecs)
	}
	hours := mins / 60
	remMins := mins % 60
	return fmt.Sprintf("%dh%02dm", hours, remMins)
}

func getThinkingTokens(cw statuslineContextWindow) *int {
	if cw.ThinkingTokens != nil {
		return cw.ThinkingTokens
	}
	if cw.ReasoningTokens != nil {
		return cw.ReasoningTokens
	}
	if cw.ThoughtsTokens != nil {
		return cw.ThoughtsTokens
	}
	if cw.ThoughtsTokenCount != nil {
		return cw.ThoughtsTokenCount
	}
	if cw.ReasoningTokenCount != nil {
		return cw.ReasoningTokenCount
	}
	if cw.ThinkingTokenCount != nil {
		return cw.ThinkingTokenCount
	}
	if cw.TotalThinkingTokens != nil {
		return cw.TotalThinkingTokens
	}

	if cw.CurrentUsage != nil {
		cu := cw.CurrentUsage
		if cu.ThinkingTokens != nil {
			return cu.ThinkingTokens
		}
		if cu.ReasoningTokens != nil {
			return cu.ReasoningTokens
		}
		if cu.ThoughtsTokens != nil {
			return cu.ThoughtsTokens
		}
		if cu.ThoughtsTokenCount != nil {
			return cu.ThoughtsTokenCount
		}
		if cu.ReasoningTokenCount != nil {
			return cu.ReasoningTokenCount
		}
		if cu.ThinkingTokenCount != nil {
			return cu.ThinkingTokenCount
		}
		if cu.TotalThinkingTokens != nil {
			return cu.TotalThinkingTokens
		}
	}
	return nil
}

func getInputTokens(cw statuslineContextWindow) *int {
	if cw.TotalInputTokens != nil {
		return cw.TotalInputTokens
	}
	if cw.InputTokens != nil {
		return cw.InputTokens
	}
	if cw.CurrentUsage != nil {
		return cw.CurrentUsage.InputTokens
	}
	return nil
}

func getOutputTokens(cw statuslineContextWindow) *int {
	if cw.TotalOutputTokens != nil {
		return cw.TotalOutputTokens
	}
	if cw.OutputTokens != nil {
		return cw.OutputTokens
	}
	if cw.CurrentUsage != nil {
		return cw.CurrentUsage.OutputTokens
	}
	return nil
}

var inactiveStatuslineStatuses = map[string]struct{}{
	"done":      {},
	"completed": {},
	"failed":    {},
	"stopped":   {},
	"cancelled": {},
	"canceled":  {},
	"idle":      {},
	"error":     {},
}

func isActiveStatuslineStatus(status string) bool {
	if status == "" {
		return true
	}
	_, inactive := inactiveStatuslineStatuses[strings.ToLower(status)]
	return !inactive
}

func countActiveStatuslineTasks(tasks []statuslineTask) int {
	count := 0
	for _, task := range tasks {
		if isActiveStatuslineStatus(task.Status) {
			count++
		}
	}
	return count
}

func getBackgroundTaskCount(payload statuslineInputPayload) int {
	if payload.TaskCount != nil {
		return *payload.TaskCount
	}
	return countActiveStatuslineTasks(payload.BackgroundTasks)
}

func getSubagentCount(subagents []statuslineSubagent) int {
	count := 0
	for _, subagent := range subagents {
		if isActiveStatuslineStatus(subagent.Status) {
			count++
		}
	}
	return count
}

func formatCountLabel(count int, singular, plural string) string {
	noun := plural
	if count == 1 {
		noun = singular
	}
	return fmt.Sprintf("%d %s", count, noun)
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Round(time.Second).Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	rem := secs % 60
	if mins < 60 {
		if rem == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm%ds", mins, rem)
	}
	hours := mins / 60
	mins %= 60
	if mins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, mins)
}

func sessionKey(payload statuslineInputPayload) string {
	if payload.ConversationID != "" {
		return payload.ConversationID
	}
	if payload.SessionID != "" {
		return payload.SessionID
	}
	return ""
}

func taskTrackerKey(task statuslineTask, fallbackIndex int) string {
	if task.Index != nil {
		return fmt.Sprintf("%d", *task.Index)
	}
	return fmt.Sprintf("i:%d", fallbackIndex)
}

func taskDisplayName(task statuslineTask, fallbackIndex int) string {
	if task.Name != "" {
		return task.Name
	}
	return fmt.Sprintf("terminal %d", fallbackIndex+1)
}

func activeBackgroundTasks(tasks []statuslineTask) []statuslinetrack.ActiveTask {
	var active []statuslinetrack.ActiveTask
	for i, task := range tasks {
		if !isActiveStatuslineStatus(task.Status) {
			continue
		}
		sortKey := i
		if task.Index != nil {
			sortKey = *task.Index
		}
		active = append(active, statuslinetrack.ActiveTask{
			Key:  taskTrackerKey(task, i),
			Name: taskDisplayName(task, i),
			Sort: sortKey,
		})
	}
	return active
}

func formatBackgroundTasksLine(entries []statuslinetrack.Entry, now time.Time) string {
	if len(entries) == 0 {
		return ""
	}
	parts := make([]string, len(entries))
	for i, entry := range entries {
		parts[i] = fmt.Sprintf("`%s` (%s)", entry.Name, formatDuration(now.Sub(entry.StartedAt)))
	}
	return "🛠️ " + strings.Join(parts, " · ")
}

func backgroundTasksDisplay(payload statuslineInputPayload, now time.Time) string {
	active := activeBackgroundTasks(payload.BackgroundTasks)
	key := sessionKey(payload)
	if len(active) > 0 && key != "" {
		entries, err := statuslinetrack.Sync(statuslineTrackRoot(), key, active, now)
		if err == nil {
			if line := formatBackgroundTasksLine(entries, now); line != "" {
				return line
			}
		}
	}

	if n := getBackgroundTaskCount(payload); n > 0 {
		return fmt.Sprintf("🛠️ %s", formatCountLabel(n, "terminal", "terminals"))
	}
	return ""
}

func getBackgroundTasksDisplay(payload statuslineInputPayload) []string {
	return getBackgroundTasksDisplayAt(payload, statuslineNow())
}

func getBackgroundTasksDisplayAt(payload statuslineInputPayload, now time.Time) []string {
	var parts []string
	if line := backgroundTasksDisplay(payload, now); line != "" {
		parts = append(parts, line)
	}
	if n := getSubagentCount(payload.Subagents); n > 0 {
		parts = append(parts, fmt.Sprintf("🤖 %s", formatCountLabel(n, "agent", "agents")))
	}
	return parts
}

func getCacheReadTokens(cw statuslineContextWindow) *int {
	if cw.CacheReadTokens != nil {
		return cw.CacheReadTokens
	}
	if cw.CacheReadInputTokens != nil {
		return cw.CacheReadInputTokens
	}
	if cw.CurrentUsage != nil {
		if cw.CurrentUsage.CacheReadTokens != nil {
			return cw.CurrentUsage.CacheReadTokens
		}
		if cw.CurrentUsage.CacheReadInputTokens != nil {
			return cw.CurrentUsage.CacheReadInputTokens
		}
	}
	return nil
}

func getCacheTokens(payload statuslineInputPayload) *int {
	if t := getCacheReadTokens(payload.ContextWindow); t != nil && *t > 0 {
		return t
	}
	if payload.ContextWindow.CacheCreationTokens != nil && *payload.ContextWindow.CacheCreationTokens > 0 {
		return payload.ContextWindow.CacheCreationTokens
	}
	if payload.ContextWindow.CacheCreationInputTokens != nil && *payload.ContextWindow.CacheCreationInputTokens > 0 {
		return payload.ContextWindow.CacheCreationInputTokens
	}
	if payload.ContextWindow.CurrentUsage != nil {
		cu := payload.ContextWindow.CurrentUsage
		if cu.CacheCreationTokens != nil && *cu.CacheCreationTokens > 0 {
			return cu.CacheCreationTokens
		}
		if cu.CacheCreationInputTokens != nil && *cu.CacheCreationInputTokens > 0 {
			return cu.CacheCreationInputTokens
		}
	}
	return nil
}

func formatExpirationClock(expTime, now time.Time) string {
	loc := now.Location()
	expLocal := expTime.In(loc)
	nowLocal := now.In(loc)

	if expLocal.Year() == nowLocal.Year() && expLocal.YearDay() == nowLocal.YearDay() {
		return strings.ToLower(expLocal.Format("3:04pm"))
	}
	return strings.ToLower(expLocal.Format("Jan 2 3:04pm"))
}

func formatColdCache(toksStr, timeStr string) string {
	var detail string
	switch {
	case toksStr != "" && timeStr != "":
		detail = fmt.Sprintf("%s cold since %s", toksStr, timeStr)
	case toksStr != "":
		detail = fmt.Sprintf("%s cold", toksStr)
	case timeStr != "":
		detail = fmt.Sprintf("cold since %s", timeStr)
	default:
		detail = "cold"
	}
	return fmt.Sprintf("%s⚠️ ⧉ %s (start new session)%s", ansiWarning, detail, ansiReset)
}

func getCacheDisplayAt(payload statuslineInputPayload, now time.Time) string {
	cacheToks := getCacheTokens(payload)

	totalContextToks := getContextTotalUsedTokens(payload.ContextWindow)
	calcPctPrefix := func(toks *int) string {
		if payload.PromptCache != nil && payload.PromptCache.HitRatio != nil {
			return fmt.Sprintf("%.1f%% — ", *payload.PromptCache.HitRatio*100.0)
		}
		if toks != nil && totalContextToks > 0 && *toks <= totalContextToks {
			return fmt.Sprintf("%.1f%% — ", float64(*toks)/float64(totalContextToks)*100.0)
		}
		return ""
	}

	// 1. Claude Code native prompt_cache
	if pc := payload.PromptCache; pc != nil {
		if !pc.CachingObserved && !pc.Warm && cacheToks == nil {
			return ""
		}
		toksStr := formatTokenCount(cacheToks)
		pctPrefix := calcPctPrefix(cacheToks)
		if pc.ExpiresAt != nil {
			expTime := time.Unix(*pc.ExpiresAt, 0)
			timeStr := formatExpirationClock(expTime, now)
			if pc.Warm && now.Before(expTime) {
				if toksStr != "" {
					return fmt.Sprintf("%s⧉ %s%s (cold %s)%s", ansiMuted, pctPrefix, toksStr, timeStr, ansiReset)
				}
				return fmt.Sprintf("%s⧉ (cold %s)%s", ansiMuted, timeStr, ansiReset)
			}
			return formatColdCache(toksStr, timeStr)
		}
		if pc.Warm {
			if toksStr != "" {
				return fmt.Sprintf("%s⧉ %s%s%s", ansiMuted, pctPrefix, toksStr, ansiReset)
			}
			return fmt.Sprintf("%s⧉ warm%s", ansiMuted, ansiReset)
		}
		return formatColdCache(toksStr, "")
	}

	// 2. Fallback via statuslinetrack (Antigravity CLI / generic)
	isActive := payload.AgentState != "" && payload.AgentState != "idle"
	key := sessionKey(payload)
	if key != "" {
		sessState, err := statuslinetrack.SyncSessionState(
			statuslineTrackRoot(),
			key,
			cacheToks,
			isActive,
			now,
			payload.TranscriptPath,
		)
		if err == nil && sessState.HasCache {
			ttl := 5 * time.Minute
			if envTTL := os.Getenv("AGENTS_CACHE_TTL"); envTTL != "" {
				if d, parseErr := time.ParseDuration(envTTL); parseErr == nil && d > 0 {
					ttl = d
				}
			}
			expTime := sessState.LastActiveAt.Add(ttl)
			dispTokens := cacheToks
			if dispTokens == nil {
				dispTokens = sessState.CacheTokens
			}
			toksStr := formatTokenCount(dispTokens)
			pctPrefix := calcPctPrefix(dispTokens)
			timeStr := formatExpirationClock(expTime, now)

			if now.Before(expTime) {
				if toksStr != "" {
					return fmt.Sprintf("%s⧉ %s%s (cold %s)%s", ansiMuted, pctPrefix, toksStr, timeStr, ansiReset)
				}
				return fmt.Sprintf("%s⧉ (cold %s)%s", ansiMuted, timeStr, ansiReset)
			}
			return formatColdCache(toksStr, timeStr)
		}
	}

	if cacheToks != nil {
		toksStr := formatTokenCount(cacheToks)
		pctPrefix := calcPctPrefix(cacheToks)
		return fmt.Sprintf("%s⧉ %s%s%s", ansiMuted, pctPrefix, toksStr, ansiReset)
	}
	return ""
}

func formatEffortLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	case "high":
		return "High"
	case "xhigh":
		return "XHigh"
	case "max":
		return "Max"
	default:
		return ""
	}
}

func formatModelDisplay(payload statuslineInputPayload) string {
	modelName := payload.Model.DisplayName
	if modelName == "" {
		modelName = payload.Model.ID
	}
	if modelName == "" {
		modelName = "No Model"
	}
	if effort := formatEffortLevel(payload.Effort.Level); effort != "" {
		return fmt.Sprintf("%s (%s)", modelName, effort)
	}
	return modelName
}

// Render builds the statusline from a harness's JSON payload. It also reads the
// git state of the payload's working directory and updates the per-session
// tracker files under statuslinetrack.DefaultRoot.
func Render(jsonData []byte) (string, error) {
	var payload statuslineInputPayload
	if err := json.Unmarshal(jsonData, &payload); err != nil {
		return "", err
	}

	modelName := formatModelDisplay(payload)

	realCWD := payload.CWD
	if realCWD == "" {
		if payload.Workspace.CurrentDir != "" {
			realCWD = payload.Workspace.CurrentDir
		} else if payload.Workspace.ProjectDir != "" {
			realCWD = payload.Workspace.ProjectDir
		} else if dir, err := os.Getwd(); err == nil {
			realCWD = dir
		}
	}

	targetDir := realCWD
	if strings.HasPrefix(targetDir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			targetDir = filepath.Join(home, targetDir[2:])
		}
	}

	displayCWD := realCWD
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if displayCWD == home || strings.HasPrefix(displayCWD, home+string(filepath.Separator)) {
			displayCWD = "~" + strings.TrimPrefix(displayCWD, home)
		}
	}

	var vcsStr string
	branch := ""
	dirty := false

	if payload.VCS.Type == "git" && payload.VCS.Branch != "" {
		branch = payload.VCS.Branch
		dirty = payload.VCS.Dirty
		if !dirty {
			_, diskDirty := getGitInfo(targetDir)
			dirty = diskDirty
		}
	} else {
		branch, dirty = getGitInfo(targetDir)
	}

	if branch != "" {
		dirtyStr := ""
		if dirty {
			dirtyStr = "*"
			if diffStat := getGitDiffStat(targetDir); diffStat != "" {
				dirtyStr = "* " + diffStat
			}
		}
		stackStatus := git.GetStackStatus(targetDir, branch)
		if stackStatus != "" {
			if dirtyStr != "" {
				vcsStr = fmt.Sprintf(" (%s%s · 🥞 %s)", branch, dirtyStr, stackStatus)
			} else {
				vcsStr = fmt.Sprintf(" (%s · 🥞 %s)", branch, stackStatus)
			}
		} else {
			vcsStr = fmt.Sprintf(" (%s%s)", branch, dirtyStr)
		}
	}

	ctxDisplay := getContextDisplay(payload.ContextWindow)

	var parts []string
	parts = append(parts, fmt.Sprintf("%s%s%s", ansiModel, modelName, ansiReset))
	parts = append(parts, ctxDisplay)

	if cacheDisplay := getCacheDisplayAt(payload, statuslineNow()); cacheDisplay != "" {
		parts = append(parts, cacheDisplay)
	}

	if payload.Cost.TotalCostUSD > 0 {
		parts = append(parts, fmt.Sprintf("%s$%.2f%s", ansiText, payload.Cost.TotalCostUSD, ansiReset))
	}

	if payload.Cost.TotalDurationMS > 0 {
		if durStr := formatSessionDuration(payload.Cost.TotalDurationMS); durStr != "" {
			parts = append(parts, fmt.Sprintf("%s%s%s", ansiText, durStr, ansiReset))
		}
	}

	parts = append(parts, fmt.Sprintf("%s%s%s%s", ansiCWD, displayCWD, vcsStr, ansiReset))

	if payload.AgentState != "" && payload.AgentState != "idle" {
		stateColors := map[string]string{
			"idle":         "\033[2;37m",
			"thinking":     "\033[38;2;249;226;175m", // Catppuccin Yellow
			"working":      "\033[38;2;166;227;161m", // Catppuccin Green
			"tool_use":     "\033[38;2;137;220;235m", // Catppuccin Sky
			"initializing": "\033[38;2;203;166;247m", // Catppuccin Mauve
		}
		ansiState, exists := stateColors[payload.AgentState]
		if !exists {
			ansiState = ansiError
		}
		parts = append(parts, fmt.Sprintf("%s%s%s", ansiState, payload.AgentState, ansiReset))
	}

	if mode := getExecutionMode(payload); mode != "" {
		ansiMode := "\033[38;2;203;166;247m" // Catppuccin Mauve
		parts = append(parts, fmt.Sprintf("%s%s%s", ansiMode, mode, ansiReset))
	}

	if payload.Vim != nil && payload.Vim.Mode != "" {
		ansiVim := "\033[38;2;137;220;235m" // Catppuccin Sky
		parts = append(parts, fmt.Sprintf("%s%s%s", ansiVim, payload.Vim.Mode, ansiReset))
	}

	parts = append(parts, getBackgroundTasksDisplay(payload)...)

	return strings.Join(parts, ansiSep), nil
}

func getContextColor(usedPct float64) string {
	return getPressureColor(usedPct / 100.0)
}

func formatTokenCount(n *int) string {
	if n == nil {
		return ""
	}
	val := float64(*n)
	if val >= 1000000.0 {
		return fmt.Sprintf("%.1fM", val/1000000.0)
	}
	if val >= 1000.0 {
		return fmt.Sprintf("%.1fk", val/1000.0)
	}
	return fmt.Sprintf("%d", *n)
}

func getContextTotalUsedTokens(cw statuslineContextWindow) int {
	total := 0
	if in := getInputTokens(cw); in != nil {
		total += *in
	}
	if out := getOutputTokens(cw); out != nil {
		total += *out
	}
	if think := getThinkingTokens(cw); think != nil {
		total += *think
	}
	if cache := getCacheReadTokens(cw); cache != nil {
		if total == 0 {
			total += *cache
		}
	}
	return total
}

func formatContextDisplay(ctxData statuslineContextWindow) string {
	ratio := ctxData.UsedPercentage / 100.0
	if ratio < 0 {
		ratio = 0
	}
	fillTone := getPressureColor(ratio)
	fill, track := renderContextBar(ratio, contextBarWidth)

	barStr := fmt.Sprintf("%s%s%s%s%s", fillTone, fill, ansiMuted, track, edgeEnd)
	pctStr := fmt.Sprintf("%s %d%%%s", fillTone, int(math.Round(ctxData.UsedPercentage)), ansiReset)

	totalTokens := getContextTotalUsedTokens(ctxData)
	toksStr := "0"
	if totalTokens > 0 {
		toksStr = formatTokenCount(&totalTokens)
	}

	return fmt.Sprintf("%s%s %s—%s %s%s%s", barStr, pctStr, ansiMuted, ansiReset, ansiMuted, toksStr, ansiReset)
}

func getContextDisplay(ctxData statuslineContextWindow) string {
	return formatContextDisplay(ctxData)
}
