package git

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanFilePatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		wantContains []string
		wantOut      []string
	}{
		{
			name: "strips diff --git and index lines",
			input: "diff --git a/foo.go b/foo.go\n" +
				"index abc123..def456 100644\n" +
				"--- a/foo.go\n" +
				"+++ b/foo.go\n" +
				"@@ -1,3 +1,4 @@\n" +
				" line1\n" +
				"-line2\n" +
				"+line2 modified\n" +
				" line3\n",
			wantContains: []string{"@@ -1,3 +1,4 @@", " line1", "-line2", "+line2 modified"},
			wantOut:      []string{"diff --git", "index abc123", "--- a/", "+++ b/"},
		},
		{
			name: "strips new file mode",
			input: "diff --git a/new.txt b/new.txt\n" +
				"new file mode 100644\n" +
				"index 0000000..abc1234\n" +
				"--- /dev/null\n" +
				"+++ b/new.txt\n" +
				"@@ -0,0 +1,2 @@\n" +
				"+line one\n" +
				"+line two\n",
			wantContains: []string{"@@ -0,0 +1,2 @@", "+line one", "+line two"},
			wantOut:      []string{"new file mode", "index 0000000", "--- /dev/null", "+++ b/"},
		},
		{
			name: "strips deleted file mode",
			input: "diff --git a/removed.go b/removed.go\n" +
				"deleted file mode 100644\n" +
				"index abc123..0000000\n" +
				"--- a/removed.go\n" +
				"+++ /dev/null\n" +
				"@@ -1,2 +0,0 @@\n" +
				"-line one\n" +
				"-line two\n",
			wantContains: []string{"@@ -1,2 +0,0 @@", "-line one", "-line two"},
			wantOut:      []string{"deleted file mode", "--- a/", "+++ /dev/null"},
		},
		{
			name: "strips no-newline annotation",
			input: "diff --git a/foo.txt b/foo.txt\n" +
				"index abc..def 100644\n" +
				"--- a/foo.txt\n" +
				"+++ b/foo.txt\n" +
				"@@ -1 +1 @@\n" +
				"-old\n" +
				`\ No newline at end of file` + "\n" +
				"+new\n",
			wantContains: []string{"@@ -1 +1 @@", "-old", "+new"},
			wantOut:      []string{`\ No newline at end of file`},
		},
		{
			name: "trims context glued to hunk header",
			input: "diff --git a/foo.go b/foo.go\n" +
				"--- a/foo.go\n" +
				"+++ b/foo.go\n" +
				"@@ -102,7 +102,7 @@ \t\t\t\t}\n" +
				" \t\t\t}\n" +
				"\n" +
				" \t\t\tif anyFound {\n" +
				"-\t\t\t\told\n" +
				"+\t\t\t\tnew\n",
			wantContains: []string{"@@ -102,7 +102,7 @@", " \t\t\t}", "-\t\t\t\told", "+\t\t\t\tnew"},
			wantOut:      []string{"@@ -102,7 +102,7 @@ \t\t\t\t}"},
		},
		{
			name:         "empty input returns empty",
			input:        "",
			wantContains: nil,
			wantOut:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := cleanFilePatch(tt.input)
			for _, w := range tt.wantContains {
				assert.Contains(t, result, w)
			}
			for _, w := range tt.wantOut {
				assert.NotContains(t, result, w)
			}
		})
	}
}

