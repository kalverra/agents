package buildinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReleaseTag(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"v1.2.3":                             "v1.2.3",
		"v1.2.3-rc.1":                        "",
		"v0.0.0-20261026120000-abcdef123456": "",
		"v1.2.3+dirty":                       "",
		"v1.2.4-0.SNAPSHOT-abcdef1":          "",
		"(devel)":                            "",
		"":                                   "",
	}
	for in, want := range tests {
		assert.Equal(t, want, releaseTag(in), in)
	}
}
