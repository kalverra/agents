// Package config provides configuration for the application.
package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Config is the configuration for the application.
type Config struct {
	LogLevel string `mapstructure:"log_level"`
	AIOutput bool   `mapstructure:"ai_output"`

	TodoistAPIToken string `mapstructure:"todoist_api_token"`
	// TodoistRESTBase overrides the Todoist API v1 base URL (default https://api.todoist.com/api/v1). Env: TODOIST_REST_BASE.
	TodoistRESTBase string `mapstructure:"todoist_rest_base"`
	// TodoistProjects narrows personal Todoist task searches to specific project names or IDs. Env: TODOIST_PROJECTS.
	TodoistProjects []string `mapstructure:"todoist_projects"`

	// JiraCloud: Atlassian account email for Basic auth. Env: JIRA_EMAIL.
	JiraEmail string `mapstructure:"jira_email"`
	// JiraCloud: API token (not password). Env: JIRA_API_TOKEN.
	JiraAPIToken string `mapstructure:"jira_api_token"`
	// JiraCloud: site hostname only, e.g. your-org.atlassian.net (no scheme). Env: JIRA_DOMAIN.
	JiraDomain string `mapstructure:"jira_domain"`
	// JiraCloud: default project key when creating tickets (default "DX"). Env: JIRA_DEFAULT_PROJECT.
	JiraDefaultProject string `mapstructure:"jira_default_project"`

	// WorkRepos is a list of organization wildcards or owner/repo paths representing work repositories. Env: WORK_REPOS.
	WorkRepos []string `mapstructure:"work_repos"`

	// ActivityAgentCmd is the AI agent CLI used for activity reviews (default "agy"). Env: ACTIVITY_AGENT_CMD.
	ActivityAgentCmd string `mapstructure:"activity_agent_cmd"`
	// ActivityAgentArgs are extra arguments passed to the agent CLI. Use {{MODEL}} placeholder. Env: ACTIVITY_AGENT_ARGS.
	ActivityAgentArgs string `mapstructure:"activity_agent_args"`
	// ActivityLLMProvider selects the LLM backend: "agy" or "llama" (default "agy"). Env: ACTIVITY_LLM_PROVIDER.
	ActivityLLMProvider string `mapstructure:"activity_llm_provider"`
	// ActivityModel is the model identifier passed to the agent (default "gpt-oss-120b-medium"). Env: ACTIVITY_MODEL.
	ActivityModel string `mapstructure:"activity_model"`
	// ActivityLlamaCmd is the llama.cpp CLI binary used when provider is "llama" (default "llama-cli"). Env: ACTIVITY_LLAMA_CMD.
	ActivityLlamaCmd string `mapstructure:"activity_llama_cmd"`
	// ActivityLlamaArgs are extra arguments passed to the llama.cpp CLI. Env: ACTIVITY_LLAMA_ARGS.
	ActivityLlamaArgs string `mapstructure:"activity_llama_args"`
	// ActivityOllamaHost is the host:port for Ollama HTTP server. Env: ACTIVITY_OLLAMA_HOST.
	ActivityOllamaHost string `mapstructure:"activity_ollama_host"`
	// ActivityWindowsMAC is the MAC address for sending WoL packets. Env: ACTIVITY_WINDOWS_MAC.
	ActivityWindowsMAC string `mapstructure:"activity_windows_mac"`
	// ActivityReportsDir is where generated reports are saved. Env: ACTIVITY_REPORTS_DIR.
	ActivityReportsDir string `mapstructure:"activity_reports_dir"`
	// ActivityPromptsDir is where custom prompt markdown files live. Env: ACTIVITY_PROMPTS_DIR.
	ActivityPromptsDir string `mapstructure:"activity_prompts_dir"`
	// ActivityProject is the Todoist project name for work reports (default "Work"). Env: ACTIVITY_PROJECT.
	ActivityProject string `mapstructure:"activity_project"`
	// ActivityScheduleDaily is when the daily report runs (e.g. "weekday:09:30"). Env: ACTIVITY_SCHEDULE_DAILY.
	ActivityScheduleDaily string `mapstructure:"activity_schedule_daily"`
	// ActivityScheduleWeekly is when the weekly report runs (e.g. "fri:15:30"). Env: ACTIVITY_SCHEDULE_WEEKLY.
	ActivityScheduleWeekly string `mapstructure:"activity_schedule_weekly"`
	// ActivityScheduleMonthly is when the monthly report runs (e.g. "28:17:00"). Env: ACTIVITY_SCHEDULE_MONTHLY.
	ActivityScheduleMonthly string `mapstructure:"activity_schedule_monthly"`
	// ActivityScheduleQuarterly is when the quarterly report runs (e.g. "28:17:00"). Env: ACTIVITY_SCHEDULE_QUARTERLY.
	ActivityScheduleQuarterly string `mapstructure:"activity_schedule_quarterly"`
}

const (
	// DefaultLogLevel is the default log level.
	DefaultLogLevel = "info"
	// DefaultJiraProject is the default Jira project key.
	DefaultJiraProject = "DX"
)

// LoadOption is a function that can be used to load configuration.
type LoadOption func(*viper.Viper) error

// WithConfigFile sets a specific config file to load.
func WithConfigFile(path string) LoadOption {
	return func(v *viper.Viper) error {
		v.SetConfigFile(path)
		return nil
	}
}

// WithFlags binds flags to the viper instance.
func WithFlags(flags *pflag.FlagSet) LoadOption {
	return func(v *viper.Viper) error {
		normalizeFunc := flags.GetNormalizeFunc()
		flags.SetNormalizeFunc(func(fs *pflag.FlagSet, name string) pflag.NormalizedName {
			result := normalizeFunc(fs, name)
			name = strings.ReplaceAll(string(result), "-", "_") // Replace hyphens with underscores
			return pflag.NormalizedName(name)
		})
		return v.BindPFlags(flags)
	}
}

// Load loads configuration from file, env vars, and optionally flags.
func Load(opts ...LoadOption) (*Config, error) {
	v := viper.New()

	v.SetConfigName(".env")
	v.SetConfigType("env")
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	v.AddConfigPath(filepath.Join(home, ".agents"))

	v.SetDefault("log_level", DefaultLogLevel)
	v.SetDefault("jira_default_project", DefaultJiraProject)

	// Bind all configuration fields to environment variables
	typ := reflect.TypeFor[Config]()
	for field := range typ.Fields() {
		tag := field.Tag.Get("mapstructure")
		if tag != "" {
			if err := v.BindEnv(tag); err != nil {
				return nil, err
			}
		}
	}

	for _, opt := range opts {
		if err := opt(v); err != nil {
			return nil, err
		}
	}

	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// Handle WORK_REPOS env var if it was provided as a comma-separated string
	if rawWorkRepos := v.GetString("work_repos"); len(cfg.WorkRepos) == 0 && rawWorkRepos != "" {
		parts := strings.SplitSeq(rawWorkRepos, ",")
		for p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.WorkRepos = append(cfg.WorkRepos, trimmed)
			}
		}
	}

	// Handle TODOIST_PROJECTS env var if it was provided as a comma-separated string
	if rawTodoistProjects := v.GetString(
		"todoist_projects",
	); len(cfg.TodoistProjects) == 0 &&
		rawTodoistProjects != "" {
		parts := strings.SplitSeq(rawTodoistProjects, ",")
		for p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.TodoistProjects = append(cfg.TodoistProjects, trimmed)
			}
		}
	}

	return cfg, nil
}
