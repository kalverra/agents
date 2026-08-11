package review

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"

	"github.com/kalverra/agents/internal/eval"
)

// Finalizer checks pending report files for user-reviewed frontmatter and saves eval records.
type Finalizer struct {
	ReportsDir string
	EvalDir    string
	Logger     zerolog.Logger
}

// FinalizeResult holds the outcome of a single report finalization.
type FinalizeResult struct {
	ReportPath string
	RecordID   string
	Score      int
	Reason     string
}

// Run scans ReportsDir for reports with eval_status: reviewed, extracts the score/reason/corrected body,
// loads the matching pending eval record, updates it with the corrected response, and saves a finalized record.
func (f *Finalizer) Run(_ context.Context) ([]FinalizeResult, error) {
	var results []FinalizeResult

	entries, err := os.ReadDir(f.ReportsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return results, nil
		}
		return nil, fmt.Errorf("read reports dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(f.ReportsDir, entry.Name())
		contentBytes, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			f.Logger.Warn().Err(err).Str("file", entry.Name()).Msg("Failed to read report")
			continue
		}
		content := string(contentBytes)

		frontmatter, body, err := ParseReportFrontmatter(content)
		if err != nil {
			f.Logger.Debug().Err(err).Str("file", entry.Name()).Msg("Failed to parse frontmatter")
			continue
		}

		if frontmatter.EvalStatus != "reviewed" {
			continue
		}

		// entry.Name(): 2026-08-11-daily.md
		parts := strings.Split(strings.TrimSuffix(entry.Name(), ".md"), "-")
		if len(parts) < 4 { // YYYY-MM-DD-mode
			f.Logger.Warn().Str("file", entry.Name()).Msg("Invalid report filename format")
			continue
		}
		date := parts[0] + parts[1] + parts[2]
		mode := parts[3]

		// pending_activity-<mode>-<date>-<time>.json
		prefix := fmt.Sprintf("pending_activity-%s-%s-", mode, date)

		evalEntries, err := os.ReadDir(f.EvalDir)
		if err != nil {
			f.Logger.Warn().Err(err).Msg("Failed to read eval dir")
			continue
		}

		var pendingRecordPath string
		for _, evalEntry := range evalEntries {
			if strings.HasPrefix(evalEntry.Name(), prefix) && strings.HasSuffix(evalEntry.Name(), ".json") {
				pendingRecordPath = filepath.Join(f.EvalDir, evalEntry.Name())
				break
			}
		}

		if pendingRecordPath == "" {
			f.Logger.Warn().Str("file", entry.Name()).Msg("No matching pending eval record found")
			continue
		}

		rec, err := eval.LoadLiveFeedback(pendingRecordPath)
		if err != nil {
			f.Logger.Warn().Err(err).Str("path", pendingRecordPath).Msg("Failed to load pending record")
			continue
		}

		rec.UserScore = frontmatter.Score
		rec.UserReason = frontmatter.Reason
		rec.CorrectedResponse = strings.TrimSpace(body)

		if _, err := eval.SaveLiveFeedback(f.EvalDir, *rec); err != nil {
			f.Logger.Warn().Err(err).Str("id", rec.ID).Msg("Failed to save finalized record")
			continue
		}

		_ = os.Remove(pendingRecordPath)
		pendingTranscriptPath := strings.TrimSuffix(pendingRecordPath, ".json") + ".transcript.txt"
		_ = os.Remove(pendingTranscriptPath)

		newContent := strings.Replace(content, "eval_status: reviewed", "eval_status: finalized", 1)
		if err := os.WriteFile(path, []byte(newContent), 0600); err != nil { //nolint:gosec
			f.Logger.Warn().Err(err).Str("file", entry.Name()).Msg("Failed to update report eval_status")
			continue
		}

		results = append(results, FinalizeResult{
			ReportPath: path,
			RecordID:   rec.ID,
			Score:      frontmatter.Score,
			Reason:     frontmatter.Reason,
		})
	}

	return results, nil
}
