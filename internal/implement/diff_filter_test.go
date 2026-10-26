package implement

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsLockfile(t *testing.T) {
	t.Parallel()

	lockfiles := []string{
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
		"pnpm-lock.json",
		"bun.lockb",
		"bun.lock",
		"Cargo.lock",
		"Gemfile.lock",
		"poetry.lock",
		"Pipfile.lock",
		"composer.lock",
		"flake.lock",
		"mix.lock",
		"uv.lock",
		"Podfile.lock",
		"berksfile.lock",
		"packages.lock.json",
		"pdm.lock",
		"go.sum",
		"go.work.sum",
		"custom.lock",
		"foo/bar/package-lock.json",
		"sub/dir/app.lock",
		"vendor/github.com/pkg/errors/errors.go",
		"node_modules/lodash/index.js",
	}

	for _, path := range lockfiles {
		assert.True(t, IsLockfile(path), "path %q should be identified as lockfile/dependency", path)
	}

	nonLockfiles := []string{
		"main.go",
		"internal/flow/orchestrator.go",
		"package.json",
		"Cargo.toml",
		"go.mod",
		"Makefile",
		"README.md",
		"src/lock_service.go",
	}

	for _, path := range nonLockfiles {
		assert.False(t, IsLockfile(path), "path %q should not be identified as lockfile/dependency", path)
	}
}

func TestGitattributesParser(t *testing.T) {
	t.Parallel()

	content := `
# Gitattributes test
*.pb.go linguist-generated
*.min.js linguist-generated=true
build/** -diff
assets/bundle.bin binary
gen/schema.json gitlab-generated=yes
code.gen.go generated
normal.go text
`
	ga := ParseGitattributes(content, "")

	assert.True(t, ga.IsGeneratedOrNoDiff("model.pb.go"))
	assert.True(t, ga.IsGeneratedOrNoDiff("pkg/sub/model.pb.go"))
	assert.True(t, ga.IsGeneratedOrNoDiff("app.min.js"))
	assert.True(t, ga.IsGeneratedOrNoDiff("build/output.js"))
	assert.True(t, ga.IsGeneratedOrNoDiff("assets/bundle.bin"))
	assert.True(t, ga.IsGeneratedOrNoDiff("gen/schema.json"))
	assert.True(t, ga.IsGeneratedOrNoDiff("code.gen.go"))

	assert.False(t, ga.IsGeneratedOrNoDiff("main.go"))
	assert.False(t, ga.IsGeneratedOrNoDiff("normal.go"))
	assert.False(t, ga.IsGeneratedOrNoDiff("other/schema.json"))
}

func TestLoadRepoGitattributes(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	rootAttr := "*.pb.go linguist-generated\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmp, ".gitattributes"), []byte(rootAttr), 0o600))

	subDir := filepath.Join(tmp, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0o750))
	subAttr := "generated.txt -diff\n"
	require.NoError(t, os.WriteFile(filepath.Join(subDir, ".gitattributes"), []byte(subAttr), 0o600))

	ga, err := LoadRepoGitattributes(tmp)
	require.NoError(t, err)

	assert.True(t, ga.IsGeneratedOrNoDiff("foo.pb.go"))
	assert.True(t, ga.IsGeneratedOrNoDiff("sub/generated.txt"))
	assert.False(t, ga.IsGeneratedOrNoDiff("sub/normal.txt"))
}

const sampleUnifiedDiff = `diff --git a/internal/flow/work.go b/internal/flow/work.go
index 1111111..2222222 100644
--- a/internal/flow/work.go
+++ b/internal/flow/work.go
@@ -1,3 +1,4 @@
 package flow
+func Work() {}
diff --git a/package-lock.json b/package-lock.json
index 3333333..4444444 100644
--- a/package-lock.json
+++ b/package-lock.json
@@ -10,3 +10,4 @@
 "version": "1.0.1"
diff --git a/proto/user.pb.go b/proto/user.pb.go
new file mode 100644
index 0000000..5555555
--- /dev/null
+++ b/proto/user.pb.go
@@ -0,0 +1,5 @@
+package proto
diff --git a/internal/flow/work_test.go b/internal/flow/work_test.go
index 6666666..7777777 100644
--- a/internal/flow/work_test.go
+++ b/internal/flow/work_test.go
@@ -1,2 +1,3 @@
 package flow
+func TestWork(t *testing.T) {}
`

