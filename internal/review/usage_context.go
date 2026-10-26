package review

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/kalverra/agents/internal/review/lsp"
	"github.com/kalverra/agents/pkg/git"
)

// SymbolKind represents the type of symbol being tracked.
type SymbolKind string

const (
	// KindEnvVar represents an environment variable or config key.
	KindEnvVar SymbolKind = "env_var"
	// KindCodeSymbol represents a generic code symbol.
	KindCodeSymbol SymbolKind = "code_symbol"
	// KindMethod represents a struct/type method.
	KindMethod SymbolKind = "method"
	// KindFunction represents a package-level function.
	KindFunction SymbolKind = "func"
	// KindType represents a type declaration.
	KindType SymbolKind = "type"
	// KindDeletedFile represents a deleted file name.
	KindDeletedFile SymbolKind = "deleted_file"
)

// SymbolToScan describes an identifier extracted from diff to check for external usage.
type SymbolToScan struct {
	Name       string
	FullName   string
	Receiver   string
	Kind       SymbolKind
	File       string
	Line       int
	Col        int
	IsModified bool
}

// ExternalUsage records references found outside the diff for a symbol.
type ExternalUsage struct {
	Symbol     string          `json:"symbol"`
	Kind       SymbolKind      `json:"kind"`
	References []lsp.Reference `json:"references"`
}

var (
	envWithUnderscoreRegex = regexp.MustCompile(`\b([A-Z][A-Z0-9]*_[A-Z0-9_]+)\b`)
	explicitEnvRegex       = regexp.MustCompile(
		`(?:Getenv|Setenv|LookupEnv)\(["']([A-Z0-9_]+)["']|(?:process\.env(?:\.([A-Z0-9_]+)|\[["']([A-Z0-9_]+)["']\]))|(?:os\.(?:environ|getenv)\(["']([A-Z0-9_]+)["']\))|(?:export\s+([A-Z0-9_]+)=)`,
	)
	linterCodeRegex     = regexp.MustCompile(`^[A-Z]{1,3}\d{3,5}$`)
	hunkHeaderScanRegex = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

	goMethodRegex    = regexp.MustCompile(`^-\s*func\s+\((?:[a-zA-Z_]\w*\s+)?(\*?[A-Za-z]\w*)\)\s+([A-Z]\w+)\(`)
	goMethodAddRegex = regexp.MustCompile(`^\+\s*func\s+\((?:[a-zA-Z_]\w*\s+)?(\*?[A-Za-z]\w*)\)\s+([A-Z]\w+)\(`)
	goFuncRegex      = regexp.MustCompile(`^-\s*func\s+([A-Z]\w+)\(`)
	goFuncAddRegex   = regexp.MustCompile(`^\+\s*func\s+([A-Z]\w+)\(`)
	goTypeRegex      = regexp.MustCompile(`^-\s*type\s+([A-Z]\w+)`)
	pyDefRegex       = regexp.MustCompile(`^-\s*(?:def|class)\s+([a-zA-Z_]\w+)`)
	tsExportRegex    = regexp.MustCompile(
		`^-\s*export\s+(?:default\s+)?(?:async\s+)?(?:function|class|const|let|var|type|interface)\s+([a-zA-Z_]\w+)`,
	)
	commonEnvIgnore = map[string]bool{
		"HTTP": true, "HTTPS": true, "JSON": true, "TRUE": true, "FALSE": true,
		"NULL": true, "POST": true, "HEAD": true, "TODO": true, "NOTE": true,
		"PATH": true, "TEXT": true, "HTML": true, "DIFF": true, "WARN": true,
	}
)

type addedDecl struct {
	line int
	col  int
	recv string
}

