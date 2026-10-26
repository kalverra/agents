//go:build darwin

package mactls

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// maxBundleAge bounds how stale the vendored Mozilla root bundle may get.
// Roots are added and removed over time; a bundle left to rot surfaces as
// TLS handshake failures that look like network flakes. Twelve months is
// loose enough not to churn CI and tight enough to catch real neglect.
const maxBundleAge = 365 * 24 * time.Hour

var bundleDateRe = regexp.MustCompile(
	`(?m)^## Certificate data from Mozilla last updated on:\s*(.+?)\s*$`,
)

func TestEmbeddedBundleParses(t *testing.T) {
	t.Parallel()
	pool, ok := certPool(mozillaCACertPEM, "")
	require.True(t, ok, "embedded CA bundle must parse")
	assert.NotNil(t, pool)
}

func TestCertPoolRejectsUnparseableBundle(t *testing.T) {
	t.Parallel()
	pool, ok := certPool([]byte("this is not a certificate"), "")
	assert.False(t, ok)
	assert.Nil(t, pool)
}

func TestCertPoolMergesExtraRoots(t *testing.T) {
	t.Parallel()
	// A missing or unreadable SSL_CERT_FILE is ignored, not fatal: the
	// embedded roots still stand on their own.
	pool, ok := certPool(mozillaCACertPEM, filepath.Join(t.TempDir(), "absent.txt"))
	require.True(t, ok)
	assert.NotNil(t, pool)
}

func TestRoundTripperHonoursKeychainOptOut(t *testing.T) {
	t.Setenv("AGENTS_TLS_MACOS_USE_KEYCHAIN", "1")
	assert.Nil(t, RoundTripper(), "opt-out must fall back to http.DefaultTransport")
}

func TestRoundTripperUsesEmbeddedRoots(t *testing.T) {
	t.Setenv("AGENTS_TLS_MACOS_USE_KEYCHAIN", "")
	assert.NotNil(t, RoundTripper())
}

func TestEmbeddedBundleIsFresh(t *testing.T) {
	t.Parallel()
	m := bundleDateRe.FindSubmatch(mozillaCACertPEM)
	require.NotNil(t, m, "bundle is missing its 'last updated on' header")

	updated, err := time.Parse("Mon Jan 2 15:04:05 2006 MST", string(m[1]))
	require.NoError(t, err, "unparseable bundle date %q", string(m[1]))

	age := time.Since(updated)
	t.Logf("Mozilla CA bundle is %.0f days old (updated %s)", age.Hours()/24, updated.Format(time.DateOnly))
	assert.Less(t, age, maxBundleAge,
		"CA bundle is stale; refresh internal/mactls/ca-bundle.txt from https://curl.se/docs/caextract.html")
}

func TestBundleFileIsNotPEMExtension(t *testing.T) {
	t.Parallel()
	// The bundle is public trust anchors, not a secret, but agent sandboxes
	// routinely deny reads of /**/*.pem to protect private keys. A .pem
	// extension here breaks `go:embed` (and therefore the whole module
	// build) inside those sandboxes.
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotEqual(t, ".pem", filepath.Ext(e.Name()),
			"keep the CA bundle off the .pem sandbox deny glob")
	}
}
