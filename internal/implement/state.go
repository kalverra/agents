package implement

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Phase is the orchestrator state-machine phase.
type Phase string

// Phases of an implement session.
const (
	PhaseWork        Phase = "work"
	PhaseReview      Phase = "review"
	PhaseFinalReview Phase = "final_review"
	PhaseFinalHuman  Phase = "final_human"
	PhaseDone        Phase = "done"
	PhaseEscalated   Phase = "escalated"
)

// StepStatus tracks a plan step through the pipeline.
type StepStatus string

// Step statuses.
const (
	StepPending  StepStatus = "pending"
	StepApproved StepStatus = "approved"
	StepRejected StepStatus = "rejected"
)

// StepUsage is accumulated token usage and provider-reported cost for one
// step, from either the worker or the reviewer side.
type StepUsage struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheRead    int64   `json:"cache_read,omitempty"`
	CostUSD      float64 `json:"cost_usd"`
	HasTokens    bool    `json:"has_tokens"`
	HasCost      bool    `json:"has_cost"`
}

// Add accumulates another usage report into s. Cost only accumulates when
// the addend actually reported one.
func (s *StepUsage) Add(o StepUsage) {
	s.InputTokens += o.InputTokens
	s.OutputTokens += o.OutputTokens
	s.CacheRead += o.CacheRead
	if o.InputTokens > 0 || o.OutputTokens > 0 {
		s.HasTokens = true
	}
	if o.HasCost {
		s.CostUSD += o.CostUSD
		s.HasCost = true
	}
}

// Plus returns the sum of two usage reports without mutating either.
func (s StepUsage) Plus(o StepUsage) StepUsage {
	out := s
	out.Add(o)
	return out
}

// ActivityRecord is one tool or file action executed during a role run.
type ActivityRecord struct {
	Label    string        `json:"label"`
	Duration time.Duration `json:"duration,omitempty"`
	In       int64         `json:"input_tokens,omitempty"`
	Out      int64         `json:"output_tokens,omitempty"`
}

