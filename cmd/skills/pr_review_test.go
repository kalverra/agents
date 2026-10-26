package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/review"
)

func TestPRReviewCmd_Flags(t *testing.T) {
	t.Parallel()

	flags := PRReviewCmd.Flags()
	assert.NotNil(t, flags.Lookup("dir"))
	assert.NotNil(t, flags.Lookup("owner"))
	assert.NotNil(t, flags.Lookup("repo"))
	assert.NotNil(t, flags.Lookup("branch"))
	assert.NotNil(t, flags.Lookup("base"))
	assert.NotNil(t, flags.Lookup("context"))
	assert.NotNil(t, flags.Lookup("include-resolved"))
	assert.NotNil(t, flags.Lookup("max-inline-lines"))
}

func TestPRReviewCmd_Subcommands_Flags(t *testing.T) {
	t.Parallel()

	cleanCmd, _, err := PRReviewCmd.Find([]string{"clean"})
	require.NoError(t, err)
	assert.NotNil(t, cleanCmd.Flags().Lookup("all"))
	assert.NotNil(t, cleanCmd.Flags().Lookup("dir"))

	listCmd, _, err := PRReviewCmd.Find([]string{"list"})
	require.NoError(t, err)
	assert.NotNil(t, listCmd.Flags().Lookup("dir"))

	updateCmd, _, err := PRReviewCmd.Find([]string{"update-description"})
	require.NoError(t, err)
	assert.NotNil(t, updateCmd.Flags().Lookup("file"))
	assert.NotNil(t, updateCmd.Flags().Lookup("dir"))
	assert.NotNil(t, updateCmd.Flags().Lookup("dry-run"))
}

func TestPRReviewCleanCmd_All(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	_, reviewsDir, err := review.LocalReviewsDir(tmp)
	require.NoError(t, err)

	b1 := filepath.Join(reviewsDir, "1-first-pr")
	b2 := filepath.Join(reviewsDir, "2-second-pr")
	require.NoError(t, os.MkdirAll(b1, 0o750))
	require.NoError(t, os.MkdirAll(b2, 0o750))

	rootCmd := &cobra.Command{Use: "root"}
	rootCmd.AddCommand(PRReviewCmd)
	rootCmd.SetArgs([]string{"pr-review", "clean", "--all", "--dir", tmp})

	err = rootCmd.ExecuteContext(context.Background())
	require.NoError(t, err)

	assert.NoDirExists(t, b1)
	assert.NoDirExists(t, b2)
}

func TestPRReviewListCmd(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	_, reviewsDir, err := review.LocalReviewsDir(tmp)
	require.NoError(t, err)

	b1 := filepath.Join(reviewsDir, "10-cool-feature")
	require.NoError(t, os.MkdirAll(b1, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(b1, "review.md"), []byte("# Review"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(b1, "description.md"), []byte("## Intent"), 0o600))

	rootCmd := &cobra.Command{Use: "root"}
	rootCmd.AddCommand(PRReviewCmd)
	rootCmd.SetArgs([]string{"pr-review", "list", "--dir", tmp})

	err = rootCmd.ExecuteContext(context.Background())
	require.NoError(t, err)
}

func TestPRReviewCleanCmd_SingleEntryFallback(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	_, reviewsDir, err := review.LocalReviewsDir(tmp)
	require.NoError(t, err)

	b1 := filepath.Join(reviewsDir, "42-auth-token")
	require.NoError(t, os.MkdirAll(b1, 0o750))

	rootCmd := &cobra.Command{Use: "root"}
	rootCmd.AddCommand(PRReviewCmd)
	rootCmd.SetArgs([]string{"pr-review", "clean", "--dir", tmp})

	err = rootCmd.ExecuteContext(context.Background())
	require.NoError(t, err)

	assert.NoDirExists(t, b1)
}
