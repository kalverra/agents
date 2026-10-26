// Package ghskill runs GitHub CLI's gh skill commands, which install and
// update agent skills.
package ghskill

import (
	"io"
	"os"
	"os/exec"
	"strings"
)

// Repo is the GitHub repo that holds the built-in skills.
const Repo = "kalverra/agents"

// Client runs gh. The zero value runs "gh" from PATH.
type Client struct {
	Bin string
}

func (c Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "gh"
}

// Available reports whether gh is installed and has the skill command
// (gh 2.90 or newer).
func (c Client) Available() bool {
	cmd := exec.Command(c.bin(), "skill", "--help") //nolint:gosec // bin is gh or a test fake
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run() == nil
}

// InstallArgs returns the gh arguments that install one skill from repo for
// one agent at user scope, overwriting an existing copy. An empty pin
// installs gh's default: the latest release, else the default branch.
func InstallArgs(repo, skill, agent, pin string) []string {
	args := []string{"skill", "install", repo, skill, "--agent", agent, "--scope", "user", "--force"}
	if pin != "" {
		args = append(args, "--pin", pin)
	}
	return args
}

// InstallLocalArgs returns the gh arguments that install the skill in dir, a
// local <name>/SKILL.md folder, like InstallArgs. Pointing gh at the skill
// folder rather than a repo root keeps it from walking unrelated files.
func InstallLocalArgs(dir, skill, agent string) []string {
	return []string{"skill", "install", dir, skill, "--agent", agent, "--scope", "user", "--force", "--from-local"}
}

// Run runs gh with args. gh's output goes to stdout and stderr, and it reads
// stdin, so it can prompt.
func (c Client) Run(args []string, stdout, stderr io.Writer) error {
	cmd := exec.Command(c.bin(), args...) //nolint:gosec // bin is gh or a test fake; args are built here
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	return cmd.Run()
}

// Command returns args as a gh command line a user can copy.
func Command(args []string) string {
	return "gh " + strings.Join(args, " ")
}
