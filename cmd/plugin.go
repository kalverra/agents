package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/install"
	"github.com/kalverra/agents/internal/plugins"
	"github.com/kalverra/agents/pkg/output"
)

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Add, list, remove and update plugins",
	Long: `A plugin adds commands, rules and skills to agents. It uses the Claude Code
plugin layout: bin/agents-<cmd> executables run as "agents <cmd>", rules/*.md
and skills/*/SKILL.md are offered by "agents install".

Plugins run with your permissions and no sandbox. Only add plugins you trust.`,
}

var pluginAddCmd = &cobra.Command{
	Use:   "add <path | git-url[@ref]>",
	Short: "Add a plugin from a local directory or a git URL",
	Long: `Add a plugin. A local directory is used in place. A git URL is cloned into
the agents data dir; @ref picks a branch or tag. If the plugin has a build
command (metadata.agents-build in plugin.json), it runs after you confirm.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newPluginManager(cmd)
		if err != nil {
			return err
		}
		p, err := m.Add(args[0])
		if err != nil {
			return err
		}
		if err := m.Config.Save(); err != nil {
			return err
		}
		output.Write("plugin add", p, func() {
			output.Successf("Added plugin %s (%s)\n", p.Name, p.Root)
			output.Println(`Run "agents install --reconfigure" to pick its rules and skills.`)
		})
		return nil
	},
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List added plugins and their commands",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		added, loadErr := plugins.Load(cfg.Plugins)
		cmds, shadowed, err := plugins.AllCommands(added)
		if err != nil {
			return err
		}
		res := pluginList{Plugins: added, Commands: cmds, Shadowed: shadowed}
		if loadErr != nil {
			res.Errors = strings.Split(loadErr.Error(), "\n")
		}
		output.Write("plugin list", res, func() { printPluginList(res) })
		return nil
	},
}

var pluginRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a plugin",
	Long: `Remove a plugin from config.toml and delete its clone, if agents cloned it.
Its data dir is kept. Run "agents install" afterwards to drop its rules and
skills from your harnesses.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newPluginManager(cmd)
		if err != nil {
			return err
		}
		ref, err := m.Remove(args[0])
		if err != nil {
			return err
		}
		if err := m.Config.Save(); err != nil {
			return err
		}
		output.Write("plugin remove", ref, func() {
			output.Successf("Removed plugin %s\n", ref.Name)
			output.Println(`Run "agents install" to drop its rules and skills.`)
		})
		return nil
	},
}

var pluginUpdateCmd = &cobra.Command{
	Use:   "update [name...]",
	Short: "Pull and rebuild plugins (all when no name is given)",
	Long: `Update pulls each git plugin's clone and re-runs its build command after you
confirm. A plugin added at a tag is not pulled; remove and add it again to
change the tag. Local plugins are only rebuilt.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newPluginManager(cmd)
		if err != nil {
			return err
		}
		updated, err := m.Update(args)
		names := make([]string, len(updated))
		for k, p := range updated {
			names[k] = p.Name
		}
		if err != nil {
			return err
		}
		output.Write("plugin update", updated, func() {
			if len(names) == 0 {
				output.Println("No plugins to update.")
				return
			}
			output.Successf("Updated %s\n", strings.Join(names, ", "))
		})
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{pluginAddCmd, pluginUpdateCmd} {
		c.Flags().BoolP("yes", "y", false, "Run the plugin's build command without asking")
	}
	pluginCmd.AddCommand(pluginAddCmd, pluginListCmd, pluginRemoveCmd, pluginUpdateCmd)
	rootCmd.AddCommand(pluginCmd)
}

// newPluginManager loads config.toml into a Manager that asks before running
// a build command, unless --yes is set.
func newPluginManager(cmd *cobra.Command) (*plugins.Manager, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	yes, _ := cmd.Flags().GetBool("yes") // false for commands without --yes
	return &plugins.Manager{
		Config: cfg,
		Out:    os.Stderr,
		ConfirmBuild: func(p plugins.Plugin) error {
			fmt.Fprintf(os.Stderr, "Plugin %s builds with:\n  %s\n  (in %s)\n", p.Name, p.Build, p.Root)
			if yes {
				return nil
			}
			if !interactive() {
				return errors.New(
					"refusing to run a plugin build without --yes (non-interactive terminal or --ai-output)",
				)
			}
			return install.Confirm("Run this build command?")
		},
	}, nil
}

// pluginList is the result of `plugin list`.
type pluginList struct {
	Plugins  []plugins.Plugin  `json:"plugins"`
	Commands []plugins.Command `json:"commands,omitempty"`
	// Shadowed are commands hidden by an earlier plugin's command of the
	// same name.
	Shadowed []plugins.Command `json:"shadowed,omitempty"`
	Errors   []string          `json:"errors,omitempty"`
}

func printPluginList(res pluginList) {
	if len(res.Plugins) == 0 && len(res.Errors) == 0 {
		output.Println(`No plugins. Add one with "agents plugin add <path | git-url>".`)
		return
	}
	for _, p := range res.Plugins {
		source := p.Root
		if p.URL != "" {
			source = p.URL
			if p.Ref != "" {
				source += "@" + p.Ref
			}
		}
		output.Printf("%s  %s\n", p.Name, source)
		if p.Description != "" {
			output.Printf("  %s\n", p.Description)
		}
		for _, c := range res.Commands {
			if c.Plugin == p.Name {
				output.Printf("  agents %s\n", c.Name)
			}
		}
	}
	for _, c := range res.Shadowed {
		output.Warnf("agents %s from plugin %s is hidden by an earlier plugin\n", c.Name, c.Plugin)
	}
	for _, e := range res.Errors {
		output.Warnf("%s\n", e)
	}
}
