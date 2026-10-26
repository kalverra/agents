// Package github provides GitHub GraphQL API types and fetch helpers.
package github

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/shurcooL/githubv4"
)

// PR holds the fetched data for a GitHub pull request.
type PR struct {
	ID             string `graphql:"id"`
	Number         int
	Title          string
	Body           string
	URL            string
	State          string
	CreatedAt      time.Time
	Author         string
	BaseRef        string
	HeadRef        string
	ReviewDecision string
	Mergeable      string
	Reviews        []Review
	Threads        []ReviewThread
	Comments       []Comment
	Stack          *PRStack
}

// PRStack represents the stack of pull requests a PR belongs to.
type PRStack struct {
	ID          string
	Number      int
	Size        int
	BaseRefName string
	Entries     []PRStackEntry
}

// PRStackEntry represents an individual layer/PR entry in a stack.
type PRStackEntry struct {
	Position    int
	Number      int
	Title       string
	HeadRefName string
	BaseRefName string
	State       string
	URL         string
}

// Review represents a reviewer's overall assessment of a pull request.
type Review struct {
	Author    string
	State     string
	Body      string
	CreatedAt time.Time
}

// ReviewThread groups inline review comments on a specific file location.
type ReviewThread struct {
	ID           string
	IsResolved   bool
	IsOutdated   bool
	Path         string
	StartLine    int
	Line         int
	OriginalLine int
	DiffSide     string
	SubjectType  string
	Comments     []Comment
}

// IsFileLevel reports whether the review thread is attached to a file rather than a line.
func (t *ReviewThread) IsFileLevel() bool {
	if t == nil {
		return false
	}
	if strings.EqualFold(t.SubjectType, "FILE") {
		return true
	}
	if strings.EqualFold(t.SubjectType, "LINE") {
		return false
	}
	return t.Line == 0 && t.Path != ""
}

// Comment is a single comment left on a PR or review thread.
type Comment struct {
	Author      string
	Body        string
	Suggestions []Suggestion
	DiffHunk    string
	CreatedAt   time.Time
}

// CheckRun represents a single CI check or status context on a PR.
type CheckRun struct {
	Name        string `json:"name"`
	Workflow    string `json:"workflow,omitempty"`
	State       string `json:"state"`
	Bucket      string `json:"bucket"`
	Link        string `json:"link,omitempty"`
	Description string `json:"description,omitempty"`
	IsRequired  bool   `json:"is_required"`
}

// Verdict returns an explicit verdict for the check run (e.g. blocking vs non-blocking).
func (c CheckRun) Verdict() string {
	bucket := strings.ToLower(c.Bucket)
	if bucket == "" {
		bucket = strings.ToLower(c.State)
	}
	switch bucket {
	case "pass", "success":
		return "passed"
	case "pending":
		if c.IsRequired {
			return "BLOCKING (required check pending)"
		}
		return "non-blocking (optional check pending)"
	case "fail", "failure", "error", "cancel":
		if c.IsRequired {
			return "BLOCKING (required check failed)"
		}
		return "failing (optional check failed)"
	case "skipping", "skip", "skipped":
		return "skipped"
	default:
		return bucket
	}
}

// CIStatusRollup summarizes the status of all CI checks on a PR.
type CIStatusRollup struct {
	Total           int        `json:"total"`
	Pass            int        `json:"pass"`
	Fail            int        `json:"fail"`
	Pending         int        `json:"pending"`
	Skip            int        `json:"skip"`
	PendingRequired int        `json:"pending_required"`
	PendingOptional int        `json:"pending_optional"`
	FailRequired    int        `json:"fail_required"`
	FailOptional    int        `json:"fail_optional"`
	Checks          []CheckRun `json:"checks,omitempty"`
}

