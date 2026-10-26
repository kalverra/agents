package cmd

import (
	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/buildinfo"
	"github.com/kalverra/agents/pkg/output"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the agents version",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		type info struct {
			Version string `json:"version"`
			Commit  string `json:"commit,omitempty"`
		}
		v := info{Version: buildinfo.Version(), Commit: buildinfo.Commit()}
		output.Write("version", v, func() {
			if v.Commit == "" {
				output.Println(v.Version)
				return
			}
			output.Printf("%s (%s)\n", v.Version, v.Commit)
		})
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
