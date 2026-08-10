package activity

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/git"
	github "github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/internal/ticket"
)

// Fetcher gathers activity from Todoist, Jira, and GitHub.
type Fetcher struct {
	Config     *config.Config
	Logger     zerolog.Logger
	Todoist    TodoistClient
	Jira       JiraClient
	GitHub     GitHubClient
	DetectRepo func(dir string) (*git.RepoInfo, error)
}

// NewFetcher creates a Fetcher with real clients derived from cfg.
func NewFetcher(cfg *config.Config, logger zerolog.Logger) *Fetcher {
	f := &Fetcher{
		Config:     cfg,
		Logger:     logger,
		DetectRepo: git.DetectRepo,
	}
	if cfg.TodoistAPIToken != "" {
		f.Todoist = ticket.NewTodoist(logger, ticket.TodoistConfig{
			Token:   cfg.TodoistAPIToken,
			BaseURL: cfg.TodoistRESTBase,
		})
	}
	if cfg.JiraEmail != "" && cfg.JiraAPIToken != "" && cfg.JiraDomain != "" {
		f.Jira = ticket.NewJira(logger, ticket.JiraConfig{
			Email:    cfg.JiraEmail,
			APIToken: cfg.JiraAPIToken,
			Domain:   cfg.JiraDomain,
		})
	}
	f.GitHub = &githubClientAdapter{}
	return f
}

// Fetch returns a populated Report for the given timeframe and project.
func (f *Fetcher) Fetch(ctx context.Context, now time.Time, timeframe, projectName string) (*Report, error) {
	since, err := ParseTimeframe(timeframe, now)
	if err != nil {
		return nil, err
	}

	report := &Report{
		Timeframe: timeframe,
		Since:     since,
		Until:     now,
		Project:   projectName,
	}

	f.fetchCurrentTicket(report)
	f.fetchTodoist(ctx, report, projectName)
	f.fetchJira(ctx, report)
	f.fetchGitHub(ctx, report)

	return report, nil
}

func (f *Fetcher) fetchCurrentTicket(report *Report) {
	if f.DetectRepo == nil {
		return
	}
	repo, repoErr := f.DetectRepo(".")
	if repoErr != nil || repo == nil {
		return
	}
	if key := ticket.ExtractJiraKey(repo.Branch); key != "" {
		report.CurrentTicket = &CurrentTicketInfo{
			ID:     key,
			Source: "jira",
		}
	} else if taskRef, rErr := ticket.ParseTaskRef(repo.Branch); rErr == nil && taskRef != "" {
		report.CurrentTicket = &CurrentTicketInfo{
			ID:     taskRef,
			Source: "todoist",
		}
	}
}

func (f *Fetcher) fetchTodoist(ctx context.Context, report *Report, projectName string) {
	if f.Todoist == nil {
		return
	}
	projectID, pErr := f.Todoist.GetProjectIDByName(ctx, projectName)
	if pErr != nil || projectID == "" {
		return
	}
	completedTasks, cErr := f.Todoist.FetchCompletedTasks(ctx, projectID)
	if cErr != nil {
		return
	}
	for _, ct := range completedTasks {
		if ct.CompletedAt != "" {
			if parsed, ok := ParseTimestamp(ct.CompletedAt); ok && parsed.Before(report.Since) {
				continue
			}
		}
		key, isLinked := ParseJiraInfo(ct.Content, ct.Description, ct.Labels)
		if !isLinked {
			continue
		}

		taskObj := TodoistTask{
			ID:           ct.ID,
			Title:        ct.Content,
			Description:  ct.Description,
			CompletedAt:  ct.CompletedAt,
			URL:          ct.URL,
			Labels:       ct.Labels,
			JiraKey:      key,
			IsJiraLinked: isLinked,
		}

		if fetched, fErr := f.Todoist.Fetch(ctx, ct.ID); fErr == nil && fetched != nil {
			for _, c := range fetched.Comments {
				taskObj.Comments = append(taskObj.Comments, TaskComment{
					ID:       c.ID,
					PostedAt: c.PostedAt,
					Content:  c.Content,
				})
			}
		}

		report.Todoist = append(report.Todoist, taskObj)
	}
}

