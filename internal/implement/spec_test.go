package implement

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSpecDir scaffolds a spec-kit style spec dir (spec.md + plan.md +
// tasks.md) and returns its path.
func writeSpecDir(t *testing.T, tasksMD string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spec.md"), []byte(
		"# Add feature X\n\n## Requirements\n\n- The feature must work.\n",
	), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plan.md"), []byte(
		"# Plan\n\nSome implementation notes.\n",
	), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(tasksMD), 0o600))
	return dir
}

func TestDetectSpecKind(t *testing.T) {
	t.Parallel()

	openspecDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(openspecDir, "proposal.md"), []byte("# p"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(openspecDir, "tasks.md"), []byte("## 1. A\n\n- [ ] 1.1 a\n"), 0o600))
	kind, err := DetectSpecKind(openspecDir)
	require.NoError(t, err)
	assert.Equal(t, SpecKindOpenSpec, kind)

	// design.md also marks openspec (proposal is optional in the wild).
	designDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(designDir, "design.md"), []byte("# d"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(designDir, "tasks.md"), []byte("## 1. A\n\n- [ ] 1.1 a\n"), 0o600))
	kind, err = DetectSpecKind(designDir)
	require.NoError(t, err)
	assert.Equal(t, SpecKindOpenSpec, kind)

	speckitDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(speckitDir, "spec.md"), []byte("# s"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(speckitDir, "tasks.md"), []byte("## 1. A\n\n- [ ] 1.1 a\n"), 0o600))
	kind, err = DetectSpecKind(speckitDir)
	require.NoError(t, err)
	assert.Equal(t, SpecKindSpecKit, kind)

	noTasks := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(noTasks, "spec.md"), []byte("# s"), 0o600))
	_, err = DetectSpecKind(noTasks)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tasks.md")

	noMarker := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(noMarker, "tasks.md"), []byte("## 1. A\n\n- [ ] 1.1 a\n"), 0o600))
	_, err = DetectSpecKind(noMarker)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot determine spec format")
}

func TestResolveSpecSourceFlags(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()

	// Both flags is an error.
	_, err := ResolveSpecSource(repo, "a.md", "b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")

	// --plan must exist and be a file.
	_, err = ResolveSpecSource(repo, filepath.Join(repo, "missing.md"), "")
	require.Error(t, err)

	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	require.NoError(t, os.WriteFile(planPath, []byte("# Plan: x\n\n## Step 1: a\nbody\n"), 0o600))
	src, err := ResolveSpecSource(repo, planPath, "")
	require.NoError(t, err)
	assert.Equal(t, SpecKindPlan, src.Kind)
	assert.Equal(t, planPath, src.Path)

	specDir := writeSpecDir(t, "## 1. A\n\n- [ ] 1.1 a\n")
	src, err = ResolveSpecSource(repo, "", specDir)
	require.NoError(t, err)
	assert.Equal(t, SpecKindSpecKit, src.Kind)
}

