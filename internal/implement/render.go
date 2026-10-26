package implement

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ANSI control sequences used by the TTY renderer.
const (
	hideCursor   = "\x1b[?25l"
	showCursor   = "\x1b[?25h"
	eraseToEOS   = "\x1b[J"
	spinnerChars = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
)

// ActivityItem is one tool or file action displayed under a runner.
type ActivityItem struct {
	Label     string
	Elapsed   time.Duration
	In        int64
	Out       int64
	HasTokens bool
	Active    bool
}

// SubtaskView is one runner execution under a step (worker, reviewer, fix).
type SubtaskView struct {
	Role       string
	Harness    string
	Model      string
	Effort     string
	Elapsed    time.Duration
	HasElapsed bool
	In         int64
	Out        int64
	CostUSD    float64
	HasTokens  bool
	HasCost    bool
	Status     string // "approved", "rejected", "active", "pending"
	Active     bool
	DiffStat   *DiffStat
	Activities []ActivityItem
}

// StepView is one step of a session snapshot, pre-formatted for rendering.
type StepView struct {
	Number     int
	Title      string
	Status     StepStatus
	Elapsed    time.Duration
	HasElapsed bool
	Rounds     int
	// Usage display fields; nothing renders unless the flags are set.
	In        int64
	Out       int64
	CostUSD   float64
	HasTokens bool
	HasCost   bool
	DiffStat  *DiffStat
	Subtasks  []SubtaskView
}

// SessionView is a snapshot of the session used to render one progress frame.
type SessionView struct {
	Name  string
	Steps []StepView
	// Current is the step number in flight; 0 = none.
	Current int
	// Role is "working", "reviewing", or "fixing" for the current step.
	Role string
	// Round is the review round for the current step (review/fix roles).
	Round int
	// Harness is the harness name driving the current step.
	Harness string
	// Model is the model name/id driving the current step.
	Model string
	// Effort is the reasoning effort (high/medium/low) for the current step.
	Effort string
	// Activity is the latest tool label seen on the stream.
	Activity string
	// CurrentStartedAt is when the current step's work began; zero if
	// unknown. The renderer derives live elapsed time from it.
	CurrentStartedAt time.Time
	// SubtaskStartedAt is when the current role run (reviewer/fix) began;
	// zero falls back to CurrentStartedAt for live subtask elapsed.
	SubtaskStartedAt  time.Time
	SessionStartedAt  time.Time
	SessionElapsed    time.Duration
	HasSessionElapsed bool
	Verbose           bool
}

// SpinnerFrame returns the braille spinner glyph for a tick count. The cycle
// wraps (and tolerates negative ticks).
func SpinnerFrame(tick int) string {
	frames := []rune(spinnerChars)
	n := len(frames)
	i := tick % n
	if i < 0 {
		i += n
	}
	return string(frames[i])
}

// FormatDuration renders a duration compactly: 42s, 2m14s, 1h04m.
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Seconds())
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
	default:
		return fmt.Sprintf("%dh%02dm", secs/3600, (secs%3600)/60)
	}
}

// TruncateToWidth caps a string at width runes, marking cuts with "…".
// Non-positive widths yield "".
func TruncateToWidth(s string, width int) string {
	if width < 1 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:width-1]) + "…"
}

// FormatTokens renders a token count compactly: 842, 5.8k, 197.0k, 1.2m.
func FormatTokens(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1000*1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	}
}

// FormatCost renders a USD amount rounded to 2 decimal places: $0.68, $0.50, $2.50.
func FormatCost(c float64) string {
	return "$" + strconv.FormatFloat(c, 'f', 2, 64)
}

// FormatUsage renders "↓3.6m ↑25.6k tok · $0.68". ↓ is input tokens, ↑ is
// output tokens. The token segment renders only when reported; cost only
// when the provider reported it.
func FormatUsage(in, out int64, cost float64, hasTokens, hasCost bool) string {
	var parts []string
	if hasTokens {
		parts = append(parts, fmt.Sprintf("↓%s ↑%s tok", FormatTokens(in), FormatTokens(out)))
	}
	if hasCost {
		parts = append(parts, FormatCost(cost))
	}
	return strings.Join(parts, " · ")
}

