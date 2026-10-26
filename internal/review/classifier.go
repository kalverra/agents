package review

import (
	"path/filepath"
	"strings"

	"github.com/kalverra/agents/pkg/git"
)

// FileTier represents the noise/signal tier of a changed file.
type FileTier string

const (
	// TierSemantic represents logic, behavior, configuration, or structural changes.
	TierSemantic FileTier = "semantic"
	// TierFormatting represents pure formatting, whitespace, or import changes with no logic edits.
	TierFormatting FileTier = "formatting"
	// TierDeletion represents files removed in the PR.
	TierDeletion FileTier = "deletion"
	// TierUnreadable represents files git failed to read (e.g. sandbox read-denials).
	TierUnreadable FileTier = "unreadable"
)

// FileClassification details the tier and category of a file diff.
type FileClassification struct {
	Tier     FileTier `json:"tier"`
	Category string   `json:"category,omitempty"`
}

// ClassifyFile categorizes a file diff into one of the four tiers.
func ClassifyFile(f git.FileDiff) FileClassification {
	if f.Status == "unreadable" {
		return FileClassification{Tier: TierUnreadable}
	}
	if f.Status == "deleted" {
		return FileClassification{Tier: TierDeletion}
	}
	if f.Patch == "" {
		return FileClassification{Tier: TierSemantic}
	}

	if isWhitespaceOnly(f.Patch) {
		return FileClassification{Tier: TierFormatting, Category: "whitespace"}
	}

	if isImportOnly(f.Path, f.Patch) {
		return FileClassification{Tier: TierFormatting, Category: "imports"}
	}

	return FileClassification{Tier: TierSemantic}
}

func isWhitespaceOnly(patch string) bool {
	hasDiff := false
	for line := range strings.SplitSeq(patch, "\n") {
		if len(line) == 0 {
			continue
		}
		if line[0] != '+' && line[0] != '-' {
			continue
		}
		if git.SkipPatchLine(line) {
			continue
		}
		hasDiff = true
		content := strings.TrimSpace(line[1:])
		if content != "" {
			return false
		}
	}
	return hasDiff
}

func isImportOnly(path, patch string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	hasDiff := false
	inGoImportBlock := false

	for line := range strings.SplitSeq(patch, "\n") {
		if len(line) == 0 || git.SkipPatchLine(line) || strings.HasPrefix(line, "@@") {
			continue
		}

		trimmed := strings.TrimSpace(line)
		if ext == ".go" {
			if strings.HasPrefix(trimmed, "import (") || strings.HasPrefix(trimmed, "+import (") ||
				strings.HasPrefix(trimmed, "-import (") {
				inGoImportBlock = true
			} else if inGoImportBlock && (trimmed == ")" || trimmed == "+)" || trimmed == "-)") {
				inGoImportBlock = false
			}
		}

		char := line[0]
		if char != '+' && char != '-' {
			continue
		}

		hasDiff = true
		content := strings.TrimSpace(line[1:])
		if content == "" {
			continue
		}

		if !isLanguageImport(ext, content, inGoImportBlock) {
			return false
		}
	}

	return hasDiff
}

func isLanguageImport(ext, content string, inGoImportBlock bool) bool {
	switch ext {
	case ".go":
		return isGoImport(content, inGoImportBlock)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return isTSImport(content)
	case ".py":
		return strings.HasPrefix(content, "import ") || strings.HasPrefix(content, "from ")
	case ".rs":
		return strings.HasPrefix(content, "use ") || strings.HasPrefix(content, "pub use ")
	default:
		return false
	}
}

func isGoImport(content string, inBlock bool) bool {
	if content == "import (" || content == ")" {
		return true
	}
	if strings.HasPrefix(content, "import \"") || strings.HasPrefix(content, "import (") {
		return true
	}
	return inBlock && (strings.HasPrefix(content, "\"") || strings.Contains(content, "\""))
}

func isTSImport(content string) bool {
	return strings.HasPrefix(content, "import ") || strings.HasPrefix(content, "import{") ||
		strings.HasPrefix(content, "export {") || strings.HasPrefix(content, "export * from") ||
		strings.HasPrefix(content, "} from") || strings.HasPrefix(content, "from \"") ||
		strings.HasPrefix(content, "from '") || strings.Contains(content, "require(")
}
