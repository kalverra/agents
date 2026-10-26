// Package skills contains skill-focused commands and flows
package skills

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/review"
	"github.com/kalverra/agents/pkg/output"
)

// PRReviewCmd builds and manages PR review bundles.
var PRReviewCmd = &cobra.Command{
	Use:   "pr-review",
	Short: "Bundle open PR review threads and branch diff into one review file",
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := cmd.Flags().GetString("dir")
		if err != nil {
			return err
		}

		owner, err := cmd.Flags().GetString("owner")
		if err != nil {
			return err
		}
		repo, err := cmd.Flags().GetString("repo")
		if err != nil {
			return err
		}
		branch, err := cmd.Flags().GetString("branch")
		if err != nil {
			return err
		}
		base, err := cmd.Flags().GetString("base")
		if err != nil {
			return err
		}
		contextLines, err := cmd.Flags().GetInt("context")
		if err != nil {
			return err
		}
		includeResolved, err := cmd.Flags().GetBool("include-resolved")
		if err != nil {
			return err
		}
		maxInlineLines, err := cmd.Flags().GetInt("max-inline-lines")
		if err != nil {
			return err
		}

		result, err := review.Build(cmd.Context(), review.BuildOptions{
			Dir:             dir,
			Owner:           owner,
			Repo:            repo,
			Branch:          branch,
			Base:            base,
			ContextLines:    contextLines,
			IncludeResolved: includeResolved,
			MaxInlineLines:  maxInlineLines,
		})
		if err != nil {
			return err
		}

		output.Write("pr-review", result, func() {
			output.Printf(
				"Saved review bundle to %s\nRead this file to review the PR and code changes.\nDraft description pre-scaffolded at %s/description.md.\nRun 'agents pr-review update-description' when approved.\n",
				result.ReviewPath,
				result.ReviewDir,
			)
			if result.CIStatus != nil && result.CIStatus.Total > 0 {
				output.Printf(
					"\nCI Status: %d/%d passing\n",
					result.CIStatus.Pass,
					result.CIStatus.Total,
				)
				if result.CIStatus.Fail > 0 {
					output.Printf("%d CI checks failing (see review bundle)\n", result.CIStatus.Fail)
				}
			}
			if len(result.ExternalUsages) > 0 {
				output.Printf(
					"\n%d symbols have remaining references outside the diff (see review bundle)\n",
					len(result.ExternalUsages),
				)
			}

			if result.Stack != nil {
				output.Printf(
					"\nStack: layer %d of %d (trunk: %s)\n",
					result.Stack.Position,
					result.Stack.Total,
					result.Stack.Trunk,
				)
				for _, layer := range result.Stack.Layers {
					currMarker := ""
					if layer.IsCurrent {
						currMarker = " (current)"
					}
					prStr := ""
					if layer.PRNumber > 0 {
						prStr = fmt.Sprintf("#%d ", layer.PRNumber)
					}
					stateStr := ""
					if layer.State != "" {
						stateStr = fmt.Sprintf("[%s] ", layer.State)
					}
					output.Printf("  %d. %s%s%s%s\n", layer.Position, prStr, stateStr, layer.Branch, currMarker)
				}
				rebaseTarget := result.Stack.PrevBranch
				if rebaseTarget == "" {
					rebaseTarget = result.Stack.Trunk
				}
				if result.Stack.RebaseStatus != nil {
					switch result.Stack.RebaseStatus.Reason {
					case review.RebaseReasonConflict:
						output.Printf("\nBranch conflicts with %s (rebase required)\n", rebaseTarget)
					case review.RebaseReasonBaseMerged:
						output.Printf(
							"\nBase branch %s merged (rebase required onto %s)\n",
							rebaseTarget,
							result.Stack.Trunk,
						)
					case review.RebaseReasonCleanBehind:
						output.Printf(
							"\nBranch mergeable with %s (behind by %d commit(s), no conflicts)\n",
							rebaseTarget,
							result.Stack.RebaseStatus.BehindCount,
						)
					case review.RebaseReasonUpToDate:
						output.Printf("\nBranch up-to-date with %s (no rebase needed)\n", rebaseTarget)
					default:
						if result.Stack.NeedsRebase {
							output.Printf("\nBranch needs rebase on %s (run `gh stack rebase`)\n", rebaseTarget)
						} else {
							output.Printf("\nBranch up-to-date with %s (no rebase needed)\n", rebaseTarget)
						}
					}
				} else if result.Stack.NeedsRebase {
					output.Printf("\nBranch needs rebase on %s (run `gh stack rebase`)\n", rebaseTarget)
				} else if rebaseTarget != "" {
					output.Printf("\nBranch up-to-date with %s (no rebase needed)\n", rebaseTarget)
				}
			} else if result.RebaseStatus != nil {
				switch result.RebaseStatus.Reason {
				case review.RebaseReasonConflict:
					output.Printf(
						"\nBranch conflicts with %s (rebase required)\n",
						result.RebaseStatus.TargetBranch,
					)
				case review.RebaseReasonCleanBehind:
					output.Printf(
						"\nBranch mergeable with %s (behind by %d commit(s), no conflicts)\n",
						result.RebaseStatus.TargetBranch,
						result.RebaseStatus.BehindCount,
					)
				case review.RebaseReasonUpToDate:
					output.Printf(
						"\nBranch up-to-date with %s (no rebase needed)\n",
						result.RebaseStatus.TargetBranch,
					)
				}
			}
		})
		return nil
	},
}

