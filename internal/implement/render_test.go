package implement

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testView is a 3-step session with step 2 in flight.
func testView() SessionView {
	return SessionView{
		Name: "add-CI-timer",
		Steps: []StepView{
			{
				Number:     1,
				Title:      "Timestamps & Duration",
				Status:     StepApproved,
				Elapsed:    134 * time.Second,
				HasElapsed: true,
				Rounds:     1,
			},
			{
				Number:     2,
				Title:      "GraphQL & Wire Hydration",
				Status:     StepPending,
				Elapsed:    67 * time.Second,
				HasElapsed: true,
			},
			{Number: 3, Title: "TUI Timer Rendering", Status: StepPending},
		},
		Current: 2,
		Role:    "working",
		Harness: "agy",
	}
}

func TestSpinnerFrame(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "⠋", SpinnerFrame(0))
	assert.Equal(t, "⠙", SpinnerFrame(1))
	assert.Equal(t, "⠏", SpinnerFrame(9))
	assert.Equal(t, "⠋", SpinnerFrame(10))  // wraps
	assert.Equal(t, "⠙", SpinnerFrame(11))  // wraps
	assert.Equal(t, "⠋", SpinnerFrame(-10)) // negative wraps to 0
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{42 * time.Second, "42s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m00s"},
		{2*time.Minute + 14*time.Second, "2m14s"},
		{time.Hour + 4*time.Minute, "1h04m"},
		{3 * time.Hour, "3h00m"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, FormatDuration(tc.d))
		})
	}
}

func TestTruncateToWidth(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "short", TruncateToWidth("short", 10))
	assert.Equal(t, "0123456789", TruncateToWidth("0123456789", 10))
	assert.Equal(t, "012345678…", TruncateToWidth("01234567890", 10))
	assert.Equal(t, "日本語…", TruncateToWidth("日本語テスト", 4)) // rune-based, not bytes
	assert.Empty(t, TruncateToWidth("anything", 0))
	assert.Empty(t, TruncateToWidth("anything", -1))
}

func TestBuildFrameChecklist(t *testing.T) {
	t.Parallel()
	v := testView()
	v.Activity = "Edit wire.go"

	lines := BuildFrame(v, "⠸", 80, 24)
	require.Len(t, lines, 4)

	assert.Contains(t, lines[0], "add-CI-timer")
	assert.Contains(t, lines[0], "1/3 approved")

	assert.Contains(t, lines[1], "✓ 1. Timestamps & Duration")
	assert.Contains(t, lines[1], "2m14s")
	assert.Contains(t, lines[1], "1 round")

	assert.Contains(t, lines[2], "⠸ 2. GraphQL & Wire Hydration")
	assert.Contains(t, lines[2], "1m07s")
	assert.Contains(t, lines[2], "agy")
	assert.Contains(t, lines[2], "Edit wire.go")

	assert.Contains(t, lines[3], "· 3. TUI Timer Rendering")
	assert.NotContains(t, lines[3], "⠸")
}

func TestBuildFrameReviewLine(t *testing.T) {
	t.Parallel()
	v := testView()
	v.Role = "reviewing"
	v.Round = 1
	v.Harness = "opencode"
	v.Activity = "Read step-2-work.md"

	lines := BuildFrame(v, "⠸", 120, 24)
	require.Len(t, lines, 4)
	assert.Contains(t, lines[2], "review r1")
	assert.Contains(t, lines[2], "opencode")
	assert.Contains(t, lines[2], "Read step-2-work.md")
}

func TestBuildFrameFixLine(t *testing.T) {
	t.Parallel()
	v := testView()
	v.Role = "fixing"
	v.Round = 2
	v.Harness = "claude"

	lines := BuildFrame(v, "⠸", 80, 24)
	assert.Contains(t, lines[2], "fix r2")
	assert.Contains(t, lines[2], "claude")
}