// BuildFrame renders the persistent checklist for a session view. Lines are
// truncated to width; if the frame exceeds height it is clamped around the
// current step with "… +N more" elision markers.
func BuildFrame(v SessionView, spin string, width, height int) []string {
	return BuildFrameStyled(v, spin, width, height, false)
}

type stepBlock struct {
	lines []string
}

// ModelCost is one model family's attributed share of session cost for the
// header breakdown.
type ModelCost struct {
	Model   string
	CostUSD float64
}

// workerStoryRole reports whether a subtask role speaks for the worker
// conversation: the original worker run, or a fix round (whose resumed
// conversation reports cumulative usage — the whole worker story).
func workerStoryRole(role string) bool {
	return role == "worker" || strings.HasPrefix(role, "fix")
}

// modelCosts attributes the session's rendered costs to model families,
// mirroring the header total's own sources: per step, the last worker/fix
// run replaces earlier worker runs (fix rounds report cumulative usage)
// while reviewer rounds each add. Returns models sorted by cost, highest
// first.
func modelCosts(v SessionView) []ModelCost {
	costs := map[string]float64{}
	add := func(model string, st SubtaskView) {
		if model == "" || !st.HasCost || st.CostUSD <= 0 {
			return
		}
		costs[FormatModelName(modelFamily(model))] += st.CostUSD
	}
	for _, s := range v.Steps {
		var story *SubtaskView
		for i := range s.Subtasks {
			st := &s.Subtasks[i]
			switch {
			case workerStoryRole(st.Role):
				story = st
			case strings.HasPrefix(st.Role, "reviewer"):
				add(st.Model, *st)
			}
		}
		if story != nil {
			add(story.Model, *story)
		}
	}
	out := make([]ModelCost, 0, len(costs))
	for model, cost := range costs {
		out = append(out, ModelCost{Model: model, CostUSD: cost})
	}
	slices.SortFunc(out, func(a, b ModelCost) int {
		switch {
		case a.CostUSD > b.CostUSD:
			return -1
		case a.CostUSD < b.CostUSD:
			return 1
		}
		return strings.Compare(a.Model, b.Model)
	})
	return out
}

// FormatModelCosts renders a per-model cost breakdown:
// "Kimi K3: $0.77 | Gemini 3.8 Flash: $1.77".
func FormatModelCosts(costs []ModelCost) string {
	parts := make([]string, len(costs))
	for i, c := range costs {
		parts[i] = fmt.Sprintf("%s: %s", c.Model, FormatCost(c.CostUSD))
	}
	return strings.Join(parts, " | ")
}

// FormatPlanSummary renders the plan title header and numbered step list for progress output.
func FormatPlanSummary(title string, steps []Step) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Plan"
	}
	var b strings.Builder
	b.WriteString(title)
	b.WriteByte('\n')
	for i, s := range steps {
		num := s.Number
		if num == 0 {
			num = i + 1
		}
		fmt.Fprintf(&b, "  %d. %s\n", num, s.Title)
	}
	return b.String()
}

func renderHeader(v SessionView, useColor bool) string {
	approved := 0
	var totalIn, totalOut int64
	var totalCost float64
	var totalHasTokens, totalHasCost bool

	for _, s := range v.Steps {
		if s.Status == StepApproved {
			approved++
		}
		if s.HasTokens {
			totalIn += s.In
			totalOut += s.Out
			totalHasTokens = true
		}
		if s.HasCost {
			totalCost += s.CostUSD
			totalHasCost = true
		}
	}

	var headerParts []string
	if useColor {
		headerParts = append(
			headerParts,
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).Render(v.Name),
		)
	} else {
		headerParts = append(headerParts, v.Name)
	}

	if v.HasSessionElapsed {
		durStr := FormatDuration(v.SessionElapsed)
		if useColor {
			durStr = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Render(durStr)
		}
		headerParts = append(headerParts, durStr)
	}

	countStr := fmt.Sprintf("%d/%d approved", approved, len(v.Steps))
	if useColor {
		countStr = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(countStr)
	}
	headerParts = append(headerParts, countStr)

	if usageStr := FormatUsage(totalIn, totalOut, totalCost, totalHasTokens, totalHasCost); usageStr != "" {
		if useColor {
			usageStr = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Render(usageStr)
		}
		headerParts = append(headerParts, usageStr)
	}

	if breakdown := FormatModelCosts(modelCosts(v)); breakdown != "" {
		if useColor {
			breakdown = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Render(breakdown)
		}
		headerParts = append(headerParts, breakdown)
	}

	headerSep := " · "
	if useColor {
		headerSep = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" · ")
	}
	return strings.Join(headerParts, headerSep)
}

