package skills

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/kalverra/agents/internal/activity"
	review "github.com/kalverra/agents/internal/activity/review"
	schedule "github.com/kalverra/agents/internal/activity/schedule"
	"github.com/kalverra/agents/internal/config"
	"github.com/kalverra/agents/internal/llm"
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
		ctx := cmd.Context()
		logger := zerolog.Ctx(ctx)

		cfg, err := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		fetcher := activity.NewFetcher(cfg, *logger)
		report, err := fetcher.Fetch(ctx, time.Now(), timeframe, projectName)
		if err != nil {
			return err
		}

		xmlContent := activity.FormatXMLReport(report)
		_ = os.WriteFile("activity.xml", []byte(xmlContent), 0600)

		if output.JSON() {
			output.Write("activity-fetch", xmlContent, nil)
			return nil
		}

		fmt.Printf(
			"Fetched activity for timeframe %s (%s to %s):\n",
			timeframe,
			report.Since.Format("2006-01-02"),
			report.Until.Format("2006-01-02"),
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

var activityReviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Generate a standup/summary report using an AI agent",
	RunE: func(cmd *cobra.Command, _ []string) error {
		mode, _ := cmd.Flags().GetString("mode")
		timeframe, _ := cmd.Flags().GetString("since")
		projectName, _ := cmd.Flags().GetString("project")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		provider, _ := cmd.Flags().GetString("provider")
		model, _ := cmd.Flags().GetString("model")

		ctx := cmd.Context()
		logger := zerolog.Ctx(ctx)

		cfg, err := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("home dir: %w", err)
		}
		reportsDir := cfg.ActivityReportsDir
		if reportsDir == "" {
			reportsDir = filepath.Join(home, ".agents", "reports", "activity")
		}
		promptsDir := cfg.ActivityPromptsDir
		if promptsDir == "" {
			promptsDir = filepath.Join(home, ".agents", "prompts", "activity")
		}

		fetcher := activity.NewFetcher(cfg, *logger)

		llmCfg := llmConfigFrom(cfg, provider, model)
		llmRunner := llm.NewRunner(llmCfg)

		var poster review.Poster
		var projectID string
		if !dryRun && cfg.TodoistAPIToken != "" {
			td := ticket.NewTodoist(*logger, ticket.TodoistConfig{
				Token:   cfg.TodoistAPIToken,
				BaseURL: cfg.TodoistRESTBase,
			})
			projectID, _ = td.GetProjectIDByName(ctx, projectName)
			poster = td
		}

		evalMode, _ := cmd.Flags().GetBool("eval")

		evalDir := cfg.ActivityEvalDir
		if evalDir == "" {
			evalDir = filepath.Join(home, ".agents", "eval", "live_feedback")
		}

		runner := &review.Runner{
			Fetcher:    fetcher,
			LLM:        llmRunner,
			Poster:     poster,
			ReportsDir: reportsDir,
			PromptsDir: promptsDir,
			EvalDir:    evalDir,
			ProjectID:  projectID,
			Logger:     *logger,
			EvalMode:   evalMode,
			Provider:   llmCfg.Provider,
			Model:      llmCfg.Model,
		}

		res, err := runner.Run(ctx, mode, timeframe, projectName, dryRun)
		if err != nil {
			return err
		}

		if output.JSON() {
			output.Write("activity-review", map[string]any{
				"mode":        res.Mode,
				"timeframe":   res.Timeframe,
				"project":     res.ProjectName,
				"report_path": res.ReportPath,
				"task_id":     res.TaskID,
				"task_url":    res.TaskURL,
				"generated":   res.Generated,
			}, nil)
			return nil
		}

		fmt.Printf("Generated %s report for %s (%s)\n", mode, projectName, res.Timeframe)
		fmt.Printf("Saved to: %s\n", res.ReportPath)
		if res.TaskID != "" {
			fmt.Printf("Created Todoist task: %s\n", res.TaskURL)
		}
		return nil
	},
}

