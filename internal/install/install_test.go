package install

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/ghskill"
	"github.com/kalverra/agents/internal/harness"
	"github.com/kalverra/agents/internal/plugins"
	"github.com/kalverra/agents/internal/rules"
)

// fakeGh writes a gh stand-in that logs its argv, one call per line, and
// creates SKILL.md where gh skill install would put it.
func fakeGh(t *testing.T, home string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "gh"), filepath.Join(dir, "gh.log")
	script := `#!/bin/sh
echo "$*" >> "` + log + `"
if [ "$1 $2" = "skill install" ]; then
  case "$6" in
    claude-code) d="` + home + `/.claude/skills" ;;
    antigravity-cli) d="` + home + `/.gemini/antigravity-cli/skills" ;;
    opencode) d="` + home + `/.config/opencode/skills" ;;
  esac
  mkdir -p "$d/$4" && echo "installed" > "$d/$4/SKILL.md"
fi
`
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o700)) //nolint:gosec // test script must be executable
	return bin, log
}

func ghCalls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log) //nolint:gosec // test temp file
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var installs []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "skill install") {
			installs = append(installs, line)
		}
	}
	return installs
}

// snapshot maps every file under root, except skill dirs, to its content.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() ||
			strings.Contains(path, string(filepath.Separator)+"skills"+string(filepath.Separator)) {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // test temp file
		out[path] = string(data)
		return err
	}))
	return out
}

func newTestInstaller(t *testing.T, cfg *config.Config, ghBin string) *Installer {
	t.Helper()
	rec, err := LoadRecord()
	require.NoError(t, err)
	return &Installer{Config: cfg, Record: rec, GH: ghskill.Client{Bin: ghBin}, RulesDir: t.TempDir()}
}

func run(t *testing.T, inst *Installer, req Request) (*Plan, *Result) {
	t.Helper()
	plan, err := inst.Plan(req)
	require.NoError(t, err)
	res, err := inst.Apply(plan, io.Discard)
	require.NoError(t, err)
	require.NoError(t, inst.Record.Save())
	return plan, res
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test temp file
	require.NoError(t, err)
	return string(data)
}

//nolint:paralleltest // t.Setenv mutates process env
func TestInstallUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTIGRAVITY_CONFIG_DIR", "")
	ghBin, ghLog := fakeGh(t, home)

	claude, opencode := harness.Claude{}, harness.OpenCode{}
	require.NoError(t, os.MkdirAll(filepath.Dir(claude.SettingsPath()), 0o750))
	require.NoError(t, os.WriteFile(claude.InstructionsPath(), []byte("# Mine\n"), 0o600))
	require.NoError(t, os.WriteFile(
		claude.SettingsPath(),
		// Pre-formatted the way the settings package writes JSON.
		[]byte(
			"{\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(cat)\"\n    ]\n  },\n  \"theme\": \"dark\"\n}\n",
		),
		0o600,
	))
	before := snapshot(t, home)

	cfg := &config.Config{Permissions: config.Permissions{
		Directories: []config.Directory{{Path: "~/Projects", Trust: config.TrustParent}},
	}}

	// First install: claude and opencode, one skill.
	plan, _ := run(t, newTestInstaller(t, cfg, ghBin), Request{
		Targets: []string{"claude", "opencode"}, Skills: []string{"refactoring"},
	})
	assert.Equal(t, "claude", plan.Harnesses[1].SkillsFrom, "opencode reads claude's skills")

	claudeMD := read(t, claude.InstructionsPath())
	assert.True(t, strings.HasPrefix(claudeMD, "# Mine\n\n"+rules.BeginMarker), "keeps user text")
	assert.Contains(t, claudeMD, "### rg")
	assert.Contains(t, read(t, opencode.InstructionsPath()), rules.BeginMarker)
	assert.Contains(t, read(t, claude.SettingsPath()), `"Bash(git status)"`)
	assert.Contains(t, read(t, claude.SettingsPath()), filepath.Join(home, "Projects"))
	assert.Contains(t, read(t, opencode.SettingsPath()), `"external_directory"`)
	assert.Equal(t, []string{"skill install kalverra/agents refactoring --agent claude-code --scope user --force"},
		ghCalls(t, ghLog))
	require.NotNil(t, cfg.Rules)
	assert.Contains(t, *cfg.Rules, "rg", "non-interactive install picks every rule")

	// Same choices again: nothing new for gh to do.
	plan, _ = run(t, newTestInstaller(t, cfg, ghBin), Request{})
	assert.Empty(t, plan.Harnesses[0].InstallSkills)
	assert.Len(t, ghCalls(t, ghLog), 1)

	// Drop the skill and opencode.
	_, res := run(t, newTestInstaller(t, cfg, ghBin), Request{Targets: []string{"claude"}, Skills: []string{}})
	assert.Equal(t, []string{"opencode"}, res.Removed)
	assert.Equal(t, []string{filepath.Join(claude.SkillsDir(), "refactoring")}, res.LeftoverSkills)
	assert.NoFileExists(t, opencode.InstructionsPath())

	// Uninstall from a fresh load of installed.json.
	inst := newTestInstaller(t, cfg, ghBin)
	_, err := inst.Uninstall()
	require.NoError(t, err)
	require.NoError(t, inst.Record.Save())

	assert.Equal(t, before, snapshot(t, home), "home is back to its starting state")
	assert.NoFileExists(t, RecordPath())
}

