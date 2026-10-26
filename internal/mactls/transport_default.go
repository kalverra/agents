//go:build !darwin

package mactls

import "net/http"

// RoundTripper returns nil on non-macOS; callers use [http.DefaultTransport].
func RoundTripper() http.RoundTripper {
	return nil
}