// ExtractSymbolsToScan inspects diff hunks to identify env vars, deleted declarations, and deleted files.
func ExtractSymbolsToScan(diff *git.BranchDiffResult) []SymbolToScan {
	if diff == nil {
		return nil
	}

	seen := make(map[string]bool)
	var symbols []SymbolToScan

	for _, f := range diff.Files {
		if f.Status == "deleted" {
			base := filepath.Base(f.Path)
			if base != "" && !seen[base] {
				seen[base] = true
				symbols = append(symbols, SymbolToScan{
					Name: base,
					Kind: KindDeletedFile,
					File: f.Path,
				})
			}
		}

		if f.Patch == "" {
			continue
		}

		basePath := filepath.Base(f.Path)
		isLinterConfig := strings.HasPrefix(basePath, ".golangci") ||
			strings.HasPrefix(basePath, ".eslint") ||
			strings.HasPrefix(basePath, "ruff.") ||
			strings.HasPrefix(basePath, ".flake8")

		var oldLine, newLine int
		addedDecls := make(map[string]addedDecl)

		// Pre-scan + lines to detect modified declarations
		for line := range strings.SplitSeq(f.Patch, "\n") {
			if len(line) == 0 {
				continue
			}
			if m := hunkHeaderScanRegex.FindStringSubmatch(line); m != nil {
				newLine, _ = strconv.Atoi(m[2])
				continue
			}
			switch line[0] {
			case '+':
				srcLine := line[1:]
				if m := goMethodAddRegex.FindStringSubmatch(line); m != nil {
					cleanRecv := strings.TrimPrefix(m[1], "*")
					col := strings.Index(srcLine, m[2]) + 1
					addedDecls[m[2]] = addedDecl{line: newLine, col: col, recv: cleanRecv}
				} else if m := goFuncAddRegex.FindStringSubmatch(line); m != nil {
					col := strings.Index(srcLine, m[1]) + 1
					addedDecls[m[1]] = addedDecl{line: newLine, col: col}
				}
				newLine++
			case ' ':
				newLine++
			}
		}

		for line := range strings.SplitSeq(f.Patch, "\n") {
			if len(line) == 0 {
				continue
			}

			if m := hunkHeaderScanRegex.FindStringSubmatch(line); m != nil {
				oldLine, _ = strconv.Atoi(m[1])
				newLine, _ = strconv.Atoi(m[2])
				continue
			}

			char := line[0]
			currentLine := 0
			switch char {
			case ' ':
				oldLine++
				newLine++
				continue
			case '-':
				currentLine = oldLine
				oldLine++
			case '+':
				currentLine = newLine
				newLine++
			default:
				continue
			}

			trimmedLine := strings.TrimSpace(line[1:])
			isComment := strings.HasPrefix(trimmedLine, "//") ||
				strings.HasPrefix(trimmedLine, "/*") ||
				strings.HasPrefix(trimmedLine, "*") ||
				strings.HasPrefix(trimmedLine, "#")

			// Env vars / config keys from changed lines (skip comments and linter configs)
			if !isComment && !isLinterConfig {
				symbols = append(symbols, scanEnvVarsFromLine(trimmedLine, currentLine, f.Path, seen)...)
			}

			// Deleted or modified declarations
			if char == '-' {
				if sym, ok := scanDeletedDeclaration(line, currentLine, f.Path, seen, addedDecls); ok {
					symbols = append(symbols, sym)
				}
			}
		}
	}

	return symbols
}

func scanEnvVarsFromLine(
	trimmedLine string,
	currentLine int,
	filePath string,
	seen map[string]bool,
) []SymbolToScan {
	var symbols []SymbolToScan

	// Pattern 1: tokens with underscore (e.g. CTF_CONFIGS, STREAMS_API_KEY)
	for _, m := range envWithUnderscoreRegex.FindAllStringSubmatch(trimmedLine, -1) {
		name := m[1]
		if len(name) >= 4 && !linterCodeRegex.MatchString(name) && !commonEnvIgnore[name] && !seen[name] {
			seen[name] = true
			symbols = append(symbols, SymbolToScan{
				Name: name,
				Kind: KindEnvVar,
				File: filePath,
				Line: currentLine,
			})
		}
	}

	// Pattern 2: explicit env var accessors (e.g. os.Getenv("FOO"), os.Setenv("GOTMPDIR", ...))
	for _, m := range explicitEnvRegex.FindAllStringSubmatch(trimmedLine, -1) {
		for _, name := range m[1:] {
			if name == "" {
				continue
			}
			if len(name) >= 3 && !linterCodeRegex.MatchString(name) && !commonEnvIgnore[name] && !seen[name] {
				seen[name] = true
				symbols = append(symbols, SymbolToScan{
					Name: name,
					Kind: KindEnvVar,
					File: filePath,
					Line: currentLine,
				})
			}
		}
	}

	return symbols
}

