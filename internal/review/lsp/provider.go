// Package lsp provides language server protocol integration and symbol reference resolution.
package lsp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Reference represents a symbol reference location.
type Reference struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col,omitempty"`
	Text string `json:"text,omitempty"`
}

// Provider defines the interface for an LSP symbol resolver.
type Provider interface {
	Supports(ext string) bool
	FindReferences(ctx context.Context, dir, file string, line, col int) ([]Reference, error)
}

// Registry manages language-specific LSP providers.
type Registry struct {
	providers []Provider
}

// DefaultRegistry returns a registry pre-configured with known language servers.
func DefaultRegistry() *Registry {
	return &Registry{
		providers: []Provider{
			NewGoplsProvider(),
			NewGenericProvider(".rs", "rust-analyzer"),
			NewGenericProvider(".ts", "typescript-language-server", "vtsls"),
			NewGenericProvider(".tsx", "typescript-language-server", "vtsls"),
			NewGenericProvider(".js", "typescript-language-server", "vtsls"),
			NewGenericProvider(".jsx", "typescript-language-server", "vtsls"),
			NewGenericProvider(".py", "pyright-langserver", "pylsp"),
			NewGenericProvider(".c", "clangd"),
			NewGenericProvider(".cpp", "clangd"),
			NewGenericProvider(".h", "clangd"),
			NewGenericProvider(".hpp", "clangd"),
		},
	}
}

// ForExtension returns the first provider that supports the given file extension.
func (r *Registry) ForExtension(ext string) Provider {
	ext = strings.ToLower(ext)
	for _, p := range r.providers {
		if p.Supports(ext) {
			return p
		}
	}
	return nil
}

// Supports reports whether any provider in the registry supports the given extension.
func (r *Registry) Supports(ext string) bool {
	return r.ForExtension(ext) != nil
}

// FindReferences dispatches symbol reference queries to the matching language provider.
func (r *Registry) FindReferences(ctx context.Context, dir, file string, line, col int) ([]Reference, error) {
	p := r.ForExtension(filepath.Ext(file))
	if p == nil {
		return nil, errors.New("no LSP provider registered for file extension")
	}
	return p.FindReferences(ctx, dir, file, line, col)
}

// GoplsProvider implements Provider using the gopls CLI fast path.
type GoplsProvider struct{}

// NewGoplsProvider creates a new gopls provider.
func NewGoplsProvider() *GoplsProvider {
	return &GoplsProvider{}
}

// Supports reports whether gopls supports the given extension.
func (p *GoplsProvider) Supports(ext string) bool {
	return strings.ToLower(ext) == ".go"
}

// FindReferences queries gopls for references to the symbol at line:col.
func (p *GoplsProvider) FindReferences(ctx context.Context, dir, file string, line, col int) ([]Reference, error) {
	t0 := time.Now()
	logger := zerolog.Ctx(ctx)

	if _, err := exec.LookPath("gopls"); err != nil {
		logger.Debug().Msg("gopls not found in PATH")
		return nil, errors.New("gopls not found in PATH")
	}

	targetFile := file
	if !filepath.IsAbs(targetFile) && dir != "" {
		targetFile = filepath.Join(dir, file)
	}

	target := fmt.Sprintf("%s:%d:%d", targetFile, line, col)
	//nolint:gosec // gopls is a fixed trusted tool with validated coordinates
	cmd := exec.CommandContext(ctx, "gopls", "references", target)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		logger.Debug().
			Dur("elapsed", time.Since(t0)).
			Str("target", target).
			Str("stderr", strings.TrimSpace(stderr.String())).
			Err(err).
			Msg("gopls references failed")
		return nil, fmt.Errorf("running gopls references: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	var refs []Reference
	for rawLine := range strings.SplitSeq(stdout.String(), "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			continue
		}
		if ref, ok := parseRefLine(dir, trimmed); ok {
			refs = append(refs, ref)
		}
	}

	logger.Debug().
		Dur("elapsed", time.Since(t0)).
		Str("target", target).
		Int("refs_found", len(refs)).
		Msg("gopls references completed")

	return refs, nil
}

// GenericProvider represents an LSP server configured for specific extensions.
type GenericProvider struct {
	exts     []string
	commands []string
}

// NewGenericProvider creates a generic provider for specified extension and binaries.
func NewGenericProvider(ext string, commands ...string) *GenericProvider {
	return &GenericProvider{
		exts:     []string{strings.ToLower(ext)},
		commands: commands,
	}
}

// Supports reports whether this provider handles the extension.
func (p *GenericProvider) Supports(ext string) bool {
	return slices.Contains(p.exts, strings.ToLower(ext))
}

// FindReferences attempts reference discovery using configured LSP binaries.
func (p *GenericProvider) FindReferences(_ context.Context, _, file string, _, _ int) ([]Reference, error) {
	for _, cmdName := range p.commands {
		if _, err := exec.LookPath(cmdName); err == nil {
			return nil, fmt.Errorf("lsp %s found but stdio session not active", cmdName)
		}
	}
	return nil, fmt.Errorf("no lsp found for %s", filepath.Ext(file))
}

func parseRefLine(baseDir, line string) (Reference, bool) {
	parts := strings.Split(line, ":")
	if len(parts) < 2 {
		return Reference{}, false
	}

	filePath := parts[0]
	if baseDir != "" {
		if rel, err := filepath.Rel(baseDir, filePath); err == nil && !strings.HasPrefix(rel, "..") {
			filePath = rel
		}
	}

	lineNum, err := strconv.Atoi(parts[1])
	if err != nil {
		return Reference{}, false
	}

	colNum := 0
	if len(parts) >= 3 {
		colPart := parts[2]
		if dash := strings.Index(colPart, "-"); dash != -1 {
			colPart = colPart[:dash]
		}
		colNum, _ = strconv.Atoi(colPart)
	}

	return Reference{
		File: filePath,
		Line: lineNum,
		Col:  colNum,
	}, true
}