func TestBuildFrameCommitLine(t *testing.T) {
	t.Parallel()
	v := testView()
	v.Role = "committing"

	lines := BuildFrame(v, "⠸", 80, 24)
	assert.Contains(t, lines[2], "pre-commit/push hooks...")
}

func TestBuildFrameStyledColor(t *testing.T) {
	t.Parallel()
	v := testView()
	v.Role = "committing"

	plain := BuildFrameStyled(v, "⠸", 80, 24, false)
	assert.Contains(t, plain[2], "pre-commit/push hooks...")
	assert.NotContains(t, plain[1], "\x1b[")

	styled := BuildFrameStyled(v, "⠸", 80, 24, true)
	assert.Contains(t, styled[1], "\x1b[") // has ANSI color codes
	assert.Contains(t, styled[2], "pre-commit/push hooks...")
}

func TestFormatCommitError(t *testing.T) {
	t.Parallel()
	ge := &GitError{
		Args:   []string{"commit", "-m", "feat: test"},
		Err:    errors.New("exit status 1"),
		Output: "golangci-lint exited with code 1\nmain.go:10: undefined",
	}

	rendered := FormatCommitError("GraphQL & Wire Hydration", ge)
	assert.Contains(t, rendered, "Commit Failed")
	assert.Contains(t, rendered, "GraphQL & Wire Hydration")
	assert.Contains(t, rendered, "golangci-lint exited with code 1")
}

func TestBuildFrameNoElapsed(t *testing.T) {
	t.Parallel()
	v := SessionView{
		Name: "s",
		Steps: []StepView{
			{Number: 1, Title: "done", Status: StepApproved, Rounds: 0},
			{Number: 2, Title: "live", Status: StepPending},
		},
		Current:  2,
		Role:     "working",
		Harness:  "agy",
		Activity: "Read x.go",
	}
	lines := BuildFrame(v, "⠸", 80, 24)
	assert.Equal(t, "  ✓ 1. done", lines[1])
	assert.NotRegexp(t, `\d+[smh]`, lines[2])
	assert.Contains(t, lines[2], "agy · Read x.go")
}

func TestBuildFrameTruncatesLongTitles(t *testing.T) {
	t.Parallel()
	v := SessionView{
		Name: "s",
		Steps: []StepView{
			{
				Number:     1,
				Title:      strings.Repeat("w", 60),
				Status:     StepApproved,
				HasElapsed: true,
				Elapsed:    2 * time.Minute,
			},
		},
		Current:  1,
		Role:     "working",
		Harness:  "agy",
		Activity: strings.Repeat("a", 60),
	}
	lines := BuildFrame(v, "⠸", 30, 24)
	require.Len(t, lines, 2)
	for _, line := range lines {
		assert.LessOrEqual(t, len([]rune(line)), 30, "line exceeds width: %q", line)
	}
}

func TestBuildFrameClampToHeight(t *testing.T) {
	t.Parallel()
	v := SessionView{Name: "big"}
	for i := 1; i <= 12; i++ {
		v.Steps = append(v.Steps, StepView{
			Number: i,
			Title:  fmt.Sprintf("Title %d", i),
			Status: StepPending,
		})
	}
	v.Current = 7
	v.Role = "working"
	v.Harness = "agy"

	lines := BuildFrame(v, "⠸", 80, 6)
	assert.LessOrEqual(t, len(lines), 6, "frame exceeds terminal height")
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "0/12 approved") // header always rendered
	assert.Contains(t, joined, "Title 7")       // current step always visible
	assert.Contains(t, joined, "… +")           // elision marker for hidden steps
}

func TestRendererNonTTYSilentFrame(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	r := NewRenderer(buf, RendererOpts{TTY: false, Verbose: false, Width: 80, Height: 24})

	r.SetView(testView())
	r.Activity("Read x.go")
	r.Tick()
	r.Stop()

	assert.Empty(t, buf.String(), "non-TTY renderer must not emit frame or event output")

	r.PlainLine("step %d: APPROVED", 1)
	assert.Equal(t, "step 1: APPROVED\n", buf.String())
}

