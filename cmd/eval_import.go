package cmd

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/eval"
	"github.com/kalverra/agents/internal/output"
)

var evalImportCmd = &cobra.Command{
	Use:   "eval-import",
	Short: "Import captured live feedback JSON records into YAML eval test cases",
	RunE: func(cmd *cobra.Command, _ []string) error {
		liveDir, _ := cmd.Flags().GetString("live-dir")
		casesDir, _ := cmd.Flags().GetString("cases")

		if liveDir == "" {
			liveDir = filepath.Join("eval", "live_feedback")
		}

		count, err := eval.ImportLiveFeedbackToCases(liveDir, casesDir)
		if err != nil {
			return err
		}

		if output.JSON() {
			output.Write("eval-import", map[string]any{
				"imported":  count,
				"live_dir":  liveDir,
				"cases_dir": casesDir,
			}, nil)
			return nil
		}

		output.Successf("Imported %d live feedback records from %s to %s\n", count, liveDir, casesDir)
		return nil
	},
}

func init() {
	evalImportCmd.Flags().
		String("live-dir", filepath.Join("eval", "live_feedback"), "Directory containing live feedback JSON records")
	evalImportCmd.Flags().String("cases", filepath.Join("eval", "cases"), "Directory to save imported YAML test cases")
	rootCmd.AddCommand(evalImportCmd)
}
