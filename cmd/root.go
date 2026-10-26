// Package cmd contains command execution logic.
package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/kalverra/agents/cmd/skills"
	"github.com/kalverra/agents/internal/buildinfo"
	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/dispatch"
	"github.com/kalverra/agents/internal/plugins"
	"github.com/kalverra/agents/pkg/output"
	"github.com/kalverra/agents/pkg/plugin"
)

// rootFlags holds the root flags. Their defaults come from the plugin env
// contract, so an agents run by a plugin inherits the caller's settings.
var rootFlags = plugin.FromEnviron(os.Getenv)

var rootCmd = &cobra.Command{
	Use:   "agents",
	Short: "Helper CLI for AI agent workflows",
	Long:  `Helper CLI for AI agent workflows`,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// Built-in commands log the same way plugin commands do.
		if _, err := zerolog.ParseLevel(rootFlags.LogLevel); err != nil {
			return err
		}
		logger := rootFlags.Logger()
		log.Logger = logger
		cmd.SetContext(logger.WithContext(cmd.Context()))
		output.SetJSON(rootFlags.AIOutput)

		return nil
	},
}

func init() {
	if rootFlags.LogLevel == "" {
		rootFlags.LogLevel = zerolog.LevelInfoValue
	}
	rootCmd.PersistentFlags().
		StringVarP(&rootFlags.LogLevel, "log-level", "l", rootFlags.LogLevel, "Log level (env: "+plugin.EnvLogLevel+")")
	rootCmd.PersistentFlags().
		BoolVarP(&rootFlags.AIOutput, "ai-output", "a", rootFlags.AIOutput,
			"Format output for consumption by LLMs (env: "+plugin.EnvAIOutput+")")

	rootCmd.AddCommand(skills.PRReviewCmd)
	rootCmd.AddCommand(skills.ImplementCmd)
}

// Execute runs the root command, or a plugin command when the first argument
// names no built-in command but a plugin or PATH provides agents-<name>.
func Execute() {
	added := addedPlugins()
	if code, handled := runPlugin(os.Args[1:], added); handled {
		os.Exit(code)
	}
	addPluginHelp(added)
	if err := fang.Execute(context.Background(), rootCmd,
		fang.WithVersion(buildinfo.Version()), fang.WithCommit(buildinfo.Commit())); err != nil {
		os.Exit(1)
	}
}

// addedPlugins returns the plugins in config.yaml that can be read. Problems
// are left for `agents plugin list` to report, so a broken plugin never
// breaks unrelated commands.
func addedPlugins() []plugins.Plugin {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	added, _ := plugins.Load(cfg.Plugins)
	return added
}

// runPlugin runs argv as a plugin command. handled is false when argv names a
// built-in command or no plugin command matches; cobra then handles it.
func runPlugin(argv []string, added []plugins.Plugin) (code int, handled bool) {
	inv, ok := dispatch.Parse(argv)
	if !ok || isBuiltin(inv.Name) {
		return 0, false
	}

	env := plugin.FromEnviron(os.Getenv)
	if inv.AIOutputSet {
		env.AIOutput = inv.AIOutput
	}
	if inv.LogLevel != "" {
		env.LogLevel = inv.LogLevel
	}
	if bin, err := os.Executable(); err == nil {
		env.AgentsBin = bin
	}

	var path string
	if c, ok := plugins.Find(added, inv.Name); ok {
		owner := c.Owner()
		path, env.PluginRoot, env.PluginData = c.Path, owner.Root, owner.DataDir()
		if err := os.MkdirAll(env.PluginData, 0o750); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1, true
		}
	} else if path, ok = dispatch.Lookup(inv.Name); ok {
		// A PATH command belongs to no plugin, even when a plugin ran this agents.
		env.PluginRoot, env.PluginData = "", ""
	} else {
		return 0, false
	}

	code, err := dispatch.Run(path, inv.Args, env)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
	return code, true
}

const pluginGroup = "plugin-commands"

// addPluginHelp lists the plugins' commands in `agents --help`. Execute runs
// plugin commands through runPlugin before cobra sees them, so these entries
// only serve help and completion.
func addPluginHelp(added []plugins.Plugin) {
	cmds, _, err := plugins.AllCommands(added)
	if err != nil || len(cmds) == 0 {
		return
	}
	rootCmd.AddGroup(&cobra.Group{ID: pluginGroup, Title: "Plugin commands"})
	for _, c := range cmds {
		if isBuiltin(c.Name) {
			continue
		}
		short := c.Description
		if short == "" {
			short = "From plugin " + c.Plugin
		}
		rootCmd.AddCommand(&cobra.Command{
			Use:                c.Name,
			Short:              short,
			GroupID:            pluginGroup,
			DisableFlagParsing: true,
			RunE: func(*cobra.Command, []string) error {
				return fmt.Errorf("run it as: agents %s", c.Name)
			},
		})
	}
}

// isBuiltin reports whether name is a root command, including the ones cobra
// and fang add at execute time (help, completion, man, __complete*).
func isBuiltin(name string) bool {
	switch {
	case name == "help", name == "completion", name == "man", strings.HasPrefix(name, "__"):
		return true
	}
	for _, c := range rootCmd.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return true
		}
	}
	return false
}
