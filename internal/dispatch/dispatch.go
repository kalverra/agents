// Package dispatch runs plugin commands: `agents foo a b` runs the
// executable `agents-foo` with args `a b`, passing through stdio and the exit
// code. The root flags --ai-output and --log-level are taken out of argv and
// passed to the command as AGENTS_* env vars (see pkg/plugin).
package dispatch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kalverra/agents/pkg/plugin"
)

// Prefix is prepended to a command name to get its executable name.
const Prefix = "agents-"

// Invocation is a parsed `agents <name> [args]` command line.
type Invocation struct {
	Name string
	Args []string
	// AIOutput and LogLevel hold root flags found in argv. AIOutputSet is
	// false when --ai-output was absent, so the caller can keep an inherited
	// value. LogLevel is "" when --log-level was absent.
	AIOutput    bool
	AIOutputSet bool
	LogLevel    string
}

// Parse splits argv (without the program name) into a command name, its args
// and the root flags.
//
// Before the name, the long and short root flags (-a, -l) are accepted; any
// other flag means this is not a plugin call, and ok is false. After the name,
// only the long forms are taken out, so a plugin keeps its own -a and -l.
// Nothing after a "--" is touched.
func Parse(argv []string) (inv Invocation, ok bool) {
	i := 0
	for ; i < len(argv); i++ {
		consumed, isRoot := inv.takeRootFlag(argv, i, true)
		if !isRoot {
			break
		}
		i += consumed
	}
	if i >= len(argv) || strings.HasPrefix(argv[i], "-") {
		return Invocation{}, false
	}
	inv.Name = argv[i]

	rest := argv[i+1:]
	for j := 0; j < len(rest); j++ {
		if rest[j] == "--" {
			inv.Args = append(inv.Args, rest[j:]...)
			break
		}
		consumed, isRoot := inv.takeRootFlag(rest, j, false)
		if isRoot {
			j += consumed
			continue
		}
		inv.Args = append(inv.Args, rest[j])
	}
	return inv, true
}

// takeRootFlag records argv[i] when it is a root flag. consumed is how many
// extra args the flag's value used (0 or 1).
func (inv *Invocation) takeRootFlag(argv []string, i int, allowShort bool) (consumed int, isRoot bool) {
	arg := argv[i]
	name, value, hasValue := strings.Cut(arg, "=")
	switch {
	case name == "--ai-output" || (allowShort && name == "-a"):
		inv.AIOutputSet = true
		inv.AIOutput = !hasValue || value == "true" || value == "1"
		return 0, true
	case name == "--log-level" || (allowShort && name == "-l"):
		if hasValue {
			inv.LogLevel = value
			return 0, true
		}
		if i+1 < len(argv) {
			inv.LogLevel = argv[i+1]
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// Lookup returns the path of the executable for command name, searching PATH
// for agents-<name>.
func Lookup(name string) (string, bool) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return "", false
	}
	path, err := exec.LookPath(Prefix + name)
	if err != nil {
		return "", false
	}
	return path, true
}

// Run executes path with args and env merged into the current environment, wired
// to this process's stdio. It returns the command's exit code; a command killed
// by a signal returns 128+signal, like a shell. err is set only when the command
// could not start.
//
// While the command runs, agents ignores SIGINT (the terminal already sends it
// to the command) and forwards SIGTERM and SIGHUP.
func Run(path string, args []string, env plugin.Env) (int, error) {
	cmd := exec.Command(path, args...) //nolint:gosec // G204: running the user's plugin is the point
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env.Environ(os.Environ())

	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	forward := make(chan os.Signal, 1)
	signal.Notify(forward, syscall.SIGTERM, syscall.SIGHUP)

	if err := cmd.Start(); err != nil {
		signal.Stop(forward)
		return 1, fmt.Errorf("starting %s: %w", path, err)
	}
	go func() {
		for sig := range forward {
			_ = cmd.Process.Signal(sig)
		}
	}()

	err := cmd.Wait()
	// Stop delivery before close so no signal is sent on a closed channel.
	signal.Stop(forward)
	close(forward)
	return exitCode(err), nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 1
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return exitErr.ExitCode()
}
