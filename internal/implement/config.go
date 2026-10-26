// Package implement executes an existing plan or spec (openspec change,
// spec-kit specs/ folder, or flow-native plan.md) through a worker/reviewer
// agent pipeline across agent harnesses (agy, claude, opencode) via their
// headless stream-json modes. Session state lives in
// <repo>/.agents/local/<session>/.
package implement

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kalverra/agents/internal/paths"
)

// Harness names supported by the orchestrator.
const (
	HarnessAgy      = "agy"
	HarnessClaude   = "claude"
	HarnessOpenCode = "opencode"
)

// Roles in the implement pipeline.
const (
	RoleWorker   = "worker"
	RoleReviewer = "reviewer"
)

// ValidHarnesses lists every supported harness.
var ValidHarnesses = []string{HarnessAgy, HarnessClaude, HarnessOpenCode}

var agentNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// RoleSpec configures one role: which harness runs it, with which model.
type RoleSpec struct {
	Harness     string `yaml:"harness"`
	Model       string `yaml:"model"`
	Effort      string `yaml:"effort,omitempty"`
	Description string `yaml:"description,omitempty"`
	// Timeout bounds one headless agent run (Go duration string, e.g. "30m").
	// Consumed by harnesses with a run-timeout flag (agy --print-timeout);
	// others ignore it.
	Timeout string `yaml:"timeout,omitempty"`
}

// NamedSpec is one selectable agent: its catalog name plus resolved spec.
type NamedSpec struct {
	Name string
	Spec RoleSpec
}

// ModeConfig configures all roles for an implement mode. A nil Reviewer
// skips that stage (worker only).
type ModeConfig struct {
	WorkerName      string   `yaml:"worker"`
	WorkerOptions   []string `yaml:"worker_options,omitempty"`
	ReviewerName    string   `yaml:"reviewer,omitempty"`
	ReviewerOptions []string `yaml:"reviewer_options,omitempty"`

	// Resolved by Config.Validate from the top-level agents catalog.
	Worker          RoleSpec    `yaml:"-"`
	Reviewer        *RoleSpec   `yaml:"-"`
	WorkerChoices   []NamedSpec `yaml:"-"`
	ReviewerChoices []NamedSpec `yaml:"-"`
}

// Config is the parsed contents of implement.yaml.
type Config struct {
	// Agents defines the catalog of reusable agent specs.
	Agents map[string]RoleSpec `yaml:"agents"`
	// Modes maps mode name (simple/medium/hard/extreme/...) to role specs.
	Modes map[string]ModeConfig `yaml:"modes"`
	// Verification declares the repo's test/lint/build commands the
	// orchestrator runs itself after each step, in the order written. A
	// "## Verification" section in the imported spec overrides it.
	Verification Verification `yaml:"verification"`
	// ArchiveOnDone closes the loop with the source spec when the session
	// completes: archives an openspec change, marks a spec-kit spec done.
	// Defaults to true. Nil means "use the default".
	ArchiveOnDone *bool `yaml:"archive_on_done,omitempty"`
	// ReviewMaxRounds bounds worker fix rounds after reviewer rejection
	// before escalating to the user. Defaults to 3.
	ReviewMaxRounds int `yaml:"review_max_rounds"`
	// VerifyMaxRounds bounds worker fix attempts after a plan-declared
	// verification command fails, before escalating to the user. Defaults
	// to 2. Zero in implement.yaml means "use the default"; set it negative to
	// reject (validation) rather than disable.
	VerifyMaxRounds int `yaml:"verify_max_rounds"`
	// VerifyMaxTotalRounds bounds total verification fix attempts for
	// one step across all commands, so a worker cycling between two failing
	// commands cannot loop forever under the per-command budget. Defaults
	// to 6 (DefaultVerifyMaxTotalRounds). Zero in implement.yaml means "use the default";
	// set it negative to reject (validation) rather than disable.
	VerifyMaxTotalRounds int `yaml:"verify_max_total_rounds"`
	// VerifyTimeout bounds one verification command execution (Go duration string,
	// e.g. "15m", "45m"). Defaults to 15m (DefaultVerifyTimeout).
	VerifyTimeout string `yaml:"verify_timeout,omitempty"`
	// AskMaxRounds bounds worker escalation question rounds before escalating
	// to the user. Defaults to 3 (DefaultAskMaxRounds). Zero in implement.yaml means
	// "use the default"; set it negative to reject (validation) rather than disable.
	AskMaxRounds int `yaml:"ask_max_rounds"`
	// ContextCommand is a shell command run in the repo dir at session start
	// and resume. Its stdout is inlined into every worker prompt (e.g. the
	// current ticket). Empty means no extra context.
	ContextCommand string `yaml:"context_command,omitempty"`
	// RefuseDefaultBranch stops a session from starting or resuming while the
	// repo is on its default branch.
	RefuseDefaultBranch bool `yaml:"refuse_default_branch,omitempty"`

	path string
}

