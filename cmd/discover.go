package cmd

import (
	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/harness"
	"github.com/kalverra/agents/pkg/output"
)

var discoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "List detected harnesses and their install paths",
	RunE: func(_ *cobra.Command, _ []string) error {
		type info struct {
			Name         string `json:"name"`
			Detected     bool   `json:"detected"`
			Instructions string `json:"instructions"`
			Skills       string `json:"skills"`
		}

		var results []info
		anyFound := false
		for _, h := range harness.All() {
			r := info{Name: h.Name(), Detected: h.Detect(), Instructions: h.InstructionsPath(), Skills: h.SkillsDir()}
			anyFound = anyFound || r.Detected
			results = append(results, r)
		}

		output.Write("discover", results, func() {
			output.Println("Discovery (signals only; tools need not be running):")
			output.Println()
			for _, r := range results {
				if !r.Detected {
					output.Printf("  %-12s no\n", r.Name)
					continue
				}
				output.Printf("  %-12s yes   instructions -> %s\n", r.Name, r.Instructions)
				output.Printf("                     skills       -> %s\n", r.Skills)
			}
			output.Println()
			if anyFound {
				output.Println("Run: agents install [--dry-run]")
			} else {
				output.Println("No harnesses detected. Install one first, or force paths with install --targets.")
			}
		})
		return nil
	},
}

func init() {
	rootCmd.AddCommand(discoverCmd)
}