func scanDeletedDeclaration(
	line string,
	currentLine int,
	filePath string,
	seen map[string]bool,
	addedDecls map[string]addedDecl,
) (SymbolToScan, bool) {
	srcLine := line
	if len(srcLine) > 0 && (srcLine[0] == '-' || srcLine[0] == '+') {
		srcLine = srcLine[1:]
	}

	if m := goMethodRegex.FindStringSubmatch(line); m != nil && !seen[m[2]] {
		seen[m[2]] = true
		cleanRecv := strings.TrimPrefix(m[1], "*")
		col := strings.Index(srcLine, m[2]) + 1
		sym := SymbolToScan{
			Name:     m[2],
			FullName: cleanRecv + "." + m[2],
			Receiver: cleanRecv,
			Kind:     KindMethod,
			File:     filePath,
			Line:     currentLine,
			Col:      col,
		}
		if added, ok := addedDecls[m[2]]; ok && added.recv == cleanRecv {
			sym.IsModified = true
			if added.line > 0 {
				sym.Line = added.line
			}
			if added.col > 0 {
				sym.Col = added.col
			}
		}
		return sym, true
	}

	if m := goFuncRegex.FindStringSubmatch(line); m != nil && !seen[m[1]] {
		seen[m[1]] = true
		col := strings.Index(srcLine, m[1]) + 1
		sym := SymbolToScan{
			Name: m[1],
			Kind: KindFunction,
			File: filePath,
			Line: currentLine,
			Col:  col,
		}
		if added, ok := addedDecls[m[1]]; ok {
			sym.IsModified = true
			if added.line > 0 {
				sym.Line = added.line
			}
			if added.col > 0 {
				sym.Col = added.col
			}
		}
		return sym, true
	}

	if m := goTypeRegex.FindStringSubmatch(line); m != nil && !seen[m[1]] {
		seen[m[1]] = true
		col := strings.Index(srcLine, m[1]) + 1
		return SymbolToScan{
			Name: m[1],
			Kind: KindType,
			File: filePath,
			Line: currentLine,
			Col:  col,
		}, true
	}

	if m := pyDefRegex.FindStringSubmatch(line); m != nil && len(m[1]) >= 4 && !seen[m[1]] {
		seen[m[1]] = true
		col := strings.Index(srcLine, m[1]) + 1
		return SymbolToScan{
			Name: m[1],
			Kind: KindCodeSymbol,
			File: filePath,
			Line: currentLine,
			Col:  col,
		}, true
	}

	if m := tsExportRegex.FindStringSubmatch(line); m != nil && len(m[1]) >= 4 && !seen[m[1]] {
		seen[m[1]] = true
		col := strings.Index(srcLine, m[1]) + 1
		return SymbolToScan{
			Name: m[1],
			Kind: KindCodeSymbol,
			File: filePath,
			Line: currentLine,
			Col:  col,
		}, true
	}

	return SymbolToScan{}, false
}

const maxSymbolsToScan = 30