// DefaultReviewMaxRounds is used when implement.yaml omits review_max_rounds.
const DefaultReviewMaxRounds = 3

// DefaultAskMaxRounds is used when implement.yaml omits ask_max_rounds.
const DefaultAskMaxRounds = 3

// DefaultVerifyMaxRounds is used when implement.yaml omits verify_max_rounds.
// Lower than the review budget: a verification failure is a hard, machine-
// checked signal, so a worker that cannot fix it in two tries needs a human.
const DefaultVerifyMaxRounds = 2

// DefaultVerifyMaxTotalRounds bounds total verification fix attempts for
// one step across all commands, so a worker cycling between two failing
// commands cannot loop forever under the per-command budget.
const DefaultVerifyMaxTotalRounds = 6

// DefaultConfigPaths returns candidate implement.yaml locations, in precedence order:
// the current repo's .agents/implement.yaml, then implement.yaml in the user config dir
// (~/.config/agents by default).
func DefaultConfigPaths() []string {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, ".agents", "implement.yaml"))
	}
	return append(candidates, filepath.Join(paths.ConfigDir(), "implement.yaml"))
}

// LoadConfig reads the first existing implement.yaml from the given candidate paths.
// Returns an error wrapping os.ErrNotExist if none exist.
func LoadConfig(paths ...string) (*Config, error) {
	var chosen string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			chosen = p
			break
		}
	}
	if chosen == "" {
		return nil, fmt.Errorf("no implement.yaml found in %v: %w", paths, os.ErrNotExist)
	}

	data, err := os.ReadFile(chosen) //nolint:gosec // user-specified config path
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", chosen, err)
	}

	cfg := &Config{path: chosen}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", chosen, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", chosen, err)
	}
	return cfg, nil
}

// Validate checks structural constraints, resolves catalog references, and applies defaults.
func (c *Config) Validate() error {
	if len(c.Agents) == 0 {
		return errors.New("no agents defined in catalog (agents: required)")
	}
	if len(c.Modes) == 0 {
		return errors.New("no modes defined")
	}

	agentNames := c.agentNames()
	for _, name := range agentNames {
		if !agentNameRe.MatchString(name) {
			return fmt.Errorf("agent %q: invalid name (must match ^[A-Za-z0-9][A-Za-z0-9_-]*$)", name)
		}
		spec := c.Agents[name]
		if err := spec.validate(fmt.Sprintf("agent %q", name)); err != nil {
			return err
		}
	}

	modeNames := c.modeNames()
	for _, name := range modeNames {
		mode := c.Modes[name]
		if err := c.resolveMode(name, &mode, agentNames); err != nil {
			return err
		}
		c.Modes[name] = mode
	}

	if c.ReviewMaxRounds == 0 {
		c.ReviewMaxRounds = DefaultReviewMaxRounds
	}
	if c.VerifyMaxRounds == 0 {
		c.VerifyMaxRounds = DefaultVerifyMaxRounds
	}
	if c.VerifyMaxRounds < 0 {
		return fmt.Errorf("verify_max_rounds must be >= 0, got %d", c.VerifyMaxRounds)
	}
	if c.VerifyMaxTotalRounds == 0 {
		c.VerifyMaxTotalRounds = DefaultVerifyMaxTotalRounds
	}
	if c.VerifyMaxTotalRounds < 0 {
		return fmt.Errorf("verify_max_total_rounds must be >= 0, got %d", c.VerifyMaxTotalRounds)
	}
	if c.ReviewMaxRounds < 0 {
		return fmt.Errorf("review_max_rounds must be >= 0, got %d", c.ReviewMaxRounds)
	}
	if c.AskMaxRounds == 0 {
		c.AskMaxRounds = DefaultAskMaxRounds
	}
	if c.AskMaxRounds < 0 {
		return fmt.Errorf("ask_max_rounds must be >= 0, got %d", c.AskMaxRounds)
	}
	if c.VerifyTimeout != "" {
		d, err := time.ParseDuration(c.VerifyTimeout)
		if err != nil {
			return fmt.Errorf("invalid verify_timeout %q (want a Go duration, e.g. 15m): %w", c.VerifyTimeout, err)
		}
		if d <= 0 {
			return fmt.Errorf("verify_timeout must be positive, got %q", c.VerifyTimeout)
		}
	}
	return nil
}