//nolint:paralleltest // t.Setenv mutates process env
func TestInstall_GhMissingPrintsCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	inst := newTestInstaller(t, &config.Config{}, filepath.Join(t.TempDir(), "no-gh"))
	inst.Pin = "v1.2.3"

	plan, res := run(t, inst, Request{Targets: []string{"antigravity"}, AllSkills: true, NoPermissions: true})

	assert.True(t, plan.GhMissing)
	require.NotEmpty(t, res.SkillCommands)
	assert.Equal(t,
		"gh skill install kalverra/agents coding-standards --agent antigravity-cli --scope user --force --pin v1.2.3",
		res.SkillCommands[0])
	assert.Empty(t, inst.Record.Harnesses["antigravity"].Skills, "skills it did not install are not recorded")
	assert.NoFileExists(t, harness.Antigravity{}.CLISettingsPath(), "--no-permissions leaves settings alone")
}

//nolint:paralleltest // t.Setenv mutates process env
func TestPlan_Choices(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	saved := []string{"rg"}
	cfg := &config.Config{Rules: &saved}
	var asked []string
	inst := newTestInstaller(t, cfg, "gh")
	inst.Choose = func(title string, _ []Option, selected []string) ([]string, error) {
		asked = append(asked, title)
		if strings.Contains(title, "rules") {
			assert.Equal(t, []string{"rg"}, selected, "reconfigure starts from the saved choice")
			return []string{"coding-standards"}, nil
		}
		return selected, nil
	}

	plan, err := inst.Plan(Request{Targets: []string{"claude"}, NoSkills: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"rg"}, plan.Rules, "saved choice wins without --reconfigure")
	assert.Empty(t, asked)

	plan, err = inst.Plan(Request{Targets: []string{"claude"}, NoSkills: true, Reconfigure: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"coding-standards"}, plan.Rules)
	assert.Equal(t, []string{"coding-standards"}, *cfg.Rules)
	assert.Len(t, asked, 1, "--targets is not asked again")

	_, err = inst.Plan(Request{Targets: []string{"claude"}, Skills: []string{"nope"}})
	assert.ErrorContains(t, err, `unknown skill "nope"`)
}

