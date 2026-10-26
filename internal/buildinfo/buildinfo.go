// Package buildinfo reports which build of agents is running.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// version is set by release builds:
//
//	-ldflags "-X github.com/kalverra/agents/internal/buildinfo.version=v1.2.3"
var version string

// Version returns the version this binary was built as: the ldflags version
// for release builds, the module version for `go install …@vX` and VCS
// builds, else "(devel)".
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

// Commit returns the VCS revision the binary was built from, or "" when the
// build has none (such as `go install …@vX`).
func Commit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

// Release returns the release tag this binary was built from, so its skills
// install at the same version. Dev, snapshot and pseudo-version builds
// return "".
func Release() string {
	return releaseTag(Version())
}

func releaseTag(v string) string {
	if !strings.HasPrefix(v, "v") || strings.ContainsAny(v, "-+") {
		return ""
	}
	return v
}
