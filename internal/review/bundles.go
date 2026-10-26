package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/pkg/git"
)

// LocalReviewsDir returns the repo root for dir and its
// .agents/local/pr-reviews dir, creating it when missing along with a
// .gitignore that keeps .agents/local out of git. Outside a repo, dir is the
// root.
func LocalReviewsDir(dir string) (repoRoot string, reviewsDir string, err error) {
	root, err := git.RepoRoot(dir)
	if err != nil {
		root = dir
	}
	localDir := filepath.Join(root, ".agents", "local")
	if err := os.MkdirAll(localDir, 0o750); err != nil {
		return "", "", fmt.Errorf("creating %s: %w", localDir, err)
	}
	gitignore := filepath.Join(localDir, ".gitignore")
	if _, err := os.Stat(gitignore); errors.Is(err, os.ErrNotExist) {
		_ = os.WriteFile(gitignore, []byte("*\n"), 0o600)
	}
	reviewsDir = filepath.Join(localDir, "pr-reviews")
	if err := os.MkdirAll(reviewsDir, 0o750); err != nil {
		return "", "", fmt.Errorf("creating %s: %w", reviewsDir, err)
	}
	return root, reviewsDir, nil
}

// BuildOptions configures Build. Empty Owner, Repo and Branch are detected
// from the git checkout in Dir.
type BuildOptions struct {
	Dir             string
	Owner           string
	Repo            string
	Branch          string
	Base            string // Diff base; defaults to the PR base, then the stack's previous branch.
	ContextLines    int
	IncludeResolved bool
	MaxInlineLines  int
}

// BuildResult describes a written review bundle. Paths are relative to the
// repo root. Files carry stats only; patches live in the bundle.
type BuildResult struct {
	ReviewDir       string                 `json:"review_dir"`
	ReviewPath      string                 `json:"review_path"`
	OverallDiffPath string                 `json:"overall_diff_path,omitempty"`
	DiffsDir        string                 `json:"diffs_dir,omitempty"`
	PRNumber        int                    `json:"pr_number,omitempty"`
	PRURL           string                 `json:"pr_url,omitempty"`
	CurrentBranch   string                 `json:"current_branch"`
	BaseBranch      string                 `json:"base_branch"`
	MergeBase       string                 `json:"merge_base"`
	Head            string                 `json:"head"`
	DiffSource      string                 `json:"diff_source,omitempty"`
	HasLocalChanges bool                   `json:"has_local_changes"`
	UnresolvedCount int                    `json:"unresolved_thread_count"`
	Stats           git.DiffStats          `json:"stats"`
	Files           []git.FileDiff         `json:"files"`
	Stack           *StackInfo             `json:"stack,omitempty"`
	ExternalUsages  []ExternalUsage        `json:"external_usages,omitempty"`
	CIStatus        *github.CIStatusRollup `json:"ci_status,omitempty"`
	RebaseStatus    *RebaseStatus          `json:"rebase_status,omitempty"`
}

