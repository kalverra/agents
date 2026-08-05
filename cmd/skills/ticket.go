package skills

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/git"
	"github.com/kalverra/agents/internal/output"
	"github.com/kalverra/agents/internal/ticket"
)

// TicketCmd is the root command for ticket operations.
var TicketCmd = &cobra.Command{
	Use:   "ticket",
	Short: "Todoist and Jira issue helpers for session workflows",
}

var ticketFetchCmd = &cobra.Command{
	Use:   "fetch <task_id_or_url_or_jira_key>",
	Short: "Fetch a Todoist or Jira task by id, task URL, or Jira key (defaults to Todoist)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		useJira, _ := cmd.Flags().GetBool("jira")
		ref := args[0]
		ctx := cmd.Context()

		var tk *ticket.Ticket
		var err error

		if !useJira {
			td, loadErr := loadTodoist(cmd)
			if loadErr != nil {
				return loadErr
			}
			if key, parseErr := ticket.ParseJiraRef(ref); parseErr == nil {
				tk, err = td.SearchTasksByJiraKey(ctx, key)
			} else {
				tk, err = td.Fetch(ctx, ref)
			}
			if err != nil && errors.Is(err, ticket.ErrNotJiraRef) {
				// Fall back to Jira if not found or ref shape demands Jira
				j, jErr := loadJira(cmd)
				if jErr == nil {
					tk, err = j.Fetch(ctx, ref)
				}
			}
		} else {
			j, jErr := loadJira(cmd)
			if jErr != nil {
				return jErr
			}
			tk, err = j.Fetch(ctx, ref)
		}

		if err != nil {
			return err
		}

		// Write <ID>.md markdown file for AI agent context
		fileName := tk.ID + ".md"
		mdContent := ticket.TicketToMarkdownFile(tk)
		_ = os.WriteFile(fileName, []byte(mdContent), 0600)

		payload := tk.ToFetchPayload()
		if output.JSON() {
			outXML := fmt.Sprintf(
				"<ticket_fetch status=\"ok\" file=%q>\n%s</ticket_fetch>",
				fileName,
				ticket.FetchPayloadToAIXML(payload),
			)
			output.Write("ticket-fetch", outXML, nil)
			return nil
		}
		printTicket(tk)
		fmt.Printf("\nSaved ticket context to %s\n", fileName)
		return nil
	},
}

var ticketCommentCmd = &cobra.Command{
	Use:   "comment <task_id_or_url_or_jira_key>",
	Short: "Add a comment to a task (defaults to Todoist)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body, err := cmd.Flags().GetString("body")
		if err != nil {
			return err
		}
		useJira, _ := cmd.Flags().GetBool("jira")
		ref := args[0]
		ctx := cmd.Context()

		var targetID string

		if !useJira {
			td, loadErr := loadTodoist(cmd)
			if loadErr != nil {
				return loadErr
			}
			if key, parseErr := ticket.ParseJiraRef(ref); parseErr == nil {
				matchedTask, sErr := td.SearchTasksByJiraKey(ctx, key)
				if sErr != nil {
					return fmt.Errorf("could not find Todoist task for Jira key %s: %w", key, sErr)
				}
				targetID = matchedTask.ID
			} else {
				resolved, rErr := ticket.ParseTaskRef(ref)
				if rErr != nil {
					return rErr
				}
				targetID = resolved
			}

			if err := td.Comment(ctx, targetID, body); err != nil {
				return err
			}
		} else {
			j, jErr := loadJira(cmd)
			if jErr != nil {
				return jErr
			}
			key, parseErr := ticket.ParseJiraRef(ref)
			if parseErr != nil {
				return parseErr
			}
			targetID = key
			if err := j.Comment(ctx, targetID, body); err != nil {
				return err
			}
		}

		if output.JSON() {
			outXML := fmt.Sprintf("<ticket_comment task_id=%q status=\"posted\"/>", targetID)
			output.Write("ticket-comment", outXML, nil)
			return nil
		}

		output.Write("ticket-comment", map[string]string{"task_id": targetID}, func() {
			fmt.Printf("Comment posted on %s\n", targetID)
		})
		return nil
	},
}

var ticketCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new issue/task (defaults to Jira Cloud in work repos, Todoist in personal repos; use --todoist or --jira to override)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		title, _ := cmd.Flags().GetString("title")
		desc, _ := cmd.Flags().GetString("description")
		useTodoist, _ := cmd.Flags().GetBool("todoist")
		useJiraFlag, _ := cmd.Flags().GetBool("jira")
		epicKey, _ := cmd.Flags().GetString("epic")
		projectKey, _ := cmd.Flags().GetString("project")
		issueType, _ := cmd.Flags().GetString("type")

		ctx := cmd.Context()
		cfg, loadErr := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
		if loadErr != nil {
			return loadErr
		}

		// Auto-default to Todoist in personal repos if neither --todoist nor --jira flag was explicitly set
		if !useTodoist && !useJiraFlag {
			repo, rErr := git.DetectRepo(".")
			if rErr == nil && repo != nil {
				isWork := ticket.IsWorkRepo(repo, cfg.WorkRepos)
				if !isWork {
					useTodoist = true
				}
			}
		}

		var tk *ticket.Ticket
		var err error

		if useTodoist {
			td, tErr := loadTodoist(cmd)
			if tErr != nil {
				return tErr
			}
			tk, err = td.CreateTask(ctx, ticket.CreateTaskRequest{
				Title:       title,
				Description: desc,
			})
		} else {
			j, jErr := loadJira(cmd)
			if jErr != nil {
				return jErr
			}
			if projectKey == "" {
				projectKey = cfg.JiraDefaultProject
			}
			tk, err = j.CreateIssue(ctx, ticket.CreateIssueRequest{
				Project:     projectKey,
				Summary:     title,
				Description: desc,
				IssueType:   issueType,
				EpicKey:     epicKey,
			})
		}

		if err != nil {
			return err
		}

		if output.JSON() {
			outXML := fmt.Sprintf("<ticket_create id=%q title=%q url=%q/>", tk.ID, tk.Title, tk.URL)
			output.Write("ticket-create", outXML, nil)
			return nil
		}

		output.Write("ticket-create", tk, func() {
			fmt.Printf("Created ticket %s: %s\nURL: %s\n", tk.ID, tk.Title, tk.URL)
		})
		return nil
	},
}

var ticketEpicsCmd = &cobra.Command{
	Use:   "epics",
	Short: "List active Jira Epics assigned to current user or updated recently",
	RunE: func(cmd *cobra.Command, _ []string) error {
		j, err := loadJira(cmd)
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		epics, err := j.ListEpics(ctx)
		if err != nil {
			return err
		}
		if output.JSON() {
			var b strings.Builder
			b.WriteString("<epics>\n")
			for _, e := range epics {
				fmt.Fprintf(&b, "  <epic id=%q title=%q status=%q/>\n", e.ID, e.Title, e.Status)
			}
			b.WriteString("</epics>")
			output.Write("ticket-epics", b.String(), nil)
			return nil
		}
		output.Write("ticket-epics", epics, func() {
			if len(epics) == 0 {
				fmt.Println("No active Jira Epics found.")
				return
			}
			fmt.Println("Active Jira Epics:")
			for _, e := range epics {
				fmt.Printf("  - %s: %s [%s]\n", e.ID, e.Title, e.Status)
			}
		})
		return nil
	},
}

var ticketStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Inspect current working directory git repo, work status, and branch Jira ticket",
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		repo, repoErr := git.DetectRepo(".")
		isWork := false
		jiraKey := ""
		defaultBranch := ""
		dirtyFiles := 0
		untrackedFiles := 0
		var warnings []string
		var candidateEpics []*ticket.Ticket
		var assignedTickets []*ticket.Ticket
		var assignedTodoist []*ticket.Ticket
		var matchedJira *ticket.Ticket
		var matchedTodoist *ticket.Ticket

		if repoErr != nil {
			warnings = append(warnings, fmt.Sprintf("git repo detection: %v", repoErr))
		} else if repo != nil {
			isWork = ticket.IsWorkRepo(repo, cfg.WorkRepos)
			jiraKey = ticket.ExtractJiraKey(repo.Branch)
			defaultBranch, _ = git.DetectDefaultBranch(".")

			d, u, wErr := git.WorktreeState(".")
			if wErr != nil {
				warnings = append(warnings, fmt.Sprintf("worktree state check failed: %v", wErr))
			} else {
				dirtyFiles = d
				untrackedFiles = u
			}
		}

		mode := "personal"
		if isWork {
			mode = "work"
		}

		ctx := cmd.Context()
		writtenFile := ""

		if isWork {
			if jiraKey != "" {
				j, jErr := loadJira(cmd)
				if jErr != nil {
					warnings = append(warnings, fmt.Sprintf("jira load failed: %v", jErr))
				} else {
					matchedJira, err = j.Fetch(ctx, jiraKey)
					if err != nil {
						warnings = append(warnings, fmt.Sprintf("jira fetch failed: %v", err))
					}
				}
				if td, tErr := loadTodoist(cmd); tErr == nil {
					matchedTodoist, _ = td.SearchTasksByJiraKey(ctx, jiraKey)
				}

				// Automatically write <KEY>.md file if ticket data is fetched
				var targetTicket *ticket.Ticket
				if matchedJira != nil {
					targetTicket = matchedJira
				} else if matchedTodoist != nil {
					targetTicket = matchedTodoist
				}
				if targetTicket != nil {
					writtenFile = jiraKey + ".md"
					mdContent := ticket.TicketToMarkdownFile(targetTicket)
					_ = os.WriteFile(writtenFile, []byte(mdContent), 0600)
				}
			} else {
				branchName := ""
				if repo != nil {
					branchName = repo.Branch
				}
				if w := dirtyDefaultBranchWarning(dirtyFiles, untrackedFiles, branchName, defaultBranch); w != "" {
					warnings = append(warnings, w)
				}
				j, jErr := loadJira(cmd)
				if jErr != nil {
					warnings = append(warnings, fmt.Sprintf("jira load failed: %v", jErr))
				} else {
					epics, eErr := j.ListEpics(ctx)
					if eErr != nil {
						warnings = append(warnings, fmt.Sprintf("failed to list epics: %v", eErr))
					} else {
						candidateEpics = epics
					}

					assigned, aErr := j.ListAssignedTickets(ctx)
					if aErr != nil {
						warnings = append(warnings, fmt.Sprintf("failed to list assigned tickets: %v", aErr))
					} else {
						assignedTickets = assigned
					}
				}
			}
		} else { // personal mode
			td, tErr := loadTodoist(cmd)
			if tErr != nil {
				warnings = append(warnings, fmt.Sprintf("todoist load failed: %v", tErr))
			} else {
				if jiraKey != "" {
					matchedTodoist, _ = td.SearchTasksByJiraKey(ctx, jiraKey)
				}
				if matchedTodoist == nil && repo != nil {
					if taskRef, rErr := ticket.ParseTaskRef(repo.Branch); rErr == nil && taskRef != "" {
						matchedTodoist, _ = td.Fetch(ctx, taskRef)
					}
				}
				if matchedTodoist != nil {
					writtenFile = matchedTodoist.ID + ".md"
					mdContent := ticket.TicketToMarkdownFile(matchedTodoist)
					_ = os.WriteFile(writtenFile, []byte(mdContent), 0600)
				} else {
					projectsFilter, _ := cmd.Flags().GetStringSlice("projects")
					if len(projectsFilter) == 0 {
						projectsFilter = cfg.TodoistProjects
					}
					activeTasks, lErr := td.ListActiveTasksForProjects(ctx, projectsFilter)
					if lErr != nil {
						warnings = append(warnings, fmt.Sprintf("failed to list todoist tasks: %v", lErr))
					} else {
						assignedTodoist = activeTasks
					}
				}
			}
		}

		repoOwner := ""
		repoName := ""
		repoBranch := ""
		if repo != nil {
			repoOwner = repo.Owner
			repoName = repo.Name
			repoBranch = repo.Branch
		}

		targetTicket := matchedJira
		if targetTicket == nil {
			targetTicket = matchedTodoist
		}

		needsTicketDecision := false
		if mode == "work" {
			needsTicketDecision = (jiraKey == "")
		} else {
			needsTicketDecision = (targetTicket == nil)
		}

		payload := ticket.StatusPayload{
			Mode:                 mode,
			RepoOwner:            repoOwner,
			RepoName:             repoName,
			Branch:               repoBranch,
			IsWork:               isWork,
			DefaultBranch:        defaultBranch,
			DirtyFiles:           dirtyFiles,
			UntrackedFiles:       untrackedFiles,
			OnTicketBranch:       targetTicket != nil || jiraKey != "",
			TicketFound:          targetTicket != nil,
			NeedsTicketDecision:  needsTicketDecision,
			Ticket:               targetTicket,
			CandidateEpics:       candidateEpics,
			AssignedTickets:      assignedTickets,
			AssignedTodoistTasks: assignedTodoist,
			Warnings:             warnings,
		}

		if output.JSON() {
			outXML := ticket.StatusPayloadToAIXML(payload)
			output.Write("ticket-status", outXML, nil)
			return nil
		}

		var b strings.Builder
		if repoErr != nil {
			b.WriteString("Git status: Not in a git repository\n")
		} else {
			fmt.Fprintf(&b, "Repo:        %s/%s\n", repo.Owner, repo.Name)
			fmt.Fprintf(&b, "Mode:        %s\n", mode)
			fmt.Fprintf(&b, "Branch:      %s\n", formatBranchOutput(repo.Branch, defaultBranch))
			fmt.Fprintf(&b, "Is Work:     %t\n", isWork)
			if jiraKey != "" {
				fmt.Fprintf(&b, "Jira Key:    %s\n", jiraKey)
			} else {
				b.WriteString("Jira Key:    (none)\n")
			}
			if dirtyFiles > 0 || untrackedFiles > 0 {
				fmt.Fprintf(&b, "Worktree:    dirty (%d modified, %d untracked)\n", dirtyFiles, untrackedFiles)
			} else {
				b.WriteString("Worktree:    clean\n")
			}
			if writtenFile != "" {
				fmt.Fprintf(&b, "Ticket File: ./%s\n", writtenFile)
			}
			if matchedTodoist != nil {
				fmt.Fprintf(&b, "Todoist:     %s (%s)\n", matchedTodoist.Title, matchedTodoist.URL)
			}
			if matchedJira != nil {
				fmt.Fprintf(&b, "Jira Ticket: %s (%s)\n", matchedJira.Title, matchedJira.URL)
			}
			if len(warnings) > 0 {
				b.WriteString("\nWarnings:\n")
				for _, w := range warnings {
					fmt.Fprintf(&b, "  - %s\n", w)
				}
			}
			if len(candidateEpics) > 0 || len(assignedTickets) > 0 {
				b.WriteString("\nAssigned Tickets:\n")
				b.WriteString(formatTicketTree(candidateEpics, assignedTickets))
			}
			if len(assignedTodoist) > 0 {
				b.WriteString("\nActive Todoist Tasks:\n")
				projMap := make(map[string][]*ticket.Ticket)
				var projOrder []string
				var unassigned []*ticket.Ticket
				for _, t := range assignedTodoist {
					pName := t.ProjectName
					if pName == "" {
						pName = t.ProjectID
					}
					if pName == "" {
						unassigned = append(unassigned, t)
						continue
					}
					if _, exists := projMap[pName]; !exists {
						projOrder = append(projOrder, pName)
					}
					projMap[pName] = append(projMap[pName], t)
				}
				for _, pName := range projOrder {
					tasks := projMap[pName]
					slices.SortFunc(tasks, func(a, b *ticket.Ticket) int {
						return cmp.Compare(b.UpdatedAt, a.UpdatedAt)
					})
					fmt.Fprintf(&b, "  %s:\n", pName)
					for _, t := range tasks {
						updatedStr := ""
						if rel := ticket.FormatRelativeTime(t.UpdatedAt, time.Now()); rel != "" {
							updatedStr = fmt.Sprintf(" [%s]", rel)
						}
						fmt.Fprintf(&b, "    - %s: %s%s\n", t.ID, t.Title, updatedStr)
					}
				}
				slices.SortFunc(unassigned, func(a, b *ticket.Ticket) int {
					return cmp.Compare(b.UpdatedAt, a.UpdatedAt)
				})
				for _, t := range unassigned {
					updatedStr := ""
					if rel := ticket.FormatRelativeTime(t.UpdatedAt, time.Now()); rel != "" {
						updatedStr = fmt.Sprintf(" [%s]", rel)
					}
					fmt.Fprintf(&b, "  - %s: %s%s\n", t.ID, t.Title, updatedStr)
				}
			}
		}

		statusText := b.String()
		output.Write("ticket-status", statusText, func() {
			fmt.Print(statusText)
		})
		return nil
	},
}

