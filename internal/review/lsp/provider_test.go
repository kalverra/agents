package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoplsProvider_Supports(t *testing.T) {
	t.Parallel()

	p := NewGoplsProvider()
	assert.True(t, p.Supports(".go"))
	assert.False(t, p.Supports(".ts"))
	assert.False(t, p.Supports(".py"))
}

func TestGoplsProvider_FindReferences(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}

	p := NewGoplsProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	repoRoot, err := filepath.Abs("../../..")
	require.NoError(t, err)

	//nolint:gosec // reading test file in repository
	bundleContent, err := os.ReadFile(filepath.Join(repoRoot, "internal/review/bundle.go"))
	require.NoError(t, err)

	targetLine := 0
	for i, l := range strings.Split(string(bundleContent), "\n") {
		if strings.HasPrefix(l, "func FormatBundle(") {
			targetLine = i + 1
			break
		}
	}
	require.Positive(t, targetLine)

	// Query FormatBundle in internal/review/bundle.go
	refs, err := p.FindReferences(ctx, repoRoot, "internal/review/bundle.go", targetLine, 6)
	require.NoError(t, err)
	assert.NotEmpty(t, refs, "expected gopls to find references for FormatBundle")

	hasTestRef := false
	for _, r := range refs {
		if r.File != "" && r.Line > 0 {
			hasTestRef = true
			break
		}
	}
	assert.True(t, hasTestRef)
}

func TestRegistry_Lookup(t *testing.T) {
	t.Parallel()

	reg := DefaultRegistry()
	assert.NotNil(t, reg.ForExtension(".go"))
	assert.NotNil(t, reg.ForExtension(".rs"))
	assert.NotNil(t, reg.ForExtension(".ts"))
	assert.NotNil(t, reg.ForExtension(".py"))
	assert.NotNil(t, reg.ForExtension(".c"))
	assert.Nil(t, reg.ForExtension(".unknown_xyz"))
}
