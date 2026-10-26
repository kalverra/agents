package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/pkg/git"
)

func TestExtractSymbolsToScan(t *testing.T) {
	t.Parallel()

	diff := &git.BranchDiffResult{
		Files: []git.FileDiff{
			{
				Path:   "core/config.go",
				Status: "modified",
				Patch: `@@ -10,3 +10,3 @@
-	val := os.Getenv("CTF_CONFIGS")
+	val := os.Getenv("NEW_CONFIGS")
-func OldExportedMethod() error {
`,
			},
			{
				Path:   "legacy/cleanup.go",
				Status: "deleted",
			},
		},
	}

	symbols := ExtractSymbolsToScan(diff)
	require.NotEmpty(t, symbols)

	symbolMap := make(map[string]SymbolToScan)
	for _, s := range symbols {
		symbolMap[s.Name] = s
	}

	assert.Equal(t, KindEnvVar, symbolMap["CTF_CONFIGS"].Kind)
	assert.Equal(t, KindFunction, symbolMap["OldExportedMethod"].Kind)
	assert.Positive(t, symbolMap["OldExportedMethod"].Line, "expected non-zero line number for code symbol")
}

func TestExtractSymbolsToScan_ExcludesNoise(t *testing.T) {
	t.Parallel()

	diff := &git.BranchDiffResult{
		Files: []git.FileDiff{
			{
				Path:   ".golangci.yml",
				Status: "modified",
				Patch: `@@ -50,3 +50,3 @@
-        text: "SA1019: (.*).AddressBook is deprecated"
+        text: "something else"
`,
			},
			{
				Path:   "pkg/service.go",
				Status: "modified",
				Patch: `@@ -20,6 +20,6 @@
-//nolint:staticcheck // SA1019 AddressBook is deprecated
-	RPCURL string ` + "`json:\"rpcUrl\"`" + `
-	// OCR2 and LOOPP are remote plugin types
-	os.Setenv("GOTMPDIR", tmp)
+	os.Setenv("GOTMPDIR", "")
`,
			},
		},
	}

	symbols := ExtractSymbolsToScan(diff)
	symbolNames := make(map[string]bool)
	for _, s := range symbols {
		symbolNames[s.Name] = true
	}

	// Noise should be excluded
	assert.False(t, symbolNames["SA1019"], "linter ID SA1019 should not be extracted as env var")
	assert.False(t, symbolNames["RPCURL"], "struct field RPCURL should not be extracted as env var")
	assert.False(t, symbolNames["OCR2"], "comment acronym OCR2 should not be extracted as env var")
	assert.False(t, symbolNames["LOOPP"], "comment acronym LOOPP should not be extracted as env var")

	// Real explicit env var should be included
	assert.True(t, symbolNames["GOTMPDIR"], "os.Setenv(\"GOTMPDIR\") should be extracted")
}