type epicNode struct {
	id      string
	title   string
	status  string
	tickets []*ticket.Ticket
}

func formatTicketTree(epics []*ticket.Ticket, assignedTickets []*ticket.Ticket) string {
	var b strings.Builder

	epicMap := make(map[string]*epicNode)
	var epicOrder []string

	for _, e := range epics {
		if e == nil {
			continue
		}
		if _, exists := epicMap[e.ID]; !exists {
			epicMap[e.ID] = &epicNode{
				id:     e.ID,
				title:  e.Title,
				status: e.Status,
			}
			epicOrder = append(epicOrder, e.ID)
		}
	}

	var standaloneTickets []*ticket.Ticket

	for _, t := range assignedTickets {
		if t == nil {
			continue
		}
		if t.ParentID != "" {
			node, exists := epicMap[t.ParentID]
			if !exists {
				node = &epicNode{
					id:    t.ParentID,
					title: t.ParentTitle,
				}
				epicMap[t.ParentID] = node
				epicOrder = append(epicOrder, t.ParentID)
			}
			node.tickets = append(node.tickets, t)
		} else {
			standaloneTickets = append(standaloneTickets, t)
		}
	}

	for _, epicID := range epicOrder {
		node := epicMap[epicID]
		title := node.title
		if title == "" {
			title = node.id
		}
		fmt.Fprintf(&b, "- %s: %s\n", node.id, title)
		for _, t := range node.tickets {
			statusStr := ""
			if t.Status != "" {
				statusStr = fmt.Sprintf(" [%s]", t.Status)
			}
			fmt.Fprintf(&b, "  - %s: %s%s\n", t.ID, t.Title, statusStr)
		}
	}

	for _, t := range standaloneTickets {
		statusStr := ""
		if t.Status != "" {
			statusStr = fmt.Sprintf(" [%s]", t.Status)
		}
		fmt.Fprintf(&b, "- %s: %s%s\n", t.ID, t.Title, statusStr)
	}

	return b.String()
}

