package implement

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SpecSource is a resolved spec source: its kind plus on-disk location.
type SpecSource struct {
	Kind SpecKind
	Path string
}

// ImportedPlan is the result of importing a spec source into a session:
// the session-local plan.md content plus its recorded provenance.
type ImportedPlan struct {
	Source PlanSource
	Task   string
	PlanMD string
	// SkippedDone lists already-completed task items (checked boxes) that
	// the import left out of the plan.
	SkippedDone []string
}

// ErrNoSpec is returned when no plan or spec source can be resolved.
var ErrNoSpec = errors.New("no plan or spec found to implement")

// ResolveSpecSource picks the spec to implement. An explicit --plan or
// --spec path wins; otherwise the repo is scanned for in-flight specs (an
// openspec/changes/<name>/ change or a specs/<name>/ spec-kit folder). Zero
// candidates and ambiguous candidate sets both error with guidance.
func ResolveSpecSource(repoDir, planPath, specDir string) (SpecSource, error) {
	if planPath != "" && specDir != "" {
		return SpecSource{}, errors.New("pass either --plan or --spec, not both")
	}
	if planPath != "" {
		info, err := os.Stat(planPath)
		if err != nil {
			return SpecSource{}, fmt.Errorf("--plan %s: %w", planPath, err)
		}
		if info.IsDir() {
			return SpecSource{}, fmt.Errorf("--plan %s: is a directory (use --spec for spec dirs)", planPath)
		}
		return SpecSource{Kind: SpecKindPlan, Path: planPath}, nil
	}
	if specDir != "" {
		kind, err := DetectSpecKind(specDir)
		if err != nil {
			return SpecSource{}, fmt.Errorf("--spec %s: %w", specDir, err)
		}
		return SpecSource{Kind: kind, Path: specDir}, nil
	}
	return autoDetectSpecSource(repoDir)
}

// DetectSpecKind identifies a spec dir by its contents: openspec changes
// carry proposal.md/design.md, spec-kit specs carry spec.md. Both need
// tasks.md.
func DetectSpecKind(dir string) (SpecKind, error) {
	if !fileExists(filepath.Join(dir, "tasks.md")) {
		return "", errors.New("no tasks.md (want an openspec change dir or a spec-kit spec dir)")
	}
	switch {
	case fileExists(filepath.Join(dir, "proposal.md")) || fileExists(filepath.Join(dir, "design.md")):
		return SpecKindOpenSpec, nil
	case fileExists(filepath.Join(dir, "spec.md")):
		return SpecKindSpecKit, nil
	default:
		return "", errors.New(
			"cannot determine spec format: want proposal.md/design.md (openspec) or spec.md (spec-kit) alongside tasks.md",
		)
	}
}