func TestRendererLive(t *testing.T) {
	t.Parallel()
	var nilR *Renderer
	assert.False(t, nilR.Live())

	buf := &bytes.Buffer{}
	nonTTY := NewRenderer(buf, RendererOpts{TTY: false})
	assert.False(t, nonTTY.Live())

	tty := NewRenderer(buf, RendererOpts{TTY: true})
	assert.True(t, tty.Live())

	tty.Stop()
	assert.False(t, tty.Live())

	tty.SetView(testView())
	assert.True(t, tty.Live())
}

func TestRendererNonTTYVerboseTail(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	r := NewRenderer(buf, RendererOpts{TTY: false, Verbose: true, Width: 80, Height: 24})

	r.SetView(testView())
	r.Activity("Read x.go")
	assert.Equal(t, "  ● Read x.go\n", buf.String())
}

func newTTYRenderer(buf *bytes.Buffer, opts RendererOpts) *Renderer {
	opts.TTY = true
	opts.Width = 80
	opts.Height = 24
	opts.Interval = time.Hour // ticker idle during tests; Tick called manually
	return NewRenderer(buf, opts)
}

func TestRendererTTYFrameLifecycle(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	r := newTTYRenderer(buf, RendererOpts{})

	r.SetView(testView())
	out := buf.String()
	assert.True(t, strings.HasPrefix(out, "\x1b[?25l"), "first frame must hide cursor")
	assert.Contains(t, out, "GraphQL & Wire Hydration")

	// Second frame: move up over the 4 drawn lines (3 ups), erase, redraw.
	// The cursor-hide sequence must not repeat on redraws.
	buf.Reset()
	v2 := testView()
	v2.Activity = "Edit wire.go"
	r.SetView(v2)
	out = buf.String()
	assert.Contains(t, out, "\x1b[3A")
	assert.Contains(t, out, "\x1b[J")
	assert.Contains(t, out, "Edit wire.go")
	assert.NotContains(t, out, "\x1b[?25l")

	// PlainLine interleaves: erase frame, print line, redraw frame below it.
	buf.Reset()
	r.PlainLine("step 2: APPROVED")
	out = buf.String()
	assert.Contains(t, out, "step 2: APPROVED\n")
	iErase := strings.Index(out, "\x1b[3A")
	iLine := strings.Index(out, "step 2: APPROVED")
	iFrame := strings.Index(out, "GraphQL & Wire Hydration")
	require.GreaterOrEqual(t, iErase, 0)
	require.GreaterOrEqual(t, iFrame, 0)
	assert.Less(t, iErase, iLine, "erase must precede the plain line")
	assert.Less(t, iLine, iFrame, "redraw must follow the plain line")

	// Stop: erase frame, restore cursor, no redraw.
	buf.Reset()
	r.Stop()
	out = buf.String()
	assert.Contains(t, out, "\x1b[?25h")
	assert.NotContains(t, out, "GraphQL & Wire Hydration")

	buf.Reset()
	r.PlainLine("done")
	assert.Equal(t, "done\n", buf.String())

	// SetView reactivates after Stop.
	buf.Reset()
	r.SetView(testView())
	out = buf.String()
	assert.Contains(t, out, "\x1b[?25l")
	assert.Contains(t, out, "GraphQL & Wire Hydration")
	r.Stop()
}

func TestRendererTTYActivityModes(t *testing.T) {
	t.Parallel()
	// Verbose: event printed as a permanent line above the frame.
	buf := &bytes.Buffer{}
	r := newTTYRenderer(buf, RendererOpts{Verbose: true})
	r.SetView(testView())
	buf.Reset()
	r.Activity("Read x.go")
	out := buf.String()
	assert.Contains(t, out, "  ● Read x.go\n")
	assert.Contains(t, out, "Read x.go")
	r.Stop()

	// Non-verbose: no plain event line; activity only inside the frame.
	buf = &bytes.Buffer{}
	r = newTTYRenderer(buf, RendererOpts{Verbose: false})
	r.SetView(testView())
	buf.Reset()
	r.Activity("Edit wire.go")
	out = buf.String()
	assert.NotContains(t, out, "●")
	assert.Contains(t, out, "Edit wire.go")
	r.Stop()
}