// rollupFromChecks computes the CIStatusRollup counters from bucketed checks,
// using the same semantics as the former `gh pr checks --json` parsing.
func rollupFromChecks(checks []CheckRun) *CIStatusRollup {
	rollup := &CIStatusRollup{
		Total:  len(checks),
		Checks: checks,
	}

	for i := range rollup.Checks {
		c := &rollup.Checks[i]

		bucket := strings.ToLower(c.Bucket)
		if bucket == "" {
			bucket = strings.ToLower(c.State)
		}

		switch bucket {
		case "pass", "success":
			rollup.Pass++
		case "fail", "failure", "error", "cancel":
			rollup.Fail++
			if c.IsRequired {
				rollup.FailRequired++
			} else {
				rollup.FailOptional++
			}
		case "pending":
			rollup.Pending++
			if c.IsRequired {
				rollup.PendingRequired++
			} else {
				rollup.PendingOptional++
			}
		case "skipping", "skip", "skipped":
			rollup.Skip++
		default:
			rollup.Skip++
		}
	}
	return rollup
}

// checkContextNode is a single statusCheckRollup context: a CheckRun or a StatusContext.
type checkContextNode struct {
	TypeName string `graphql:"__typename"`

	StatusContext struct {
		Context     string
		State       string
		TargetURL   string `graphql:"targetUrl"`
		Description string
		CreatedAt   time.Time
		IsRequired  bool `graphql:"isRequired(pullRequestId: $prId)"`
	} `graphql:"... on StatusContext"`

	CheckRun struct {
		Name        string
		Status      string
		Conclusion  string
		StartedAt   time.Time
		CompletedAt time.Time
		DetailsURL  string `graphql:"detailsUrl"`
		IsRequired  bool   `graphql:"isRequired(pullRequestId: $prId)"`
		CheckSuite  struct {
			WorkflowRun struct {
				Event    string
				Workflow struct {
					Name string
				}
			}
		}
	} `graphql:"... on CheckRun"`
}

// checkBucket maps a check state to a bucket, mirroring `gh pr checks`.
func checkBucket(state string) string {
	switch state {
	case "SUCCESS":
		return "pass"
	case "SKIPPED", "NEUTRAL":
		return "skipping"
	case "ERROR", "FAILURE", "TIMED_OUT", "ACTION_REQUIRED":
		return "fail"
	case "CANCELLED":
		return "cancel"
	default: // EXPECTED, REQUESTED, WAITING, QUEUED, PENDING, IN_PROGRESS, STALE
		return "pending"
	}
}

// aggregateCheckContexts converts rollup context nodes into bucketed CheckRuns,
// keeping only the most recent run per check (mirroring `gh pr checks` dedup).
func aggregateCheckContexts(nodes []checkContextNode) []CheckRun {
	// Sort most-recent first so duplicate runs of the same check lose to newer ones.
	slices.SortFunc(nodes, func(a, b checkContextNode) int {
		return b.CheckRun.StartedAt.Compare(a.CheckRun.StartedAt)
	})

	seenChecks := make(map[string]bool)
	checks := make([]CheckRun, 0, len(nodes))

	for _, n := range nodes {
		var check CheckRun
		var dedupKey string
		switch {
		case n.TypeName == "StatusContext" || n.StatusContext.Context != "":
			state := n.StatusContext.State
			check = CheckRun{
				Name:        n.StatusContext.Context,
				State:       state,
				Bucket:      checkBucket(state),
				Link:        n.StatusContext.TargetURL,
				Description: n.StatusContext.Description,
				IsRequired:  n.StatusContext.IsRequired,
			}
			dedupKey = "ctx:" + n.StatusContext.Context
		default:
			state := n.CheckRun.Status
			if state == "COMPLETED" {
				state = n.CheckRun.Conclusion
			}
			check = CheckRun{
				Name:       n.CheckRun.Name,
				State:      state,
				Bucket:     checkBucket(state),
				Link:       n.CheckRun.DetailsURL,
				IsRequired: n.CheckRun.IsRequired,
				Workflow:   n.CheckRun.CheckSuite.WorkflowRun.Workflow.Name,
			}
			dedupKey = fmt.Sprintf(
				"run:%s/%s/%s",
				n.CheckRun.Name,
				check.Workflow,
				n.CheckRun.CheckSuite.WorkflowRun.Event,
			)
		}

		if dedupKey == "" || seenChecks[dedupKey] {
			continue
		}
		seenChecks[dedupKey] = true

		if check.Link == "" && n.StatusContext.TargetURL != "" {
			check.Link = n.StatusContext.TargetURL
		}
		checks = append(checks, check)
	}
	return checks
}