func TestResolveSpecSourceAutoDetect(t *testing.T) {
	t.Parallel()

	t.Run("none found", func(t *testing.T) {
		t.Parallel()
		repo := t.TempDir()
		_, err := ResolveSpecSource(repo, "", "")
		require.ErrorIs(t, err, ErrNoSpec)
		assert.Contains(t, err.Error(), "--spec")
	})

	t.Run("single openspec change picked", func(t *testing.T) {
		t.Parallel()
		repo := t.TempDir()
		change := filepath.Join(repo, "openspec", "changes", "add-broker")
		require.NoError(t, os.MkdirAll(change, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(change, "proposal.md"), []byte("# p"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(change, "tasks.md"), []byte("## 1. A\n\n- [ ] 1.1 a\n"), 0o600))

		src, err := ResolveSpecSource(repo, "", "")
		require.NoError(t, err)
		assert.Equal(t, SpecKindOpenSpec, src.Kind)
		assert.Equal(t, change, src.Path)
	})

	t.Run("archive excluded", func(t *testing.T) {
		t.Parallel()
		repo := t.TempDir()
		archived := filepath.Join(repo, "openspec", "changes", "archive", "old-change")
		require.NoError(t, os.MkdirAll(archived, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(archived, "tasks.md"), []byte("## 1. A\n\n- [ ] 1.1 a\n"), 0o600))

		_, err := ResolveSpecSource(repo, "", "")
		require.ErrorIs(t, err, ErrNoSpec)
	})

	t.Run("spec-kit specs dir picked", func(t *testing.T) {
		t.Parallel()
		repo := t.TempDir()
		spec := filepath.Join(repo, "specs", "007-job-broker")
		require.NoError(t, os.MkdirAll(spec, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(spec, "spec.md"), []byte("# s"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(spec, "tasks.md"), []byte("## 1. A\n\n- [ ] 1.1 a\n"), 0o600))

		src, err := ResolveSpecSource(repo, "", "")
		require.NoError(t, err)
		assert.Equal(t, SpecKindSpecKit, src.Kind)
		assert.Equal(t, spec, src.Path)
	})

	t.Run("multiple candidates error lists them", func(t *testing.T) {
		t.Parallel()
		repo := t.TempDir()
		for _, name := range []string{"one", "two"} {
			change := filepath.Join(repo, "openspec", "changes", name)
			require.NoError(t, os.MkdirAll(change, 0o750))
			require.NoError(
				t,
				os.WriteFile(filepath.Join(change, "tasks.md"), []byte("## 1. A\n\n- [ ] 1.1 a\n"), 0o600),
			)
			require.NoError(t, os.WriteFile(filepath.Join(change, "proposal.md"), []byte("# p"), 0o600))
		}

		_, err := ResolveSpecSource(repo, "", "")
		require.ErrorIs(t, err, ErrNoSpec)
		assert.Contains(t, err.Error(), "multiple in-flight specs")
		assert.Contains(t, err.Error(), "one")
		assert.Contains(t, err.Error(), "two")
	})
}

func TestParseTasksMD(t *testing.T) {
	t.Parallel()

	t.Run("groups, numbering, details, done items", func(t *testing.T) {
		t.Parallel()
		const md = `# Tasks

## 1. Setup

- [x] 1.1 Install deps
- [ ] 1.2 Create schema
- [ ] 1.3 Write migration script
  - Use the existing migrator
  - Reversible only

## 2. API

- [ ] 2.1 Expose endpoint
`
		steps, skipped, err := ParseTasksMD(md)
		require.NoError(t, err)
		require.Len(t, steps, 3)

		assert.Equal(t, 1, steps[0].Number)
		assert.Equal(t, "Create schema", steps[0].Title)
		assert.Equal(t, "Task group: Setup", steps[0].Body)

		assert.Equal(t, 2, steps[1].Number)
		assert.Equal(t, "Write migration script", steps[1].Title)
		assert.Contains(t, steps[1].Body, "Use the existing migrator")
		assert.Contains(t, steps[1].Body, "Reversible only")

		assert.Equal(t, 3, steps[2].Number)
		assert.Equal(t, "Expose endpoint", steps[2].Title)
		assert.Contains(t, steps[2].Body, "API")

		assert.Equal(t, []string{"Install deps"}, skipped)
		for _, s := range steps {
			assert.Equal(t, StepPending, s.Status)
		}
	})

	t.Run("no items", func(t *testing.T) {
		t.Parallel()
		_, _, err := ParseTasksMD("# Tasks\n\nnothing here\n")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no '- [ ] task' checkbox items found")
	})

	t.Run("all checked off", func(t *testing.T) {
		t.Parallel()
		_, skipped, err := ParseTasksMD("## 1. A\n\n- [x] 1.1 done\n- [X] 1.2 also done\n")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already checked off")
		require.Len(t, skipped, 2)
	})
}

func TestImportSpecKitDir(t *testing.T) {
	t.Parallel()
	session := t.TempDir()
	specDir := writeSpecDir(t, "## 1. Setup\n\n- [ ] 1.1 Create schema\n\n## 2. API\n\n- [ ] 2.1 Expose endpoint\n")

	imported, err := ImportSpec(session, SpecSource{Kind: SpecKindSpecKit, Path: specDir})
	require.NoError(t, err)

	// Snapshot created and hashed.
	assert.Equal(t, SpecKindSpecKit, imported.Source.Kind)
	assert.NotEmpty(t, imported.Source.SpecHash)
	assert.DirExists(t, imported.Source.SnapshotDir)
	assert.FileExists(t, filepath.Join(imported.Source.SnapshotDir, "tasks.md"))

	// Task title from the spec's first H1.
	assert.Equal(t, "Add feature X", imported.Task)

	// Session plan.md written and parseable.
	require.NoError(t, os.WriteFile(filepath.Join(session, "plan.md"), []byte(imported.PlanMD), 0o600))
	steps, err := ParseAndValidatePlan(imported.PlanMD)
	require.NoError(t, err)
	require.Len(t, steps, 2)
	assert.Equal(t, "Create schema", steps[0].Title)
	assert.Equal(t, "Expose endpoint", steps[1].Title)
	assert.Contains(t, imported.PlanMD, "### spec.md")
	assert.Contains(t, imported.PlanMD, "### plan.md")
}

func TestImportOpenSpecDirWithVerificationAndDeltas(t *testing.T) {
	t.Parallel()
	session := t.TempDir()
	change := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(change, "proposal.md"), []byte(
		"# Dedicated job broker\n\n## Why\n\nPostgres is melting.\n\n## Verification\n\n- test: go test ./...\n",
	), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(change, "tasks.md"), []byte(
		"## 1. Broker\n\n- [ ] 1.1 Stand up NATS\n",
	), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(change, "specs"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(change, "specs", "queue.md"), []byte("# queue spec delta\n"), 0o600))

	imported, err := ImportSpec(session, SpecSource{Kind: SpecKindOpenSpec, Path: change})
	require.NoError(t, err)

	assert.Equal(t, SpecKindOpenSpec, imported.Source.Kind)
	assert.Equal(t, "Dedicated job broker", imported.Task)
	assert.Contains(t, imported.PlanMD, "### proposal.md")
	assert.Contains(t, imported.PlanMD, "### specs/queue.md")

	// The spec's own ## Verification section carries into the compiled plan.
	assert.Contains(t, imported.PlanMD, "## Verification")
	v := ParseVerification(imported.PlanMD)
	require.Len(t, v.Commands(), 1)
	assert.Equal(t, VerifyCommand{Name: "test", Command: "go test ./..."}, v.Commands()[0])
}

func TestImportPlanFile(t *testing.T) {
	t.Parallel()
	session := t.TempDir()
	planPath := filepath.Join(t.TempDir(), "my-plan.md")
	require.NoError(t, os.WriteFile(planPath, []byte("# Plan: thing\n\n## Step 1: first\ndo A\n"), 0o600))

	imported, err := ImportSpec(session, SpecSource{Kind: SpecKindPlan, Path: planPath})
	require.NoError(t, err)
	assert.Equal(t, SpecKindPlan, imported.Source.Kind)
	assert.Equal(t, "thing", imported.Task)
	assert.NoDirExists(t, filepath.Join(session, "spec"))
	assert.Equal(t, "# Plan: thing\n\n## Step 1: first\ndo A\n", imported.PlanMD)
}

func TestReconcileSteps(t *testing.T) {
	t.Parallel()
	old := []Step{
		{Number: 1, Title: "first", Body: "do A", Status: StepApproved, WorkSessionID: "w1"},
		{Number: 2, Title: "second", Body: "do B", Status: StepApproved},
	}

	t.Run("unchanged steps keep runtime state", func(t *testing.T) {
		t.Parallel()
		got := ReconcileSteps(old, []Step{
			{Number: 1, Title: "first", Body: "do A"},
			{Number: 2, Title: "second", Body: "do B"},
		})
		require.Len(t, got, 2)
		assert.Equal(t, StepApproved, got[0].Status)
		assert.Equal(t, "w1", got[0].WorkSessionID)
		assert.Equal(t, StepApproved, got[1].Status)
	})

	t.Run("changed body resets, new steps pending, removed dropped", func(t *testing.T) {
		t.Parallel()
		got := ReconcileSteps(old, []Step{
			{Number: 1, Title: "first", Body: "do A differently"},
			{Number: 2, Title: "second", Body: "do B"},
			{Number: 3, Title: "third", Body: "do C"},
		})
		require.Len(t, got, 3)
		assert.Equal(t, StepPending, got[0].Status)
		assert.Empty(t, got[0].WorkSessionID)
		assert.Equal(t, StepApproved, got[1].Status)
		assert.Equal(t, StepPending, got[2].Status)
	})

	t.Run("changed title resets", func(t *testing.T) {
		t.Parallel()
		got := ReconcileSteps(old, []Step{
			{Number: 1, Title: "renamed", Body: "do A"},
		})
		require.Len(t, got, 1)
		assert.Equal(t, StepPending, got[0].Status)
	})
}

func TestSetFrontmatterStatus(t *testing.T) {
	t.Parallel()

	t.Run("frontmatter status rewritten", func(t *testing.T) {
		t.Parallel()
		out, changed := setFrontmatterStatus("---\ntitle: x\nstatus: draft\n---\n\nbody\n", "done")
		assert.True(t, changed)
		assert.Contains(t, out, "status: done")
		assert.NotContains(t, out, "status: draft")
		assert.Contains(t, out, "body")
	})

	t.Run("no frontmatter status left alone", func(t *testing.T) {
		t.Parallel()
		const md = "---\ntitle: x\n---\n\nbody\n"
		out, changed := setFrontmatterStatus(md, "done")
		assert.False(t, changed)
		assert.Equal(t, md, out)
	})
}

func TestPlanDriftEscalatesAndReaccepts(t *testing.T) {
	t.Parallel()

	writeSessionPlan := func(t *testing.T, o *Orchestrator, plan string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(o.SessionDir(), "plan.md"), []byte(plan), 0o600))
	}

	editedPlan := "# Plan: thing\n\n## Context\n\nchanged\n\n## Step 1: first\ndo A differently\n\n## Step 2: second\ndo B\n"

	t.Run("drift escalates without accept", func(t *testing.T) {
		t.Parallel()
		mock := &mockRunner{skipWork: map[int]bool{1: true}}
		o, progress := newTestOrchestrator(t, "full", "s-drift-esc", mock)

		require.ErrorIs(t, runWithPlan(t, o, twoStepPlan), ErrWorkSummaryMissing)
		writeSessionPlan(t, o, editedPlan)

		err := o.Resume(context.Background(), ResumeOpts{})
		require.ErrorIs(t, err, ErrPlanDrift)
		assert.Contains(t, progress.String(), "--accept-plan-changes")

		state, sErr := LoadState(o.SessionDir())
		require.NoError(t, sErr)
		assert.Equal(t, PhaseEscalated, state.Phase)
		assert.Empty(t, mock.callsFor("worker", 2))
	})

	t.Run("accept re-gates and reconciles", func(t *testing.T) {
		t.Parallel()
		mock := &mockRunner{skipWork: map[int]bool{1: true}}
		o, progress := newTestOrchestrator(t, "full", "s-drift-acc", mock)

		require.ErrorIs(t, runWithPlan(t, o, twoStepPlan), ErrWorkSummaryMissing)
		writeSessionPlan(t, o, editedPlan)
		mock.skipWork = nil

		require.NoError(t, o.Resume(context.Background(), ResumeOpts{AcceptPlanChanges: true}))

		// The changed step 1 re-ran (initial failed run + reconciled rework);
		// unchanged step 2 ran once.
		assert.Len(t, mock.callsFor("worker", 1), 2)
		assert.Len(t, mock.callsFor("worker", 2), 1)
		assert.Contains(t, progress.String(), "changed plan re-accepted")

		state, sErr := LoadState(o.SessionDir())
		require.NoError(t, sErr)
		assert.Equal(t, PhaseDone, state.Phase)
		require.Len(t, state.Steps, 2)
		// The reconciled step 1 carries the edited body.
		assert.Contains(t, state.Steps[0].Body, "do A differently")
	})

	t.Run("missing plan.md after acceptance escalates", func(t *testing.T) {
		t.Parallel()
		mock := &mockRunner{skipWork: map[int]bool{1: true}}
		o, _ := newTestOrchestrator(t, "full", "s-drift-missing", mock)

		require.ErrorIs(t, runWithPlan(t, o, twoStepPlan), ErrWorkSummaryMissing)
		require.NoError(t, os.Remove(filepath.Join(o.SessionDir(), "plan.md")))

		err := o.Resume(context.Background(), ResumeOpts{})
		require.ErrorIs(t, err, ErrPlanDrift)
		assert.Contains(t, err.Error(), "missing")
	})

	t.Run("session without recorded hash skips drift check", func(t *testing.T) {
		t.Parallel()
		mock := &mockRunner{}
		o, _ := newTestOrchestrator(t, "full", "s-drift-legacy", mock)

		// Pre-upgrade seeded state: no PlanHash, plan.md present.
		require.NoError(t, o.EnsureSessionDir())
		writeSessionPlan(t, o, twoStepPlan)
		seed := &State{
			SessionName: "s-drift-legacy",
			Mode:        "full",
			Task:        "task",
			Phase:       PhaseWork,
			CurrentStep: 2,
			BaseCommit:  "base-sha",
			Steps: []Step{
				{Number: 1, Title: "first", Body: "do A", Status: StepApproved},
				{Number: 2, Title: "second", Body: "do B", Status: StepPending},
			},
		}
		require.NoError(t, SaveState(o.SessionDir(), seed))

		require.NoError(t, o.Resume(context.Background(), ResumeOpts{}))
		assert.Len(t, mock.callsFor("worker", 2), 1)
	})
}

