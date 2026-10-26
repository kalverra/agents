package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/ghskill"
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage installed skills",
}

var skillsUpdateCmd = &cobra.Command{
	Use:                "update [gh skill update args]",
	Short:              "Update installed skills (runs gh skill update)",
	DisableFlagParsing: true,
	RunE: func(_ *cobra.Command, args []string) error {
		return ghskill.Client{}.Run(append([]string{"skill", "update"}, args...), os.Stdout, os.Stderr)
	},
}

func init() {
	skillsCmd.AddCommand(skillsUpdateCmd)
	rootCmd.AddCommand(skillsCmd)
}