// Build gathers the branch's PR threads, CI checks, stack and diff, and
// writes a review bundle (review.md, overall.diff, diffs/ and a
// description.md scaffold kept across runs) under LocalReviewsDir.
func Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	owner, repo, branch := opts.Owner, opts.Repo, opts.Branch

	totalStart := time.Now()
	logger := zerolog.Ctx(ctx)
	logger.Debug().
		Str("dir", dir).
		Str("owner", owner).
		Str("repo", repo).
		Str("branch", branch).
		Str("base", opts.Base).
		Int("context", opts.ContextLines).
		Msg("starting pr-review flow")

	if owner == "" || repo == "" || branch == "" {
		t0 := time.Now()
		info, detectErr := git.DetectRepo(dir)
		if detectErr != nil {
			return nil, fmt.Errorf("detecting repo: %w", detectErr)
		}
		logger.Debug().
			Dur("elapsed", time.Since(t0)).
			Str("owner", info.Owner).
			Str("repo", info.Name).
			Str("branch", info.Branch).
			Msg("detected git repo")
		if owner == "" {
			owner = info.Owner
		}
		if repo == "" {
			repo = info.Name
		}
		if branch == "" {
			branch = info.Branch
		}
	}

	tToken := time.Now()
	token, err := github.ResolveToken()
	if err != nil {
		return nil, err
	}
	logger.Debug().Dur("elapsed", time.Since(tToken)).Msg("resolved github token")

	client := github.NewClient(token)

	stackDone := make(chan struct{})
	var localStack *git.StackData
	go func() {
		tStack := time.Now()
		localStack, _ = git.DetectStack(dir, branch)
		logger.Debug().Dur("elapsed", time.Since(tStack)).Msg("detected local stack")
		close(stackDone)
	}()

	tPR := time.Now()
	pr, err := github.FetchPR(ctx, client, owner, repo, branch)
	if err != nil {
		return nil, fmt.Errorf("fetching PR: %w", err)
	}
	logger.Debug().
		Dur("elapsed", time.Since(tPR)).
		Bool("found", pr != nil).
		Msg("fetched PR metadata")

	<-stackDone
	stackInfo := ResolveStackInfo(pr, localStack, branch)

	diffBase := opts.Base
	if diffBase == "" && pr != nil {
		diffBase = pr.BaseRef
	}
	if diffBase == "" && stackInfo != nil && stackInfo.PrevBranch != "" {
		diffBase = stackInfo.PrevBranch
	}

	var ciStatus *github.CIStatusRollup
	var diff *git.BranchDiffResult
	var usages []ExternalUsage

	g, gctx := errgroup.WithContext(ctx)

	if pr != nil {
		g.Go(func() error {
			tSugg := time.Now()
			if enrichErr := github.EnrichAutomatedSuggestions(gctx, token, owner, repo, pr); enrichErr != nil {
				return fmt.Errorf("fetching Copilot suggestions: %w", enrichErr)
			}
			logger.Debug().Dur("elapsed", time.Since(tSugg)).Msg("enriched automated suggestions")
			return nil
		})
		g.Go(func() error {
			tChecks := time.Now()
			checks, checksErr := github.FetchPRChecks(gctx, client, owner, repo, pr)
			if checksErr == nil {
				ciStatus = checks
			}
			logger.Debug().Dur("elapsed", time.Since(tChecks)).Msg("fetched PR checks")
			return nil
		})
	}

	g.Go(func() error {
		tDiff := time.Now()
		var diffErr error
		diff, diffErr = git.BranchDiff(dir, git.BranchDiffOptions{
			Base:         diffBase,
			ContextLines: opts.ContextLines,
		})
		if diffErr != nil {
			return fmt.Errorf("reading branch diff: %w", diffErr)
		}
		logger.Debug().
			Dur("elapsed", time.Since(tDiff)).
			Int("files", len(diff.Files)).
			Int("insertions", diff.Stats.Insertions).
			Int("deletions", diff.Stats.Deletions).
			Msg("generated branch diff")

		tRefs := time.Now()
		usages, _ = FindExternalReferences(gctx, dir, diff, nil)
		logger.Debug().
			Dur("elapsed", time.Since(tRefs)).
			Int("usages_found", len(usages)).
			Msg("scanned external references")
		return nil
	})

	if waitErr := g.Wait(); waitErr != nil {
		return nil, waitErr
	}

	repoRoot, reviewsBaseDir, err := LocalReviewsDir(dir)
	if err != nil {
		return nil, err
	}

	reviewDirName := Dir(pr, diff)
	reviewDirPath := filepath.Join(reviewsBaseDir, reviewDirName)
	if err := os.MkdirAll(reviewDirPath, 0o750); err != nil {
		return nil, fmt.Errorf("creating review directory: %w", err)
	}

	overallDiffPath := filepath.Join(reviewDirPath, "overall.diff")
	if diff.Patch != "" {
		if err := os.WriteFile(overallDiffPath, []byte(diff.Patch), 0o600); err != nil {
			return nil, fmt.Errorf("writing overall diff: %w", err)
		}
	}

	diffsDirPath := filepath.Join(reviewDirPath, "diffs")
	reviewFilePath := filepath.Join(reviewDirPath, "review.md")

	bundleOpts := BundleOptions{
		PR:              pr,
		Diff:            diff,
		Stack:           stackInfo,
		IncludeResolved: opts.IncludeResolved,
		ExternalUsages:  usages,
		RepoRoot:        repoRoot,
		DiffsDir:        diffsDirPath,
		MaxPatchLines:   opts.MaxInlineLines,
		CIStatus:        ciStatus,
	}
	// FormatBundleWithOptions takes opts by value, so resolve the status here to
	// keep it for the result.
	bundleOpts.RebaseStatus = resolveRebaseStatus(bundleOpts, resolveRepoRoot(bundleOpts))
	tBundle := time.Now()
	content := FormatBundleWithOptions(bundleOpts)
	logger.Debug().
		Dur("elapsed", time.Since(tBundle)).
		Int("bundle_bytes", len(content)).
		Msg("formatted review bundle")

	if err := os.WriteFile(reviewFilePath, []byte(content), 0o600); err != nil {
		return nil, fmt.Errorf("writing review file: %w", err)
	}

	descriptionFilePath := filepath.Join(reviewDirPath, "description.md")
	if _, err := os.Stat(descriptionFilePath); errors.Is(err, os.ErrNotExist) {
		scaffold := ScaffoldDescription(pr, diff)
		_ = os.WriteFile(descriptionFilePath, []byte(scaffold), 0o600)
	}
	logger.Debug().
		Dur("total_elapsed", time.Since(totalStart)).
		Msg("completed pr-review generation")

	return newBuildResult(pr, diff, stackInfo, usages, ciStatus, bundleOpts.RebaseStatus, reviewDirName), nil
}