// FetchPRChecks queries the CI check rollup for a PR via the GitHub GraphQL API,
// returning the rollup status including required-check tagging.
func FetchPRChecks(ctx context.Context, client *githubv4.Client, owner, repo string, pr *PR) (*CIStatusRollup, error) {
	if pr == nil || pr.Number <= 0 {
		return nil, nil
	}

	prID := pr.ID
	if prID == "" {
		var err error
		prID, err = lookupPRID(ctx, client, owner, repo, pr.Number)
		if err != nil {
			return nil, err
		}
	}

	var query struct {
		Node struct {
			PullRequest struct {
				Commits struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup struct {
								Contexts struct {
									Nodes    []checkContextNode
									PageInfo struct {
										HasNextPage bool
										EndCursor   string
									}
								} `graphql:"contexts(first: 100, after: $cursor)"`
							}
						}
					}
				} `graphql:"commits(last: 1)"`
			} `graphql:"... on PullRequest"`
		} `graphql:"node(id: $prId)"`
	}

	variables := map[string]any{
		"prId":   githubv4.ID(prID),
		"cursor": (*githubv4.String)(nil),
	}

	var nodes []checkContextNode
	for range 5 {
		if err := client.Query(ctx, &query, variables); err != nil {
			return nil, fmt.Errorf("GitHub GraphQL query failed: %w", err)
		}
		commits := query.Node.PullRequest.Commits.Nodes
		if len(commits) == 0 {
			return nil, nil
		}
		nodes = append(nodes, commits[0].Commit.StatusCheckRollup.Contexts.Nodes...)
		if !commits[0].Commit.StatusCheckRollup.Contexts.PageInfo.HasNextPage {
			break
		}
		variables["cursor"] = new(githubv4.String(commits[0].Commit.StatusCheckRollup.Contexts.PageInfo.EndCursor))
	}

	checks := aggregateCheckContexts(nodes)
	if len(checks) == 0 {
		return nil, nil
	}
	return rollupFromChecks(checks), nil
}