func (f *Fetcher) fetchJira(ctx context.Context, report *Report) {
	if f.Jira == nil {
		return
	}
	assigned, aErr := f.Jira.ListAssignedTickets(ctx)
	if aErr != nil {
		return
	}
	for _, t := range assigned {
		statusLower := strings.ToLower(strings.TrimSpace(t.Status))
		isStaleOpen := strings.Contains(statusLower, "open") ||
			strings.Contains(statusLower, "to do") ||
			strings.Contains(statusLower, "descheduled")
		isDone := strings.Contains(statusLower, "done") ||
			strings.Contains(statusLower, "close") ||
			strings.Contains(statusLower, "resolve")
		if (isStaleOpen || isDone) && t.UpdatedAt != "" {
			if parsed, ok := ParseTimestamp(t.UpdatedAt); ok && parsed.Before(report.Since) {
				continue
			}
		}

		report.JiraIssues = append(report.JiraIssues, JiraIssueInfo{
			Key:       t.ID,
			Summary:   t.Title,
			Status:    t.Status,
			EpicKey:   t.ParentID,
			EpicTitle: t.ParentTitle,
			Sprint:    t.Sprint,
			URL:       t.URL,
			UpdatedAt: t.UpdatedAt,
		})
	}
}

func (f *Fetcher) fetchGitHub(ctx context.Context, report *Report) {
	if f.GitHub == nil {
		return
	}
	ghAct, ghErr := f.GitHub.FetchUserActivity(ctx, report.Since)
	if ghErr != nil || ghAct == nil {
		return
	}
	for _, pr := range ghAct.PRsOpened {
		if !pr.CreatedAt.IsZero() && pr.CreatedAt.Before(report.Since) {
			continue
		}
		jKey, tID := ExtractKeyAndTaskID(pr.Title)
		prInfo := PullRequestInfo{
			Number:     pr.Number,
			Title:      pr.Title,
			Body:       CleanPRDescription(pr.Body),
			URL:        pr.URL,
			State:      pr.State,
			Repository: pr.Repository,
			CreatedAt:  pr.CreatedAt,
			MergedAt:   pr.MergedAt,
			JiraKey:    jKey,
			TaskID:     tID,
		}
		if commits, cErr := f.GitHub.FetchPRCommits(ctx, pr.Repository, pr.Number); cErr == nil {
			for _, cm := range commits {
				if cm.Date != "" {
					if parsed, ok := ParseTimestamp(cm.Date); ok && parsed.Before(report.Since) {
						continue
					}
				}
				cjKey, ctID := ExtractKeyAndTaskID(cm.Message)
				prInfo.Commits = append(prInfo.Commits, CommitInfo{
					SHA:     cm.SHA,
					Date:    cm.Date,
					Message: cm.Message,
					JiraKey: cjKey,
					TaskID:  ctID,
				})
			}
		}
		report.GitHub.PRsOpened = append(report.GitHub.PRsOpened, prInfo)
	}
	for _, pr := range ghAct.PRsMerged {
		if !pr.MergedAt.IsZero() && pr.MergedAt.Before(report.Since) {
			continue
		}
		jKey, tID := ExtractKeyAndTaskID(pr.Title)
		report.GitHub.PRsMerged = append(report.GitHub.PRsMerged, PullRequestInfo{
			Number:     pr.Number,
			Title:      pr.Title,
			Body:       CleanPRDescription(pr.Body),
			URL:        pr.URL,
			State:      pr.State,
			Repository: pr.Repository,
			CreatedAt:  pr.CreatedAt,
			MergedAt:   pr.MergedAt,
			JiraKey:    jKey,
			TaskID:     tID,
		})
	}
	for _, rev := range ghAct.PRReviews {
		report.GitHub.PRReviews = append(report.GitHub.PRReviews, ReviewInfo{
			PRNumber:   rev.Number,
			PRTitle:    rev.Title,
			State:      rev.State,
			Repository: rev.Repository,
			URL:        rev.URL,
		})
	}
}

// TodoistClient abstracts the parts of ticket.Todoist needed by Fetcher.
type TodoistClient interface {
	GetProjectIDByName(ctx context.Context, name string) (string, error)
	FetchCompletedTasks(ctx context.Context, projectID string) ([]ticket.TodoistCompletedTask, error)
	Fetch(ctx context.Context, id string) (*ticket.Ticket, error)
}

// JiraClient abstracts the parts of ticket.Jira needed by Fetcher.
type JiraClient interface {
	ListAssignedTickets(ctx context.Context) ([]*ticket.Ticket, error)
}

// GitHubClient abstracts the parts of internal/github needed by Fetcher.
type GitHubClient interface {
	FetchUserActivity(ctx context.Context, since time.Time) (*github.UserActivity, error)
	FetchPRCommits(ctx context.Context, repository string, prNumber int) ([]github.CommitSummary, error)
}

type githubClientAdapter struct{}

func (githubClientAdapter) FetchUserActivity(ctx context.Context, since time.Time) (*github.UserActivity, error) {
	return github.FetchUserActivityWithCLI(ctx, since)
}

func (githubClientAdapter) FetchPRCommits(
	ctx context.Context,
	repository string,
	prNumber int,
) ([]github.CommitSummary, error) {
	return github.FetchPRCommitsWithCLI(ctx, repository, prNumber)
}