// autoDetectSpecSource scans repoDir for in-flight specs: non-archive
// openspec/changes/<name>/ dirs with tasks.md, then specs/<name>/ dirs with
// tasks.md plus spec.md or plan.md.
func autoDetectSpecSource(repoDir string) (SpecSource, error) {
	var candidates []SpecSource

	changesDir := filepath.Join(repoDir, "openspec", "changes")
	if entries, err := os.ReadDir(changesDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || e.Name() == "archive" {
				continue
			}
			dir := filepath.Join(changesDir, e.Name())
			if fileExists(filepath.Join(dir, "tasks.md")) {
				candidates = append(candidates, SpecSource{Kind: SpecKindOpenSpec, Path: dir})
			}
		}
	}

	specsDir := filepath.Join(repoDir, "specs")
	if entries, err := os.ReadDir(specsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(specsDir, e.Name())
			if !fileExists(filepath.Join(dir, "tasks.md")) {
				continue
			}
			if fileExists(filepath.Join(dir, "spec.md")) || fileExists(filepath.Join(dir, "plan.md")) {
				candidates = append(candidates, SpecSource{Kind: SpecKindSpecKit, Path: dir})
			}
		}
	}

	switch len(candidates) {
	case 0:
		return SpecSource{}, fmt.Errorf(
			"%w: no in-flight spec found in openspec/changes/ or specs/; pass --plan <plan.md> or --spec <dir>",
			ErrNoSpec,
		)
	case 1:
		return candidates[0], nil
	default:
		names := make([]string, 0, len(candidates))
		for _, c := range candidates {
			names = append(names, fmt.Sprintf("%s %s", c.Kind, relPathOrSelf(repoDir, c.Path)))
		}
		return SpecSource{}, fmt.Errorf(
			"%w: multiple in-flight specs found (%s); pass --spec <dir> to pick one",
			ErrNoSpec,
			strings.Join(names, ", "),
		)
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func relPathOrSelf(base, path string) string {
	if rel, err := filepath.Rel(base, path); err == nil {
		return rel
	}
	return path
}

// ImportSpec imports the resolved source into sessionDir:
//   - an adopted plan.md (--plan) is copied to sessionDir/plan.md as-is;
//   - an external spec dir is snapshotted to sessionDir/spec/ and compiled
//     into a flow-native plan.md (context docs inlined, tasks.md checkboxes
//     mapped to sequentially numbered steps, ## Verification section or the
//     implement.yaml defaults carried over).
//
// The returned ImportedPlan is validated and gated by the caller; the
// session's plan.md is the frozen execution contract from here on.
func ImportSpec(sessionDir string, src SpecSource) (*ImportedPlan, error) {
	switch src.Kind {
	case SpecKindPlan:
		return importPlanFile(sessionDir, src)
	case SpecKindOpenSpec, SpecKindSpecKit:
		return importSpecDir(sessionDir, src)
	default:
		return nil, fmt.Errorf("unknown spec kind %q", src.Kind)
	}
}

func importPlanFile(sessionDir string, src SpecSource) (*ImportedPlan, error) {
	raw, err := os.ReadFile(src.Path) //nolint:gosec // user-specified plan path
	if err != nil {
		return nil, fmt.Errorf("reading plan %s: %w", src.Path, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("plan %s is empty", src.Path)
	}
	if err := writeSessionPlan(sessionDir, string(raw)); err != nil {
		return nil, err
	}
	return &ImportedPlan{
		Source: PlanSource{
			Kind:     SpecKindPlan,
			Path:     src.Path,
			SpecHash: hashBytes(raw),
		},
		Task:   ParsePlanTitle(string(raw)),
		PlanMD: string(raw),
	}, nil
}

func importSpecDir(sessionDir string, src SpecSource) (*ImportedPlan, error) {
	snapshotDir := filepath.Join(sessionDir, "spec")
	if err := copyTree(src.Path, snapshotDir); err != nil {
		return nil, fmt.Errorf("snapshotting %s: %w", src.Path, err)
	}
	specHash, err := hashTree(snapshotDir)
	if err != nil {
		return nil, fmt.Errorf("hashing snapshot %s: %w", snapshotDir, err)
	}

	tasksRaw, err := os.ReadFile( //nolint:gosec // session dir is harness-managed
		filepath.Join(snapshotDir, "tasks.md"),
	)
	if err != nil {
		return nil, fmt.Errorf("reading tasks.md: %w", err)
	}
	steps, skipped, err := ParseTasksMD(string(tasksRaw))
	if err != nil {
		return nil, fmt.Errorf("parsing tasks.md in %s: %w", src.Path, err)
	}

	contextDocs := contextDocsForKind(snapshotDir, src.Kind)
	task := specTitle(snapshotDir, contextDocs, src.Path)

	planMD := compilePlanMD(src, snapshotDir, task, steps, contextDocs)

	if err := writeSessionPlan(sessionDir, planMD); err != nil {
		return nil, err
	}
	return &ImportedPlan{
		Source: PlanSource{
			Kind:        src.Kind,
			Path:        src.Path,
			SnapshotDir: snapshotDir,
			SpecHash:    specHash,
		},
		Task:        task,
		PlanMD:      planMD,
		SkippedDone: skipped,
	}, nil
}

// contextDocsForKind lists the context documents inlined into the compiled
// plan's Context section, in read order.
func contextDocsForKind(snapshotDir string, kind SpecKind) []string {
	var names []string
	switch kind {
	case SpecKindOpenSpec:
		names = []string{"proposal.md", "design.md"}
	case SpecKindSpecKit:
		names = []string{"spec.md", "plan.md"}
	}
	var docs []string
	for _, name := range names {
		if fileExists(filepath.Join(snapshotDir, name)) {
			docs = append(docs, name)
		}
	}
	// openspec requirement deltas: specs/**/*.md.
	if deltas, err := filepath.Glob(filepath.Join(snapshotDir, "specs", "*.md")); err == nil {
		sort.Strings(deltas)
		for _, d := range deltas {
			docs = append(docs, filepath.Join("specs", filepath.Base(d)))
		}
	}
	return docs
}

// specTitle picks the session task title: the first context doc's H1, else
// the spec dir name.
func specTitle(snapshotDir string, contextDocs []string, srcPath string) string {
	for _, doc := range contextDocs {
		if raw, err := os.ReadFile( //nolint:gosec // session dir is harness-managed
			filepath.Join(snapshotDir, doc),
		); err == nil {
			if title := firstHeading(string(raw)); title != "" {
				return title
			}
		}
	}
	if base := filepath.Base(srcPath); base != "" && base != "." {
		return base
	}
	return "Implement spec"
}

var headingRe = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)

// firstHeading returns the first H1 heading text in md, or "".
func firstHeading(md string) string {
	if m := headingRe.FindStringSubmatch(md); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// compilePlanMD renders the flow-native plan.md for an imported spec dir.
// Verification commands declared in the spec's own docs (## Verification)
// win; the caller's config defaults are merged in by the orchestrator.
func compilePlanMD(src SpecSource, snapshotDir, task string, steps []Step, contextDocs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Plan: %s\n\n", task)
	fmt.Fprintf(&b, "Imported from %s spec at %s (snapshot: %s).\n\n", src.Kind, src.Path, snapshotDir)

	b.WriteString("## Context\n\n")
	for _, doc := range contextDocs {
		raw, err := os.ReadFile(filepath.Join(snapshotDir, doc)) //nolint:gosec // session dir is harness-managed
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", doc, strings.TrimSpace(string(raw)))
	}

	if verify := specVerification(snapshotDir, contextDocs); !verify.Empty() {
		b.WriteString("## Verification\n\n")
		for _, c := range verify.Commands() {
			fmt.Fprintf(&b, "- %s: `%s`\n", c.Name, c.Command)
		}
		b.WriteString("\n")
	}

	for _, s := range steps {
		fmt.Fprintf(&b, "## Step %d: %s\n\n%s\n\n", s.Number, s.Title, s.Body)
	}
	return b.String()
}

// specVerification extracts a "## Verification" section from the spec's own
// docs, first match wins.
func specVerification(snapshotDir string, contextDocs []string) Verification {
	for _, doc := range contextDocs {
		raw, err := os.ReadFile(filepath.Join(snapshotDir, doc)) //nolint:gosec // session dir is harness-managed
		if err != nil {
			continue
		}
		if v := ParseVerification(string(raw)); !v.Empty() {
			return v
		}
	}
	return Verification{}
}

func writeSessionPlan(sessionDir, planMD string) error {
	if err := os.MkdirAll(sessionDir, 0o750); err != nil { //nolint:gosec // session dir is harness-managed
		return fmt.Errorf("creating %s: %w", sessionDir, err)
	}
	if err := os.WriteFile( //nolint:gosec // session dir is harness-managed
		filepath.Join(sessionDir, "plan.md"),
		[]byte(planMD),
		0o600,
	); err != nil {
		return fmt.Errorf("writing session plan.md: %w", err)
	}
	return nil
}

// tasks.md parsing: "## N. Group" headings plus "- [ ] N.M Task" checkbox
// items (spec-kit items may carry indented detail bullets).
var (
	taskGroupRe = regexp.MustCompile(`(?m)^#{1,4}\s+\d+\.\s+(.+?)\s*$`)
	taskItemRe  = regexp.MustCompile(`^[-*]\s*\[([ xX])\]\s*(.*)$`)
	taskNumRe   = regexp.MustCompile(`^\d+(?:\.\d+)*\.?\s+`)
)

// ParseTasksMD maps a tasks.md to sequentially numbered steps. Checked
// items are skipped (already done) and returned as done titles. Indented
// detail lines under an item join its body, as does the group heading.
func ParseTasksMD(md string) (steps []Step, skipped []string, err error) {
	lines := strings.Split(md, "\n")
	group := ""
	var cur *Step
	bodyLines := []string{}

	flush := func() {
		if cur != nil {
			cur.Body = strings.TrimSpace(strings.Join(append(
				[]string{fmt.Sprintf("Task group: %s", group)},
				bodyLines...,
			), "\n"))
			steps = append(steps, *cur)
			cur = nil
		}
		bodyLines = nil
	}

	for _, line := range lines {
		if m := taskGroupRe.FindStringSubmatch(line); m != nil {
			flush()
			group = strings.TrimSpace(m[1])
			continue
		}
		if m := taskItemRe.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			flush()
			title := strings.TrimSpace(m[2])
			if title == "" {
				continue
			}
			if m[1] == "x" || m[1] == "X" {
				skipped = append(skipped, taskNumRe.ReplaceAllString(title, ""))
				continue
			}
			title = taskNumRe.ReplaceAllString(title, "")
			if title == "" {
				title = "task"
			}
			cur = &Step{Number: len(steps) + 1, Title: title, Status: StepPending}
			continue
		}
		if cur != nil && strings.TrimSpace(line) != "" {
			bodyLines = append(bodyLines, strings.TrimRight(line, "\r"))
		}
	}
	flush()

	if len(steps) == 0 {
		if len(skipped) > 0 {
			return nil, skipped, fmt.Errorf(
				"all %d task(s) are already checked off; nothing to implement",
				len(skipped),
			)
		}
		return nil, nil, errors.New("no '- [ ] task' checkbox items found")
	}
	return steps, skipped, nil
}

// copyTree recursively copies src's regular files to dst, preserving
// relative layout and file modes. Symlinks and other specials are skipped.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o750) //nolint:gosec // session dir is harness-managed
		case d.Type().IsRegular():
			return copyFile(path, target)
		default:
			return nil // skip symlinks and specials
		}
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // session dir is harness-managed
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // read-only close

	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil { //nolint:gosec // session dir is harness-managed
		return err
	}
	out, err := os.Create(dst) //nolint:gosec // session dir is harness-managed
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() // best-effort close on the error path
		return err
	}
	return out.Close()
}

// hashTree hashes a directory tree's structure and contents (relative
// paths and file bytes, in sorted order) into a stable hex digest.
func hashTree(dir string) (string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return "", relErr
		}
		_, _ = fmt.Fprintf(h, "%s\n", rel)
		data, readErr := os.ReadFile(p) //nolint:gosec // session dir is harness-managed
		if readErr != nil {
			return "", readErr
		}
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