//nolint:paralleltest // t.Setenv mutates process env
func TestInstall_PluginRulesAndSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ghBin, ghLog := fakeGh(t, home)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "rules"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rules", "hello.md"), []byte("### hello\n\nSay hi."), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "skills", "hi"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "skills", "hi", "SKILL.md"),
		[]byte("---\nname: hi\ndescription: Greets.\n---\n"), 0o600))
	pl, err := plugins.Open(config.Plugin{Name: "greeter", Path: dir})
	require.NoError(t, err)

	skills := []string{"hi", "refactoring"}
	cfg := &config.Config{Skills: &skills}
	newInst := func(added ...plugins.Plugin) *Installer {
		inst := newTestInstaller(t, cfg, ghBin)
		inst.Plugins = added
		return inst
	}
	claude := harness.Claude{}

	plan, _ := run(t, newInst(pl), Request{Targets: []string{"claude"}, NoPermissions: true})
	assert.Contains(t, plan.Rules, "hello")
	assert.Contains(t, read(t, claude.InstructionsPath()), "Say hi.")
	assert.ElementsMatch(t, []string{
		"skill install kalverra/agents refactoring --agent claude-code --scope user --force",
		"skill install " + filepath.Join(
			dir,
			"skills",
			"hi",
		) + " hi --agent claude-code --scope user --force --from-local",
	}, ghCalls(t, ghLog))

	// A plugin skill may have changed locally, so it installs every time.
	plan, _ = run(t, newInst(pl), Request{NoPermissions: true})
	assert.Equal(t, []string{"hi"}, plan.Harnesses[0].InstallSkills)

	// Without the plugin, its saved rule and skill are dropped, not errors.
	plan, res := run(t, newInst(), Request{NoPermissions: true})
	assert.ElementsMatch(t, []string{"rule hello", "skill hi"}, plan.Dropped)
	assert.Equal(t, []string{"refactoring"}, *cfg.Skills)
	assert.NotContains(t, *cfg.Rules, "hello")
	assert.NotContains(t, read(t, claude.InstructionsPath()), "Say hi.")
	assert.Equal(t, []string{filepath.Join(claude.SkillsDir(), "hi")}, res.LeftoverSkills)
}

//nolint:paralleltest // t.Setenv mutates process env
func TestInstall_RemoteSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ghBin, ghLog := fakeGh(t, home)

	// The remote refactoring hides the built-in one of the same name, so the
	// saved built-in choice is dropped.
	skills := []string{"refactoring", "mattpocock/skills/grill-me", "other/repo/refactoring@v2"}
	cfg := &config.Config{Skills: &skills}
	req := Request{Targets: []string{"claude"}, NoPermissions: true}

	plan, _ := run(t, newTestInstaller(t, cfg, ghBin), req)
	assert.Equal(t, []string{"skill refactoring"}, plan.Dropped)
	assert.Equal(t, []string{"mattpocock/skills/grill-me", "other/repo/refactoring@v2"}, *cfg.Skills)
	assert.ElementsMatch(t, []string{
		"skill install mattpocock/skills grill-me --agent claude-code --scope user --force",
		"skill install other/repo refactoring --agent claude-code --scope user --force --pin v2",
	}, ghCalls(t, ghLog))

	// Same source again: nothing new for gh to do.
	plan, _ = run(t, newTestInstaller(t, cfg, ghBin), req)
	assert.Empty(t, plan.Harnesses[0].InstallSkills)

	// A new ref reinstalls.
	req.Skills = []string{"mattpocock/skills/grill-me@v1.3.1"}
	plan, res := run(t, newTestInstaller(t, cfg, ghBin), req)
	assert.Equal(t, []string{"grill-me"}, plan.Harnesses[0].InstallSkills)
	assert.Equal(t, []string{filepath.Join(harness.Claude{}.SkillsDir(), "refactoring")}, res.LeftoverSkills)
}

func TestParseRemoteSkill(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id   string
		want Skill
		ok   bool
	}{
		{id: "mattpocock/skills/grill-me", want: Skill{Name: "grill-me", Repo: "mattpocock/skills"}, ok: true},
		{id: "o/r/s@v1.2", want: Skill{Name: "s", Repo: "o/r", Ref: "v1.2"}, ok: true},
		{id: "grilling"},
		{id: "o/r"},
		{id: "o/r/s/t"},
		{id: "o//s"},
		{id: "o/r/s@"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()

			got, ok := ParseRemoteSkill(tt.id)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
			if ok {
				assert.Equal(t, tt.id, got.ID(), "ID round-trips")
			}
		})
	}
}
