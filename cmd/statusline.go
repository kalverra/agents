package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/statusline"
	"github.com/kalverra/agents/pkg/git"
	"github.com/kalverra/agents/pkg/output"
)

var statuslineCmd = &cobra.Command{
	Use:   "statusline",
	Short: "Render statusline for agent",
	RunE: func(_ *cobra.Command, _ []string) error {
		inputBytes, err := io.ReadAll(os.Stdin)
		if err != nil || len(strings.TrimSpace(string(inputBytes))) == 0 {
			inputBytes = []byte("{}")
		}
		out, err := statusline.Render(inputBytes)
		if err != nil {
			fmt.Println("agy | error")
			return nil
		}
		fmt.Println(out)
		return nil
	},
}

var (
	statuslineStackCWD    string
	statuslineStackSymbol string
	statuslineStackJSON   bool
)

var statuslineStackCmd = &cobra.Command{
	Use:   "stack",
	Short: "Render GitHub stack prompt status",
	RunE: func(_ *cobra.Command, args []string) error {
		if statuslineStackJSON {
			inputBytes, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			out, err := git.FormatStackJSON(inputBytes)
			if err != nil {
				return err
			}
			if out != "" && statuslineStackSymbol != "" {
				out = statuslineStackSymbol + out
			}
			output.Write("statusline-stack", map[string]string{"stack": out}, func() {
				if out != "" {
					fmt.Print(out)
				}
			})
			return nil
		}

		cwd := statuslineStackCWD
		if cwd == "" {
			if len(args) > 0 {
				cwd = args[0]
			} else {
				dir, err := os.Getwd()
				if err == nil {
					cwd = dir
				}
			}
		}

		data, err := git.DetectStack(cwd, "")
		if err != nil || data == nil {
			output.Write("statusline-stack", map[string]any{"stack": ""}, func() {})
			return nil
		}

		out := git.FormatStack(*data)
		if out != "" && statuslineStackSymbol != "" {
			out = statuslineStackSymbol + out
		}

		output.Write("statusline-stack", map[string]any{
			"stack": out,
			"data":  data,
		}, func() {
			if out != "" {
				fmt.Print(out)
			}
		})
		return nil
	},
}

func init() {
	statuslineStackCmd.Flags().StringVar(&statuslineStackCWD, "cwd", "", "Working directory to inspect")
	statuslineStackCmd.Flags().
		StringVar(&statuslineStackSymbol, "symbol", "", "Symbol prefix to prepend if stack is active")
	statuslineStackCmd.Flags().BoolVar(&statuslineStackJSON, "json", false, "Read stack JSON from stdin")
	statuslineCmd.AddCommand(statuslineStackCmd)
	rootCmd.AddCommand(statuslineCmd)
}