func TestRunUsesConfigVerificationFallback(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-verify-cfg-fallback", mock)
	o.cfg = testConfig()
	o.cfg.Verification = Verification{Cmds: []VerifyCommand{
		{Name: "test", Command: "echo ok"},
	}}

	// twoStepPlan declares no ## Verification: the config default applies.
	require.NoError(t, runWithPlan(t, o, twoStepPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	require.Len(t, state.Verification.Commands(), 1)
	assert.Equal(t, VerifyCommand{Name: "test", Command: "echo ok"}, state.Verification.Commands()[0])
}

func TestRunSpecDeclaresVerificationOverConfig(t *testing.T) {
	t.Parallel()
	mock := &mockRunner{}
	o, _ := newTestOrchestrator(t, "full", "s-verify-spec-wins", mock)
	o.cfg = testConfig()
	o.cfg.Verification = Verification{Cmds: []VerifyCommand{
		{Name: "test", Command: "echo config"},
	}}

	require.NoError(t, runWithPlan(t, o, twoStepVerifyPlan))

	state, err := LoadState(o.SessionDir())
	require.NoError(t, err)
	require.Len(t, state.Verification.Commands(), 2)
	assert.Equal(t, "echo build", state.Verification.Commands()[0].Command)
	assert.Equal(t, "echo test", state.Verification.Commands()[1].Command)
}