func TestRendererActivityPreservedAcrossSetView(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	r := newTTYRenderer(buf, RendererOpts{})
	r.SetView(testView())
	r.Activity("Edit wire.go")
	buf.Reset()

	// Phase transition (work -> review) keeps the live activity label.
	v2 := testView()
	v2.Role = "reviewing"
	v2.Round = 1
	v2.Harness = "opencode"
	r.SetView(v2)
	assert.Contains(t, buf.String(), "Edit wire.go")
	r.Stop()
}

func TestRendererTick(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	clock := time.Now()
	r := newTTYRenderer(buf, RendererOpts{Now: func() time.Time { return clock }})

	v := testView()
	v.Steps[1].HasElapsed = false // live elapsed comes from CurrentStartedAt
	v.CurrentStartedAt = clock.Add(-90 * time.Second)
	r.SetView(v)
	assert.Contains(t, buf.String(), "1m30s")

	buf.Reset()
	clock = clock.Add(30 * time.Second)
	r.Tick()
	out := buf.String()
	assert.Contains(t, out, "2m00s")
	assert.Contains(t, out, SpinnerFrame(1), "spinner must advance on tick")
	r.Stop()
}

func TestFormatTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{842, "842"},
		{5800, "5.8k"},
		{197000, "197.0k"},
		{999999, "1000.0k"},
		{1200000, "1.2m"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, FormatTokens(tc.n))
		})
	}
}

func TestFormatCost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		c    float64
		want string
	}{
		{0, "$0.00"},
		{0.0421, "$0.04"},
		{0.5, "$0.50"},
		{2.5, "$2.50"},
		{0.0123, "$0.01"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, FormatCost(tc.c))
		})
	}
}

func TestBuildFrameUsageSegments(t *testing.T) {
	t.Parallel()
	v := testView()
	v.Steps[0] = StepView{
		Number: 1, Title: "Timestamps & Duration", Status: StepApproved,
		Elapsed: 134 * time.Second, HasElapsed: true, Rounds: 1,
		In: 197000, Out: 5800, CostUSD: 0.21, HasTokens: true, HasCost: true,
	}
	v.Steps[1].In = 315
	v.Steps[1].Out = 40
	v.Steps[1].CostUSD = 0.0123
	v.Steps[1].HasTokens = true
	v.Steps[1].HasCost = true
	v.Activity = "Edit wire.go"

	lines := BuildFrame(v, "⠸", 120, 24)
	assert.Contains(t, lines[1], "↓197.0k ↑5.8k tok · $0.21")
	assert.Contains(t, lines[1], "1 round")
	assert.Contains(t, lines[2], "↓315 ↑40 tok · $0.01")

	// Tokens without cost: no dollar figure.
	v.Steps[1].HasCost = false
	lines = BuildFrame(v, "⠸", 120, 24)
	assert.Contains(t, lines[2], "↓315 ↑40 tok")
	assert.NotContains(t, lines[2], "$")

	// No usage at all: no token segment.
	v.Steps[1].HasTokens = false
	lines = BuildFrame(v, "⠸", 120, 24)
	assert.NotContains(t, lines[2], "tok")
	assert.NotContains(t, lines[3], "tok")
}

func TestRendererUsageLive(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	r := newTTYRenderer(buf, RendererOpts{})
	r.SetView(testView())
	buf.Reset()
	r.Usage(StepUsage{InputTokens: 315, OutputTokens: 40, CostUSD: 0.0123, HasTokens: true, HasCost: true})
	assert.Contains(t, buf.String(), "↓315 ↑40 tok · $0.01")
	r.Stop()

	// Non-TTY: usage never emits output on its own.
	buf = &bytes.Buffer{}
	r = NewRenderer(buf, RendererOpts{TTY: false, Width: 80, Height: 24})
	r.SetView(testView())
	r.Usage(StepUsage{InputTokens: 315, OutputTokens: 40, HasTokens: true})
	assert.Empty(t, buf.String())
}

