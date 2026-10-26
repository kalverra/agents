package implement

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validConfigYAML = `
agents:
  fast:
    harness: agy
    model: gemini-3.8-flash-medium
    description: cheap and fast
  strong:
    harness: agy
    model: gemini-3.8-flash-high
    description: default worker
  opus:
    harness: claude
    model: opus-5
    effort: high
    description: complex reasoning
  review-fast:
    harness: opencode
    model: fireworks-ai/accounts/fireworks/models/glm-5p3-flash
  review-deep:
    harness: opencode
    model: fireworks-ai/accounts/fireworks/models/glm-5p3
    effort: high

modes:
  simple:
    worker: fast
  medium:
    worker: fast
    reviewer: review-fast
  hard:
    worker: strong
    worker_options: [fast, strong, opus]
    reviewer: review-fast
    reviewer_options: [review-fast, review-deep]
  extreme:
    worker: strong
    worker_options: [opus]
    reviewer: review-deep
review_max_rounds: 2
verification:
  build: go build ./...
  test: go test ./...
  lint: golangci-lint run ./... --fix
archive_on_done: false
`

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "implement.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadConfigValid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeConfig(t, dir, validConfigYAML)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, path, cfg.Path())
	assert.Equal(t, 2, cfg.ReviewMaxRounds)
	assert.Equal(t, 6, cfg.VerifyMaxTotalRounds)
	assert.Len(t, cfg.Agents, 5)
	assert.Equal(t, []string{"fast", "opus", "review-deep", "review-fast", "strong"}, cfg.AgentNames())

	// Verify catalog entry description
	assert.Equal(t, "cheap and fast", cfg.Agents["fast"].Description)

	// Mode "simple": worker only, choices = [fast]
	simple, err := cfg.Mode("simple")
	require.NoError(t, err)
	assert.Nil(t, simple.Reviewer)
	assert.Equal(t, "fast", simple.WorkerName)
	assert.Equal(t, HarnessAgy, simple.Worker.Harness)
	assert.Equal(t, "gemini-3.8-flash-medium", simple.Worker.Model)
	require.Len(t, simple.WorkerChoices, 1)
	assert.Equal(t, "fast", simple.WorkerChoices[0].Name)
	assert.Equal(t, simple.Worker, simple.WorkerChoices[0].Spec)
	assert.Empty(t, simple.ReviewerChoices)

	// Mode "hard": worker with options [fast, strong, opus] (contains default "strong")
	hard, err := cfg.Mode("hard")
	require.NoError(t, err)
	assert.Equal(t, "strong", hard.WorkerName)
	assert.Equal(t, "gemini-3.8-flash-high", hard.Worker.Model)
	require.Len(t, hard.WorkerChoices, 3)
	assert.Equal(t, "fast", hard.WorkerChoices[0].Name)
	assert.Equal(t, "strong", hard.WorkerChoices[1].Name)
	assert.Equal(t, "opus", hard.WorkerChoices[2].Name)

	require.NotNil(t, hard.Reviewer)
	assert.Equal(t, "review-fast", hard.ReviewerName)
	assert.Equal(t, "fireworks-ai/accounts/fireworks/models/glm-5p3-flash", hard.Reviewer.Model)
	require.Len(t, hard.ReviewerChoices, 2)
	assert.Equal(t, "review-fast", hard.ReviewerChoices[0].Name)
	assert.Equal(t, "review-deep", hard.ReviewerChoices[1].Name)

	// Mode "extreme": worker_options omits default "strong", so "strong" must be prepended
	extreme, err := cfg.Mode("extreme")
	require.NoError(t, err)
	assert.Equal(t, "strong", extreme.WorkerName)
	require.Len(t, extreme.WorkerChoices, 2)
	assert.Equal(t, "strong", extreme.WorkerChoices[0].Name)
	assert.Equal(t, "opus", extreme.WorkerChoices[1].Name)
	// reviewer_options omitted, so ReviewerChoices holds only default
	require.Len(t, extreme.ReviewerChoices, 1)
	assert.Equal(t, "review-deep", extreme.ReviewerChoices[0].Name)

	// Verification commands preserve written order.
	require.Len(t, cfg.Verification.Commands(), 3)
	assert.Equal(t, VerifyCommand{Name: "build", Command: "go build ./..."}, cfg.Verification.Commands()[0])
	assert.Equal(t, VerifyCommand{Name: "test", Command: "go test ./..."}, cfg.Verification.Commands()[1])
	assert.Equal(
		t,
		VerifyCommand{Name: "lint", Command: "golangci-lint run ./... --fix"},
		cfg.Verification.Commands()[2],
	)

	// archive_on_done: false parsed; helper reports it.
	require.NotNil(t, cfg.ArchiveOnDone)
	assert.False(t, cfg.ArchiveSpecsOnDone())
}

func TestLoadConfigDefaultsArchiveOnDone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agents:
  w:
    harness: opencode
    model: m
modes:
  simple:
    worker: w
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Nil(t, cfg.ArchiveOnDone)
	assert.True(t, cfg.ArchiveSpecsOnDone())
}

func TestLoadConfigNoVerification(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agents:
  w:
    harness: opencode
    model: m
modes:
  simple:
    worker: w
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.True(t, cfg.Verification.Empty())
}

func TestLoadConfigRoleTimeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agents:
  timed:
    harness: agy
    model: gemini-3.8-flash-medium
    timeout: 45m
modes:
  simple:
    worker: timed
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	simple, err := cfg.Mode("simple")
	require.NoError(t, err)
	assert.Equal(t, "45m", simple.Worker.Timeout)
}

func TestLoadConfigFirstExistingWins(t *testing.T) {
	t.Parallel()
	dir1, dir2 := t.TempDir(), t.TempDir()
	p1 := writeConfig(t, dir1, validConfigYAML)
	missing := filepath.Join(dir2, "implement.yaml")

	cfg, err := LoadConfig(p1, missing)
	require.NoError(t, err)
	assert.Equal(t, p1, cfg.Path())

	cfg2, err := LoadConfig(missing, p1)
	require.NoError(t, err)
	assert.Equal(t, p1, cfg2.Path())
}

func TestLoadConfigNoneExist(t *testing.T) {
	t.Parallel()
	_, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestLoadConfigDefaultsReviewRounds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agents:
  w:
    harness: opencode
    model: m
modes:
  simple:
    worker: w
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, DefaultReviewMaxRounds, cfg.ReviewMaxRounds)
	assert.Equal(t, DefaultVerifyMaxRounds, cfg.VerifyMaxRounds)
	assert.Equal(t, DefaultAskMaxRounds, cfg.AskMaxRounds)
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "no agents catalog",
			yaml: `
modes:
  simple:
    worker: fast
`,
			want: "no agents defined in catalog",
		},
		{
			name: "no modes",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
`,
			want: "no modes defined",
		},
		{
			name: "invalid catalog agent name",
			yaml: `
agents:
  _fast:
    harness: agy
    model: m
modes:
  simple:
    worker: _fast
`,
			want: `agent "_fast": invalid name`,
		},
		{
			name: "invalid harness in unused catalog entry",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
  bad:
    harness: cursor
    model: m
modes:
  simple:
    worker: fast
`,
			want: `unknown harness "cursor"`,
		},
		{
			name: "missing model in unused catalog entry",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
  no-model:
    harness: agy
modes:
  simple:
    worker: fast
`,
			want: `model is required`,
		},
		{
			name: "bad timeout in catalog entry",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
    timeout: soon
modes:
  simple:
    worker: fast
`,
			want: `invalid timeout "soon"`,
		},
		{
			name: "missing worker in mode",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple: {}
`,
			want: `worker is required`,
		},
		{
			name: "unknown worker agent",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple:
    worker: nope
`,
			want: `unknown worker "nope"`,
		},
		{
			name: "unknown reviewer agent",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple:
    worker: fast
    reviewer: nope
`,
			want: `unknown reviewer "nope"`,
		},
		{
			name: "unknown agent in worker_options",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple:
    worker: fast
    worker_options: [fast, nope]
`,
			want: `unknown agent "nope" in worker_options`,
		},
		{
			name: "unknown agent in reviewer_options",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple:
    worker: fast
    reviewer: fast
    reviewer_options: [fast, nope]
`,
			want: `unknown agent "nope" in reviewer_options`,
		},
		{
			name: "reviewer_options specified without reviewer",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple:
    worker: fast
    reviewer_options: [fast]
`,
			want: `reviewer_options specified without reviewer`,
		},
		{
			name: "negative verify_max_rounds",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple:
    worker: fast
verify_max_rounds: -1
`,
			want: "verify_max_rounds must be >= 0",
		},
		{
			name: "negative review_max_rounds",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple:
    worker: fast
review_max_rounds: -1
`,
			want: "review_max_rounds must be >= 0",
		},
		{
			name: "invalid verify_timeout format",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple:
    worker: fast
verify_timeout: soon
`,
			want: `invalid verify_timeout "soon"`,
		},
		{
			name: "negative verify_timeout",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple:
    worker: fast
verify_timeout: -5m
`,
			want: "verify_timeout must be positive",
		},
		{
			name: "negative verify_max_total_rounds",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple:
    worker: fast
verify_max_total_rounds: -1
`,
			want: "verify_max_total_rounds must be >= 0",
		},
		{
			name: "negative ask_max_rounds",
			yaml: `
agents:
  fast:
    harness: agy
    model: m
modes:
  simple:
    worker: fast
ask_max_rounds: -1
`,
			want: "ask_max_rounds must be >= 0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := writeConfig(t, dir, tc.yaml)
			_, err := LoadConfig(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestModeUnknown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeConfig(t, dir, validConfigYAML)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	_, err = cfg.Mode("nope")
	require.Error(t, err)
	assert.Len(t, cfg.ModeNames(), 4)
}

func TestLoadConfigVerifyTimeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agents:
  w:
    harness: opencode
    model: m
modes:
  simple:
    worker: w
verify_timeout: 45m
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "45m", cfg.VerifyTimeout)
}

func TestLoadConfigVerifyMaxTotalRounds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agents:
  w:
    harness: opencode
    model: m
modes:
  simple:
    worker: w
verify_max_total_rounds: 10
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, 10, cfg.VerifyMaxTotalRounds)
}

func TestLoadConfigAskMaxRounds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agents:
  w:
    harness: opencode
    model: m
modes:
  simple:
    worker: w
ask_max_rounds: 5
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, 5, cfg.AskMaxRounds)
}
