package implement

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// verdictMarker is the review verdict contract. ParseVerdict depends on it.
const (
	verdictApprove        = "VERDICT: APPROVE"
	verdictRequestChanges = "VERDICT: REQUEST_CHANGES"
)

//go:embed prompts/*.tmpl.md
var defaultPromptsFS embed.FS

var defaultTemplates = template.Must(
	template.New("flow").Funcs(templateFuncs()).ParseFS(defaultPromptsFS, "prompts/*.tmpl.md"),
)

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"trim": strings.TrimSpace,
	}
}

// WorkerPromptData holds data for worker prompt rendering.
type WorkerPromptData struct {
	SessionDir    string
	Step          Step
	PrevSummaries []string
	WorkPath      string
	Context       string
	Verification  Verification
}

// VerifyFixPromptData holds data for the verification-failure fix prompt.
type VerifyFixPromptData struct {
	Step        Step
	WorkPath    string
	Name        string
	Command     string
	Output      string
	Attempt     int
	MaxAttempts int
}

// EscalationAnswerPromptData holds data for the escalation answer resume prompt.
type EscalationAnswerPromptData struct {
	Step        Step
	WorkPath    string
	Escalations []Escalation
}

// ReviewerPromptData holds data for reviewer prompt rendering.
type ReviewerPromptData struct {
	SessionDir string
	Step       Step
	WorkPath   string
	DiffPath   string
	ReviewPath string
}

// FixPromptData holds data for fix prompt rendering.
type FixPromptData struct {
	SessionDir string
	Step       Step
	WorkPath   string
	ReviewPath string
}

// FinalReviewerPromptData holds data for the final whole-diff review prompt.
type FinalReviewerPromptData struct {
	SessionDir string
	Task       string
	BaseCommit string
}

// FinalFixPromptData holds data for the final-review fix prompt rendering.
type FinalFixPromptData struct {
	SessionDir   string
	ReviewRounds int
}

// RenderPromptWithOverride loads and renders a prompt template by name,
// checking overrideDir first, then falling back to embedded defaults.
func RenderPromptWithOverride(name, overrideDir string, data any) (string, error) {
	tmplName := fmt.Sprintf("%s.tmpl.md", name)
	if overrideDir != "" {
		for _, candidate := range []string{tmplName, fmt.Sprintf("%s.md", name)} {
			overridePath := filepath.Join(overrideDir, candidate)
			if content, err := os.ReadFile(overridePath); err == nil { //nolint:gosec
				t, parseErr := template.New(candidate).Funcs(templateFuncs()).Parse(string(content))
				if parseErr != nil {
					return "", fmt.Errorf("parsing override template %s: %w", overridePath, parseErr)
				}
				var buf bytes.Buffer
				if err := t.Execute(&buf, data); err != nil {
					return "", fmt.Errorf("executing override template %s: %w", overridePath, err)
				}
				return strings.TrimSpace(buf.String()), nil
			} else if !os.IsNotExist(err) {
				return "", fmt.Errorf("reading override template %s: %w", overridePath, err)
			}
		}
	}

	t := defaultTemplates.Lookup(tmplName)
	if t == nil {
		return "", fmt.Errorf("template %q not found in embedded defaults", tmplName)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing template %s: %w", tmplName, err)
	}
	return strings.TrimSpace(buf.String()), nil
}

// RenderPrompt renders an embedded template by name.
func RenderPrompt(name string, data any) string {
	res, err := RenderPromptWithOverride(name, "", data)
	if err != nil {
		panic(fmt.Sprintf("rendering prompt %s: %v", name, err))
	}
	return res
}

// WorkerPrompt instructs the worker to implement one plan step. sessionContext
// is the output of implement.toml's context_command (empty when unset); handing
// it to the worker saves it from fetching the same context itself.
// verify carries the plan's declared verification commands: when they are
// present the orchestrator runs them itself, so the worker is told to iterate
// with targeted filters instead of re-running the full suite into its context.
func WorkerPrompt(
	sessionDir string,
	step Step,
	prevSummaries []string,
	sessionContext string,
	verify Verification,
) string {
	return RenderPrompt("worker", WorkerPromptData{
		SessionDir:    sessionDir,
		Step:          step,
		PrevSummaries: prevSummaries,
		WorkPath:      filepath.Join(sessionDir, step.WorkFile()),
		Context:       strings.TrimSpace(sessionContext),
		Verification:  verify,
	})
}

// VerifyFixPrompt instructs the worker (resumed session) to fix a failing
// verification command.
func VerifyFixPrompt(
	sessionDir string,
	step Step,
	cmd VerifyCommand,
	output string,
	attempt, maxAttempts int,
) string {
	return RenderPrompt("verify_fix", VerifyFixPromptData{
		Step:        step,
		WorkPath:    filepath.Join(sessionDir, step.WorkFile()),
		Name:        cmd.Name,
		Command:     cmd.Command,
		Output:      strings.TrimSpace(output),
		Attempt:     attempt,
		MaxAttempts: maxAttempts,
	})
}

// EscalationAnswerPrompt instructs the worker (resumed session) to continue
// implementing the step with the provided answers to its escalation questions.
func EscalationAnswerPrompt(
	sessionDir string,
	step Step,
	escalations []Escalation,
) string {
	return RenderPrompt("escalation_answer", EscalationAnswerPromptData{
		Step:        step,
		WorkPath:    filepath.Join(sessionDir, step.WorkFile()),
		Escalations: escalations,
	})
}

// ReviewerPrompt instructs the reviewer to review one step's diff, for the
// given review round.
func ReviewerPrompt(sessionDir string, step Step, round int) string {
	return RenderPrompt("reviewer", ReviewerPromptData{
		SessionDir: sessionDir,
		Step:       step,
		WorkPath:   filepath.Join(sessionDir, step.WorkFile()),
		DiffPath:   filepath.Join(sessionDir, step.DiffFile(round)),
		ReviewPath: filepath.Join(sessionDir, step.ReviewFile(round)),
	})
}

// FixPrompt instructs the worker (resumed session) to apply the fixes from
// the given review round.
func FixPrompt(sessionDir string, step Step, round int) string {
	return RenderPrompt("fix", FixPromptData{
		SessionDir: sessionDir,
		Step:       step,
		WorkPath:   filepath.Join(sessionDir, step.WorkFile()),
		ReviewPath: filepath.Join(sessionDir, step.ReviewFile(round)),
	})
}

// FinalReviewerPrompt instructs the reviewer to judge the whole session diff.
func FinalReviewerPrompt(sessionDir, task, baseCommit string) string {
	return RenderPrompt("final_reviewer", FinalReviewerPromptData{
		SessionDir: sessionDir,
		Task:       strings.TrimSpace(task),
		BaseCommit: baseCommit,
	})
}

// FinalFixPrompt instructs the worker to apply final whole-diff review fixes.
func FinalFixPrompt(sessionDir string, reviewRounds int) string {
	return RenderPrompt("final_fix", FinalFixPromptData{
		SessionDir:   sessionDir,
		ReviewRounds: reviewRounds,
	})
}