func TestFormatHuman(t *testing.T) {
	t.Parallel()

	modifiedGoPatch := "diff --git a/main.go b/main.go\n" +
		"index abc..def 100644\n" +
		"--- a/main.go\n" +
		"+++ b/main.go\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new\n"

	tests := []struct {
		name         string
		result       *BranchDiffResult
		wantContains []string
		wantOut      []string
	}{
		{
			name: "header contains branch names and merge-base",
			result: &BranchDiffResult{
				BaseBranch:    "main",
				CurrentBranch: "feature",
				MergeBase:     "abc1234",
				Files:         []FileDiff{},
			},
			wantContains: []string{"main", "feature", "abc1234"},
		},
		{
			name: "local changes label when on base branch",
			result: &BranchDiffResult{
				BaseBranch:      "main",
				CurrentBranch:   "main",
				MergeBase:       "abc1234",
				HasLocalChanges: true,
				Files: []FileDiff{
					{Path: "main.go", Status: "modified", Additions: 1, Deletions: 0, Patch: "@@\n+x\n"},
				},
			},
			wantContains: []string{"main (local changes)"},
		},
		{
			name: "no-change result says no changes",
			result: &BranchDiffResult{
				BaseBranch: "main", CurrentBranch: "main", MergeBase: "abc1234",
				Files: []FileDiff{},
			},
			wantContains: []string{"No changes"},
		},
		{
			name: "separates code and tooling stats",
			result: &BranchDiffResult{
				BaseBranch: "main", CurrentBranch: "feature", MergeBase: "abc1234",
				Files: []FileDiff{
					{Path: "main.go", Status: "modified", Additions: 10, Deletions: 2, Patch: modifiedGoPatch},
					{
						Path:      ".serena/config.yml",
						Status:    "added",
						Additions: 5,
						Deletions: 0,
						Patch:     "@@ -0,0 +1 @@\n+config\n",
					},
				},
			},
			wantContains: []string{
				"Code:",
				"Tooling:",
				"<file path=\"main.go\" status=\"modified\"",
				"<file path=\".serena/config.yml\" status=\"added\"",
			},
		},
		{
			name: "file header includes per-file stats",
			result: &BranchDiffResult{
				BaseBranch: "main", CurrentBranch: "feature", MergeBase: "abc1234",
				Files: []FileDiff{
					{Path: "main.go", Status: "modified", Additions: 10, Deletions: 2, Patch: modifiedGoPatch},
				},
			},
			wantContains: []string{"<file path=\"main.go\" status=\"modified\" additions=\"10\" deletions=\"2\">"},
		},
		{
			name: "truncates large added config file",
			result: &BranchDiffResult{
				BaseBranch: "main", CurrentBranch: "feature", MergeBase: "abc1234",
				Files: []FileDiff{
					{
						Path:      "config.yml",
						Status:    "added",
						Additions: 100,
						Deletions: 0,
						Patch:     strings.Repeat("+line\n", 100),
					},
				},
			},
			wantContains: []string{"truncated", "100"},
			wantOut:      []string{strings.Repeat("+line\n", 5)},
		},
		{
			name: "does not truncate small added config file",
			result: &BranchDiffResult{
				BaseBranch: "main", CurrentBranch: "feature", MergeBase: "abc1234",
				Files: []FileDiff{
					{
						Path:      ".gitignore",
						Status:    "added",
						Additions: 5,
						Deletions: 0,
						Patch:     "+line1\n+line2\n+line3\n+line4\n+line5\n",
					},
				},
			},
			wantContains: []string{"+line1"},
			wantOut:      []string{"truncated"},
		},
		{
			name: "does not truncate modified config file",
			result: &BranchDiffResult{
				BaseBranch: "main", CurrentBranch: "feature", MergeBase: "abc1234",
				Files: []FileDiff{
					{
						Path:      "config.yml",
						Status:    "modified",
						Additions: 100,
						Deletions: 1,
						Patch:     strings.Repeat("+line\n", 100) + "-old\n",
					},
				},
			},
			wantContains: []string{"+line"},
			wantOut:      []string{"truncated"},
		},
		{
			name: "omits patch for dependency and lockfiles",
			result: &BranchDiffResult{
				BaseBranch: "main", CurrentBranch: "feature", MergeBase: "abc1234",
				Files: []FileDiff{
					{
						Path:      "go.sum",
						Status:    "modified",
						Additions: 100,
						Deletions: 10,
						Patch:     "@@ -1 +1 @@\n-old\n+new\n",
					},
					{
						Path:      "vendor/foo/bar.go",
						Status:    "added",
						Additions: 50,
						Deletions: 0,
						Patch:     "@@ -0,0 +1 @@\n+foo\n",
					},
				},
			},
			wantContains: []string{
				"<file path=\"go.sum\" status=\"modified\" additions=\"100\" deletions=\"10\">",
				"[patch omitted — dependency/lockfile/generated]",
				"</file>",
			},
			wantOut: []string{"-old", "+new", "+foo"},
		},
		{
			name: "code files appear before tooling files",
			result: &BranchDiffResult{
				BaseBranch: "main", CurrentBranch: "feature", MergeBase: "abc1234",
				Files: []FileDiff{
					{Path: ".serena/config.yml", Status: "added", Additions: 5, Deletions: 0, Patch: "@@ +config\n"},
					{Path: "main.go", Status: "modified", Additions: 1, Deletions: 1, Patch: modifiedGoPatch},
				},
			},
			wantContains: []string{"main.go", ".serena/config.yml"},
		},
		{
			name: "git metadata stripped from code file patches",
			result: &BranchDiffResult{
				BaseBranch: "main", CurrentBranch: "feature", MergeBase: "abc1234",
				Files: []FileDiff{
					{Path: "main.go", Status: "modified", Additions: 1, Deletions: 1, Patch: modifiedGoPatch},
				},
			},
			wantContains: []string{"@@ -1 +1 @@", "-old", "+new"},
			wantOut:      []string{"diff --git", "index abc..def", "--- a/main.go", "+++ b/main.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := FormatHuman(tt.result)
			for _, w := range tt.wantContains {
				assert.Contains(t, result, w, "expected %q in output:\n%s", w, result)
			}
			for _, w := range tt.wantOut {
				assert.NotContains(t, result, w, "expected %q NOT in output:\n%s", w, result)
			}
		})
	}
}

func TestFormatHumanCodeBeforeTooling(t *testing.T) {
	t.Parallel()

	result := FormatHuman(&BranchDiffResult{
		BaseBranch: "main", CurrentBranch: "feature", MergeBase: "abc1234",
		Files: []FileDiff{
			{Path: ".serena/config.yml", Status: "added", Additions: 5, Deletions: 0, Patch: "@@ +config\n"},
			{Path: "main.go", Status: "modified", Additions: 1, Deletions: 1, Patch: "@@ -1 +1 @@\n-old\n+new\n"},
		},
	})

	goIdx := strings.Index(result, "main.go")
	ymlIdx := strings.Index(result, ".serena/config.yml")
	assert.Greater(t, goIdx, -1, "main.go not found in output")
	assert.Greater(t, ymlIdx, -1, ".serena/config.yml not found in output")
	assert.Less(t, goIdx, ymlIdx, "code file (main.go) should appear before tooling file (.serena/config.yml)")
}

func TestIsDependencyOrLockfile(t *testing.T) {
	t.Parallel()

	lockfiles := []string{
		"go.sum",
		"go.work.sum",
		"sub/pkg/go.sum",
		"package-lock.json",
		"npm-shrinkwrap.json",
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
		"custom.lock",
		"vendor/github.com/foo/bar.go",
		"node_modules/express/index.js",
	}

	for _, path := range lockfiles {
		assert.True(t, IsDependencyOrLockfile(path), "path %q should be identified as lockfile or dependency", path)
	}

	nonLockfiles := []string{
		"main.go",
		"package.json",
		"Cargo.toml",
		"Gemfile",
		"pyproject.toml",
		"README.md",
	}

	for _, path := range nonLockfiles {
		assert.False(t, IsDependencyOrLockfile(path), "path %q should not be identified as lockfile", path)
	}
}