func TestFindExternalReferences_EnvVarOutsideDiff(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// File in diff that removes CTF_CONFIGS
	diffFile := filepath.Join(tmpDir, "chiprouter", "config.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(diffFile), 0o750))
	require.NoError(t, os.WriteFile(diffFile, []byte("package chiprouter\n"), 0o600))

	// Code file OUTSIDE diff that still references CTF_CONFIGS
	outsideFile := filepath.Join(tmpDir, "chiprouter", "router.go")
	require.NoError(
		t,
		os.WriteFile(outsideFile, []byte("package chiprouter\nvar x = os.Getenv(\"CTF_CONFIGS\")\n"), 0o600),
	)

	// Noise files outside diff that should be filtered out
	docsFile := filepath.Join(tmpDir, "docs", "guide.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(docsFile), 0o750))
	require.NoError(t, os.WriteFile(docsFile, []byte("CTF_CONFIGS in docs\n"), 0o600))

	changelogFile := filepath.Join(tmpDir, "CHANGELOG.md")
	require.NoError(t, os.WriteFile(changelogFile, []byte("CTF_CONFIGS mentioned in changelog\n"), 0o600))

	diff := &git.BranchDiffResult{
		RepoPath: tmpDir,
		Files: []git.FileDiff{
			{
				Path:   "chiprouter/config.go",
				Status: "modified",
				Patch:  "@@ -1,2 +1,2 @@\n-var env = \"CTF_CONFIGS\"\n+var env = \"\"\n",
			},
		},
	}

	usages, err := FindExternalReferences(context.Background(), tmpDir, diff, nil)
	require.NoError(t, err)
	require.NotEmpty(t, usages)

	for _, u := range usages {
		if u.Symbol == "CTF_CONFIGS" {
			for _, r := range u.References {
				assert.NotEqual(t, "chiprouter/config.go", r.File)
				assert.NotEqual(t, "docs/guide.md", r.File, "docs should be filtered out")
				assert.NotEqual(t, "CHANGELOG.md", r.File, "changelog should be filtered out")
				assert.Equal(t, "chiprouter/router.go", r.File)
			}
		}
	}
}

func TestExtractSymbolsToScan_MethodWithReceiver(t *testing.T) {
	t.Parallel()

	diff := &git.BranchDiffResult{
		Files: []git.FileDiff{
			{
				Path:   "secrets.go",
				Status: "modified",
				Patch: `@@ -90,4 +90,4 @@
-func (n NodeKeys) PeerID() string {
+func (n *NodeKeys) PeerID() string {
`,
			},
		},
	}

	symbols := ExtractSymbolsToScan(diff)
	require.NotEmpty(t, symbols)

	var peerIDSym *SymbolToScan
	for i := range symbols {
		if symbols[i].Name == "PeerID" {
			peerIDSym = &symbols[i]
			break
		}
	}

	require.NotNil(t, peerIDSym)
	assert.Equal(t, "PeerID", peerIDSym.Name)
	assert.Equal(t, "NodeKeys", peerIDSym.Receiver)
	assert.Equal(t, "NodeKeys.PeerID", peerIDSym.FullName)
	assert.Equal(t, KindMethod, peerIDSym.Kind)
	assert.True(t, peerIDSym.IsModified)
	assert.Equal(t, 20, peerIDSym.Col, "expected column of PeerID in line")
}

func TestFindExternalReferences_MethodExcludesFieldFalseAlarms(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Target diff file
	diffFile := filepath.Join(tmpDir, "secrets.go")
	require.NoError(t, os.WriteFile(diffFile, []byte("package cre\n"), 0o600))

	// File outside diff with struct field access (false alarm)
	fieldAccessFile := filepath.Join(tmpDir, "node.go")
	fieldAccessContent := `package cre
type Attr struct {
	PeerID string
}
func test(a Attr) {
	p := a.PeerID
}
`
	require.NoError(t, os.WriteFile(fieldAccessFile, []byte(fieldAccessContent), 0o600))

	// File outside diff with real method call
	methodCallFile := filepath.Join(tmpDir, "caller.go")
	methodCallContent := `package cre
func check(k *NodeKeys) {
	id := k.PeerID()
}
`
	require.NoError(t, os.WriteFile(methodCallFile, []byte(methodCallContent), 0o600))

	diff := &git.BranchDiffResult{
		RepoPath: tmpDir,
		Files: []git.FileDiff{
			{
				Path:   "secrets.go",
				Status: "modified",
				Patch: `@@ -1,2 +1,2 @@
-func (n NodeKeys) PeerID() string {
+func (n *NodeKeys) PeerID() string {
`,
			},
		},
	}

	usages, err := FindExternalReferences(context.Background(), tmpDir, diff, nil)
	require.NoError(t, err)
	require.NotEmpty(t, usages)

	var peerIDUsage *ExternalUsage
	for i := range usages {
		if usages[i].Symbol == "NodeKeys.PeerID" || usages[i].Symbol == "PeerID" {
			peerIDUsage = &usages[i]
			break
		}
	}

	require.NotNil(t, peerIDUsage)
	// Must contain caller.go with method call
	foundMethodCall := false
	for _, r := range peerIDUsage.References {
		if r.File == "caller.go" {
			foundMethodCall = true
		}
		// Should NOT include node.go struct field access false alarms
		assert.NotEqual(t, "node.go", r.File, "struct field access must not be included as method reference")
	}
	assert.True(t, foundMethodCall, "real method call in caller.go must be found")
}