func renderStepMarker(s StepView, currentStep int, spin string, useColor bool) string {
	marker := "·"
	if s.Status == StepApproved {
		marker = "✓"
	}
	if currentStep != 0 && s.Number == currentStep {
		marker = spin
	}
	if !useColor {
		return marker
	}
	switch {
	case s.Status == StepApproved:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(marker)
	case currentStep != 0 && s.Number == currentStep:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Render(marker)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(marker)
	}
}

func renderStepLine(v SessionView, s StepView, marker string, useColor bool) string {
	if len(s.Subtasks) == 0 {
		return fmt.Sprintf("  %s %d. %s", marker, s.Number, s.Title) + stepSegmentsStyled(v, s, useColor)
	}
	text := fmt.Sprintf("  %s %d. %s", marker, s.Number, s.Title)
	var segs []string
	if s.HasElapsed {
		segs = append(segs, FormatDuration(s.Elapsed))
	}
	if usage := FormatUsage(s.In, s.Out, s.CostUSD, s.HasTokens, s.HasCost); usage != "" {
		segs = append(segs, usage)
	}
	if s.Status == StepApproved && s.Rounds > 1 {
		segs = append(segs, fmt.Sprintf("%d rounds", s.Rounds))
	}
	if len(segs) > 0 {
		sep := " · "
		if useColor {
			sep = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" · ")
		}
		text += "  " + strings.Join(segs, sep)
	}
	return text
}

func renderSubtaskLine(st SubtaskView, isLastSubtask bool, spin string, useColor bool) (string, string) {
	connector := "├── "
	childIndent := "  │   "
	if isLastSubtask {
		connector = "└── "
		childIndent = "      "
	}

	stMarker := "·"
	switch {
	case st.Active:
		stMarker = spin
	case st.Status == "approved" || st.Status == "done" || st.Status == "success":
		stMarker = "✓"
	case st.Status == "rejected" || st.Status == "failed":
		stMarker = "✗"
	}
	if useColor {
		switch stMarker {
		case "✓":
			stMarker = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(stMarker)
		case "✗":
			stMarker = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(stMarker)
		case spin:
			stMarker = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Render(stMarker)
		default:
			stMarker = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(stMarker)
		}
	}

	var stParts []string
	stParts = append(stParts, st.Role)
	modelName := FormatModelName(st.Model, st.Effort)
	if modelName != "" {
		stParts = append(stParts, StyleModel(modelName, st.Harness, useColor))
	} else if st.Harness != "" {
		stParts = append(stParts, st.Harness)
	}
	if st.HasElapsed {
		stParts = append(stParts, FormatDuration(st.Elapsed))
	}
	if usage := FormatUsage(st.In, st.Out, st.CostUSD, st.HasTokens, st.HasCost); usage != "" {
		stParts = append(stParts, usage)
	}
	if st.DiffStat != nil && !st.DiffStat.IsEmpty() {
		stParts = append(stParts, FormatDiffStat(*st.DiffStat, useColor))
	}

	sep := " · "
	if useColor {
		sep = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" · ")
	}
	conn := "  " + connector
	if useColor {
		conn = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(conn)
	}
	return fmt.Sprintf("%s%s %s", conn, stMarker, strings.Join(stParts, sep)), childIndent
}

func resolveActivitiesToRender(v SessionView, st SubtaskView) []ActivityItem {
	if v.Verbose {
		return st.Activities
	}
	if !st.Active {
		return nil
	}
	if len(st.Activities) > 0 {
		for _, act := range slices.Backward(st.Activities) {
			if act.Active {
				return []ActivityItem{act}
			}
		}
		return []ActivityItem{st.Activities[len(st.Activities)-1]}
	}
	if v.Activity != "" {
		return []ActivityItem{{Label: v.Activity, Active: true}}
	}
	return nil
}