func TestRendererActiveSubtaskOwnTiming(t *testing.T) {
	t.Parallel()
	now := time.Now()

	// Reviewer mid-run: subtask counts from its own start (15s), step line
	// keeps the step's total (100s).
	buf := &bytes.Buffer{}
	r := newTTYRenderer(buf, RendererOpts{Now: func() time.Time { return now }})
	v := SessionView{
		Name:             "review-timing",
		Current:          2,
		Role:             "reviewing",
		CurrentStartedAt: now.Add(-100 * time.Second),
		SubtaskStartedAt: now.Add(-15 * time.Second),
		Steps: []StepView{
			{Number: 1, Title: "One", Status: StepApproved},
			{
				Number: 2,
				Title:  "Two",
				Status: StepPending,
				Subtasks: []SubtaskView{
					{Role: "reviewer (r1)", Harness: "claude", Model: "kimi-k3", Status: "pending", Active: true},
				},
			},
		},
	}
	r.SetView(v)
	lines := strings.Split(buf.String(), "\n")
	sub := lines[len(lines)-1]
	assert.Contains(t, sub, "reviewer (r1)")
	assert.Contains(t, sub, "15s")
	assert.NotContains(t, sub, "1m40s")
	assert.Contains(t, lines[len(lines)-2], "1m40s")
	r.Stop()

	// No subtask start known: falls back to the step's start.
	buf = &bytes.Buffer{}
	r = newTTYRenderer(buf, RendererOpts{Now: func() time.Time { return now }})
	v.SubtaskStartedAt = time.Time{}
	r.SetView(v)
	lines = strings.Split(buf.String(), "\n")
	assert.Contains(t, lines[len(lines)-1], "1m40s")
	r.Stop()
}

func TestRendererConcurrentAccess(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	r := newTTYRenderer(buf, RendererOpts{})

	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range 50 {
				switch g {
				case 0:
					v := testView()
					v.Current = (i % 3) + 1
					r.SetView(v)
				case 1:
					r.Activity(fmt.Sprintf("tool %d", i))
				case 2:
					r.Tick()
				case 3:
					r.PlainLine("line %d", i)
				}
			}
		}(g)
	}
	wg.Wait()
	r.Stop()
	assert.NotEmpty(t, buf.String())
}

func TestBuildFrameHeaderWithSessionElapsedAndTotals(t *testing.T) {
	t.Parallel()
	v := SessionView{
		Name:              "ci-timer",
		SessionElapsed:    94 * time.Second,
		HasSessionElapsed: true,
		Steps: []StepView{
			{
				Number:     1,
				Title:      "Step One",
				Status:     StepApproved,
				In:         10000,
				Out:        1500,
				CostUSD:    0.04,
				HasTokens:  true,
				HasCost:    true,
				Elapsed:    40 * time.Second,
				HasElapsed: true,
			},
			{
				Number:     2,
				Title:      "Step Two",
				Status:     StepApproved,
				In:         5000,
				Out:        500,
				CostUSD:    0.02,
				HasTokens:  true,
				HasCost:    true,
				Elapsed:    35 * time.Second,
				HasElapsed: true,
			},
			{
				Number: 3,
				Title:  "Step Three",
				Status: StepPending,
			},
		},
		Current: 3,
	}

	lines := BuildFrame(v, "⠸", 120, 24)
	require.NotEmpty(t, lines)
	// Header must contain session elapsed timer (1m34s), approved count, aggregate tokens (15.0k in 2.0k out), total cost ($0.06)
	assert.Contains(t, lines[0], "ci-timer")
	assert.Contains(t, lines[0], "1m34s")
	assert.Contains(t, lines[0], "2/3 approved")
	assert.Contains(t, lines[0], "↓15.0k")
	assert.Contains(t, lines[0], "↑2.0k")
	assert.Contains(t, lines[0], "$0.06")
}