// lookupPRID resolves a PR's GraphQL node ID from its owner/repo/number.
func lookupPRID(ctx context.Context, client *githubv4.Client, owner, repo string, prNumber int) (string, error) {
	var query struct {
		Repository struct {
			PullRequest struct {
				ID string `graphql:"id"`
			} `graphql:"pullRequest(number: $prNumber)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}
	variables := map[string]any{
		"owner":    githubv4.String(owner),
		"name":     githubv4.String(repo),
		"prNumber": githubv4.Int(prNumber), //nolint:gosec // PR numbers fit comfortably in int32
	}
	if err := client.Query(ctx, &query, variables); err != nil {
		return "", fmt.Errorf("GitHub GraphQL query failed: %w", err)
	}
	return query.Repository.PullRequest.ID, nil
}

// UpdatePRBody updates the markdown description (body) of a PR by its GraphQL node ID.
func UpdatePRBody(ctx context.Context, client *githubv4.Client, prID, body string) error {
	var mutation struct {
		UpdatePullRequest struct {
			PullRequest struct {
				ID  string `graphql:"id"`
				URL string `graphql:"url"`
			}
		} `graphql:"updatePullRequest(input: $input)"`
	}
	input := githubv4.UpdatePullRequestInput{
		PullRequestID: prID,
		Body:          new(githubv4.String(body)),
	}
	if err := client.Mutate(ctx, &mutation, input, nil); err != nil {
		return fmt.Errorf("updating PR description: %w", err)
	}
	return nil
}

// prQueryFields is the shared PR field selection used by FetchPR and its stack-less fallback.
type prQueryFields struct {
	ID        string `graphql:"id"`
	Number    int
	Title     string
	Body      string
	URL       string `graphql:"url"`
	State     string
	CreatedAt time.Time
	Author    struct {
		Login string
	}
	BaseRefName    string
	HeadRefName    string
	ReviewDecision string
	Mergeable      string

	Reviews struct {
		Nodes []struct {
			Author struct {
				Login string
			}
			State     string
			Body      string
			CreatedAt time.Time
		}
	} `graphql:"reviews(first: 50)"`

	ReviewThreads struct {
		Nodes []struct {
			ID           string
			IsResolved   bool
			IsOutdated   bool
			Path         string
			StartLine    int
			Line         int
			OriginalLine int
			DiffSide     string
			SubjectType  string
			Comments     struct {
				Nodes []struct {
					Author struct {
						Login string
					}
					Body      string
					DiffHunk  string
					CreatedAt time.Time
				}
			} `graphql:"comments(first: 50)"`
		}
	} `graphql:"reviewThreads(first: 100)"`

	Comments struct {
		Nodes []struct {
			Author struct {
				Login string
			}
			Body      string
			CreatedAt time.Time
		}
	} `graphql:"comments(first: 100)"`
}

// prStackQuery is the stacked-PR field selection, only present on the primary FetchPR query.
type prStackQuery struct {
	ID          string
	Number      int
	Size        int
	BaseRefName string
	Entries     struct {
		Nodes []struct {
			Position    int
			PullRequest struct {
				Number      int
				Title       string
				HeadRefName string
				BaseRefName string
				State       string
				URL         string `graphql:"url"`
			}
		}
	} `graphql:"entries(first: 50)"`
}

func mapPRFields(n *prQueryFields) *PR {
	pr := &PR{
		ID:             n.ID,
		Number:         n.Number,
		Title:          n.Title,
		Body:           n.Body,
		URL:            n.URL,
		State:          n.State,
		CreatedAt:      n.CreatedAt,
		Author:         n.Author.Login,
		BaseRef:        n.BaseRefName,
		HeadRef:        n.HeadRefName,
		ReviewDecision: n.ReviewDecision,
		Mergeable:      n.Mergeable,
	}

	for _, r := range n.Reviews.Nodes {
		pr.Reviews = append(pr.Reviews, Review{
			Author:    r.Author.Login,
			State:     r.State,
			Body:      r.Body,
			CreatedAt: r.CreatedAt,
		})
	}

	for _, t := range n.ReviewThreads.Nodes {
		thread := ReviewThread{
			ID:           t.ID,
			IsResolved:   t.IsResolved,
			IsOutdated:   t.IsOutdated,
			Path:         t.Path,
			StartLine:    t.StartLine,
			Line:         t.Line,
			OriginalLine: t.OriginalLine,
			DiffSide:     t.DiffSide,
			SubjectType:  t.SubjectType,
		}
		for _, c := range t.Comments.Nodes {
			body, suggestions := ExtractSuggestions(c.Body)
			thread.Comments = append(thread.Comments, Comment{
				Author:      c.Author.Login,
				Body:        body,
				Suggestions: suggestions,
				DiffHunk:    c.DiffHunk,
				CreatedAt:   c.CreatedAt,
			})
		}
		pr.Threads = append(pr.Threads, thread)
	}

	for _, c := range n.Comments.Nodes {
		pr.Comments = append(pr.Comments, Comment{
			Author:    c.Author.Login,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
		})
	}

	return pr
}

func mapPRStack(s *prStackQuery) *PRStack {
	stack := &PRStack{
		ID:          s.ID,
		Number:      s.Number,
		Size:        s.Size,
		BaseRefName: s.BaseRefName,
	}
	for _, entryNode := range s.Entries.Nodes {
		stack.Entries = append(stack.Entries, PRStackEntry{
			Position:    entryNode.Position,
			Number:      entryNode.PullRequest.Number,
			Title:       entryNode.PullRequest.Title,
			HeadRefName: entryNode.PullRequest.HeadRefName,
			BaseRefName: entryNode.PullRequest.BaseRefName,
			State:       entryNode.PullRequest.State,
			URL:         entryNode.PullRequest.URL,
		})
	}
	return stack
}

// FetchPR queries GitHub for the most recent open PR on the given branch.
func FetchPR(ctx context.Context, client *githubv4.Client, owner, repo, branch string) (*PR, error) {
	var query struct {
		Repository struct {
			PullRequests struct {
				Nodes []struct {
					prQueryFields
					Stack *prStackQuery
				}
			} `graphql:"pullRequests(headRefName: $branch, states: OPEN, first: 1, orderBy: {field: CREATED_AT, direction: DESC})"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	variables := map[string]any{
		"owner":  githubv4.String(owner),
		"name":   githubv4.String(repo),
		"branch": githubv4.String(branch),
	}

	if err := client.Query(ctx, &query, variables); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "stack") {
			return fetchPRWithoutStack(ctx, client, owner, repo, branch)
		}
		return nil, fmt.Errorf("GitHub GraphQL query failed: %w", err)
	}

	nodes := query.Repository.PullRequests.Nodes
	if len(nodes) == 0 {
		return nil, nil
	}

	pr := mapPRFields(&nodes[0].prQueryFields)
	if nodes[0].Stack != nil {
		pr.Stack = mapPRStack(nodes[0].Stack)
	}
	return pr, nil
}