func renderActivityLines(actsToRender []ActivityItem, childIndent, spin string, width int, useColor bool) []string {
	var lines []string
	for k, act := range actsToRender {
		isLastAct := (k == len(actsToRender)-1)
		actConn := childIndent + "├── "
		if isLastAct {
			actConn = childIndent + "└── "
		}
		actMarker := "●"
		if act.Active {
			actMarker = spin
		}
		var actParts []string
		actParts = append(actParts, act.Label)
		if act.Elapsed > 0 {
			actParts = append(actParts, FormatDuration(act.Elapsed))
		}
		if act.HasTokens {
			actParts = append(
				actParts,
				fmt.Sprintf("↓%s ↑%s tok", FormatTokens(act.In), FormatTokens(act.Out)),
			)
		}
		actContent := strings.Join(actParts, " · ")
		actLine := fmt.Sprintf("%s%s %s", actConn, actMarker, actContent)
		if useColor {
			actLine = lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render(actLine)
		}
		lines = append(lines, ansi.Truncate(actLine, width, "…"))
	}
	return lines
}

// BuildFrameStyled renders the persistent checklist for a session view with optional ANSI styling.
func BuildFrameStyled(v SessionView, spin string, width, height int, useColor bool) []string {
	headerLine := ansi.Truncate(renderHeader(v, useColor), width, "…")
	var blocks []stepBlock

	for _, s := range v.Steps {
		var stepLines []string
		marker := renderStepMarker(s, v.Current, spin, useColor)
		stepLines = append(stepLines, ansi.Truncate(renderStepLine(v, s, marker, useColor), width, "…"))

		for j, st := range s.Subtasks {
			isLastSubtask := (j == len(s.Subtasks)-1)
			stLine, childIndent := renderSubtaskLine(st, isLastSubtask, spin, useColor)
			stepLines = append(stepLines, ansi.Truncate(stLine, width, "…"))

			acts := resolveActivitiesToRender(v, st)
			stepLines = append(stepLines, renderActivityLines(acts, childIndent, spin, width, useColor)...)
		}

		blocks = append(blocks, stepBlock{lines: stepLines})
	}

	return clampFrameBlocks(headerLine, blocks, v, height)
}

func clampFrameBlocks(headerLine string, blocks []stepBlock, v SessionView, height int) []string {
	totalLines := 1
	for _, b := range blocks {
		totalLines += len(b.lines)
	}
	if height < 1 || totalLines <= height {
		out := make([]string, 0, totalLines)
		out = append(out, headerLine)
		for _, b := range blocks {
			out = append(out, b.lines...)
		}
		return out
	}

	if len(blocks) == 0 {
		return []string{headerLine}
	}

	cur := len(blocks) - 1
	for i, s := range v.Steps {
		if s.Number == v.Current {
			cur = i
			break
		}
	}

	budget := height - 1
	if budget <= 0 {
		return []string{headerLine}
	}

	start := cur
	end := cur
	linesUsed := len(blocks[cur].lines)

	if linesUsed > budget {
		out := make([]string, 0, budget+1)
		out = append(out, headerLine)
		out = append(out, blocks[cur].lines[:budget]...)
		return out
	}

	for {
		expanded := false
		// Try expanding before
		if start > 0 {
			cost := len(blocks[start-1].lines)
			topElision := 0
			if start-1 > 0 {
				topElision = 1
			}
			bottomElision := 0
			if end < len(blocks)-1 {
				bottomElision = 1
			}
			if linesUsed+cost+topElision+bottomElision <= budget {
				start--
				linesUsed += cost
				expanded = true
			}
		}
		// Try expanding after
		if end < len(blocks)-1 {
			cost := len(blocks[end+1].lines)
			topElision := 0
			if start > 0 {
				topElision = 1
			}
			bottomElision := 0
			if end+1 < len(blocks)-1 {
				bottomElision = 1
			}
			if linesUsed+cost+topElision+bottomElision <= budget {
				end++
				linesUsed += cost
				expanded = true
			}
		}
		if !expanded {
			break
		}
	}

	out := make([]string, 0, budget+1)
	out = append(out, headerLine)
	if start > 0 {
		out = append(out, fmt.Sprintf("  … +%d more", start))
	}
	for i := start; i <= end; i++ {
		out = append(out, blocks[i].lines...)
	}
	if rem := len(blocks) - 1 - end; rem > 0 {
		out = append(out, fmt.Sprintf("  … +%d more", rem))
	}
	return out
}