func TestBuildFrameHeaderModelCostBreakdown(t *testing.T) {
	t.Parallel()
	v := SessionView{
		Name: "model-costs",
		Steps: []StepView{
			{
				Number:  1,
				Title:   "Step One",
				Status:  StepApproved,
				CostUSD: 0.77,
				HasCost: true,
				Subtasks: []SubtaskView{
					{Role: "worker", Model: "kimi-k3", CostUSD: 0.07, HasCost: true, Status: "approved"},
					{Role: "reviewer (r1)", Model: "kimi-k3", CostUSD: 0.70, HasCost: true, Status: "approved"},
				},
			},
			{
				Number:  2,
				Title:   "Step Two",
				Status:  StepApproved,
				CostUSD: 1.77,
				HasCost: true,
				Subtasks: []SubtaskView{
					{Role: "worker", Model: "gemini-3.8-flash-high", CostUSD: 1.77, HasCost: true, Status: "approved"},
				},
			},
		},
	}

	lines := BuildFrame(v, "⠸", 200, 24)
	// Models merge across roles/steps (effort suffixes stripped), sorted by cost desc.
	assert.Contains(t, lines[0], "Gemini 3.8 Flash: $1.77 | Kimi K3: $0.77")
}

func TestBuildFrameHeaderModelCostFixSupersedesWorker(t *testing.T) {
	t.Parallel()
	v := SessionView{
		Name: "fix-costs",
		Steps: []StepView{
			{
				Number:  1,
				Title:   "Step One",
				Status:  StepApproved,
				CostUSD: 0.30,
				HasCost: true,
				Subtasks: []SubtaskView{
					// Fix rounds report cumulative worker-conversation usage;
					// the original worker run's report is superseded.
					{Role: "worker", Model: "claude-sonnet-4-6", CostUSD: 0.10, HasCost: true, Status: "approved"},
					{Role: "fix (r1)", Model: "kimi-k3", CostUSD: 0.30, HasCost: true, Status: "approved"},
				},
			},
		},
	}

	lines := BuildFrame(v, "⠸", 200, 24)
	assert.Contains(t, lines[0], "Kimi K3: $0.30")
	assert.NotContains(t, lines[0], "Sonnet")
}

func TestBuildFrameHeaderNoModelCostWithoutCost(t *testing.T) {
	t.Parallel()
	v := SessionView{
		Name: "no-costs",
		Steps: []StepView{
			{
				Number: 1,
				Title:  "Step One",
				Status: StepApproved,
				Subtasks: []SubtaskView{
					{Role: "worker", Model: "kimi-k3", In: 100, Out: 10, HasTokens: true, Status: "approved"},
				},
			},
		},
	}

	lines := BuildFrame(v, "⠸", 200, 24)
	assert.NotContains(t, lines[0], "Kimi K3:")
}

