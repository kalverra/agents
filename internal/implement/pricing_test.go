package implement

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEstimateAgyCost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name               string
		model              string
		in, cacheRead, out int64
		want               float64
		ok                 bool
	}{
		{"flash model with effort suffix", "gemini-3.8-flash-medium", 1_000_000, 0, 1_000_000, 0.75 + 3.75, true},
		{"flash model without effort suffix", "gemini-3.8-flash", 2_000_000, 0, 0, 1.50, true},
		{"pro model", "gemini-3.1-pro-high", 500_000, 0, 500_000, 1.00 + 6.00, true},
		{
			"flash model with cache read discount",
			"gemini-3.8-flash-medium", 2_000_000, 1_000_000, 1_000_000,
			1_000_000.0/1e6*0.75 + 1_000_000.0/1e6*0.075 + 1_000_000.0/1e6*3.75,
			true,
		},
		{"unknown model", "gemini-9.9-ultra", 1_000_000, 0, 1_000_000, 0, false},
		{"zero tokens known model", "gemini-3.8-flash", 0, 0, 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := EstimateAgyCost(tc.model, tc.in, tc.cacheRead, tc.out)
			assert.Equal(t, tc.ok, ok)
			if tc.ok {
				assert.InDelta(t, tc.want, got, 1e-9)
			}
		})
	}
}
