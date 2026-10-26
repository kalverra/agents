package implement

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// archiveSpec closes the loop with the session's source spec once the
// session completes: an openspec change is archived via the openspec CLI
// (skipped with the manual command printed when the CLI is missing), and a
// spec-kit spec gets status "done" in its frontmatter. Best-effort:
// failures warn and never fail the session. Adopted plan files have no
// spec to archive.
func (o *Orchestrator) archiveSpec(state *State) {
	if !o.cfg.ArchiveSpecsOnDone() {
		return
	}
	switch state.PlanSource.Kind {
	case SpecKindOpenSpec:
		o.archiveOpenSpec(state)
	case SpecKindSpecKit:
		o.markSpecKitDone(state)
	case SpecKindPlan:
		// Nothing to archive: the plan file is the whole contract.
	default:
		o.log.Warn().Str("kind", string(state.PlanSource.Kind)).Msg("unknown plan source kind; skipping archive")
	}
}

// archiveOpenSpec archives the implemented change with the openspec CLI.
func (o *Orchestrator) archiveOpenSpec(state *State) {
	change := filepath.Base(state.PlanSource.Path)
	if change == "" || change == "." || change == string(filepath.Separator) {
		return
	}
	bin, err := exec.LookPath("openspec")
	if err != nil {
		o.progressf(
			"spec archive skipped: openspec CLI not found; archive manually with: openspec archive %s --yes\n",
			change,
		)
		return
	}
	cmd := exec.Command(bin, "archive", change, "--yes") //nolint:gosec // openspec is a user-installed CLI
	cmd.Dir = o.repoDir
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		o.progressf(
			"spec archive failed: openspec archive %s --yes: %v\n%s\n",
			change, runErr, strings.TrimSpace(string(out)),
		)
		return
	}
	o.progressf("spec archived: openspec change %q\n", change)
}

var specKitStatusRe = regexp.MustCompile(`(?m)^([ \t]*status[ \t]*:[ \t]*)(\S.*)$`)

// markSpecKitDone sets status "done" in the source spec.md's frontmatter,
// when it has a status field. Only the original spec dir is touched, never
// the session snapshot.
func (o *Orchestrator) markSpecKitDone(state *State) {
	specPath := filepath.Join(state.PlanSource.Path, "spec.md")
	raw, err := os.ReadFile(specPath) //nolint:gosec // user's spec dir
	if err != nil {
		o.progressf("spec archive skipped: cannot read %s: %v\n", specPath, err)
		return
	}
	updated, changed := setFrontmatterStatus(string(raw), "done")
	if !changed {
		o.progressf("spec archive skipped: %s has no status field in its frontmatter\n", specPath)
		return
	}
	if err := os.WriteFile(specPath, []byte(updated), 0o600); err != nil { //nolint:gosec // user's spec dir
		o.progressf("spec archive failed: updating %s: %v\n", specPath, err)
		return
	}
	o.progressf("spec marked done: %s (commit the status change)\n", specPath)
}

// setFrontmatterStatus rewrites the frontmatter status field in a spec
// document. changed reports whether the document had a status field to
// set.
func setFrontmatterStatus(md, status string) (string, bool) {
	m := specKitStatusRe.FindStringSubmatchIndex(md)
	if m == nil {
		return md, false
	}
	var b strings.Builder
	b.WriteString(md[:m[4]])
	b.WriteString(status)
	b.WriteString(md[m[5]:])
	return b.String(), true
}