// RoleRun tracks one execution of a worker or reviewer during a step.
type RoleRun struct {
	Role       string           `json:"role"` // "worker", "reviewer (r1)", "fix (r1)", etc.
	Round      int              `json:"round"`
	Harness    string           `json:"harness"`
	Model      string           `json:"model"`
	Effort     string           `json:"effort,omitempty"`
	StartedAt  *time.Time       `json:"started_at,omitempty"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
	Duration   time.Duration    `json:"duration,omitempty"`
	Usage      StepUsage        `json:"usage"`
	Status     string           `json:"status"` // "approved", "pending", "rejected"
	DiffStat   *DiffStat        `json:"diff_stat,omitempty"`
	Activities []ActivityRecord `json:"activities,omitempty"`
	SessionID  string           `json:"session_id,omitempty"`
}

// Step is one step parsed from plan.md, plus its runtime tracking state.
type Step struct {
	Number        int        `json:"number"`
	Title         string     `json:"title"`
	Body          string     `json:"body"`
	Status        StepStatus `json:"status"`
	WorkSessionID string     `json:"work_session_id,omitempty"`
	ReviewRounds  int        `json:"review_rounds"`
	// StartedAt is when work on this step began (set before the worker runs,
	// so it survives mid-step crashes). FinishedAt is set on approval.
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// WorkerUsage is the worker conversation's usage (replaced on fix-round
	// resumes: claude-style results report cumulative conversation usage).
	// ReviewerUsage adds across rounds (each round is a fresh conversation).
	WorkerUsage   *StepUsage `json:"worker_usage,omitempty"`
	ReviewerUsage *StepUsage `json:"reviewer_usage,omitempty"`
	// CommitHash is the git commit recorded when this step was approved.
	// Empty when the step produced nothing to commit.
	CommitHash string    `json:"commit_hash,omitempty"`
	Runs       []RoleRun `json:"runs,omitempty"`
	DiffStat   *DiffStat `json:"diff_stat,omitempty"`
	// Verified records that the plan's verification commands passed against
	// this step's current working tree. Cleared whenever the tree changes,
	// so a session resumed after a verification escalation re-checks instead
	// of handing an unverified diff to the reviewer.
	Verified bool `json:"verified,omitempty"`
	// Effort is an optional per-step reasoning-effort override parsed from
	// the plan's "**Effort**:" line ("low", "medium", or "high"). Empty when
	// the plan step didn't declare one.
	Effort string `json:"effort,omitempty"`
	// Worker is an optional per-step agent override parsed from the plan's
	// "**Worker**:" line (a catalog name). Empty when the plan step didn't
	// declare one, meaning use the mode default.
	Worker string `json:"worker,omitempty"`
	// Reviewer is an optional per-step reviewer override parsed from the
	// plan's "**Reviewer**:" line (a catalog name). Empty when the plan
	// step didn't declare one, meaning use the mode default.
	Reviewer string `json:"reviewer,omitempty"`
	// Escalations tracks questions asked by the worker via the ESCALATE: channel,
	// with user answers and timestamps.
	Escalations []Escalation `json:"escalations,omitempty"`
}

// Escalation records one question asked by the worker via the ESCALATE: channel,
// the answer provided by the user (or default), and when it was asked.
type Escalation struct {
	Question string     `json:"question"`
	Answer   string     `json:"answer"`
	AskedAt  *time.Time `json:"asked_at,omitempty"`
}

var nonAlphaNumRe = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// Slugify converts a string to a clean URL/path-friendly slug.
func Slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlphaNumRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// StepDir returns the directory name for this step: "<number>-<slug>".
func (s Step) StepDir() string {
	slug := Slugify(s.Title)
	if slug == "" {
		slug = "step"
	}
	return fmt.Sprintf("%d-%s", s.Number, slug)
}

// WorkFile returns this step's live worker summary path, inside its step
// directory: "<num>-<slug>/work.md". The worker (and, across fix rounds, the
// resumed worker) appends to this single file; RoundWorkFile snapshots it
// after each round for the durable per-round archive.
func (s Step) WorkFile() string {
	return filepath.Join(s.StepDir(), "work.md")
}

// RoundWorkFile returns the round-specific worker summary snapshot path
// inside StepDir: "<num>-<slug>/r<round>.work.md".
func (s Step) RoundWorkFile(round int) string {
	if round < 1 {
		round = 1
	}
	return filepath.Join(s.StepDir(), fmt.Sprintf("r%d.work.md", round))
}

// ReviewFile returns the round-specific reviewer output path inside StepDir:
// "<num>-<slug>/r<round>.review.md". Each review round is a fresh
// conversation, so there is no separate "live" file to append to.
func (s Step) ReviewFile(round int) string {
	if round < 1 {
		round = 1
	}
	return filepath.Join(s.StepDir(), fmt.Sprintf("r%d.review.md", round))
}

// DiffFile returns the round-specific unified diff path inside StepDir:
// "<num>-<slug>/r<round>.diff".
func (s Step) DiffFile(round int) string {
	if round < 1 {
		round = 1
	}
	return filepath.Join(s.StepDir(), fmt.Sprintf("r%d.diff", round))
}

// LogsDir is the session subdirectory holding raw agent stream logs (.jsonl)
// and their human-readable markdown digests (.md).
const LogsDir = "logs"

// Session artifact names for the final whole-diff review phases.
const (
	FinalReviewFile = "final-review.md"
	FinalWorkFile   = "final-work.md"
	FinalDiffFile   = "final.diff"
	// DiffDir holds the archived base/head trees opened in the diff viewer.
	DiffDir = "diff"
)

// Stream log names for the final whole-diff review roles.
const (
	FinalReviewerLog = "final-reviewer.jsonl"
	FinalWorkerLog   = "final-worker.jsonl"
)

// StreamLogFile is the raw stream-json log name for one role on this step and round (1-based).
func (s Step) StreamLogFile(role string, round ...int) string {
	r := 1
	if len(round) > 0 && round[0] > 0 {
		r = round[0]
	}
	if r == 1 {
		return fmt.Sprintf("step-%d-%s.jsonl", s.Number, role)
	}
	return fmt.Sprintf("step-%d-%s-r%d.jsonl", s.Number, role, r)
}

// StreamLogPath is the full path of one role's raw stream log inside a
// session directory.
func (s Step) StreamLogPath(sessionDir, role string, round ...int) string {
	return filepath.Join(sessionDir, LogsDir, s.StreamLogFile(role, round...))
}

// Verdict is the reviewer's judgement parsed from a review file.
type Verdict string

// Verdicts.
const (
	VerdictApprove        Verdict = verdictApprove
	VerdictRequestChanges Verdict = verdictRequestChanges
)

// SpecKind identifies the format of an imported spec source.
type SpecKind string

// Spec source kinds.
const (
	// SpecKindPlan is a flow-native plan.md adopted as-is.
	SpecKindPlan SpecKind = "plan"
	// SpecKindOpenSpec is an openspec/changes/<name>/ directory.
	SpecKindOpenSpec SpecKind = "openspec"
	// SpecKindSpecKit is a spec-kit specs/<name>/ directory
	// (spec.md + plan.md + tasks.md).
	SpecKindSpecKit SpecKind = "speckit"
)

// PlanSource records where the executed plan came from.
type PlanSource struct {
	Kind SpecKind `json:"kind"`
	// Path is the original source location (the plan file or spec dir).
	Path string `json:"path,omitempty"`
	// SnapshotDir is the session-local snapshot of the spec dir
	// (sessionDir/spec), empty for adopted plan files.
	SnapshotDir string `json:"snapshot_dir,omitempty"`
	// SpecHash is the sha256 of the snapshot (or plan file) at import.
	SpecHash string `json:"spec_hash,omitempty"`
}

// State is the persisted session state (state.json).
type State struct {
	SessionName string `json:"session_name"`
	Mode        string `json:"mode"`
	Task        string `json:"task"`
	Phase       Phase  `json:"phase"`
	Steps       []Step `json:"steps"`
	// PlanSource records where the executed plan came from.
	PlanSource PlanSource `json:"plan_source,omitzero"`
	// PlanHash is the sha256 of plan.md at acceptance. On resume a changed
	// hash means someone edited the plan after acceptance: the session
	// escalates rather than executing an unreviewed plan.
	PlanHash string `json:"plan_hash,omitempty"`
	// Verification holds the repo's own test/lint/build commands the
	// orchestrator runs after each step. Resolved from implement.toml with
	// any "## Verification" section in the spec overriding it. Zero value
	// means no gate.
	Verification Verification `json:"verification,omitzero"`
	// CurrentStep is the 1-based step number in flight (0 = none).
	CurrentStep int `json:"current_step"`
	// BaseCommit is the repo HEAD when the session started; the final
	// whole-diff review diffs base..HEAD.
	BaseCommit string     `json:"base_commit,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// Final review tracking: fix rounds after whole-diff REQUEST_CHANGES,
	// the final fix worker conversation, its commit, and usage.
	FinalReviewRounds  int        `json:"final_review_rounds"`
	FinalWorkSessionID string     `json:"final_work_session_id,omitempty"`
	FinalCommitHash    string     `json:"final_commit_hash,omitempty"`
	FinalWorkerUsage   *StepUsage `json:"final_worker_usage,omitempty"`
	FinalReviewerUsage *StepUsage `json:"final_reviewer_usage,omitempty"`
	// FinalVerified records that the plan's verification commands passed against
	// the whole-diff fixes. Cleared whenever final review fixes are requested or
	// after an escalation, so a resumed session re-checks rather than trusting
	// a stale pass.
	FinalVerified bool `json:"final_verified,omitempty"`
}

