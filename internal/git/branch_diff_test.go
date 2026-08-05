package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wantFile struct {
	path   string
	status string
}

func TestBranchDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		setup             func(t *testing.T) (dir string, opts BranchDiffOptions)
		wantBase          string
		wantInPatch       []string
		wantNotInPatch    []string
		wantFile          *wantFile
		wantEmpty         bool
		checkLocalChanges bool
		wantLocalChanges  bool
	}{
		{
			name: "feature commit ahead of main",
			setup: func(t *testing.T) (string, BranchDiffOptions) {
				dir := initTestRepo(t)
				mainHash := addAndCommit(t, dir, "README.md", "base\n", "init")
				renameDefaultBranch(t, dir, "main")
				setOriginDefault(t, dir, "main", mainHash)
				checkoutBranch(t, dir, "feature", true)
				addAndCommit(t, dir, "feature.txt", "feature line\n", "feature work")
				return dir, BranchDiffOptions{}
			},
			wantBase:    "main",
			wantInPatch: []string{"feature.txt", "+feature line"},
		},
		{
			name: "unstaged worktree change",
			setup: func(t *testing.T) (string, BranchDiffOptions) {
				dir := initTestRepo(t)
				mainHash := addAndCommit(t, dir, "README.md", "base\n", "init")
				renameDefaultBranch(t, dir, "main")
				setOriginDefault(t, dir, "main", mainHash)
				checkoutBranch(t, dir, "feature", true)
				addAndCommit(t, dir, "feature.txt", "committed\n", "feature work")
				writeFile(t, dir, "feature.txt", "committed\nlocal edit\n")
				return dir, BranchDiffOptions{}
			},
			wantBase:    "main",
			wantInPatch: []string{"+local edit"},
		},
		{
			name: "staged change without commit",
			setup: func(t *testing.T) (string, BranchDiffOptions) {
				dir := initTestRepo(t)
				mainHash := addAndCommit(t, dir, "README.md", "base\n", "init")
				renameDefaultBranch(t, dir, "main")
				setOriginDefault(t, dir, "main", mainHash)
				checkoutBranch(t, dir, "feature", true)
				writeFile(t, dir, "staged.txt", "staged content\n")
				runGitCmd(t, dir, "add", "staged.txt")
				return dir, BranchDiffOptions{}
			},
			wantBase:    "main",
			wantInPatch: []string{"staged.txt", "+staged content"},
		},
		{
			name: "clean on default branch",
			setup: func(t *testing.T) (string, BranchDiffOptions) {
				dir := initTestRepo(t)
				mainHash := addAndCommit(t, dir, "README.md", "base\n", "init")
				renameDefaultBranch(t, dir, "main")
				setOriginDefault(t, dir, "main", mainHash)
				return dir, BranchDiffOptions{}
			},
			wantBase:  "main",
			wantEmpty: true,
		},
		{
			name: "explicit base override",
			setup: func(t *testing.T) (string, BranchDiffOptions) {
				dir := initTestRepo(t)
				addAndCommit(t, dir, "README.md", "base\n", "init")
				addAndCommit(t, dir, "second.txt", "second\n", "second")
				checkoutBranch(t, dir, "feature", true)
				addAndCommit(t, dir, "feature.txt", "feature\n", "feature")
				return dir, BranchDiffOptions{Base: "master"}
			},
			wantBase:    "master",
			wantInPatch: []string{"feature.txt", "+feature"},
		},
		{
			name: "binary file change",
			setup: func(t *testing.T) (string, BranchDiffOptions) {
				dir := initTestRepo(t)
				mainHash := addAndCommitBytes(t, dir, "data.bin", []byte{0x00, 0x01}, "init")
				renameDefaultBranch(t, dir, "main")
				setOriginDefault(t, dir, "main", mainHash)
				checkoutBranch(t, dir, "feature", true)
				addAndCommitBytes(t, dir, "data.bin", []byte{0x00, 0x02}, "update binary")
				return dir, BranchDiffOptions{}
			},
			wantBase: "main",
			wantFile: &wantFile{
				path:   "data.bin",
				status: "binary",
			},
		},
		{
			name: "deleted file on branch",
			setup: func(t *testing.T) (string, BranchDiffOptions) {
				dir := initTestRepo(t)
				addAndCommit(t, dir, "keep.txt", "stay\n", "init")
				mainHash := addAndCommit(t, dir, "remove.txt", "gone\n", "add remove")
				renameDefaultBranch(t, dir, "main")
				setOriginDefault(t, dir, "main", mainHash)
				checkoutBranch(t, dir, "feature", true)
				require.NoError(t, os.Remove(filepath.Join(dir, "remove.txt")))
				runGitCmd(t, dir, "add", "remove.txt")
				runGitCmd(t, dir, "commit", "--no-verify", "-m", "delete file")
				return dir, BranchDiffOptions{}
			},
			wantBase: "main",
			wantFile: &wantFile{
				path:   "remove.txt",
				status: "deleted",
			},
		},
		{
			name: "local changes on default branch",
			setup: func(t *testing.T) (string, BranchDiffOptions) {
				dir := initTestRepo(t)
				mainHash := addAndCommit(t, dir, "README.md", "base\n", "init")
				renameDefaultBranch(t, dir, "main")
				setOriginDefault(t, dir, "main", mainHash)
				writeFile(t, dir, "README.md", "base\nlocal edit\n")
				return dir, BranchDiffOptions{}
			},
			wantBase:          "main",
			checkLocalChanges: true,
			wantLocalChanges:  true,
			wantInPatch:       []string{"+local edit"},
		},
		{
			name: "use upstream branch for base",
			setup: func(t *testing.T) (string, BranchDiffOptions) {
				dir := initTestRepo(t)
				mainHash := addAndCommit(t, dir, "README.md", "base\n", "init")
				renameDefaultBranch(t, dir, "main")
				setOriginDefault(t, dir, "main", mainHash)

				// simulate upstream moving forward
				runGitCmd(t, dir, "checkout", "-b", "upstream_tmp")
				advancedHash := addAndCommit(t, dir, "upstream.txt", "upstream work\n", "upstream commit")

				// set origin/main to the advanced commit
				runGitCmd(t, dir, "update-ref", "refs/remotes/origin/main", advancedHash)

				// go back to local main, which is behind
				runGitCmd(t, dir, "checkout", "main")

				// create feature branch off the advanced commit (e.g. if user branched from origin/main)
				runGitCmd(t, dir, "checkout", "-b", "feature", advancedHash)
				addAndCommit(t, dir, "feature.txt", "feature work\n", "feature commit")

				return dir, BranchDiffOptions{Base: "main"}
			},
			wantBase:       "main",
			wantInPatch:    []string{"feature.txt", "+feature work"},
			wantNotInPatch: []string{"upstream.txt", "+upstream work"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir, opts := tt.setup(t)
			result, err := BranchDiff(dir, opts)
			require.NoError(t, err)
			require.NotNil(t, result)

			assert.Equal(t, tt.wantBase, result.BaseBranch)
			if tt.checkLocalChanges {
				assert.Equal(t, tt.wantLocalChanges, result.HasLocalChanges)
			}

			if tt.wantEmpty {
				assert.Empty(t, result.Patch)
				assert.Equal(t, 0, result.Stats.FilesChanged)
				return
			}

			if tt.wantFile != nil {
				var found *FileDiff
				for i := range result.Files {
					if result.Files[i].Path == tt.wantFile.path {
						found = &result.Files[i]
						break
					}
				}
				require.NotNil(t, found, "file %q not in diff", tt.wantFile.path)
				assert.Equal(t, tt.wantFile.status, found.Status)
			}

			for _, want := range tt.wantInPatch {
				assert.Contains(t, result.Patch, want, "patch missing %q:\n%s", want, result.Patch)
			}

			for _, dontWant := range tt.wantNotInPatch {
				assert.NotContains(
					t,
					result.Patch,
					dontWant,
					"patch unexpectedly contains %q:\n%s",
					dontWant,
					result.Patch,
				)
			}
		})
	}
}

