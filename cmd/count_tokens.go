package cmd

import (
	"fmt"
	"io"
	"os"

	tiktoken "github.com/pkoukk/tiktoken-go"
	"github.com/spf13/cobra"

	"github.com/kalverra/agents/pkg/output"
)

var countTokensCmd = &cobra.Command{
	Use:   "count-tokens [FILE]",
	Short: "Count LLM tokens in a file, string, or stdin",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		str, _ := cmd.Flags().GetString("string")
		encoding, _ := cmd.Flags().GetString("encoding")
		model, _ := cmd.Flags().GetString("model")
		verbose, _ := cmd.Flags().GetBool("verbose")

		var text string
		switch {
		case str != "":
			text = str
		case len(args) > 0:
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			text = string(data)
		default:
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			text = string(data)
		}

		var enc *tiktoken.Tiktoken
		var encName string

		if model != "" {
			e, err := tiktoken.EncodingForModel(model)
			if err != nil {
				return fmt.Errorf("unknown model for tiktoken: %s", model)
			}
			enc = e
			encName = fmt.Sprintf("model:%s", model)
		} else {
			e, err := tiktoken.GetEncoding(encoding)
			if err != nil {
				return fmt.Errorf("unknown encoding: %s", encoding)
			}
			enc = e
			encName = encoding
		}

		tokens := enc.Encode(text, nil, nil)
		n := len(tokens)

		if verbose {
			output.Warnf("encoding: %s\n", encName)
			output.Warnf("chars:  %d\n", len(text))
			output.Warnf("tokens: %d\n", n)
		} else {
			output.Write("count-tokens", map[string]int{"tokens": n}, func() {
				fmt.Println(n) // Keep simple for piping if not in AI mode
			})
		}

		return nil
	},
}

func init() {
	countTokensCmd.Flags().StringP("string", "s", "", "Count tokens in this string instead of a file")
	countTokensCmd.Flags().String("encoding", "cl100k_base", "tiktoken encoding name")
	countTokensCmd.Flags().String("model", "", "Map via tiktoken model (e.g. gpt-4o)")
	countTokensCmd.Flags().BoolP("verbose", "v", false, "Print encoding, char count, and token count")
	rootCmd.AddCommand(countTokensCmd)
}