// AllStepsApproved reports whether every plan step is approved.
func (s *State) AllStepsApproved() bool {
	for _, step := range s.Steps {
		if step.Status != StepApproved {
			return false
		}
	}
	return len(s.Steps) > 0
}

// ResumeCommand formats the CLI command to resume a session given mode and session name.
func ResumeCommand(mode, session string) string {
	if strings.Contains(session, " ") {
		session = fmt.Sprintf("%q", session)
	}
	if strings.Contains(mode, " ") {
		mode = fmt.Sprintf("%q", mode)
	}
	if mode == "" {
		return fmt.Sprintf("agents implement --session %s --resume", session)
	}
	return fmt.Sprintf("agents implement --mode %s --session %s --resume", mode, session)
}

// ResumeCommand returns the CLI command to resume this session.
func (s *State) ResumeCommand() string {
	return ResumeCommand(s.Mode, s.SessionName)
}

const stateFileName = "state.json"

// SaveState writes state.json into the session dir.
func SaveState(sessionDir string, s *State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding state: %w", err)
	}
	return os.WriteFile(filepath.Join(sessionDir, stateFileName), data, 0o600)
}

// LoadState reads state.json from the session dir.
func LoadState(sessionDir string) (*State, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, stateFileName)) //nolint:gosec // session dir is harness-managed
	if err != nil {
		return nil, fmt.Errorf("reading state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}
	return &s, nil
}

var stepHeaderRe = regexp.MustCompile(`(?m)^## Step (\d+): (.+?)\s*$`)

// ParsePlan extracts steps from plan.md content. Steps must be numbered
// sequentially from 1. Body is everything after the header line up to the
// next step header.
func ParsePlan(md string) ([]Step, error) {
	matches := stepHeaderRe.FindAllStringSubmatch(md, -1)
	if len(matches) == 0 {
		return nil, errors.New("plan.md has no '## Step N: title' headers")
	}
	locs := stepHeaderRe.FindAllStringIndex(md, -1)

	steps := make([]Step, 0, len(matches))
	for i, m := range matches {
		num, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("step %q: bad number: %w", m[1], err)
		}
		if num != i+1 {
			return nil, fmt.Errorf("steps must be numbered sequentially from 1; got Step %d in position %d", num, i+1)
		}
		bodyEnd := len(md)
		if i+1 < len(locs) {
			bodyEnd = locs[i+1][0]
		}
		body := strings.TrimSpace(md[locs[i][1]:bodyEnd])
		step := Step{Number: num, Title: m[2], Body: body, Status: StepPending}
		if raw, ok := parseEffortLine(body); ok {
			step.Effort = strings.ToLower(strings.TrimSpace(raw))
		}
		if m := workerLineRe.FindStringSubmatch(body); m != nil {
			step.Worker = stripBackticks(m[1])
		}
		if m := reviewerLineRe.FindStringSubmatch(body); m != nil {
			step.Reviewer = stripBackticks(m[1])
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// stepDirectiveRe builds the tolerant regex for a "**<label>**: <value>"
// step-body line: case-insensitive label, optional leading whitespace,
// optional "-"/"*" bullet prefix, value captured in group 1.
func stepDirectiveRe(label string) *regexp.Regexp {
	return regexp.MustCompile(
		fmt.Sprintf(`(?mi)^\s*(?:[-*]\s+)?\*\*%s\*\*:[ \t]*(.*?)[ \t\r]*$`, regexp.QuoteMeta(label)),
	)
}

var (
	// effortLineRe matches an optional "**Effort**: <value>" line in a step
	// body, same tolerant style as the header regexes: case
	// insensitive label, optional leading whitespace, optional "-"/"*"
	// bullet prefix. Unlike those header-only regexes, the value lives on
	// the same line, so it's captured in group 1.
	effortLineRe   = stepDirectiveRe("effort")
	workerLineRe   = stepDirectiveRe("worker")
	reviewerLineRe = stepDirectiveRe("reviewer")
)

// parseEffortLine reports the raw value of a step body's "**Effort**:"
// line, if present. The value is returned as-is (not lowercased/trimmed);
// callers normalize as needed.
func parseEffortLine(body string) (value string, present bool) {
	m := effortLineRe.FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ValidateSteps checks step bodies for recognized directive values. The
// step contract is relaxed: **Files**: and **Acceptance Criteria**: are
// honored when present but not required — specs imported from openspec or
// spec-kit tasks.md carry plain prose bodies.
func ValidateSteps(steps []Step) error {
	var errs []string
	for _, s := range steps {
		if raw, ok := parseEffortLine(s.Body); ok {
			effort := strings.ToLower(strings.TrimSpace(raw))
			if effort != "low" && effort != "medium" && effort != "high" {
				errs = append(
					errs,
					fmt.Sprintf("step %d %q: invalid **Effort**: %q (want low|medium|high)", s.Number, s.Title, effort),
				)
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ValidateStepAgents checks each step's **Worker**/**Reviewer** selection
// against the mode's allowlist. Empty selections are always valid (mode
// default). Returns one error describing every bad selection.
func ValidateStepAgents(steps []Step, mode ModeConfig) error {
	workerAllowed := make([]string, 0, len(mode.WorkerChoices))
	for _, c := range mode.WorkerChoices {
		workerAllowed = append(workerAllowed, c.Name)
	}
	reviewerAllowed := make([]string, 0, len(mode.ReviewerChoices))
	for _, c := range mode.ReviewerChoices {
		reviewerAllowed = append(reviewerAllowed, c.Name)
	}

	var errs []string
	for _, s := range steps {
		if s.Worker != "" && !slices.Contains(workerAllowed, s.Worker) {
			errs = append(errs, fmt.Sprintf(
				"step %d %q: unknown **Worker**: %q (want one of: %s)",
				s.Number, s.Title, s.Worker, strings.Join(workerAllowed, ", "),
			))
		}
		if s.Reviewer != "" {
			if mode.Reviewer == nil {
				errs = append(errs, fmt.Sprintf(
					"step %d %q: cannot select **Reviewer**: %q (mode has no reviewer stage)",
					s.Number, s.Title, s.Reviewer,
				))
			} else if !slices.Contains(reviewerAllowed, s.Reviewer) {
				errs = append(errs, fmt.Sprintf(
					"step %d %q: unknown **Reviewer**: %q (want one of: %s)",
					s.Number, s.Title, s.Reviewer, strings.Join(reviewerAllowed, ", "),
				))
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ParseAndValidatePlan extracts steps from plan.md and validates that all
// steps satisfy the (relaxed) step contract.
func ParseAndValidatePlan(md string) ([]Step, error) {
	steps, err := ParsePlan(md)
	if err != nil {
		return nil, err
	}
	if err := ValidateSteps(steps); err != nil {
		return nil, err
	}
	return steps, nil
}

// VerifyCommand is one named verification command.
type VerifyCommand struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// Verification holds the repo's own verification commands, in the order
// written. Names are free-form ("build", "typecheck", "generated", ...);
// the orchestrator runs them in order and stops at the first failure.
type Verification struct {
	Cmds []VerifyCommand `json:"commands,omitempty"`
}

// decodeVerification reads the [verification] table, keeping the written
// order (execution order matters: build before test before lint). TOML tables
// are unordered, so the order comes from the keys the decoder saw.
func decodeVerification(md toml.MetaData, data string) (Verification, error) {
	var raw struct {
		Verification map[string]string `toml:"verification"`
	}
	if _, err := toml.Decode(data, &raw); err != nil {
		return Verification{}, fmt.Errorf("verification must be a table of name = \"command\": %w", err)
	}
	var cmds []VerifyCommand
	for _, key := range md.Keys() {
		if len(key) != 2 || key[0] != "verification" {
			continue
		}
		name := strings.TrimSpace(key[1])
		command := strings.TrimSpace(raw.Verification[key[1]])
		if name == "" || command == "" {
			continue
		}
		cmds = append(cmds, VerifyCommand{Name: strings.ToLower(name), Command: command})
	}
	return Verification{Cmds: cmds}, nil
}

// Empty reports whether no command at all was declared.
func (v Verification) Empty() bool {
	return len(v.Cmds) == 0
}

// Commands returns the declared commands in the order written.
func (v Verification) Commands() []VerifyCommand {
	return v.Cmds
}

var (
	// verificationHeaderRe locates the top-level "## Verification" heading,
	// same style as contextHeaderRe.
	verificationHeaderRe = regexp.MustCompile(`(?m)^## Verification\b`)
	// nextTopHeaderRe locates the next top-level "## " heading, used to find
	// where a section ends.
	nextTopHeaderRe = regexp.MustCompile(`(?m)^## `)
	// verifyLineRe matches a "<key>: <command>" line inside the Verification
	// section, same tolerant style as filesHeaderRe/criteriaHeaderRe/
	// effortLineRe: case-insensitive key, optional leading whitespace,
	// optional "-"/"*" bullet prefix. The command lives on the same line, so
	// it's captured in group 2 (key in group 1).
	verifyLineRe = regexp.MustCompile(`(?mi)^\s*(?:[-*]\s+)?([A-Za-z][A-Za-z0-9 _-]*):[ \t]*(.*?)[ \t\r]*$`)
)

// stripBackticks trims surrounding whitespace and, if the result is wrapped
// in exactly one leading and one trailing backtick, strips those too.
func stripBackticks(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// ParseVerification extracts the "## Verification" section from plan.md.
// Returns a zero Verification when the section is absent or declares
// nothing. Commands preserve written order; duplicate names keep the
// last value at the first occurrence's position.
func ParseVerification(md string) Verification {
	var v Verification
	loc := verificationHeaderRe.FindStringIndex(md)
	if loc == nil {
		return v
	}
	rest := md[loc[1]:]
	end := len(rest)
	if m := nextTopHeaderRe.FindStringIndex(rest); m != nil {
		end = m[0]
	}
	section := rest[:end]

	indices := make(map[string]int)
	for _, m := range verifyLineRe.FindAllStringSubmatch(section, -1) {
		cmd := stripBackticks(m[2])
		if cmd == "" || strings.HasPrefix(cmd, "<") {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(m[1]))
		if idx, exists := indices[name]; exists {
			// For a duplicate name, keep the last value at the first occurrence's position.
			v.Cmds[idx].Command = cmd
		} else {
			indices[name] = len(v.Cmds)
			v.Cmds = append(v.Cmds, VerifyCommand{Name: name, Command: cmd})
		}
	}
	return v
}

var planTitleRe = regexp.MustCompile(`(?m)^# Plan:\s*(.+?)\s*$`)

// ParsePlanTitle extracts the title from a plan.md header ('# Plan: <title>').
func ParsePlanTitle(md string) string {
	if m := planTitleRe.FindStringSubmatch(md); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// ParseVerdict scans a review file for the last verdict marker line.
func ParseVerdict(md string) (Verdict, error) {
	lines := strings.Split(md, "\n")
	for _, line := range slices.Backward(lines) {
		switch strings.TrimSpace(line) {
		case verdictApprove:
			return VerdictApprove, nil
		case verdictRequestChanges:
			return VerdictRequestChanges, nil
		}
	}
	return "", fmt.Errorf("no verdict line found (want %q or %q)", verdictApprove, verdictRequestChanges)
}

var commitLineRe = regexp.MustCompile(`(?m)^COMMIT:[ \t]*(.*?)[ \t]*$`)

// ParseCommitMessage extracts the worker's proposed commit message from a
// step work summary. The last COMMIT: line wins; the message must be
// non-empty after trimming.
func ParseCommitMessage(md string) (string, error) {
	matches := commitLineRe.FindAllStringSubmatch(md, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("no COMMIT: line found (want 'COMMIT: <conventional commit message>')")
	}
	msg := matches[len(matches)-1][1]
	if msg == "" {
		return "", fmt.Errorf("COMMIT: line is empty")
	}
	return msg, nil
}

var escalateLineRe = regexp.MustCompile(`(?m)^ESCALATE:[ \t]*(.*?)[ \t]*$`)

// ParseEscalations extracts escalation questions from a step work summary.
// Returns trimmed, deduplicated, non-empty questions in order of appearance.
// Returns nil if no questions are found.
func ParseEscalations(md string) []string {
	matches := escalateLineRe.FindAllStringSubmatch(md, -1)
	if len(matches) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, m := range matches {
		q := strings.TrimSpace(m[1])
		if q == "" {
			continue
		}
		if !seen[q] {
			seen[q] = true
			out = append(out, q)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SignCommitsCommand returns the git command to retroactively GPG-sign all session
// commits from baseCommit to HEAD.
func SignCommitsCommand(baseCommit string) string {
	if baseCommit == "" {
		return "git rebase --exec 'git commit --amend --no-edit -S' HEAD~1"
	}
	return fmt.Sprintf("git rebase --exec 'git commit --amend --no-edit -S' %s", baseCommit)
}