func llmConfigFrom(cfg *config.Config, providerOverride, modelOverride string) llm.Config {
	var c llm.Config
	if strings.EqualFold(cfg.ActivityLLMProvider, llm.ProviderLlama) ||
		strings.EqualFold(providerOverride, llm.ProviderLlama) {
		c = llm.DefaultLlamaConfig()
		if cfg.ActivityLlamaCmd != "" {
			c.Cmd = cfg.ActivityLlamaCmd
		}
		if cfg.ActivityLlamaArgs != "" {
			c.Args = strings.Fields(cfg.ActivityLlamaArgs)
		}
	} else {
		c = llm.DefaultConfig()
		if cfg.ActivityAgentCmd != "" {
			c.Cmd = cfg.ActivityAgentCmd
		}
		if cfg.ActivityAgentArgs != "" {
			c.Args = strings.Fields(cfg.ActivityAgentArgs)
		}
	}

	if cfg.ActivityOllamaHost != "" {
		c.OllamaHost = cfg.ActivityOllamaHost
	}
	if cfg.ActivityWindowsMAC != "" {
		c.WindowsMAC = cfg.ActivityWindowsMAC
	}

	switch {
	case providerOverride != "":
		c.Provider = providerOverride
	case cfg.ActivityLLMProvider != "":
		c.Provider = cfg.ActivityLLMProvider
	default:
		c.Provider = llm.ProviderAuto
	}

	if cfg.ActivityModel != "" {
		c.Model = cfg.ActivityModel
	}
	if modelOverride != "" {
		c.Model = modelOverride
	}
	return c
}

func newScheduler(cmd *cobra.Command, cfg *config.Config, projectName string) (*schedule.Scheduler, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	binaryPath, err := os.Executable()
	if err != nil {
		binaryPath = filepath.Join(home, ".agents", "agents")
	}
	provider, _ := cmd.Flags().GetString("provider")
	model, _ := cmd.Flags().GetString("model")
	return &schedule.Scheduler{
		PlistDir:    filepath.Join(home, "Library", "LaunchAgents"),
		LogDir:      filepath.Join(home, "Library", "Logs", "agents"),
		BinaryPath:  binaryPath,
		LabelPrefix: "com.kalverra.agents.activity",
		Project:     projectName,
		Provider:    provider,
		Model:       model,
		UID:         strconv.Itoa(os.Getuid()),
		Logger:      *zerolog.Ctx(cmd.Context()),
		Eval:        true,
		Schedule: schedule.Schedule{
			Daily:     nonEmpty(cfg.ActivityScheduleDaily, "weekday:09:00"),
			Weekly:    nonEmpty(cfg.ActivityScheduleWeekly, "fri:15:30"),
			Monthly:   nonEmpty(cfg.ActivityScheduleMonthly, "28:16:30"),
			Quarterly: nonEmpty(cfg.ActivityScheduleQuarterly, "28:16:30"),
			Finalize:  nonEmpty(cfg.ActivityScheduleFinalize, "weekday-bh:00:30"),
		},
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec
		},
	}, nil
}

func nonEmpty(s, def string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return def
}

var activityScheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Install or manage launchd jobs for automatic activity reports",
}

var activityScheduleInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install launchd plists for daily/weekly/monthly/quarterly reports",
	RunE: func(cmd *cobra.Command, _ []string) error {
		projectName, _ := cmd.Flags().GetString("project")
		cfg, err := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		s, err := newScheduler(cmd, cfg, projectName)
		if err != nil {
			return err
		}
		if err := s.Install(cmd.Context()); err != nil {
			return err
		}
		if output.JSON() {
			output.Write("activity-schedule-install", map[string]string{"status": "installed"}, nil)
			return nil
		}
		fmt.Println("Installed activity report schedules.")
		return nil
	},
}

var activityScheduleUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Unload and remove launchd plists for activity reports",
	RunE: func(cmd *cobra.Command, _ []string) error {
		projectName, _ := cmd.Flags().GetString("project")
		cfg, err := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		s, err := newScheduler(cmd, cfg, projectName)
		if err != nil {
			return err
		}
		if err := s.Uninstall(cmd.Context()); err != nil {
			return err
		}
		if output.JSON() {
			output.Write("activity-schedule-uninstall", map[string]string{"status": "uninstalled"}, nil)
			return nil
		}
		fmt.Println("Uninstalled activity report schedules.")
		return nil
	},
}

var activityScheduleStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check whether activity report launchd jobs are loaded",
	RunE: func(cmd *cobra.Command, _ []string) error {
		projectName, _ := cmd.Flags().GetString("project")
		cfg, err := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		s, err := newScheduler(cmd, cfg, projectName)
		if err != nil {
			return err
		}
		status, err := s.Status(cmd.Context())
		if err != nil {
			return err
		}
		if output.JSON() {
			output.Write("activity-schedule-status", map[string]bool{
				"daily":     status.Daily,
				"weekly":    status.Weekly,
				"monthly":   status.Monthly,
				"quarterly": status.Quarterly,
				"finalize":  status.Finalize,
			}, nil)
			return nil
		}
		fmt.Printf("Daily:     %t\n", status.Daily)
		fmt.Printf("Weekly:    %t\n", status.Weekly)
		fmt.Printf("Monthly:   %t\n", status.Monthly)
		fmt.Printf("Quarterly: %t\n", status.Quarterly)
		fmt.Printf("Finalize:  %t\n", status.Finalize)
		return nil
	},
}

var activityModelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List available models for each LLM provider",
	RunE: func(_ *cobra.Command, _ []string) error {
		models := llm.KnownModels()
		if output.JSON() {
			output.Write("activity-models", models, nil)
			return nil
		}
		fmt.Println("agy models:")
		for _, m := range models[llm.ProviderAgy] {
			fmt.Printf("  - %s\n", m)
		}
		fmt.Println("\nollama models:")
		for _, m := range models[llm.ProviderOllama] {
			fmt.Printf("  - %s\n", m)
		}
		fmt.Println("\nllama models (GGUF identifiers):")
		for _, m := range models[llm.ProviderLlama] {
			fmt.Printf("  - %s\n", m)
		}
		return nil
	},
}

var activityFinalizeCmd = &cobra.Command{
	Use:   "finalize",
	Short: "Check pending activity reports for user-reviewed scores and save eval records",
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load(config.WithFlags(cmd.Root().PersistentFlags()))
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("home dir: %w", err)
		}

		reportsDir := cfg.ActivityReportsDir
		if reportsDir == "" {
			reportsDir = filepath.Join(home, ".agents", "reports", "activity")
		}
		evalDir := cfg.ActivityEvalDir
		if evalDir == "" {
			evalDir = filepath.Join(home, ".agents", "eval", "live_feedback")
		}

		logger := zerolog.Ctx(cmd.Context())
		finalizer := &review.Finalizer{
			ReportsDir: reportsDir,
			EvalDir:    evalDir,
			Logger:     *logger,
		}

		results, err := finalizer.Run(cmd.Context())
		if err != nil {
			return err
		}

		if output.JSON() {
			output.Write("activity-finalize", results, nil)
			return nil
		}

		if len(results) == 0 {
			fmt.Println("No pending reports to finalize.")
			return nil
		}
		for _, r := range results {
			fmt.Printf("Finalized %s (score: %d, reason: %s)\n", filepath.Base(r.ReportPath), r.Score, r.Reason)
		}
		return nil
	},
}

func init() {
	activityFetchCmd.Flags().
		String("since", "7d", "Timeframe window (e.g. 1d, 7d, 30d, today, yesterday, week, month, quarter, 24h)")
	activityFetchCmd.Flags().String("project", "Work", "Todoist project name filter")

	activityReviewCmd.Flags().String("mode", "", "Review mode: daily, weekly, monthly, quarterly")
	_ = activityReviewCmd.MarkFlagRequired("mode")
	activityReviewCmd.Flags().String("since", "", "Timeframe override (defaults per mode)")
	activityReviewCmd.Flags().String("project", "Work", "Todoist project name filter")
	activityReviewCmd.Flags().Bool("dry-run", false, "Generate report but do not create Todoist task")
	activityReviewCmd.Flags().
		String("provider", "", "LLM provider: agy, llama, ollama, auto (overrides ACTIVITY_LLM_PROVIDER)")
	activityReviewCmd.Flags().String("model", "", "Model identifier or GGUF path (overrides ACTIVITY_MODEL)")
	activityReviewCmd.Flags().Bool("eval", false, "Prompt for interactive rating and record live evaluation feedback")

	activityScheduleCmd.Flags().String("project", "Work", "Todoist project name for reports")
	activityScheduleCmd.Flags().String("provider", "", "LLM provider for scheduled jobs: agy, llama, ollama, auto")
	activityScheduleCmd.Flags().String("model", "", "Model identifier or GGUF path for scheduled jobs")
	activityScheduleCmd.AddCommand(activityScheduleInstallCmd, activityScheduleUninstallCmd, activityScheduleStatusCmd)

	ActivityCmd.AddCommand(
		activityFetchCmd,
		activityReviewCmd,
		activityScheduleCmd,
		activityModelsCmd,
		activityFinalizeCmd,
	)
}