func TestFilterUnifiedDiff(t *testing.T) {
	t.Parallel()

	ga := ParseGitattributes("*.pb.go linguist-generated\n", "")
	filtered := FilterUnifiedDiff(sampleUnifiedDiff, func(path string) bool {
		return IsLockfile(path) || ga.IsGeneratedOrNoDiff(path)
	})

	assert.Contains(t, filtered, "diff --git a/internal/flow/work.go b/internal/flow/work.go")
	assert.Contains(t, filtered, "diff --git a/internal/flow/work_test.go b/internal/flow/work_test.go")
	assert.NotContains(t, filtered, "package-lock.json")
	assert.NotContains(t, filtered, "proto/user.pb.go")
}

func TestFilterUnifiedDiffAllExcluded(t *testing.T) {
	t.Parallel()

	lockDiff := `diff --git a/go.sum b/go.sum
index 1111111..2222222 100644
--- a/go.sum
+++ b/go.sum
@@ -1 +1 @@
-old
+new
`
	filtered := FilterUnifiedDiff(lockDiff, IsLockfile)
	assert.Empty(t, filtered)
}

func TestInjectDiffReference(t *testing.T) {
	t.Parallel()

	summary := `# Step 1 Work: Add authentication

## Summary
Added auth logic.

## Key Changes
- auth.go: added token verification

COMMIT: feat: auth
`

	diffPath := "/path/to/.agents/local/s1/step-1.diff"
	injected := InjectDiffReference(summary, diffPath)

	assert.Contains(t, injected, "## Diff\n- Unified diff: `/path/to/.agents/local/s1/step-1.diff`")
	assert.Contains(t, injected, "# Step 1 Work: Add authentication")
	assert.Contains(t, injected, "## Summary\nAdded auth logic.")
	assert.Contains(t, injected, "COMMIT: feat: auth")

	// Updating already injected summary should replace existing ## Diff section without duplicating
	updated := InjectDiffReference(injected, "/new/path/to/step-1.diff")
	assert.Contains(t, updated, "## Diff\n- Unified diff: `/new/path/to/step-1.diff`")
	assert.NotContains(t, updated, "/path/to/.agents/local/s1/step-1.diff")
	assert.Equal(t, 1, strings.Count(updated, "## Diff"))
}

func TestParseAndFormatDiffStat(t *testing.T) {
	t.Parallel()

	sampleDiff := `diff --git a/existing.go b/existing.go
index 1111111..2222222 100644
--- a/existing.go
+++ b/existing.go
@@ -1,5 +1,6 @@
 unchanged
-deleted line 1
-deleted line 2
+added line 1
+added line 2
+added line 3
 unchanged
diff --git a/new.go b/new.go
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/new.go
@@ -0,0 +1,4 @@
+line 1
+line 2
+line 3
+line 4
diff --git a/old.go b/old.go
deleted file mode 100644
index 4444444..0000000
--- a/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-old 1
-old 2
-old 3
`

	stat := ParseDiffStat(sampleDiff)
	assert.Equal(t, 1, stat.FilesModified)
	assert.Equal(t, 1, stat.FilesAdded)
	assert.Equal(t, 1, stat.FilesDeleted)
	assert.Equal(t, 3, stat.TotalFiles())
	assert.Equal(t, 7, stat.LinesAdded)
	assert.Equal(t, 5, stat.LinesDeleted)

	plain := FormatDiffStat(stat, false)
	assert.Equal(t, "+7 -5 (1 edited, 1 added, 1 deleted)", plain)

	styled := FormatDiffStat(stat, true)
	assert.Contains(t, styled, "\x1b[") // has ANSI color codes
	assert.Contains(t, styled, "+7")
	assert.Contains(t, styled, "-5")

	// Empty diff
	emptyStat := ParseDiffStat("")
	assert.Equal(t, 0, emptyStat.TotalFiles())
	assert.Empty(t, FormatDiffStat(emptyStat, false))
}