func fetchPRWithoutStack(ctx context.Context, client *githubv4.Client, owner, repo, branch string) (*PR, error) {
	var query struct {
		Repository struct {
			PullRequests struct {
				Nodes []struct {
					prQueryFields
				}
			} `graphql:"pullRequests(headRefName: $branch, states: OPEN, first: 1, orderBy: {field: CREATED_AT, direction: DESC})"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	variables := map[string]any{
		"owner":  githubv4.String(owner),
		"name":   githubv4.String(repo),
		"branch": githubv4.String(branch),
	}

	if err := client.Query(ctx, &query, variables); err != nil {
		return nil, fmt.Errorf("GitHub GraphQL query failed: %w", err)
	}

	nodes := query.Repository.PullRequests.Nodes
	if len(nodes) == 0 {
		return nil, nil
	}

	return mapPRFields(&nodes[0].prQueryFields), nil
}

// PullRequestSummary represents a PR summary from GitHub search.
type PullRequestSummary struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	Body       string    `json:"body,omitempty"`
	URL        string    `json:"url"`
	State      string    `json:"state"`
	Repository string    `json:"repository"`
	CreatedAt  time.Time `json:"created_at,omitzero"`
	MergedAt   time.Time `json:"merged_at,omitzero"`
}

// UserActivity contains GitHub activity for a user within a timeframe.
type UserActivity struct {
	PRsOpened []PullRequestSummary `json:"prs_opened"`
	PRsMerged []PullRequestSummary `json:"prs_merged"`
	PRReviews []PullRequestSummary `json:"pr_reviews"`
}

type searchPRFields struct {
	Number     int
	Title      string
	Body       string
	URL        string `graphql:"url"`
	State      string
	CreatedAt  time.Time
	ClosedAt   time.Time
	Repository struct {
		Name          string
		NameWithOwner string
	}
}

func (s searchPRFields) summary() PullRequestSummary {
	repo := s.Repository.NameWithOwner
	if repo == "" {
		repo = s.Repository.Name
	}
	return PullRequestSummary{
		Number:     s.Number,
		Title:      s.Title,
		Body:       s.Body,
		URL:        s.URL,
		State:      s.State,
		Repository: repo,
		CreatedAt:  s.CreatedAt,
	}
}

// searchPRs runs a GraphQL search for PRs matching query, following pagination up to 5 pages.
func searchPRs(ctx context.Context, client *githubv4.Client, query string) ([]searchPRFields, error) {
	var q struct {
		Search struct {
			Nodes []struct {
				PullRequest searchPRFields `graphql:"... on PullRequest"`
			}
			PageInfo struct {
				HasNextPage bool
				EndCursor   string
			}
		} `graphql:"search(query: $query, type: ISSUE, first: 100, after: $cursor)"`
	}
	variables := map[string]any{
		"query":  githubv4.String(query),
		"cursor": (*githubv4.String)(nil),
	}

	var results []searchPRFields
	for range 5 {
		if err := client.Query(ctx, &q, variables); err != nil {
			return nil, err
		}
		for _, n := range q.Search.Nodes {
			results = append(results, n.PullRequest)
		}
		if !q.Search.PageInfo.HasNextPage {
			break
		}
		variables["cursor"] = new(githubv4.String(q.Search.PageInfo.EndCursor))
	}
	return results, nil
}

// FetchUserActivity queries recent PR activity for the authenticated GitHub user via GraphQL search.
func FetchUserActivity(ctx context.Context, client *githubv4.Client, since time.Time) (*UserActivity, error) {
	sinceStr := since.Format("2006-01-02")

	authored, err := searchPRs(ctx, client, fmt.Sprintf("is:pr author:@me updated:>=%s", sinceStr))
	if err != nil {
		return nil, fmt.Errorf("searching authored prs: %w", err)
	}

	act := &UserActivity{}
	for _, item := range authored {
		summary := item.summary()
		if strings.EqualFold(item.State, "MERGED") {
			summary.MergedAt = item.ClosedAt
			act.PRsMerged = append(act.PRsMerged, summary)
		} else {
			act.PRsOpened = append(act.PRsOpened, summary)
		}
	}

	// Search PRs reviewed / commented by user; best-effort, errors ignored.
	if reviewed, rErr := searchPRs(
		ctx,
		client,
		fmt.Sprintf("is:pr commenter:@me updated:>=%s", sinceStr),
	); rErr == nil {
		for _, item := range reviewed {
			act.PRReviews = append(act.PRReviews, item.summary())
		}
	}

	return act, nil
}

// CommitSummary holds basic commit details from GitHub PR.
type CommitSummary struct {
	SHA     string `json:"sha"`
	Date    string `json:"date"`
	Message string `json:"message"`
}

type restPRCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Author struct {
			Date string `json:"date"`
		} `json:"author"`
		Committer struct {
			Date string `json:"date"`
		} `json:"committer"`
		Message string `json:"message"`
	} `json:"commit"`
}

// FetchPRCommits queries commits on a PR using the GitHub REST API.
func FetchPRCommits(ctx context.Context, repo string, prNumber int) ([]CommitSummary, error) {
	owner, name, found := strings.Cut(repo, "/")
	if !found || owner == "" || name == "" {
		return nil, fmt.Errorf("invalid repository %q: expected owner/name", repo)
	}

	client, err := NewRESTClient()
	if err != nil {
		return nil, fmt.Errorf("creating GitHub REST client: %w", err)
	}

	var commits []CommitSummary
	for page := 1; page <= 10; page++ {
		path := fmt.Sprintf("repos/%s/%s/pulls/%d/commits?per_page=100&page=%d", owner, name, prNumber, page)
		var pageCommits []restPRCommit
		if err := client.DoWithContext(ctx, http.MethodGet, path, nil, &pageCommits); err != nil {
			return nil, fmt.Errorf("fetching PR commits: %w", err)
		}
		for _, c := range pageCommits {
			sha := c.SHA
			if len(sha) > 7 {
				sha = sha[:7]
			}
			date := c.Commit.Committer.Date
			if date == "" {
				date = c.Commit.Author.Date
			}
			commits = append(commits, CommitSummary{
				SHA:     sha,
				Date:    date,
				Message: commitHeadline(c.Commit.Message),
			})
		}
		if len(pageCommits) < 100 {
			break
		}
	}

	return commits, nil
}

// commitHeadline returns the first line of a commit message.
func commitHeadline(message string) string {
	headline, _, _ := strings.Cut(message, "\n")
	return strings.TrimSpace(headline)
}