func TestBuildFrameSubtasksAndTree(t *testing.T) {
	t.Parallel()
	stat := DiffStat{
		LinesAdded:    45,
		LinesDeleted:  12,
		FilesModified: 2,
		FilesAdded:    1,
	}
	v := SessionView{
		Name:              "session-tree",
		SessionElapsed:    60 * time.Second,
		HasSessionElapsed: true,
		Current:           1,
		Steps: []StepView{
			{
				Number:     1,
				Title:      "Wire Models",
				Status:     StepPending,
				Elapsed:    30 * time.Second,
				HasElapsed: true,
				In:         8200,
				Out:        450,
				CostUSD:    0.02,
				HasTokens:  true,
				HasCost:    true,
				Subtasks: []SubtaskView{
					{
						Role:       "worker",
						Harness:    "agy",
						Model:      "gemini-2.5-pro",
						Elapsed:    10 * time.Second,
						HasElapsed: true,
						In:         8200,
						Out:        450,
						CostUSD:    0.02,
						HasTokens:  true,
						HasCost:    true,
						Status:     "approved",
						DiffStat:   &stat,
					},
					{
						Role:       "reviewer (r1)",
						Harness:    "claude",
						Model:      "claude-3-7-sonnet",
						Elapsed:    5 * time.Second,
						HasElapsed: true,
						In:         1200,
						Out:        150,
						CostUSD:    0.01,
						HasTokens:  true,
						HasCost:    true,
						Active:     true,
						Activities: []ActivityItem{
							{
								Label:     "Read wire.go",
								Elapsed:   3 * time.Second,
								In:        600,
								Out:       50,
								HasTokens: true,
								Active:    true,
							},
						},
					},
				},
			},
		},
	}

	lines := BuildFrame(v, "⠸", 140, 24)
	require.Len(t, lines, 5)

	// Step line: leading marker, number, title, duration, tokens, cost
	assert.Contains(t, lines[1], "⠸ 1. Wire Models")
	assert.Contains(t, lines[1], "30s")
	assert.Contains(t, lines[1], "↓8.2k ↑450 tok")
	assert.Contains(t, lines[1], "$0.02")

	// Worker line: ├──, status ✓, role worker, Gemini 2.5 Pro, duration 10s, tokens, cost, diffstat
	assert.Contains(t, lines[2], "├── ✓ worker · Gemini 2.5 Pro")
	assert.NotContains(t, lines[2], "agy (gemini-2.5-pro)")
	assert.Contains(t, lines[2], "10s")
	assert.Contains(t, lines[2], "↓8.2k ↑450 tok")
	assert.Contains(t, lines[2], "$0.02")
	assert.Contains(t, lines[2], "+45 -12 (2 edited, 1 added)")

	// Reviewer line: └──, active spinner ⠸, role reviewer (r1), Claude 3.7 Sonnet
	assert.Contains(t, lines[3], "└── ⠸ reviewer (r1) · Claude 3.7 Sonnet")
	assert.NotContains(t, lines[3], "claude (claude-3-7-sonnet)")
	assert.Contains(t, lines[3], "5s")

	// Activity line under reviewer
	assert.Contains(t, lines[4], "Read wire.go")
	assert.Contains(t, lines[4], "3s")
	assert.Contains(t, lines[4], "↓600 ↑50 tok")
}

func TestBuildFrameSubtasksWithActivityAndVerbose(t *testing.T) {
	t.Parallel()
	v := SessionView{
		Name:    "verbose-view",
		Current: 1,
		Verbose: true,
		Steps: []StepView{
			{
				Number: 1,
				Title:  "Step 1",
				Status: StepPending,
				Subtasks: []SubtaskView{
					{
						Role:    "worker",
						Harness: "agy",
						Model:   "gemini-2.5-pro",
						Active:  true,
						Activities: []ActivityItem{
							{
								Label:     "Read main.go",
								Elapsed:   2 * time.Second,
								In:        500,
								Out:       20,
								HasTokens: true,
								Active:    false,
							},
							{
								Label:     "Edit main.go",
								Elapsed:   4 * time.Second,
								In:        1200,
								Out:       80,
								HasTokens: true,
								Active:    true,
							},
						},
					},
				},
			},
		},
	}

	lines := BuildFrame(v, "⠸", 120, 24)
	require.Len(t, lines, 5) // header + step + worker + 2 activities
	assert.Contains(t, lines[2], "worker")
	// Verbose shows both activities under worker
	assert.Contains(t, lines[3], "Read main.go")
	assert.Contains(t, lines[3], "2s")
	assert.Contains(t, lines[4], "Edit main.go")
}