func loadTodoist(cmd *cobra.Command) (*ticket.Todoist, error) {
	cfg, err := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return ticket.NewTodoist(*zerolog.Ctx(cmd.Context()), ticket.TodoistConfig{
		Token:   cfg.TodoistAPIToken,
		BaseURL: cfg.TodoistRESTBase,
	}), nil
}

func loadJira(cmd *cobra.Command) (*ticket.Jira, error) {
	cfg, err := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return ticket.NewJira(*zerolog.Ctx(cmd.Context()), ticket.JiraConfig{
		Email:    cfg.JiraEmail,
		APIToken: cfg.JiraAPIToken,
		Domain:   cfg.JiraDomain,
	}), nil
}

func printTicket(tk *ticket.Ticket) {
	fmt.Printf("ID:          %s\n", tk.ID)
	fmt.Printf("Title:       %s\n", tk.Title)
	if tk.Description != "" {
		fmt.Printf("Description: %s\n", tk.Description)
	}
	if tk.Status != "" {
		fmt.Printf("Status:      %s\n", tk.Status)
	}
	if tk.URL != "" {
		fmt.Printf("URL:         %s\n", tk.URL)
	}
	if len(tk.Comments) == 0 {
		fmt.Println("Comments:    (none)")
		return
	}
	fmt.Println("Comments:")
	for _, c := range tk.Comments {
		when := c.PostedAt
		if when == "" {
			when = "?"
		}
		fmt.Printf("  - [%s] %s\n", when, c.Content)
	}
}

func formatBranchOutput(currentBranch, defaultBranch string) string {
	if currentBranch == "" {
		return "(none)"
	}
	if defaultBranch != "" && currentBranch == defaultBranch {
		return currentBranch + " (default)"
	}
	return currentBranch
}

func dirtyDefaultBranchWarning(dirtyFiles, untrackedFiles int, branch, defaultBranch string) string {
	if (dirtyFiles > 0 || untrackedFiles > 0) && defaultBranch != "" && branch == defaultBranch {
		return fmt.Sprintf("uncommitted work on default branch %q without Jira ticket", defaultBranch)
	}
	return ""
}

func init() {
	ticketFetchCmd.Flags().Bool("jira", false, "Force fetch directly from Jira Cloud")

	ticketCommentCmd.Flags().String("body", "", "Comment body (required)")
	_ = ticketCommentCmd.MarkFlagRequired("body")
	ticketCommentCmd.Flags().Bool("jira", false, "Post comment directly to Jira Cloud instead of Todoist")

	ticketCreateCmd.Flags().String("title", "", "Task title/summary (required)")
	_ = ticketCreateCmd.MarkFlagRequired("title")
	ticketCreateCmd.Flags().String("description", "", "Task description")
	ticketCreateCmd.Flags().String("epic", "", "Parent Jira Epic key (e.g. DX-50)")
	ticketCreateCmd.Flags().String("project", "", "Jira project key (defaults to JIRA_DEFAULT_PROJECT or DX)")
	ticketCreateCmd.Flags().String("type", "Task", "Jira issue type")
	ticketCreateCmd.Flags().Bool("todoist", false, "Create task in Todoist instead of Jira Cloud")

	ticketStatusCmd.Flags().StringSlice("projects", nil, "Filter personal Todoist tasks by project names or IDs")

	TicketCmd.AddCommand(ticketFetchCmd, ticketCommentCmd, ticketCreateCmd, ticketStatusCmd, ticketEpicsCmd)
	Cmd.AddCommand(TicketCmd)
}
