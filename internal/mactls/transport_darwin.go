//go:build darwin

// Package mactls provides a transport with embedded public roots instead of using Security.framework.
// This is useful for sandboxed agent environments where Security.framework is not available.
package mactls

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"net/http"
	"os"

	"github.com/rs/zerolog/log"
)

// Mozilla CA bundle from https://curl.se/docs/caextract.html (PEM). Public trust
// anchors for TLS — verification uses Go's verifier instead of Security.framework,
// which fails in sandboxed agent environments (x509: OSStatus -26276).
//
// The file is PEM text but is deliberately NOT named *.pem: agent sandboxes
// routinely deny reads of /**/*.pem to protect private keys, and that deny
// glob breaks this go:embed — and with it the build of every package that
// transitively imports mactls. These are public roots, not a secret.
// Refresh by downloading cacert.pem from the URL above over this file; the
// freshness test in this package fails once it goes stale.
//
//go:embed ca-bundle.txt
var mozillaCACertPEM []byte

// certPool builds the root pool from bundle, merging extra PEM roots from
// extraPath when it is set and readable. Reports ok=false when the bundle
// itself does not parse, so callers can degrade to Keychain verification
// rather than take the process down.
func certPool(bundle []byte, extraPath string) (*x509.CertPool, bool) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(bundle) {
		return nil, false
	}
	if extraPath != "" {
		if data, err := os.ReadFile(extraPath); err == nil { //nolint:gosec // path from env, user-controlled by design
			pool.AppendCertsFromPEM(data)
		}
	}
	return pool, true
}

// RoundTripper returns a transport with embedded public roots, or nil to use
// [http.DefaultTransport] (Security.framework / Keychain verification).
//
// Set AGENTS_TLS_MACOS_USE_KEYCHAIN=1 to force nil. SSL_CERT_FILE, when set,
// merges additional PEM roots into the pool.
func RoundTripper() http.RoundTripper {
	if os.Getenv("AGENTS_TLS_MACOS_USE_KEYCHAIN") == "1" {
		return nil
	}

	pool, ok := certPool(mozillaCACertPEM, os.Getenv("SSL_CERT_FILE"))
	if !ok {
		// A corrupt vendored bundle is a packaging bug, not a reason to
		// kill the CLI: fall back to Keychain verification.
		log.Warn().Msg("mactls: embedded CA bundle did not parse; falling back to Keychain verification")
		return nil
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
	}
	return tr
}