func (c *Config) resolveMode(modeName string, m *ModeConfig, agentNames []string) error {
	if m.WorkerName == "" {
		return fmt.Errorf("mode %q: worker is required", modeName)
	}
	workerSpec, ok := c.Agents[m.WorkerName]
	if !ok {
		return fmt.Errorf("mode %q: unknown worker %q (want one of %v)", modeName, m.WorkerName, agentNames)
	}
	m.Worker = workerSpec

	if m.ReviewerName != "" {
		reviewerSpec, ok := c.Agents[m.ReviewerName]
		if !ok {
			return fmt.Errorf("mode %q: unknown reviewer %q (want one of %v)", modeName, m.ReviewerName, agentNames)
		}
		m.Reviewer = &reviewerSpec
	} else {
		if len(m.ReviewerOptions) > 0 {
			return fmt.Errorf("mode %q: reviewer_options specified without reviewer", modeName)
		}
		m.Reviewer = nil
		m.ReviewerChoices = nil
	}

	// Resolve WorkerChoices: preserving written order, prepend default if omitted, never duplicate.
	var workerNames []string
	if len(m.WorkerOptions) == 0 {
		workerNames = []string{m.WorkerName}
	} else {
		if !slices.Contains(m.WorkerOptions, m.WorkerName) {
			workerNames = append(workerNames, m.WorkerName)
		}
		workerNames = append(workerNames, m.WorkerOptions...)
	}

	seenWorker := make(map[string]bool, len(workerNames))
	m.WorkerChoices = make([]NamedSpec, 0, len(workerNames))
	for _, opt := range workerNames {
		if seenWorker[opt] {
			continue
		}
		seenWorker[opt] = true
		spec, ok := c.Agents[opt]
		if !ok {
			return fmt.Errorf("mode %q: unknown agent %q in worker_options (want one of %v)", modeName, opt, agentNames)
		}
		m.WorkerChoices = append(m.WorkerChoices, NamedSpec{Name: opt, Spec: spec})
	}

	// Resolve ReviewerChoices if reviewer is configured.
	if m.ReviewerName != "" {
		var reviewerNames []string
		if len(m.ReviewerOptions) == 0 {
			reviewerNames = []string{m.ReviewerName}
		} else {
			if !slices.Contains(m.ReviewerOptions, m.ReviewerName) {
				reviewerNames = append(reviewerNames, m.ReviewerName)
			}
			reviewerNames = append(reviewerNames, m.ReviewerOptions...)
		}

		seenReviewer := make(map[string]bool, len(reviewerNames))
		m.ReviewerChoices = make([]NamedSpec, 0, len(reviewerNames))
		for _, opt := range reviewerNames {
			if seenReviewer[opt] {
				continue
			}
			seenReviewer[opt] = true
			spec, ok := c.Agents[opt]
			if !ok {
				return fmt.Errorf(
					"mode %q: unknown agent %q in reviewer_options (want one of %v)",
					modeName,
					opt,
					agentNames,
				)
			}
			m.ReviewerChoices = append(m.ReviewerChoices, NamedSpec{Name: opt, Spec: spec})
		}
	}

	return nil
}

func (r RoleSpec) validate(where string) error {
	if !slices.Contains(ValidHarnesses, r.Harness) {
		return fmt.Errorf("%s: unknown harness %q (want one of %v)", where, r.Harness, ValidHarnesses)
	}
	if r.Model == "" {
		return fmt.Errorf("%s: model is required", where)
	}
	if r.Timeout != "" {
		if _, err := time.ParseDuration(r.Timeout); err != nil {
			return fmt.Errorf("%s: invalid timeout %q (want a Go duration, e.g. 30m): %v", where, r.Timeout, err)
		}
	}
	return nil
}

// ArchiveSpecsOnDone reports whether completed sessions should close the
// loop with their source spec (openspec archive / spec-kit status). Defaults
// to true.
func (c *Config) ArchiveSpecsOnDone() bool {
	return c == nil || c.ArchiveOnDone == nil || *c.ArchiveOnDone
}

// Mode returns the config for a named mode.
func (c *Config) Mode(name string) (ModeConfig, error) {
	mode, ok := c.Modes[name]
	if !ok {
		return ModeConfig{}, fmt.Errorf("mode %q not defined in implement config (defined: %v)", name, c.modeNames())
	}
	return mode, nil
}

// ModeNames returns sorted mode names.
func (c *Config) ModeNames() []string { return c.modeNames() }

func (c *Config) modeNames() []string {
	names := make([]string, 0, len(c.Modes))
	for n := range c.Modes {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// AgentNames returns sorted agent catalog names.
func (c *Config) AgentNames() []string { return c.agentNames() }

func (c *Config) agentNames() []string {
	names := make([]string, 0, len(c.Agents))
	for n := range c.Agents {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Path returns the config file path this was loaded from.
func (c *Config) Path() string { return c.path }
