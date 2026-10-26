package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectDefaultBranch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T) string
		want    string
		wantErr bool
	}{
		{
			name: "origin HEAD points to main",
			setup: func(t *testing.T) string {
				dir := initTestRepo(t)
				mainHash := addAndCommit(t, dir, "README.md", "base\n", "init")
				renameDefaultBranch(t, dir, "main")
				setOriginDefault(t, dir, "main", mainHash)
				checkoutBranch(t, dir, "feature", true)
				addAndCommit(t, dir, "feature.txt", "feature\n", "feature work")
				return dir
			},
			want: "main",
		},
		{
			name: "falls back to master",
			setup: func(t *testing.T) string {
				dir := initTestRepo(t)
				addAndCommit(t, dir, "README.md", "base\n", "init")
				return dir
			},
			want: "master",
		},
		{
			name: "falls back to local main",
			setup: func(t *testing.T) string {
				dir := initTestRepo(t)
				addAndCommit(t, dir, "README.md", "base\n", "init")
				renameDefaultBranch(t, dir, "main")
				return dir
			},
			want: "main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := tt.setup(t)
			got, err := DetectDefaultBranch(dir)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
