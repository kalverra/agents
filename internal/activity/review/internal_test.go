package review

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBuildReportContentWithEval(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	generated := "my report content"

	outTrue := buildReportContentWithEval("daily", now, generated, true)
	assert.Contains(t, outTrue, "---")
	assert.Contains(t, outTrue, "eval_status: pending")
	assert.Contains(t, outTrue, "# Daily Standup")
	assert.Contains(t, outTrue, generated)

	outFalse := buildReportContentWithEval("daily", now, generated, false)
	assert.NotContains(t, outFalse, "---")
	assert.NotContains(t, outFalse, "eval_status: pending")
	assert.Contains(t, outFalse, "# Daily Standup")
	assert.Contains(t, outFalse, generated)
}