func TestBuildFrameSubtasksColor(t *testing.T) {
	t.Parallel()
	stat := DiffStat{
		LinesAdded:    10,
		LinesDeleted:  2,
		FilesModified: 1,
	}
	v := SessionView{
		Name:              "color-test",
		SessionElapsed:    30 * time.Second,
		HasSessionElapsed: true,
		Current:           1,
		Steps: []StepView{
			{
				Number: 1,
				Title:  "Step 1",
				Status: StepPending,
				Subtasks: []SubtaskView{
					{
						Role:     "worker",
						Harness:  "agy",
						Model:    "gemini-2.5-pro",
						Status:   "approved",
						DiffStat: &stat,
						Active:   true,
						Activities: []ActivityItem{
							{
								Label:  "Read code",
								Active: true,
							},
						},
					},
				},
			},
		},
	}

	lines := BuildFrameStyled(v, "⠸", 140, 24, true)
	require.Len(t, lines, 4)
	// Header contains ANSI styling
	assert.Contains(t, lines[0], "\x1b[")
	// Worker contains green diff +10 and red -2 ANSI
	assert.Contains(t, lines[2], "\x1b[")
	// Activity line styled with light grey (color 246)
	assert.Contains(t, lines[3], "246")
	assert.Contains(t, lines[3], "Read code")
}

func TestBuildFrameModelColorByHarness(t *testing.T) {
	t.Parallel()

	v := SessionView{
		Name:    "color-models",
		Current: 1,
		Steps: []StepView{
			{
				Number: 1,
				Title:  "Multi-harness step",
				Status: StepPending,
				Subtasks: []SubtaskView{
					{
						Role:    "worker",
						Harness: "agy",
						Model:   "gemini-3.8-flash-high",
						Status:  "approved",
					},
					{
						Role:    "reviewer (r1)",
						Harness: "claude",
						Model:   "opus-5",
						Effort:  "medium",
						Status:  "approved",
					},
					{
						Role:    "reviewer (r2)",
						Harness: "opencode",
						Model:   "fireworks-ai/accounts/fireworks/models/kimi-k3",
						Effort:  "high",
						Status:  "pending",
						Active:  true,
					},
				},
			},
		},
	}

	// Plain output should not have harness names, only model names
	plain := BuildFrame(v, "⠸", 140, 24)
	require.Len(t, plain, 5)

	assert.Contains(t, plain[2], "worker · Gemini 3.8 Flash (High)")
	assert.NotContains(t, plain[2], "agy")

	assert.Contains(t, plain[3], "reviewer (r1) · Opus 5 (Medium)")
	assert.NotContains(t, plain[3], "claude")

	assert.Contains(t, plain[4], "reviewer (r2) · Kimi K3 (High)")
	assert.NotContains(t, plain[4], "opencode")

	// Styled output should color by harness
	styled := BuildFrameStyled(v, "⠸", 140, 24, true)
	require.Len(t, styled, 5)

	// AGY color: #7C3AED (RGB: 124, 58, 237)
	assert.Contains(t, styled[2], "Gemini 3.8 Flash (High)")
	assert.Contains(t, styled[2], "124;58;237")

	// Claude color: #CC785C (RGB: 204, 120, 92)
	assert.Contains(t, styled[3], "Opus 5 (Medium)")
	assert.Contains(t, styled[3], "204;120;92")

	// Opencode color: #10B981 (RGB: 16, 185, 129)
	assert.Contains(t, styled[4], "Kimi K3 (High)")
	assert.Contains(t, styled[4], "16;185;129")
}

func TestFormatPlanSummary(t *testing.T) {
	t.Parallel()

	steps := []Step{
		{Number: 1, Title: "first step"},
		{Number: 2, Title: "second step"},
	}

	t.Run("multi-step", func(t *testing.T) {
		t.Parallel()
		got := FormatPlanSummary("My Plan", steps)
		want := "My Plan\n  1. first step\n  2. second step\n"
		assert.Equal(t, want, got)
	})

	t.Run("empty title fallback", func(t *testing.T) {
		t.Parallel()
		got := FormatPlanSummary("", steps)
		want := "Plan\n  1. first step\n  2. second step\n"
		assert.Equal(t, want, got)
	})
}
