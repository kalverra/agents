package cmd

import (
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/install"
	"github.com/kalverra/agents/pkg/output"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the rules and permissions agents install wrote",
	Long: `Uninstall removes the rules block and the permission entries that
"agents install" recorded in installed.json. Entries you had before install
are kept. gh skill cannot remove skills, so uninstall lists their dirs for you
to delete.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		inst, err := newInstaller()
		if err != nil {
			return err
		}
		if len(inst.Record.Harnesses) == 0 {
			output.Write("uninstall", &install.Result{}, func() { output.Println("Nothing to uninstall.") })
			return nil
		}

		names := slices.Sorted(maps.Keys(inst.Record.Harnesses))
		output.Println("Remove agents rules and permissions from:", strings.Join(names, ", "))
		if !yes {
			if inst.Choose == nil {
				return errors.New("refusing to uninstall without --yes (non-interactive terminal or --ai-output)")
			}
			if err := install.Confirm("Proceed with uninstall?"); err != nil {
				return err
			}
		}

		res, uninstallErr := inst.Uninstall()
		if err := inst.Record.Save(); err != nil {
			return errors.Join(uninstallErr, err)
		}
		if uninstallErr != nil {
			return uninstallErr
		}
		output.Write("uninstall", res, func() { printResult(res) })
		return nil
	},
}

func init() {
	uninstallCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	rootCmd.AddCommand(uninstallCmd)
}