var prReviewUpdateDescCmd = &cobra.Command{
	Use:   "update-description",
	Short: "Update GitHub PR description with the contents of description.md",
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := cmd.Flags().GetString("dir")
		if err != nil {
			return err
		}

		filePath, err := cmd.Flags().GetString("file")
		if err != nil {
			return err
		}

		dryRun, err := cmd.Flags().GetBool("dry-run")
		if err != nil {
			return err
		}

		res, err := review.UpdateDescription(cmd.Context(), dir, filePath, dryRun)
		if err != nil {
			return err
		}

		output.Write("pr-review-update-description", res, func() {
			if res.DryRun {
				output.Printf("Dry run: would update PR #%d (%s) with:\n\n%s\n", res.PRNumber, res.PRURL, res.Body)
			} else {
				output.Printf("Successfully updated PR #%d description:\n%s\n", res.PRNumber, res.PRURL)
			}
		})

		return nil
	},
}

var prReviewCleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Delete PR review bundles from .agents/local/pr-reviews/",
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := cmd.Flags().GetString("dir")
		if err != nil {
			return err
		}

		all, err := cmd.Flags().GetBool("all")
		if err != nil {
			return err
		}

		deleted, reviewsBaseDir, err := review.CleanBundles(cmd.Context(), dir, all)
		if err != nil {
			return err
		}

		res := struct {
			Deleted []string `json:"deleted"`
		}{Deleted: deleted}
		output.Write("pr-review-clean", res, func() {
			if len(deleted) == 0 {
				output.Printf("No review bundles found to clean in %s\n", reviewsBaseDir)
			} else {
				output.Printf("Cleaned %d review bundle(s):\n", len(deleted))
				for _, d := range deleted {
					output.Printf("- %s\n", d)
				}
			}
		})

		return nil
	},
}

var prReviewListCmd = &cobra.Command{
	Use:   "list",
	Short: "List review bundles in .agents/local/pr-reviews/",
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := cmd.Flags().GetString("dir")
		if err != nil {
			return err
		}

		bundles, reviewsBaseDir, err := review.ListBundles(dir)
		if err != nil {
			return err
		}

		output.Write("pr-review-list", bundles, func() {
			if len(bundles) == 0 {
				output.Printf("No review bundles found in %s\n", reviewsBaseDir)
				return
			}
			output.Printf("Review bundles (%d):\n", len(bundles))
			for _, b := range bundles {
				descMarker := ""
				if b.HasDescription {
					descMarker = " (has description.md)"
				}
				output.Printf("  - %s -> %s%s\n", b.Name, b.ReviewPath, descMarker)
			}
		})

		return nil
	},
}

func init() {
	PRReviewCmd.Flags().String("dir", "", "Working directory for git detection (default: .)")
	PRReviewCmd.Flags().String("owner", "", "GitHub repo owner (default: detected from git remote)")
	PRReviewCmd.Flags().String("repo", "", "GitHub repo name (default: detected from git remote)")
	PRReviewCmd.Flags().String("branch", "", "Branch name (default: current branch)")
	PRReviewCmd.Flags().String("base", "", "Base branch override for diff (default: PR base or auto-detect)")
	PRReviewCmd.Flags().Int("context", 3, "Number of context lines in diffs")
	PRReviewCmd.Flags().Bool("include-resolved", false, "Include resolved review threads")
	PRReviewCmd.Flags().
		Int("max-inline-lines", 60, "Maximum patch lines per file to inline in review.md (0 uses default 60)")

	prReviewUpdateDescCmd.Flags().String("dir", "", "Working directory for git detection (default: .)")
	prReviewUpdateDescCmd.Flags().
		String("file", "", "Path to description.md (default: auto-detected from review bundle)")
	prReviewUpdateDescCmd.Flags().Bool("dry-run", false, "Preview description without updating GitHub")

	prReviewCleanCmd.Flags().String("dir", "", "Working directory for git detection (default: .)")
	prReviewCleanCmd.Flags().Bool("all", false, "Delete all review bundles in .agents/local/pr-reviews/")

	prReviewListCmd.Flags().String("dir", "", "Working directory for git detection (default: .)")

	PRReviewCmd.AddCommand(prReviewUpdateDescCmd)
	PRReviewCmd.AddCommand(prReviewCleanCmd)
	PRReviewCmd.AddCommand(prReviewListCmd)
}