func TestBranchDiffDetachedHEAD(t *testing.T) {
	t.Parallel()

	dir := initTestRepo(t)
	hash := addAndCommit(t, dir, "README.md", "base\n", "init")
	detachHEAD(t, dir, hash)

	_, err := BranchDiff(dir, BranchDiffOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "detached")
}

//nolint:paralleltest // modifies global environment variable HOME
func TestBranchDiffGlobalIgnore(t *testing.T) {
	oldHome := os.Getenv("HOME")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	homeDir := t.TempDir()
	_ = os.Setenv("HOME", homeDir)

	gitconfig := filepath.Join(homeDir, ".gitconfig")
	globalIgnorePath := filepath.Join(homeDir, ".gitignore_global")
	err := os.WriteFile(gitconfig, []byte("[core]\n\texcludesfile = ~/.gitignore_global\n"), 0600)
	require.NoError(t, err)

	err = os.WriteFile(globalIgnorePath, []byte("*.ignored\n"), 0600)
	require.NoError(t, err)

	dir := initTestRepo(t)
	mainHash := addAndCommit(t, dir, "README.md", "base\n", "init")
	renameDefaultBranch(t, dir, "main")
	setOriginDefault(t, dir, "main", mainHash)

	writeFile(t, dir, "temp.ignored", "some ignored content\n")

	result, err := BranchDiff(dir, BranchDiffOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Empty(t, result.Patch)
	assert.Equal(t, 0, result.Stats.FilesChanged)
}
