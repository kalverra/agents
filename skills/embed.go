// Package skills embeds the SKILL.md of each built-in skill, so agents install
// can list them without a network call. gh skill installs the skills
// themselves from the GitHub repo.
package skills

import "embed"

// FS holds skills/<name>/SKILL.md for every built-in skill.
//
//go:embed */SKILL.md
var FS embed.FS
