package git

import (
	"path/filepath"
	"testing"
)

func BenchmarkBranchDiff(b *testing.B) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		b.Fatal(err)
	}

	opts := BranchDiffOptions{
		Base:         "main",
		ContextLines: 3,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res, err := BranchDiff(repoRoot, opts)
		if err != nil {
			b.Fatal(err)
		}
		_ = res
	}
}

func BenchmarkCheckMergeability(b *testing.B) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res, err := CheckMergeability(repoRoot, "main", "HEAD")
		if err != nil {
			b.Fatal(err)
		}
		_ = res
	}
}
