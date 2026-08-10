package activity_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/activity"
)

func TestParseTimeframe(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 15, 0, 0, 0, time.UTC)

	tests := []struct {
		input    string
		expected time.Time
		wantErr  bool
	}{
		{input: "1d", expected: now.Add(-24 * time.Hour), wantErr: false},
		{input: "7d", expected: now.Add(-7 * 24 * time.Hour), wantErr: false},
		{input: "30d", expected: now.Add(-30 * 24 * time.Hour), wantErr: false},
		{input: "24h", expected: now.Add(-24 * time.Hour), wantErr: false},
		{input: "today", expected: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), wantErr: false},
		{input: "yesterday", expected: time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), wantErr: false},
		{input: "week", expected: now.Add(-7 * 24 * time.Hour), wantErr: false},
		{input: "month", expected: now.Add(-30 * 24 * time.Hour), wantErr: false},
		{input: "quarter", expected: now.Add(-90 * 24 * time.Hour), wantErr: false},
		{input: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, err := activity.ParseTimeframe(tt.input, now)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, got)
			}
		})
	}
}
