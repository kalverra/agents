package review

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/kalverra/agents/pkg/git"
)

// CalculateRiskScore computes a risk score for a file diff.
func CalculateRiskScore(f git.FileDiff, unresolvedThreads int) int {
	classification := ClassifyFile(f)

	switch classification.Tier {
	case TierFormatting:
		return -1000 + min(f.Additions+f.Deletions, 20)
	case TierUnreadable:
		return -500
	case TierDeletion:
		return -200 + unresolvedThreads*100
	}

	score := 0
	// 1. Thread impact
	score += unresolvedThreads * 100

	// 2. Sensitive / critical paths
	lowerPath := strings.ToLower(filepath.ToSlash(f.Path))
	switch {
	case containsAny(lowerPath, "config", ".env", "settings", "secret", "credentials"):
		score += 60
	case containsAny(lowerPath, "auth", "token", "crypto", "security", "rbac", "permission"):
		score += 50
	case containsAny(lowerPath, "migration", "schema", "db", "models", "sql"):
		score += 45
	case containsAny(lowerPath, "router", "route", "api", "server", "endpoint", "handler"):
		score += 40
	case containsAny(lowerPath, "workflow", ".github", "ci", "docker", "deploy"):
		score += 35
	}

	// 3. Code churn
	score += min(f.Additions+f.Deletions, 50)

	return score
}

// RankFiles sorts a slice of file diffs by risk score descending.
func RankFiles(files []git.FileDiff, threadCounts map[string]int) []git.FileDiff {
	type scoredFile struct {
		file  git.FileDiff
		score int
	}

	scored := make([]scoredFile, len(files))
	for i, f := range files {
		unres := 0
		if threadCounts != nil {
			unres = threadCounts[f.Path]
		}
		scored[i] = scoredFile{
			file:  f,
			score: CalculateRiskScore(f, unres),
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].file.Path < scored[j].file.Path
	})

	result := make([]git.FileDiff, len(files))
	for i := range scored {
		result[i] = scored[i].file
	}
	return result
}

func containsAny(s string, keywords ...string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}
