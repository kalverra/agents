package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/kalverra/agents/internal/buildinfo"
	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/ghskill"
	"github.com/kalverra/agents/internal/install"
	"github.com/kalverra/agents/internal/paths"
	"github.com/kalverra/agents/internal/plugins"
	"github.com/kalverra/agents/pkg/output"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install rules, permissions and skills into your AI harnesses",
	Long: `Install writes the selected rules into each harness's global instructions
file, merges permissions from config.yaml into its settings, and installs
skills with gh skill. Choices are saved to config.yaml; installed.json records
what was written so "agents uninstall" can remove it.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		flags := cmd.Flags()
		dryRun, _ := flags.GetBool("dry-run")
		yes, _ := flags.GetBool("yes")
		allSkills, _ := flags.GetBool("all")
		noSkills, _ := flags.GetBool("no-skills")
		noPermissions, _ := flags.GetBool("no-permissions")
		reconfigure, _ := flags.GetBool("reconfigure")
		req := install.Request{
			Targets:       splitList(flags, "targets"),
			Skills:        splitList(flags, "skills"),
			AllSkills:     allSkills,
			NoSkills:      noSkills,
			NoPermissions: noPermissions,
			Reconfigure:   reconfigure,
		}

		inst, err := newInstaller()
		if err != nil {
			return err
		}
		plan, err := inst.Plan(req)
		if err != nil {
			return err
		}
		if dryRun {
			output.Write("install", plan, func() { printPlan(plan) })
			return nil
		}

		printPlan(plan)
		if !yes {
			if inst.Choose == nil {
				return errors.New("refusing to install without --yes (non-interactive terminal or --ai-output)")
			}
			if err := install.Confirm("Proceed with install?"); err != nil {
				return err
			}
		}

		res, applyErr := inst.Apply(plan, os.Stderr)
		if err := inst.Record.Save(); err != nil {
			return errors.Join(applyErr, err)
		}
		if applyErr != nil {
			return applyErr
		}
		if err := inst.Config.Save(); err != nil {
			return err
		}
		output.Write("install", res, func() { printResult(res) })
		return nil
	},
}

func init() {
	f := installCmd.Flags()
	f.String("targets", "", "Comma-separated harnesses: claude,antigravity,opencode (saved to config.yaml)")
	f.String("skills", "", "Comma-separated skills to install: names, or owner/repo/name[@ref] (saved to config.yaml)")
	f.Bool("all", false, "Install every built-in skill")
	f.Bool("no-skills", false, "Leave skills alone")
	f.Bool("no-permissions", false, "Leave harness permissions alone")
	f.Bool("reconfigure", false, "Ask for harnesses, rules and skills again")
	f.Bool("dry-run", false, "Print the plan only")
	f.BoolP("yes", "y", false, "Skip the confirmation prompt")
	installCmd.MarkFlagsMutuallyExclusive("skills", "all", "no-skills")
	rootCmd.AddCommand(installCmd)
}

// newInstaller loads the saved config and record. It asks questions only in
// an interactive terminal outside --ai-output.
func newInstaller() (*install.Installer, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	rec, err := install.LoadRecord()
	if err != nil {
		return nil, err
	}
	added, err := plugins.Load(cfg.Plugins)
	if err != nil {
		output.Warnf("%v; skipping its rules and skills\n", err)
	}
	inst := &install.Installer{
		Config:   cfg,
		Record:   rec,
		GH:       ghskill.Client{},
		Pin:      buildinfo.Release(),
		RulesDir: filepath.Join(paths.ConfigDir(), "rules"),
		Plugins:  added,
	}
	if interactive() {
		inst.Choose = install.HuhChooser
	}
	return inst, nil
}

// interactive reports whether agents may ask the user questions: stdin is a
// terminal and --ai-output is off.
func interactive() bool {
	return !output.JSON() && isatty.IsTerminal(os.Stdin.Fd())
}

func printPlan(p *install.Plan) {
	output.Println("Rules:", listOrNone(p.Rules))
	for _, h := range p.Harnesses {
		output.Printf("\n%s\n  instructions: %s\n", h.Name, h.Instructions)
		for _, f := range h.PermissionFiles {
			output.Printf("  permissions:  %s\n", f)
		}
		switch {
		case h.SkillsFrom != "":
			output.Printf("  skills:       reads %s's skills\n", h.SkillsFrom)
		case len(h.InstallSkills) > 0:
			output.Printf(
				"  skills:       %s (gh skill --agent %s)\n",
				strings.Join(h.InstallSkills, ", "),
				h.SkillAgent,
			)
		}
	}
	if len(p.Remove) > 0 {
		output.Println("\nRemove (no longer selected):", strings.Join(p.Remove, ", "))
	}
	for _, name := range p.Undetected {
		output.Warnf("%s not detected; installing anyway\n", name)
	}
	for _, d := range p.Dropped {
		output.Warnf("%s no longer exists; dropping it from config.yaml\n", d)
	}
	if p.GhMissing {
		output.Warnf("gh skill not found (needs GitHub CLI 2.90+); the gh commands will be printed instead\n")
	}
	output.Println()
}

func printResult(res *install.Result) {
	if len(res.Harnesses) > 0 {
		output.Successf("Installed into %s\n", strings.Join(res.Harnesses, ", "))
	}
	if len(res.Removed) > 0 {
		output.Successf("Removed from %s\n", strings.Join(res.Removed, ", "))
	}
	if len(res.SkillCommands) > 0 {
		output.Println("\nInstall the skills with:")
		for _, c := range res.SkillCommands {
			output.Println("  " + c)
		}
	}
	if len(res.LeftoverSkills) > 0 {
		output.Println("\nagents no longer manages these skills; delete them if unwanted:")
		for _, d := range res.LeftoverSkills {
			output.Println("  " + d)
		}
	}
}

// splitList returns a comma-separated string flag as a list, or nil when the
// flag was not set.
func splitList(flags *pflag.FlagSet, name string) []string {
	if !flags.Changed(name) {
		return nil
	}
	s, _ := flags.GetString(name)
	out := []string{}
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func listOrNone(list []string) string {
	if len(list) == 0 {
		return "(none)"
	}
	return strings.Join(list, ", ")
}