// newBuildResult summarizes a written bundle. It strips the patches from
// diff.Files, since the bundle holds them.
func newBuildResult(
	pr *github.PR,
	diff *git.BranchDiffResult,
	stack *StackInfo,
	usages []ExternalUsage,
	ciStatus *github.CIStatusRollup,
	rebase *RebaseStatus,
	reviewDirName string,
) *BuildResult {
	unresolved := 0
	if pr != nil {
		for _, thread := range pr.Threads {
			if !thread.IsResolved {
				unresolved++
			}
		}
	}

	for i := range diff.Files {
		diff.Files[i].Patch = ""
	}

	relReviewDir := filepath.Join(".agents", "local", "pr-reviews", reviewDirName)
	result := &BuildResult{
		ReviewDir:       relReviewDir,
		ReviewPath:      filepath.Join(relReviewDir, "review.md"),
		OverallDiffPath: filepath.Join(relReviewDir, "overall.diff"),
		DiffsDir:        filepath.Join(relReviewDir, "diffs"),
		CurrentBranch:   diff.CurrentBranch,
		BaseBranch:      diff.BaseBranch,
		MergeBase:       diff.MergeBase,
		Head:            diff.Head,
		DiffSource:      diff.DiffSource,
		HasLocalChanges: diff.HasLocalChanges,
		UnresolvedCount: unresolved,
		Stats:           diff.Stats,
		Files:           diff.Files,
		Stack:           stack,
		ExternalUsages:  usages,
		CIStatus:        ciStatus,
		RebaseStatus:    rebase,
	}
	if result.Stack != nil && result.Stack.RebaseStatus != nil {
		result.RebaseStatus = result.Stack.RebaseStatus
	}
	if pr != nil {
		result.PRNumber = pr.Number
		result.PRURL = pr.URL
	}
	return result
}

// DescriptionUpdate describes an UpdateDescription run. Body is set only for
// dry runs.
type DescriptionUpdate struct {
	PRNumber int    `json:"pr_number"`
	PRURL    string `json:"pr_url"`
	File     string `json:"file"`
	DryRun   bool   `json:"dry_run"`
	Body     string `json:"body,omitempty"`
}

// UpdateDescription sets the body of the open PR for dir's branch from a
// description.md. An empty file means the bundle's description.md for that
// PR, else any bundle's. With dryRun, GitHub is left alone.
func UpdateDescription(ctx context.Context, dir, file string, dryRun bool) (*DescriptionUpdate, error) {
	if dir == "" {
		dir = "."
	}
	repoRoot, reviewsBaseDir, err := LocalReviewsDir(dir)
	if err != nil {
		return nil, err
	}

	info, err := git.DetectRepo(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("detecting repo: %w", err)
	}

	token, err := github.ResolveToken()
	if err != nil {
		return nil, err
	}

	client := github.NewClient(token)

	pr, err := github.FetchPR(ctx, client, info.Owner, info.Name, info.Branch)
	if err != nil {
		return nil, fmt.Errorf("fetching PR: %w", err)
	}
	if pr == nil {
		return nil, fmt.Errorf("no open PR found for branch %q", info.Branch)
	}

	if file == "" {
		file = findDescription(reviewsBaseDir, pr)
	}
	if file == "" {
		return nil, fmt.Errorf("description.md not found; specify --file")
	}

	//nolint:gosec // file is resolved from repo .agents/local/pr-reviews or user flag
	bodyBytes, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading description file %s: %w", file, err)
	}

	body := PrepareDescription(string(bodyBytes), pr.URL)
	if body == "" {
		return nil, fmt.Errorf("description file %s is empty", file)
	}

	res := &DescriptionUpdate{
		PRNumber: pr.Number,
		PRURL:    pr.URL,
		File:     file,
		DryRun:   dryRun,
	}
	if dryRun {
		res.Body = body
		return res, nil
	}
	if err := github.UpdatePRBody(ctx, client, pr.ID, body); err != nil {
		return nil, err
	}
	return res, nil
}