// FormatCommitError renders a styled Lip Gloss alert box for commit / hook failures.
func FormatCommitError(stepTitle string, commitErr error) string {
	var out string
	if gitErr, ok := errors.AsType[*GitError](commitErr); ok {
		out = strings.TrimSpace(gitErr.Output)
		if out == "" {
			out = gitErr.Error()
		}
	} else {
		out = commitErr.Error()
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	subStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("9")).
		Padding(0, 1)

	content := fmt.Sprintf("%s\n%s\n\n%s",
		titleStyle.Render("✗ Commit Failed (Pre-Commit Hooks)"),
		subStyle.Render("Step: "+stepTitle),
		out,
	)

	return boxStyle.Render(content)
}

func stepSegmentsStyled(v SessionView, s StepView, useColor bool) string {
	var segs []string
	if s.HasElapsed {
		segs = append(segs, FormatDuration(s.Elapsed))
	}
	if usage := FormatUsage(s.In, s.Out, s.CostUSD, s.HasTokens, s.HasCost); usage != "" {
		segs = append(segs, usage)
	}
	if v.Current != 0 && s.Number == v.Current {
		switch v.Role {
		case "reviewing":
			label := fmt.Sprintf("review r%d", v.Round)
			if useColor {
				label = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render(label)
			}
			segs = append(segs, label)
		case "fixing":
			label := fmt.Sprintf("fix r%d", v.Round)
			if useColor {
				label = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(label)
			}
			segs = append(segs, label)
		case "committing":
			label := "pre-commit/push hooks..."
			if useColor {
				label = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(label)
			}
			segs = append(segs, label)
		}
		if v.Model != "" {
			segs = append(segs, StyleModel(FormatModelName(v.Model, v.Effort), v.Harness, useColor))
		} else if v.Harness != "" {
			segs = append(segs, v.Harness)
		}
		if v.Activity != "" {
			segs = append(segs, v.Activity)
		}
	} else if s.Status == StepApproved {
		if s.Rounds > 0 {
			seg := fmt.Sprintf("%d round", s.Rounds)
			if s.Rounds > 1 {
				seg += "s"
			}
			segs = append(segs, seg)
		}
	}
	if len(segs) == 0 {
		return ""
	}
	sep := " · "
	if useColor {
		sep = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" · ")
	}
	return "  " + strings.Join(segs, sep)
}

// RendererOpts configures a Renderer. Zero values get sane defaults.
type RendererOpts struct {
	// TTY enables in-place frame redraws with ANSI sequences. When false,
	// only plain lines are ever written.
	TTY bool
	// Color enables ANSI color styling on drawn frames.
	Color bool
	// Verbose prints each activity event as a permanent "  ● label" line.
	Verbose bool
	Width   int
	Height  int
	// Interval is the spinner tick period (default 200ms).
	Interval time.Duration
	// Now overrides the clock for tests.
	Now func() time.Time
}

// Renderer draws live progress frames on TTYs and degrades to plain lines
// when piped. Safe for concurrent use.
type Renderer struct {
	w        io.Writer
	tty      bool
	color    bool
	verbose  bool
	width    int
	height   int
	interval time.Duration
	now      func() time.Time

	mu         sync.Mutex
	view       SessionView
	tick       int
	linesDrawn int
	active     bool // a frame is currently on screen
	stopped    bool
	tickerOn   bool
	stopTicker chan struct{}

	currentRunIn      int64
	currentRunOut     int64
	activityStartedAt time.Time
	activityStartIn   int64
	activityStartOut  int64
}

// NewRenderer builds a Renderer writing to w.
func NewRenderer(w io.Writer, opts RendererOpts) *Renderer {
	if opts.Width <= 0 {
		opts.Width = 80
	}
	if opts.Height <= 0 {
		opts.Height = 24
	}
	if opts.Interval <= 0 {
		opts.Interval = 200 * time.Millisecond
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Renderer{
		w:        w,
		tty:      opts.TTY,
		color:    opts.Color,
		verbose:  opts.Verbose,
		width:    opts.Width,
		height:   opts.Height,
		interval: opts.Interval,
		now:      opts.Now,
	}
}

// Live returns true if the renderer is an active interactive TTY that manages
// screen frames in-place (meaning routine progress lines should not be printed
// as plain lines above the frame).
func (r *Renderer) Live() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tty && !r.stopped
}

