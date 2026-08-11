package llm

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"
)

// StepState indicates the current lifecycle state of a progress step.
type StepState int

const (
	// StepPending indicates the step has not started.
	StepPending StepState = iota
	// StepActive indicates the step is currently running.
	StepActive
	// StepSuccess indicates the step completed successfully.
	StepSuccess
	// StepFailed indicates the step failed with an error.
	StepFailed
)

// Step represents a single progress item in the execution pipeline.
type Step struct {
	Label   string
	Detail  string
	Err     error
	State   StepState
	StartAt time.Time
	EndAt   time.Time
	owner   *Progress
}

// Complete marks the step as successfully finished with optional details.
func (s *Step) Complete(detail string) {
	if s == nil {
		return
	}
	s.Detail = detail
	s.State = StepSuccess
	if s.owner != nil {
		if s.EndAt.IsZero() {
			s.EndAt = s.owner.now()
		}
		s.owner.renderStep(s)
	}
}

// Fail marks the step as failed with an error.
func (s *Step) Fail(err error) {
	if s == nil {
		return
	}
	s.Err = err
	s.State = StepFailed
	if s.owner != nil {
		if s.EndAt.IsZero() {
			s.EndAt = s.owner.now()
		}
		s.owner.renderStep(s)
	}
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Progress tracks and renders multi-step progress indicators.
type Progress struct {
	w             io.Writer
	target        string
	model         string
	steps         []*Step
	useColor      bool
	IsTTY         bool
	activePrinted bool
	linesPrinted  int
	frameIdx      int
	stopChan      chan struct{}
	mu            sync.Mutex
	startAt       time.Time
	Now           func() time.Time
}

// NewProgress initializes a progress tracker.
func NewProgress(w io.Writer, target, model string, useColor bool) *Progress {
	if w == nil {
		w = os.Stderr
	}
	isTTY := false
	if f, ok := w.(*os.File); ok {
		isTTY = isatty.IsTerminal(f.Fd())
	}
	p := &Progress{
		w:        w,
		target:   target,
		model:    model,
		useColor: useColor,
		IsTTY:    isTTY,
	}
	p.startAt = p.now()
	p.renderHeader()
	return p
}

func (p *Progress) now() time.Time {
	if p != nil && p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Progress) renderHeader() {
	p.mu.Lock()
	defer p.mu.Unlock()
	header := fmt.Sprintf("Generating %s with %s...", p.target, p.model)
	if p.IsTTY {
		dur := p.now().Sub(p.startAt).Round(time.Second)
		durSec := max(int(dur.Seconds()), 0)
		header = fmt.Sprintf("Generating %s with %s... (%ds)", p.target, p.model, durSec)
	}
	if p.useColor {
		header = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).Render(header)
	}
	_, _ = fmt.Fprintf(p.w, "  %s\n  │\n", header)
	p.linesPrinted = 2
}

func (p *Progress) ensureTickerLocked() {
	if !p.IsTTY || p.stopChan != nil {
		return
	}
	stopChan := make(chan struct{})
	p.stopChan = stopChan
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.Tick()
			case <-stopChan:
				return
			}
		}
	}()
}

// Stop stops the background ticker if active.
func (p *Progress) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopTickerLocked()
}

func (p *Progress) stopTickerLocked() {
	if p.stopChan != nil {
		close(p.stopChan)
		p.stopChan = nil
	}
}

// Tick manually advances frame animation and re-renders active steps for TTYs.
func (p *Progress) Tick() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.frameIdx++
	if p.IsTTY && p.activePrinted {
		p.reRenderTreeLocked()
	}
}

func (p *Progress) reRenderTreeLocked() {
	if p.linesPrinted > 0 {
		_, _ = fmt.Fprint(p.w, ansi.CursorUp(p.linesPrinted)+"\r")
	}

	header := fmt.Sprintf("Generating %s with %s...", p.target, p.model)
	if p.IsTTY {
		dur := p.now().Sub(p.startAt).Round(time.Second)
		durSec := max(int(dur.Seconds()), 0)
		header = fmt.Sprintf("Generating %s with %s... (%ds)", p.target, p.model, durSec)
	}
	if p.useColor {
		header = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).Render(header)
	}
	_, _ = fmt.Fprintf(p.w, "  %s\033[K\n  │\033[K\n", header)

	for _, step := range p.steps {
		p.renderStepLocked(step)
	}
	p.linesPrinted = 2 + len(p.steps)
}

// AddStep appends and starts a new progress step.
func (p *Progress) AddStep(label string) *Step {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.ensureTickerLocked()

	step := &Step{
		Label:   label,
		State:   StepActive,
		StartAt: p.now(),
		owner:   p,
	}

	if p.IsTTY {
		p.steps = append(p.steps, step)
		p.reRenderTreeLocked()
		p.activePrinted = true
		return step
	}

	p.steps = append(p.steps, step)
	p.renderStepLocked(step)
	return step
}

func (p *Progress) renderStep(step *Step) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.IsTTY {
		p.reRenderTreeLocked()
		if step.State == StepActive {
			p.activePrinted = true
		} else {
			p.activePrinted = false
		}
		return
	}
	p.renderStepLocked(step)
}

func (p *Progress) renderStepLocked(step *Step) {
	connector := "├──"
	idx := p.findStepIndex(step)
	if idx == len(p.steps)-1 {
		connector = "└──"
	}

	var icon string
	switch step.State {
	case StepSuccess:
		icon = "✓"
		if p.useColor {
			icon = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(icon)
		}
	case StepFailed:
		icon = "✗"
		if p.useColor {
			icon = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(icon)
		}
	case StepActive:
		icon = spinnerFrames[p.frameIdx%len(spinnerFrames)]
		if p.useColor {
			icon = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Render(icon)
		}
	default:
		icon = "○"
		if p.useColor {
			icon = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(icon)
		}
	}

	msg := step.Label
	if step.Detail != "" {
		msg += fmt.Sprintf(" (%s)", step.Detail)
	}
	if step.Err != nil {
		msg += fmt.Sprintf(" (%v)", step.Err)
	}

	end := step.EndAt
	if end.IsZero() {
		end = p.now()
	}
	dur := end.Sub(step.StartAt).Round(time.Second)
	durSec := max(int(dur.Seconds()), 0)
	msg += fmt.Sprintf(" (%ds)", durSec)

	clearEol := ""
	if p.IsTTY {
		clearEol = "\033[K"
	}
	_, _ = fmt.Fprintf(p.w, "  %s %s %s%s\n", connector, icon, msg, clearEol)
}

func (p *Progress) findStepIndex(step *Step) int {
	for i, s := range p.steps {
		if s == step {
			return i
		}
	}
	return -1
}

// Finish renders the final summary line for the progress tracker.
func (p *Progress) Finish(dur time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopTickerLocked()

	if p.IsTTY {
		p.reRenderTreeLocked()
		p.activePrinted = false
	}

	durStr := dur.Round(time.Second).String()
	if dur < time.Second {
		durStr = dur.Round(time.Millisecond).String()
	}

	footer := fmt.Sprintf("Finished %s with %s (%s)", p.target, p.model, durStr)
	if p.useColor {
		footer = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")).Render(footer)
	}
	_, _ = fmt.Fprintf(p.w, "  %s\n", footer)
}
