package review

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/agents/pkg/git"
)

func TestRankFiles(t *testing.T) {
	t.Parallel()

	files := []git.FileDiff{
		{
			Path:      "pkg/util/strings.go",
			Status:    "modified",
			Additions: 20,
			Deletions: 5,
			Patch:     "@@ -1,1 +1,2 @@\n+func Util() {}\n",
		},
		{
			Path:      "pkg/auth/token.go",
			Status:    "modified",
			Additions: 5,
			Deletions: 2,
			Patch:     "@@ -1,1 +1,2 @@\n+func VerifyToken() {}\n",
		},
		{
			Path:      "cmd/server/main.go",
			Status:    "modified",
			Additions: 2,
			Deletions: 1,
			Patch:     "@@ -1,1 +1,2 @@\n+func Main() {}\n",
		},
		{
			Path:      "pkg/util/imports.go",
			Status:    "modified",
			Additions: 10,
			Deletions: 10,
			Patch:     "@@ -1,2 +1,2 @@\n-import \"b\"\n+import \"a\"\n",
		},
	}

	// threads: cmd/server/main.go has 1 unresolved thread
	threads := map[string]int{
		"cmd/server/main.go": 1,
	}

	ranked := RankFiles(files, threads)

	// Expected order:
	// 1. cmd/server/main.go (has unresolved thread -> +100)
	// 2. pkg/auth/token.go (auth path -> +50)
	// 3. pkg/util/strings.go (semantic -> churn)
	// 4. pkg/util/imports.go (formatting tier -> bottom)
	assert.Equal(t, "cmd/server/main.go", ranked[0].Path)
	assert.Equal(t, "pkg/auth/token.go", ranked[1].Path)
	assert.Equal(t, "pkg/util/strings.go", ranked[2].Path)
	assert.Equal(t, "pkg/util/imports.go", ranked[3].Path)
}
