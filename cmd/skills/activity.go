package skills

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/activity"
	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/git"
	github "github.com/kalverra/agents/internal/github"
	"github.com/kalverra/agents/internal/output"
	"github.com/kalverra/agents/internal/ticket"
)

// ActivityCmd is the root command for activity review operations.
var ActivityCmd = &cobra.Command{
	Use:   "activity",
	Short: "Review and summarize recent activity across Todoist and GitHub",
}

var activityFetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Fetch activity from Todoist Work project and GitHub for a specified timeframe",
	RunE: func(cmd *cobra.Command, _ []string) error {
		timeframe, _ := cmd.Flags().GetString("since")
		projectName, _ := cmd.Flags().GetString("project")

		now := time.Now()
		since, err := activity.ParseTimeframe(timeframe, now)
		if err != nil {
			return err
		}

		ctx := cmd.Context()
		logger := zerolog.Ctx(ctx)

		cfg, loadErr := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
		if loadErr != nil {
			return fmt.Errorf("loading config: %w", loadErr)
		}

		report := &activity.Report{
			Timeframe: timeframe,
			Since:     since,
			Until:     now,
			Project:   projectName,
		}

		// Detect active working directory ticket context
		repo, repoErr := git.DetectRepo(".")
		if repoErr == nil && repo != nil {
			if key := ticket.ExtractJiraKey(repo.Branch); key != "" {
				report.CurrentTicket = &activity.CurrentTicketInfo{
					ID:     key,
					Source: "jira",
				}
			} else if taskRef, rErr := ticket.ParseTaskRef(repo.Branch); rErr == nil && taskRef != "" {
				report.CurrentTicket = &activity.CurrentTicketInfo{
					ID:     taskRef,
					Source: "todoist",
				}
			}
		}

		// Fetch Todoist activity for selected project
		if cfg.TodoistAPIToken != "" {
			td := ticket.NewTodoist(*logger, ticket.TodoistConfig{
				Token:   cfg.TodoistAPIToken,
				BaseURL: cfg.TodoistRESTBase,
			})

			projectID, pErr := td.GetProjectIDByName(ctx, projectName)
			if pErr == nil && projectID != "" {
				completedTasks, cErr := td.FetchCompletedTasks(ctx, projectID)
				if cErr == nil {
					for _, ct := range completedTasks {
						key, isLinked := activity.ParseJiraInfo(ct.Content, ct.Description, ct.Labels)
						if !isLinked {
							continue
						}

						taskObj := activity.TodoistTask{

							ID:           ct.ID,
							Title:        ct.Content,
							Description:  ct.Description,
							CompletedAt:  ct.CompletedAt,
							URL:          ct.URL,
							Labels:       ct.Labels,
							JiraKey:      key,
							IsJiraLinked: isLinked,
						}

						// Fetch task comments
						if fetched, fErr := td.Fetch(ctx, ct.ID); fErr == nil && fetched != nil {
							for _, c := range fetched.Comments {
								taskObj.Comments = append(taskObj.Comments, activity.TaskComment{
									ID:       c.ID,
									PostedAt: c.PostedAt,
									Content:  c.Content,
								})
							}
						}

						report.Todoist = append(report.Todoist, taskObj)
					}
				}
			}
		}

		// Fetch Jira Cloud activity if configured
		if j, jErr := loadJira(cmd); jErr == nil {
			assigned, aErr := j.ListAssignedTickets(ctx)
			if aErr == nil {
				for _, t := range assigned {
					// Filter out stale open/descheduled tickets updated before 'since' window
					statusLower := strings.ToLower(strings.TrimSpace(t.Status))
					isStaleOpen := strings.Contains(statusLower, "open") || strings.Contains(statusLower, "to do") ||
						strings.Contains(statusLower, "descheduled")
					if isStaleOpen && t.UpdatedAt != "" {
						if parsed, ok := activity.ParseTimestamp(t.UpdatedAt); ok && parsed.Before(since) {
							continue
						}
					}

					report.JiraIssues = append(report.JiraIssues, activity.JiraIssueInfo{
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
		}

		// Fetch GitHub activity via gh CLI
		ghAct, ghErr := github.FetchUserActivityWithCLI(ctx, since)
		if ghErr == nil && ghAct != nil {
			for _, pr := range ghAct.PRsOpened {
				jKey, tID := activity.ExtractKeyAndTaskID(pr.Title)
				prInfo := activity.PullRequestInfo{
					Number:     pr.Number,
					Title:      pr.Title,
					Body:       pr.Body,
					URL:        pr.URL,
					State:      pr.State,
					Repository: pr.Repository,
					JiraKey:    jKey,
					TaskID:     tID,
				}
				if commits, cErr := github.FetchPRCommitsWithCLI(ctx, pr.Repository, pr.Number); cErr == nil {
					for _, cm := range commits {
						cjKey, ctID := activity.ExtractKeyAndTaskID(cm.Message)
						prInfo.Commits = append(prInfo.Commits, activity.CommitInfo{
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
				jKey, tID := activity.ExtractKeyAndTaskID(pr.Title)
				report.GitHub.PRsMerged = append(report.GitHub.PRsMerged, activity.PullRequestInfo{
					Number:     pr.Number,
					Title:      pr.Title,
					Body:       pr.Body,
					URL:        pr.URL,
					State:      pr.State,
					Repository: pr.Repository,
					JiraKey:    jKey,
					TaskID:     tID,
				})
			}
			for _, rev := range ghAct.PRReviews {
				report.GitHub.PRReviews = append(report.GitHub.PRReviews, activity.ReviewInfo{
					PRNumber:   rev.Number,
					PRTitle:    rev.Title,
					State:      rev.State,
					Repository: rev.Repository,
					URL:        rev.URL,
				})
			}
		}

		// Write XML report file for AI context
		xmlContent := activity.FormatXMLReport(report)
		_ = os.WriteFile("activity.xml", []byte(xmlContent), 0600)

		if output.JSON() {
			output.Write("activity-fetch", xmlContent, nil)
			return nil
		}

		fmt.Printf(
			"Fetched activity for timeframe %s (%s to %s):\n",
			timeframe,
			since.Format("2006-01-02"),
			now.Format("2006-01-02"),
		)
		fmt.Printf("- Todoist %s project completed tasks: %d\n", projectName, len(report.Todoist))
		fmt.Printf("- Jira issues active/assigned: %d\n", len(report.JiraIssues))
		fmt.Printf(
			"- GitHub PRs opened/merged/reviewed: %d\n",
			len(report.GitHub.PRsOpened)+len(report.GitHub.PRsMerged)+len(report.GitHub.PRReviews),
		)
		fmt.Println("\nSaved activity context to activity.xml")

		return nil
	},
}

func init() {
	activityFetchCmd.Flags().
		String("since", "7d", "Timeframe window (e.g. 1d, 7d, 30d, today, yesterday, week, month, 24h)")
	activityFetchCmd.Flags().String("project", "Work", "Todoist project name filter")

	ActivityCmd.AddCommand(activityFetchCmd)
	Cmd.AddCommand(ActivityCmd)
}
