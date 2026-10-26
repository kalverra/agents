package implement

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// agy encodes reasoning effort as a model-name suffix and rejects a --effort
// flag that contradicts it, so a per-step **Effort** override must rewrite the
// model rather than be passed alongside the suffixed name.
func TestAgyModelEffortReconciliation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		spec       RoleSpec
		wantModel  string
		wantEffort string
	}{
		{
			name:       "explicit effort overrides a conflicting model suffix",
			spec:       RoleSpec{Harness: HarnessAgy, Model: "gemini-3.8-flash-high", Effort: "medium"},
			wantModel:  "gemini-3.8-flash",
			wantEffort: "medium",
		},
		{
			name:       "matching effort still strips the suffix",
			spec:       RoleSpec{Harness: HarnessAgy, Model: "gemini-3.8-flash-high", Effort: "high"},
			wantModel:  "gemini-3.8-flash",
			wantEffort: "high",
		},
		{
			name:       "no effort leaves a suffixed model untouched",
			spec:       RoleSpec{Harness: HarnessAgy, Model: "gemini-3.8-flash-high"},
			wantModel:  "gemini-3.8-flash-high",
			wantEffort: "",
		},
		{
			name:       "unsuffixed model passes effort through",
			spec:       RoleSpec{Harness: HarnessAgy, Model: "gemini-3.8-pro", Effort: "low"},
			wantModel:  "gemini-3.8-pro",
			wantEffort: "low",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := AgyHarness{}

			run := h.RunArgs(tc.spec, "do work", "")
			modelIdx := indexOfArg(t, run, "--model")
			assert.Equal(t, tc.wantModel, run[modelIdx+1], "RunArgs --model")
			if tc.wantEffort == "" {
				assert.NotContains(t, run, "--effort", "RunArgs must omit --effort")
			} else {
				effortIdx := indexOfArg(t, run, "--effort")
				assert.Equal(t, tc.wantEffort, run[effortIdx+1], "RunArgs --effort")
			}

			// The failure this guards against, verbatim from agy:
			// "--model gemini-3.8-flash-high conflicts with --effort=medium".
			joined := strings.Join(run, " ")
			for _, suffix := range agyEffortSuffixes {
				if tc.wantEffort != "" {
					assert.NotContains(t, joined, "--model "+tc.spec.Model+suffix)
				}
			}
		})
	}
}
