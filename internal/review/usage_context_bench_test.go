package review

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kalverra/agents/pkg/git"
)

func BenchmarkExtractSymbolsToScan(b *testing.B) {
	diff := &git.BranchDiffResult{
		Files: []git.FileDiff{
			{
				Path:   "core/config.go",
				Status: "modified",
				Patch: `@@ -10,12 +10,12 @@
-	val := os.Getenv("CTF_CONFIGS")
+	val := os.Getenv("NEW_CONFIGS")
-	apiKey := os.Getenv("SERVICE_API_KEY")
+	apiKey := os.Getenv("NEW_API_KEY")
-func (s *Server) OldExportedMethod() error {
+func (s *Server) OldExportedMethod(ctx context.Context) error {
-func DeprecatedHelper() string {
-type OldConfig struct {
`,
			},
			{
				Path:   "legacy/cleanup.go",
				Status: "deleted",
			},
			{
				Path:   "api/client.go",
				Status: "modified",
				Patch: `@@ -45,6 +45,6 @@
-	export async function OldAPICall(param: string) {
-	export const OLD_ENDPOINT = "/api/v1/old"
-	def old_python_helper(x, y):
`,
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		symbols := ExtractSymbolsToScan(diff)
		if len(symbols) == 0 {
			b.Fatal("expected symbols to be extracted")
		}
	}
}

func BenchmarkFindExternalReferences(b *testing.B) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		b.Fatal(err)
	}

	diff := &git.BranchDiffResult{
		Files: []git.FileDiff{
			{
				Path:   "internal/git/git.go",
				Status: "modified",
				Patch: `@@ -26,4 +26,4 @@
-func DetectRepo(dir string) (*RepoInfo, error) {
+func DetectRepo(dir string, opts ...any) (*RepoInfo, error) {
-	token := os.Getenv("GITHUB_TOKEN")
+	token := os.Getenv("NEW_GITHUB_TOKEN")
`,
			},
		},
	}

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		usages, err := FindExternalReferences(ctx, repoRoot, diff, nil)
		if err != nil {
			b.Fatal(err)
		}
		_ = usages
	}
}