func symbolPriority(kind SymbolKind) int {
	switch kind {
	case KindDeletedFile:
		return 0
	case KindEnvVar:
		return 1
	case KindFunction:
		return 2
	case KindMethod:
		return 3
	case KindCodeSymbol:
		return 4
	case KindType:
		return 5
	default:
		return 6
	}
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func containsWord(s, word string) bool {
	if len(word) == 0 || len(s) < len(word) {
		return false
	}
	start := 0
	for {
		idx := strings.Index(s[start:], word)
		if idx == -1 {
			return false
		}
		absIdx := start + idx
		start = absIdx + len(word)

		if absIdx > 0 && isWordChar(s[absIdx-1]) {
			continue
		}
		if start < len(s) && isWordChar(s[start]) {
			continue
		}
		return true
	}
}

// FindExternalReferences scans for occurrences of changed/deleted symbols outside of diff files.
func FindExternalReferences(
	ctx context.Context,
	dir string,
	diff *git.BranchDiffResult,
	_ lsp.Provider,
) ([]ExternalUsage, error) {
	t0 := time.Now()
	logger := zerolog.Ctx(ctx)

	symbols := ExtractSymbolsToScan(diff)
	if len(symbols) == 0 {
		return nil, nil
	}

	logger.Debug().
		Int("extracted_symbols", len(symbols)).
		Msg("extracted candidate symbols for external reference scan")

	// Sort and cap symbols to safeguard against massive diffs
	slices.SortFunc(symbols, func(a, b SymbolToScan) int {
		return symbolPriority(a.Kind) - symbolPriority(b.Kind)
	})
	if len(symbols) > maxSymbolsToScan {
		symbols = symbols[:maxSymbolsToScan]
	}

	diffFiles := make(map[string]bool)
	if diff != nil {
		for _, f := range diff.Files {
			diffFiles[filepath.ToSlash(f.Path)] = true
		}
	}

	// Group symbols by name for fast matching
	symbolsByName := make(map[string][]SymbolToScan)
	var names []string
	for _, s := range symbols {
		if _, exists := symbolsByName[s.Name]; !exists {
			names = append(names, s.Name)
		}
		symbolsByName[s.Name] = append(symbolsByName[s.Name], s)
	}

	// Single multi-pattern rg search (Pass 1)
	args := []string{
		"-n", "-w", "--max-count=15",
		"--glob", "!.git",
		"--glob", "!node_modules",
		"--glob", "!vendor",
		"--glob", "!*.sum",
		"--glob", "!*.lock",
		"--glob", "!*.md",
		"--glob", "!CHANGELOG*",
		"--glob", "!*.toml",
		"--glob", "!*.txt",
		"--glob", "!*.txtar",
		"--glob", "!docs/**",
		"--glob", "!testdata/**",
		"--glob", "!GNUmakefile",
		"--glob", "!Makefile",
	}
	for _, name := range names {
		args = append(args, "-e", name)
	}
	args = append(args, ".")

	tRG := time.Now()
	cmd := exec.CommandContext(ctx, "rg", args...) //nolint:gosec // trusted binary with sanitized args
	if dir != "" {
		cmd.Dir = dir
	}

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()
	logger.Debug().
		Dur("elapsed", time.Since(tRG)).
		Int("patterns", len(names)).
		Msg("multi-pattern rg search completed")

	// Associate matched lines to symbols (Pass 2)
	refsBySymbolKey := make(map[string][]lsp.Reference)
	for rawLine := range strings.SplitSeq(stdout.String(), "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 3)
		if len(parts) < 2 {
			continue
		}
		relPath := filepath.ToSlash(parts[0])
		relPath = strings.TrimPrefix(relPath, "./")
		if diffFiles[relPath] || !isSourceOrScriptFile(relPath) {
			continue
		}
		lineNum, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		text := ""
		if len(parts) >= 3 {
			text = strings.TrimSpace(parts[2])
		}

		for _, name := range names {
			if !containsWord(text, name) {
				continue
			}
			for _, s := range symbolsByName[name] {
				if s.Kind == KindMethod {
					// Disambiguate: invocations must call the method (e.g. .Method( or Method()
					// Excludes struct field declarations or comments
					if !strings.Contains(text, s.Name+"(") {
						continue
					}
				}
				key := s.Name
				if s.FullName != "" {
					key = s.FullName
				}
				if len(refsBySymbolKey[key]) < 5 {
					refsBySymbolKey[key] = append(refsBySymbolKey[key], lsp.Reference{
						File: relPath,
						Line: lineNum,
						Text: text,
					})
				}
			}
		}
	}

	var usages []ExternalUsage
	seenKeys := make(map[string]bool)
	for _, s := range symbols {
		key := s.Name
		if s.FullName != "" {
			key = s.FullName
		}
		if seenKeys[key] {
			continue
		}
		refs := refsBySymbolKey[key]
		if len(refs) > 0 {
			seenKeys[key] = true
			symDisplay := s.Name
			if s.FullName != "" {
				symDisplay = s.FullName
			}
			usages = append(usages, ExternalUsage{
				Symbol:     symDisplay,
				Kind:       s.Kind,
				References: refs,
			})
		}
	}

	logger.Debug().
		Dur("total_elapsed", time.Since(t0)).
		Int("total_usages", len(usages)).
		Msg("completed external references scan")

	return usages, nil
}

func isSourceOrScriptFile(path string) bool {
	clean := filepath.ToSlash(path)
	if strings.HasPrefix(clean, "docs/") || strings.Contains(clean, "/docs/") {
		return false
	}
	if strings.HasPrefix(clean, "testdata/") || strings.Contains(clean, "/testdata/") {
		return false
	}
	base := filepath.Base(clean)
	if strings.HasPrefix(strings.ToUpper(base), "CHANGELOG") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(clean))
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".rs", ".py", ".c", ".cpp", ".h", ".hpp",
		".java", ".kt", ".swift", ".rb", ".cs", ".sh", ".bash", ".yml", ".yaml":
		return true
	}
	if base == "Dockerfile" || base == "Containerfile" || strings.HasPrefix(base, ".env") {
		return true
	}
	return false
}
