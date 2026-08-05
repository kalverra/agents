// Package github provides GitHub GraphQL API types and fetch helpers.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/shurcooL/githubv4"
)

// PR holds the fetched data for a GitHub pull request.
type PR struct {
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
	Reviews        []Review
	Threads        []ReviewThread
	Comments       []Comment
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
	ID         string
	IsResolved bool
	IsOutdated bool
	Path       string
	StartLine  int
	Line       int
	Comments   []Comment
}

// Comment is a single comment left on a PR or review thread.
type Comment struct {
	Author      string
	Body        string
	Suggestions []Suggestion
	DiffHunk    string
	CreatedAt   time.Time
}

// FetchPR queries GitHub for the most recent open PR on the given branch.
func FetchPR(ctx context.Context, client *githubv4.Client, owner, repo, branch string) (*PR, error) {
	var query struct {
		Repository struct {
			PullRequests struct {
				Nodes []struct {
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
							ID         string
							IsResolved bool
							IsOutdated bool
							Path       string
							StartLine  int
							Line       int
							Comments   struct {
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
			} `graphql:"pullRequests(headRefName: $branch, states: OPEN, first: 5, orderBy: {field: CREATED_AT, direction: DESC})"`
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

	n := nodes[0]
	pr := &PR{
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
			ID:         t.ID,
			IsResolved: t.IsResolved,
			IsOutdated: t.IsOutdated,
			Path:       t.Path,
			StartLine:  t.StartLine,
			Line:       t.Line,
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

	return pr, nil
}

// PullRequestSummary represents a PR summary from GitHub search.
type PullRequestSummary struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Repository string `json:"repository"`
}

// UserActivity contains GitHub activity for a user within a timeframe.
type UserActivity struct {
	PRsOpened []PullRequestSummary `json:"prs_opened"`
	PRsMerged []PullRequestSummary `json:"prs_merged"`
	PRReviews []PullRequestSummary `json:"pr_reviews"`
}

type ghSearchPRItem struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Repository struct {
		Name          string `json:"name"`
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

// FetchUserActivityWithCLI uses `gh search` to query recent PR activity for the authenticated GitHub user.
func FetchUserActivityWithCLI(ctx context.Context, since time.Time) (*UserActivity, error) {
	sinceStr := since.Format("2006-01-02")

	// Search PRs authored by user updated since target date
	//nolint:gosec
	outAuthored, err := exec.CommandContext(ctx, "gh", "search", "prs", "--author", "@me", "--updated", ">="+sinceStr, "--json", "number,title,body,url,state,repository").
		Output()

	if err != nil {
		return nil, fmt.Errorf("gh search prs --author: %w", err)
	}

	var itemsAuthored []ghSearchPRItem
	if err := json.Unmarshal(outAuthored, &itemsAuthored); err != nil {
		return nil, fmt.Errorf("parsing authored prs: %w", err)
	}

	act := &UserActivity{}
	for _, item := range itemsAuthored {
		repo := item.Repository.NameWithOwner
		if repo == "" {
			repo = item.Repository.Name
		}
		summary := PullRequestSummary{
			Number:     item.Number,
			Title:      item.Title,
			Body:       item.Body,
			URL:        item.URL,
			State:      item.State,
			Repository: repo,
		}
		if strings.EqualFold(item.State, "MERGED") {
			act.PRsMerged = append(act.PRsMerged, summary)
		} else {
			act.PRsOpened = append(act.PRsOpened, summary)
		}
	}

	// Search PRs reviewed / commented by user
	//nolint:gosec
	outReviewed, err := exec.CommandContext(ctx, "gh", "search", "prs", "--commenter", "@me", "--updated", ">="+sinceStr, "--json", "number,title,body,url,state,repository").
		Output()

	if err == nil {
		var itemsReviewed []ghSearchPRItem
		if err := json.Unmarshal(outReviewed, &itemsReviewed); err == nil {
			for _, item := range itemsReviewed {
				repo := item.Repository.NameWithOwner
				if repo == "" {
					repo = item.Repository.Name
				}
				act.PRReviews = append(act.PRReviews, PullRequestSummary{
					Number:     item.Number,
					Title:      item.Title,
					Body:       item.Body,
					URL:        item.URL,
					State:      item.State,
					Repository: repo,
				})
			}
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

type ghPRViewCommits struct {
	Commits []struct {
		Oid             string `json:"oid"`
		CommittedDate   string `json:"committedDate"`
		AuthoredDate    string `json:"authoredDate"`
		MessageHeadline string `json:"messageHeadline"`
	} `json:"commits"`
}

// FetchPRCommitsWithCLI queries commits on a PR using `gh pr view`.
func FetchPRCommitsWithCLI(ctx context.Context, repo string, prNumber int) ([]CommitSummary, error) {
	args := []string{"pr", "view", fmt.Sprintf("%d", prNumber), "--json", "commits"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}

	out, err := exec.CommandContext(ctx, "gh", args...).Output() //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("gh pr view commits: %w", err)
	}

	var data ghPRViewCommits
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("parsing pr commits: %w", err)
	}

	var commits []CommitSummary
	for _, c := range data.Commits {
		sha := c.Oid
		if len(sha) > 7 {
			sha = sha[:7]
		}
		date := c.CommittedDate
		if date == "" {
			date = c.AuthoredDate
		}
		commits = append(commits, CommitSummary{
			SHA:     sha,
			Date:    date,
			Message: c.MessageHeadline,
		})
	}

	return commits, nil
}