// SetView replaces the session snapshot and redraws the frame. It preserves
// the previous activity label when the new view carries none, and reactivates
// a stopped renderer.
func (r *Renderer) SetView(v SessionView) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.tty {
		return
	}
	if v.Activity == "" {
		v.Activity = r.view.Activity
	}
	r.view = v
	r.stopped = false
	r.startTickerLocked()
	r.redrawLocked()
}

// Activity records a tool label from the stream. Verbose renderers also
// print it as a permanent line; otherwise it only updates the frame.
func (r *Renderer) Activity(label string) {
	if label == "" {
		return
	}
	r.mu.Lock()
	now := r.now()
	if r.view.Activity != label {
		if r.view.Activity != "" {
			for i := range r.view.Steps {
				if r.view.Current != 0 && r.view.Steps[i].Number == r.view.Current {
					for j := range r.view.Steps[i].Subtasks {
						if r.view.Steps[i].Subtasks[j].Active {
							dur := now.Sub(r.activityStartedAt)
							inTok := r.currentRunIn - r.activityStartIn
							outTok := r.currentRunOut - r.activityStartOut
							if inTok < 0 {
								inTok = 0
							}
							if outTok < 0 {
								outTok = 0
							}
							if len(r.view.Steps[i].Subtasks[j].Activities) > 0 {
								last := len(r.view.Steps[i].Subtasks[j].Activities) - 1
								r.view.Steps[i].Subtasks[j].Activities[last].Active = false
								r.view.Steps[i].Subtasks[j].Activities[last].Elapsed = dur
								r.view.Steps[i].Subtasks[j].Activities[last].In = inTok
								r.view.Steps[i].Subtasks[j].Activities[last].Out = outTok
								r.view.Steps[i].Subtasks[j].Activities[last].HasTokens = inTok > 0 || outTok > 0
							}
						}
					}
				}
			}
		}
		r.view.Activity = label
		r.activityStartedAt = now
		r.activityStartIn = r.currentRunIn
		r.activityStartOut = r.currentRunOut
	}
	verbose := r.verbose
	r.mu.Unlock()
	if verbose {
		r.PlainLine("  ● %s", label)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.redrawLocked()
}

// PlainLine prints a permanent line. If a frame is on screen it is erased
// first and redrawn below the new line.
func (r *Renderer) PlainLine(format string, a ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line := fmt.Sprintf(format, a...)
	if !r.tty || !r.active {
		_, _ = fmt.Fprintln(r.w, line)
		return
	}
	var b strings.Builder
	r.eraseSeqLocked(&b)
	b.WriteString(line)
	b.WriteByte('\n')
	r.writeFrameLocked(&b)
	_, _ = fmt.Fprint(r.w, b.String())
}

// Tick advances the spinner and redraws (also driven by the internal ticker).
func (r *Renderer) Tick() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tick++
	r.redrawLocked()
}

// Usage updates the current step's token/cost display and redraws.
func (r *Renderer) Usage(u StepUsage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.currentRunIn = u.InputTokens
	r.currentRunOut = u.OutputTokens
	if !r.tty {
		return
	}
	for i := range r.view.Steps {
		if r.view.Current != 0 && r.view.Steps[i].Number == r.view.Current {
			r.view.Steps[i].In = u.InputTokens
			r.view.Steps[i].Out = u.OutputTokens
			r.view.Steps[i].CostUSD = u.CostUSD
			r.view.Steps[i].HasTokens = u.HasTokens
			r.view.Steps[i].HasCost = u.HasCost

			for j := range r.view.Steps[i].Subtasks {
				if r.view.Steps[i].Subtasks[j].Active {
					r.view.Steps[i].Subtasks[j].In = u.InputTokens
					r.view.Steps[i].Subtasks[j].Out = u.OutputTokens
					r.view.Steps[i].CostUSD = u.CostUSD
					r.view.Steps[i].HasTokens = u.HasTokens
					r.view.Steps[i].HasCost = u.HasCost
				}
			}
		}
	}
	r.redrawLocked()
}

// Stop erases any frame, restores the cursor, and halts the ticker. The
// renderer prints plain lines only until the next SetView reactivates it.
func (r *Renderer) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.haltTickerLocked()
	if r.tty && r.active {
		var b strings.Builder
		r.eraseSeqLocked(&b)
		b.WriteString(showCursor)
		_, _ = fmt.Fprint(r.w, b.String())
	}
	r.active = false
	r.stopped = true
}