// findDescription returns the description.md for pr's bundle, else one from
// a bundle for the same PR number, else any bundle's, else "".
func findDescription(reviewsBaseDir string, pr *github.PR) string {
	candidate := filepath.Join(reviewsBaseDir, Dir(pr, nil), "description.md")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	patterns := []string{
		filepath.Join(reviewsBaseDir, fmt.Sprintf("%d-*", pr.Number), "description.md"),
		filepath.Join(reviewsBaseDir, "*", "description.md"),
	}
	for _, pattern := range patterns {
		if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

// CleanBundles deletes review bundles under LocalReviewsDir(dir) and returns
// their names. With all, it deletes every bundle. Otherwise it deletes the
// bundles that match the current branch or its open PR, or the only bundle
// when nothing matches.
func CleanBundles(ctx context.Context, dir string, all bool) (deleted []string, reviewsDir string, err error) {
	if dir == "" {
		dir = "."
	}
	_, reviewsBaseDir, err := LocalReviewsDir(dir)
	if err != nil {
		return nil, "", err
	}

	deleted = []string{}
	remove := func(name string) {
		if rmErr := os.RemoveAll(filepath.Join(reviewsBaseDir, name)); rmErr == nil {
			deleted = append(deleted, name)
		}
	}

	entries, readErr := os.ReadDir(reviewsBaseDir)
	if readErr != nil {
		return deleted, reviewsBaseDir, nil
	}
	if all {
		for _, entry := range entries {
			remove(entry.Name())
		}
		return deleted, reviewsBaseDir, nil
	}

	match := branchMatcher(ctx, dir)
	for _, entry := range entries {
		if match(entry.Name()) {
			remove(entry.Name())
		}
	}
	if len(deleted) == 0 && len(entries) == 1 {
		remove(entries[0].Name())
	}
	return deleted, reviewsBaseDir, nil
}

// branchMatcher reports whether a bundle name belongs to dir's current branch
// or its open PR. The PR lookup is best effort.
func branchMatcher(ctx context.Context, dir string) func(name string) bool {
	info, _ := git.DetectRepo(dir)
	branch := ""
	if info != nil {
		branch = info.Branch
	}
	targetName := ""
	if branch != "" {
		targetName = strings.ReplaceAll(branch, "/", "-")
	}

	var prNumberPrefix string
	var prBundleName string
	if token, tokenErr := github.ResolveToken(); tokenErr == nil && info != nil && branch != "" {
		client := github.NewClient(token)
		if pr, _ := github.FetchPR(ctx, client, info.Owner, info.Name, branch); pr != nil {
			prNumberPrefix = fmt.Sprintf("%d-", pr.Number)
			prBundleName = Dir(pr, nil)
		}
	}

	return func(name string) bool {
		switch {
		case prBundleName != "" && name == prBundleName:
			return true
		case prNumberPrefix != "" && strings.HasPrefix(name, prNumberPrefix):
			return true
		case targetName != "":
			if strings.Contains(name, targetName) || strings.Contains(targetName, name) {
				return true
			}
			parts := strings.SplitN(name, "-", 2)
			return len(parts) == 2 && parts[1] != "" &&
				(strings.Contains(targetName, parts[1]) || strings.Contains(parts[1], targetName))
		}
		return false
	}
}

// BundleEntry is a review bundle on disk. Paths are relative to the repo
// root.
type BundleEntry struct {
	Name            string    `json:"name"`
	Dir             string    `json:"dir"`
	ReviewPath      string    `json:"review_path"`
	DescriptionPath string    `json:"description_path"`
	HasDescription  bool      `json:"has_description"`
	ModTime         time.Time `json:"mod_time"`
}

// ListBundles returns the review bundles under LocalReviewsDir(dir).
func ListBundles(dir string) (bundles []BundleEntry, reviewsDir string, err error) {
	if dir == "" {
		dir = "."
	}
	repoRoot, reviewsBaseDir, err := LocalReviewsDir(dir)
	if err != nil {
		return nil, "", err
	}

	entries, err := os.ReadDir(reviewsBaseDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("reading review bundles: %w", err)
	}

	bundles = []BundleEntry{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bundleDir := filepath.Join(reviewsBaseDir, entry.Name())
		reviewPath := filepath.Join(bundleDir, "review.md")
		descPath := filepath.Join(bundleDir, "description.md")

		modTime := time.Time{}
		if info, statErr := entry.Info(); statErr == nil {
			modTime = info.ModTime()
		}

		hasDesc := false
		if _, sErr := os.Stat(descPath); sErr == nil {
			hasDesc = true
		}

		relDir, relErr := filepath.Rel(repoRoot, bundleDir)
		if relErr != nil {
			relDir = bundleDir
		}
		relReview, _ := filepath.Rel(repoRoot, reviewPath)
		relDesc, _ := filepath.Rel(repoRoot, descPath)

		bundles = append(bundles, BundleEntry{
			Name:            entry.Name(),
			Dir:             relDir,
			ReviewPath:      relReview,
			DescriptionPath: relDesc,
			HasDescription:  hasDesc,
			ModTime:         modTime,
		})
	}
	return bundles, reviewsBaseDir, nil
}
