package github

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func checkRunNode(
	name, status, conclusion, workflow, event string,
	startedAt time.Time,
	required bool,
) checkContextNode {
	n := checkContextNode{TypeName: "CheckRun"}
	n.CheckRun.Name = name
	n.CheckRun.Status = status
	n.CheckRun.Conclusion = conclusion
	n.CheckRun.StartedAt = startedAt
	n.CheckRun.DetailsURL = "https://ci.example/" + name
	n.CheckRun.IsRequired = required
	n.CheckRun.CheckSuite.WorkflowRun.Event = event
	n.CheckRun.CheckSuite.WorkflowRun.Workflow.Name = workflow
	return n
}

func statusContextNode(context, state, targetURL string, required bool) checkContextNode {
	n := checkContextNode{TypeName: "StatusContext"}
	n.StatusContext.Context = context
	n.StatusContext.State = state
	n.StatusContext.TargetURL = targetURL
	n.StatusContext.IsRequired = required
	return n
}

func TestCheckBucket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state    string
		expected string
	}{
		{"SUCCESS", "pass"},
		{"SKIPPED", "skipping"},
		{"NEUTRAL", "skipping"},
		{"FAILURE", "fail"},
		{"ERROR", "fail"},
		{"TIMED_OUT", "fail"},
		{"ACTION_REQUIRED", "fail"},
		{"CANCELLED", "cancel"},
		{"PENDING", "pending"},
		{"IN_PROGRESS", "pending"},
		{"QUEUED", "pending"},
		{"EXPECTED", "pending"},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, checkBucket(tt.state))
		})
	}
}

func TestAggregateCheckContexts(t *testing.T) {
	t.Parallel()

	now := time.Now()
	nodes := []checkContextNode{
		// Re-run of the same check: newer run must win.
		checkRunNode("unit", "COMPLETED", "SUCCESS", "CI", "push", now.Add(-2*time.Hour), false),
		checkRunNode("unit", "COMPLETED", "FAILURE", "CI", "push", now.Add(-1*time.Hour), false),
		// Same name, different workflow: both kept.
		checkRunNode("unit", "COMPLETED", "SUCCESS", "Nightly", "schedule", now, false),
		// Status context dedup keyed on context name.
		statusContextNode("continuous-integration/jenkins", "SUCCESS", "https://ci/1", false),
		statusContextNode("continuous-integration/jenkins", "FAILURE", "https://ci/1", false),
	}

	checks := aggregateCheckContexts(nodes)
	require.Len(t, checks, 3)

	byKey := map[string]CheckRun{}
	for _, c := range checks {
		byKey[c.Name+"/"+c.Workflow] = c
	}

	unit := byKey["unit/CI"]
	require.NotNil(t, unit)
	assert.Equal(t, "fail", unit.Bucket, "most recent run of same check should win")
	assert.Equal(t, "FAILURE", unit.State)

	nightly := byKey["unit/Nightly"]
	require.NotNil(t, nightly)
	assert.Equal(t, "pass", nightly.Bucket)

	jenkins := byKey["continuous-integration/jenkins/"]
	require.NotNil(t, jenkins)
	assert.Equal(t, "SUCCESS", jenkins.State)
	assert.Equal(t, "https://ci/1", jenkins.Link)
	assert.Empty(t, jenkins.Workflow)
}

func TestAggregateCheckContexts_CompletedStatusFallsBackToConclusion(t *testing.T) {
	t.Parallel()

	now := time.Now()
	checks := aggregateCheckContexts([]checkContextNode{
		checkRunNode("e2e", "COMPLETED", "TIMED_OUT", "CI", "push", now, true),
	})
	require.Len(t, checks, 1)
	assert.Equal(t, "TIMED_OUT", checks[0].State)
	assert.Equal(t, "fail", checks[0].Bucket)
	assert.True(t, checks[0].IsRequired)
}

func TestRollupFromChecks(t *testing.T) {
	t.Parallel()

	rollup := rollupFromChecks([]CheckRun{
		{Name: "build", Bucket: "pass", State: "SUCCESS"},
		{Name: "unit", Bucket: "pass", State: "SUCCESS"},
		{Name: "vault-audit", Bucket: "pending", State: "PENDING", IsRequired: true},
		{Name: "security-scan", Bucket: "pending", State: "PENDING"},
		{Name: "notify-slack", Bucket: "fail", State: "FAILURE", IsRequired: true},
		{Name: "cache", Bucket: "cancel", State: "CANCELLED"},
		{Name: "setup", Bucket: "skipping", State: "SKIPPED"},
	})

	assert.Equal(t, 7, rollup.Total)
	assert.Equal(t, 2, rollup.Pass)
	assert.Equal(t, 2, rollup.Fail) // failure + cancel
	assert.Equal(t, 2, rollup.Pending)
	assert.Equal(t, 1, rollup.Skip)
	assert.Equal(t, 1, rollup.PendingRequired)
	assert.Equal(t, 1, rollup.PendingOptional)
	assert.Equal(t, 1, rollup.FailRequired)
	assert.Equal(t, 1, rollup.FailOptional)
}

func TestCommitHeadline(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "feat: add thing", commitHeadline("feat: add thing\nn\nSome body"))
	assert.Equal(t, "feat: add thing", commitHeadline("feat: add thing"))
	assert.Empty(t, commitHeadline(""))
}
