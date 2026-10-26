// Package plugin is the contract between the agents CLI and plugin commands.
//
// When a user runs `agents foo a b`, agents finds an `agents-foo` executable
// and runs it with args `a b` and the AGENTS_* environment variables below.
// stdin, stdout, stderr and the exit code pass through unchanged. Plugin
// commands written in Go call Load at startup:
//
//	env := plugin.Load()
//	log := env.Logger()
//	output.Write("foo", data, func() { fmt.Println("human output") })
package plugin

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"

	"github.com/kalverra/agents/internal/paths"
	"github.com/kalverra/agents/pkg/output"
)

// Environment variable names agents sets for plugin commands.
const (
	EnvAIOutput   = "AGENTS_AI_OUTPUT"   // "1" when --ai-output is on
	EnvLogLevel   = "AGENTS_LOG_LEVEL"   // zerolog level name
	EnvConfigDir  = "AGENTS_CONFIG_DIR"  // agents config dir
	EnvDataDir    = "AGENTS_DATA_DIR"    // agents data dir
	EnvStateDir   = "AGENTS_STATE_DIR"   // agents state dir
	EnvPluginRoot = "AGENTS_PLUGIN_ROOT" // plugin dir; unset for commands found on PATH
	EnvPluginData = "AGENTS_PLUGIN_DATA" // per-plugin data dir; unset for commands found on PATH
	EnvBin        = "AGENTS_BIN"         // path of the agents binary that ran the command
)

var contractKeys = []string{
	EnvAIOutput, EnvLogLevel, EnvConfigDir, EnvDataDir, EnvStateDir, EnvPluginRoot, EnvPluginData, EnvBin,
}

// Env is the context agents passes to a plugin command.
type Env struct {
	// AIOutput selects machine-readable output; see pkg/output.
	AIOutput bool
	// LogLevel is a zerolog level name. Empty or invalid means info.
	LogLevel string
	// ConfigDir, DataDir and StateDir are the agents XDG dirs.
	ConfigDir string
	DataDir   string
	StateDir  string
	// PluginRoot and PluginData are set only for commands that come from an
	// installed plugin.
	PluginRoot string
	PluginData string
	// AgentsBin is the agents binary, for plugins that call back into it.
	AgentsBin string
}

// Load reads the Env agents passed and puts pkg/output in the matching mode.
// Run outside agents, the dirs fall back to the agents defaults.
func Load() Env {
	env := FromEnviron(os.Getenv)
	output.SetJSON(env.AIOutput)
	return env
}

// FromEnviron builds an Env from getenv, filling unset dirs with the agents
// defaults.
func FromEnviron(getenv func(string) string) Env {
	aiOutput, _ := strconv.ParseBool(getenv(EnvAIOutput))
	return Env{
		AIOutput:   aiOutput,
		LogLevel:   getenv(EnvLogLevel),
		ConfigDir:  orDefault(getenv(EnvConfigDir), paths.ConfigDir),
		DataDir:    orDefault(getenv(EnvDataDir), paths.DataDir),
		StateDir:   orDefault(getenv(EnvStateDir), paths.StateDir),
		PluginRoot: getenv(EnvPluginRoot),
		PluginData: getenv(EnvPluginData),
		AgentsBin:  getenv(EnvBin),
	}
}

// Environ returns base (KEY=VALUE pairs, as from os.Environ) with any
// inherited contract variables replaced by the Env's. Empty fields are left
// unset, so a stale value from a parent agents process never leaks through.
func (e Env) Environ(base []string) []string {
	out := make([]string, 0, len(base)+len(contractKeys))
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(contractKeys, key) {
			out = append(out, kv)
		}
	}
	add := func(key, value string) {
		if value != "" {
			out = append(out, key+"="+value)
		}
	}
	if e.AIOutput {
		add(EnvAIOutput, "1")
	}
	add(EnvLogLevel, e.LogLevel)
	add(EnvConfigDir, e.ConfigDir)
	add(EnvDataDir, e.DataDir)
	add(EnvStateDir, e.StateDir)
	add(EnvPluginRoot, e.PluginRoot)
	add(EnvPluginData, e.PluginData)
	add(EnvBin, e.AgentsBin)
	return out
}

// Logger returns a stderr logger at the Env's level, so stdout stays free for
// command output. It writes JSON lines in AI-output mode and colored console
// lines otherwise.
func (e Env) Logger() zerolog.Logger {
	level, err := zerolog.ParseLevel(e.LogLevel)
	if err != nil || e.LogLevel == "" {
		level = zerolog.InfoLevel
	}
	zerolog.TimeFieldFormat = time.RFC3339
	if e.AIOutput {
		return zerolog.New(os.Stderr).Level(level).With().Timestamp().Logger()
	}
	return zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "15:04:05.000",
		NoColor:    !isatty.IsTerminal(os.Stderr.Fd()),
	}).Level(level).With().Timestamp().Logger()
}

func orDefault(value string, fallback func() string) string {
	if value != "" {
		return value
	}
	return fallback()
}