func (r *Renderer) startTickerLocked() {
	if r.tickerOn {
		return
	}
	r.tickerOn = true
	ch := make(chan struct{})
	r.stopTicker = ch
	go func() {
		t := time.NewTicker(r.interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				r.Tick()
			case <-ch:
				return
			}
		}
	}()
}

func (r *Renderer) haltTickerLocked() {
	if !r.tickerOn {
		return
	}
	close(r.stopTicker)
	r.tickerOn = false
}

// redrawLocked repaints the frame in place. The first draw hides the cursor;
// later draws move up over the previous frame and erase to end-of-screen.
func (r *Renderer) redrawLocked() {
	if !r.tty || r.stopped {
		return
	}
	var b strings.Builder
	if !r.active {
		b.WriteString(hideCursor)
		r.active = true
	} else {
		r.eraseSeqLocked(&b)
	}
	r.writeFrameLocked(&b)
	_, _ = fmt.Fprint(r.w, b.String())
}

// eraseSeqLocked emits the sequence returning the cursor to the top of the
// drawn frame and clearing to end-of-screen.
func (r *Renderer) eraseSeqLocked(b *strings.Builder) {
	b.WriteByte('\r')
	if r.linesDrawn > 1 {
		fmt.Fprintf(b, "\x1b[%dA", r.linesDrawn-1)
	}
	b.WriteString(eraseToEOS)
}

// writeFrameLocked renders the current frame, deriving live elapsed time for
// the current step from CurrentStartedAt. It never mutates the stored view.
func (r *Renderer) writeFrameLocked(b *strings.Builder) {
	v := r.view
	now := r.now()
	// The active subtask counts from its own run's start when known
	// (reviewer/fix runs begin well after the step did); otherwise it falls
	// back to the step's start.
	subStart := v.CurrentStartedAt
	if !v.SubtaskStartedAt.IsZero() {
		subStart = v.SubtaskStartedAt
	}
	if !v.SessionStartedAt.IsZero() && !v.HasSessionElapsed {
		v.SessionElapsed = now.Sub(v.SessionStartedAt)
		v.HasSessionElapsed = true
	}
	steps := make([]StepView, len(v.Steps))
	copy(steps, v.Steps)
	v.Steps = steps
	for i := range v.Steps {
		if v.Current != 0 && v.Steps[i].Number == v.Current {
			if !v.Steps[i].HasElapsed && !v.CurrentStartedAt.IsZero() {
				v.Steps[i].Elapsed = now.Sub(v.CurrentStartedAt)
				v.Steps[i].HasElapsed = true
			}
			if len(v.Steps[i].Subtasks) > 0 {
				subtasks := make([]SubtaskView, len(v.Steps[i].Subtasks))
				copy(subtasks, v.Steps[i].Subtasks)
				for j := range subtasks {
					if subtasks[j].Active {
						if !subtasks[j].HasElapsed && !subStart.IsZero() {
							subtasks[j].Elapsed = now.Sub(subStart)
							subtasks[j].HasElapsed = true
						}
						if v.Activity != "" {
							actElapsed := time.Duration(0)
							if !r.activityStartedAt.IsZero() {
								actElapsed = now.Sub(r.activityStartedAt)
							}
							actIn := r.currentRunIn - r.activityStartIn
							actOut := r.currentRunOut - r.activityStartOut
							if actIn < 0 {
								actIn = 0
							}
							if actOut < 0 {
								actOut = 0
							}
							activeItem := ActivityItem{
								Label:     v.Activity,
								Elapsed:   actElapsed,
								In:        actIn,
								Out:       actOut,
								HasTokens: actIn > 0 || actOut > 0,
								Active:    true,
							}
							if v.Verbose {
								acts := make([]ActivityItem, len(subtasks[j].Activities))
								copy(acts, subtasks[j].Activities)
								if len(acts) > 0 && acts[len(acts)-1].Active {
									acts[len(acts)-1] = activeItem
								} else {
									acts = append(acts, activeItem)
								}
								subtasks[j].Activities = acts
							} else {
								subtasks[j].Activities = []ActivityItem{activeItem}
							}
						}
					}
				}
				v.Steps[i].Subtasks = subtasks
			}
		}
	}
	lines := BuildFrameStyled(v, SpinnerFrame(r.tick), r.width, r.height, r.color)
	r.linesDrawn = len(lines)
	b.WriteString(strings.Join(lines, "\n"))
}
