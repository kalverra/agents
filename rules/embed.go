// Package rules embeds the built-in rules that agents install writes into
// each harness's global instructions file. Each rule is one markdown file
// named <rule>.md.
package rules

import "embed"

// FS holds the built-in rule files.
//
//go:embed *.md
var FS embed.FS
