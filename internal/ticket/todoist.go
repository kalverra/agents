package ticket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog"
	"resty.dev/v3"

	"github.com/kalverra/agents/internal/mactls"
)

// defaultTodoistAPIBase is the Todoist HTTP API v1 base URL.
// See https://developer.todoist.com/api/v1/
const defaultTodoistAPIBase = "https://api.todoist.com/api/v1"

// TodoistConfig holds REST credentials for Todoist.
type TodoistConfig struct {
	Token   string
	BaseURL string // Overrides defaultTodoistAPIBase (env: TODOIST_REST_BASE / mapstructure todoist_rest_base)
}

// Todoist implements Provider against the Todoist API v1.
type Todoist struct {
	client *resty.Client
	cfg    TodoistConfig
}

// NewTodoist returns a Todoist-backed provider.
func NewTodoist(l zerolog.Logger, cfg TodoistConfig) *Todoist {
	return &Todoist{
		client: newTodoistClient(l, cfg),
		cfg:    cfg,
	}
}

func todoistAPIBase(cfg TodoistConfig) string {
	b := strings.TrimSpace(cfg.BaseURL)
	if b == "" {
		return defaultTodoistAPIBase
	}
	return strings.TrimRight(b, "/")
}

func newTodoistClient(l zerolog.Logger, cfg TodoistConfig) *resty.Client {
	c := resty.New()
	if rt := mactls.RoundTripper(); rt != nil {
		c.SetTransport(rt)
	}
	c.SetBaseURL(todoistAPIBase(cfg))
	if tok := strings.TrimSpace(cfg.Token); tok != "" {
		c.SetAuthToken(tok)
	}
	c.AddResponseMiddleware(func(_ *resty.Client, resp *resty.Response) error {
		req := resp.Request
		l.Trace().
			Str("method", req.Method).
			Str("url", req.RawRequest.URL.String()).
			Func(func(e *zerolog.Event) {
				reqBodyBytes, err := json.Marshal(req.Body)
				if err != nil {
					e.Str("req_body", fmt.Sprintf("%v", req.Body)).Msg("marshal req body")
				}
				if req.Body != nil && json.Valid(reqBodyBytes) {
					e.RawJSON("req_body", reqBodyBytes)
				}
			}).
			Int("status", resp.StatusCode()).
			Str("elapsed", resp.Duration().String()).
			Func(func(e *zerolog.Event) {
				if resp != nil && json.Valid(resp.Bytes()) {
					e.RawJSON("resp_body", resp.Bytes())
				} else {
					e.Str("resp_body", string(resp.Bytes()))
				}
			}).
			Msg("http round trip")
		if resp.IsStatusFailure() {
			return fmt.Errorf(
				"todoist API error %d: %s",
				resp.StatusCode(), resp.String(),
			)
		}
		return nil
	})
	return c
}

// Fetch loads a task by id or Todoist task URL (see ParseTaskRef).
func (t *Todoist) Fetch(ctx context.Context, id string) (*Ticket, error) {
	resolved, err := ParseTaskRef(id)
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		return nil, fmt.Errorf("task id is required")
	}
	if strings.TrimSpace(t.cfg.Token) == "" {
		return nil, fmt.Errorf("TODOIST_API_TOKEN is required")
	}

	resp, err := t.client.R().
		SetContext(ctx).
		Get("/tasks/" + resolved)
	if err != nil {
		return nil, err
	}
	if resp.IsStatusFailure() {
		if resp.StatusCode() == http.StatusNotFound {
			return nil, fmt.Errorf("task %s not found", resolved)
		}
		return nil, fmt.Errorf(
			"todoist GET tasks/%s: %s: %s",
			resolved,
			resp.Status(),
			strings.TrimSpace(resp.String()),
		)
	}

	var tr todoistTaskV1
	if err := json.Unmarshal(resp.Bytes(), &tr); err != nil {
		return nil, fmt.Errorf("decoding task: %w", err)
	}

	taskURL := strings.TrimSpace(tr.URL)
	if taskURL == "" {
		taskURL = "https://app.todoist.com/app/task/" + resolved
	}

	tk := &Ticket{
		ID:          tr.ID,
		Title:       tr.Content,
		Description: tr.Description,
		Status:      tr.status(),
		URL:         taskURL,
	}

	comments, err := t.listTaskComments(ctx, resolved)
	if err != nil {
		return nil, err
	}
	tk.Comments = comments

	return tk, nil
}

func (t *Todoist) listTaskComments(ctx context.Context, taskID string) ([]TaskComment, error) {
	const maxPages = 100

	var out []TaskComment
	cursor := ""
	for range maxPages {
		r := t.client.R().
			SetContext(ctx).
			SetQueryParam("task_id", taskID)
		if cursor != "" {
			r.SetQueryParam("cursor", cursor)
		}

		resp, err := r.Get("/comments")
		if err != nil {
			return nil, err
		}
		if resp.IsStatusFailure() {
			return nil, fmt.Errorf(
				"todoist GET comments: %s: %s",
				resp.Status(),
				strings.TrimSpace(resp.String()),
			)
		}

		var page commentsListPageV1
		if err := json.Unmarshal(resp.Bytes(), &page); err != nil {
			return nil, fmt.Errorf("decoding comments: %w", err)
		}

		for i := range page.Results {
			c := page.Results[i]
			if c.IsDeleted {
				continue
			}
			out = append(out, TaskComment{
				ID:        c.ID,
				Content:   c.Content,
				PostedAt:  c.PostedAt,
				ProjectID: c.ProjectID,
			})
		}

		if strings.TrimSpace(page.NextCursor) == "" {
			break
		}
		cursor = page.NextCursor
	}

	return out, nil
}

type commentsListPageV1 struct {
	Results    []todoistCommentV1 `json:"results"`
	NextCursor string             `json:"next_cursor"`
}

type todoistCommentV1 struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	ProjectID string `json:"project_id"`
	PostedAt  string `json:"posted_at"`
	Content   string `json:"content"`
	IsDeleted bool   `json:"is_deleted"`
}

type todoistTaskV1 struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id,omitempty"`
	Content     string `json:"content"`
	Description string `json:"description"`
	URL         string `json:"url,omitempty"`
	Checked     bool   `json:"checked"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

func (tr todoistTaskV1) status() string {
	if tr.Checked {
		return "completed"
	}
	return ""
}

// Comment adds a comment to a task (id or Todoist task URL; see ParseTaskRef).
func (t *Todoist) Comment(ctx context.Context, id string, body string) error {
	resolved, err := ParseTaskRef(id)
	if err != nil {
		return err
	}
	if resolved == "" {
		return fmt.Errorf("task id is required")
	}
	if strings.TrimSpace(t.cfg.Token) == "" {
		return fmt.Errorf("TODOIST_API_TOKEN is required")
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("comment body is required")
	}

	resp, err := t.client.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]string{
			"task_id": resolved,
			"content": body,
		}).
		Post("/comments")
	if err != nil {
		return err
	}
	if resp.IsStatusFailure() {
		return fmt.Errorf(
			"todoist POST comments: %s: %s",
			resp.Status(),
			strings.TrimSpace(resp.String()),
		)
	}
	return nil
}

// CreateTaskRequest holds parameters for creating a Todoist task.
type CreateTaskRequest struct {
	Title       string `json:"content"`
	Description string `json:"description"`
}

// CreateTask creates a task in Todoist.
func (t *Todoist) CreateTask(ctx context.Context, req CreateTaskRequest) (*Ticket, error) {
	if strings.TrimSpace(t.cfg.Token) == "" {
		return nil, fmt.Errorf("TODOIST_API_TOKEN is required")
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("task title is required")
	}

	payload := map[string]string{
		"content": req.Title,
	}
	if strings.TrimSpace(req.Description) != "" {
		payload["description"] = req.Description
	}

	resp, err := t.client.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(payload).
		Post("/tasks")
	if err != nil {
		return nil, err
	}
	if resp.IsStatusFailure() {
		return nil, fmt.Errorf("todoist POST tasks: %s: %s", resp.Status(), strings.TrimSpace(resp.String()))
	}

	var tr todoistTaskV1
	if err := json.Unmarshal(resp.Bytes(), &tr); err != nil {
		return nil, fmt.Errorf("decoding created task: %w", err)
	}

	taskURL := strings.TrimSpace(tr.URL)
	if taskURL == "" {
		taskURL = "https://app.todoist.com/app/task/" + tr.ID
	}

	return &Ticket{
		ID:          tr.ID,
		Title:       tr.Content,
		Description: tr.Description,
		Status:      tr.status(),
		URL:         taskURL,
	}, nil
}

// SearchTasksByJiraKey searches Todoist tasks for one whose content or description contains the Jira key.
func (t *Todoist) SearchTasksByJiraKey(ctx context.Context, jiraKey string) (*Ticket, error) {
	if strings.TrimSpace(t.cfg.Token) == "" {
		return nil, fmt.Errorf("TODOIST_API_TOKEN is required")
	}
	jiraKey = strings.TrimSpace(jiraKey)
	if jiraKey == "" {
		return nil, fmt.Errorf("jiraKey is required")
	}

	resp, err := t.client.R().
		SetContext(ctx).
		Get("/tasks")
	if err != nil {
		return nil, err
	}
	if resp.IsStatusFailure() {
		return nil, fmt.Errorf("todoist GET tasks: %s: %s", resp.Status(), strings.TrimSpace(resp.String()))
	}

	var tasksList struct {
		Results []todoistTaskV1 `json:"results"`
	}
	if err := json.Unmarshal(resp.Bytes(), &tasksList); err != nil || len(tasksList.Results) == 0 {
		var rawList []todoistTaskV1
		if err := json.Unmarshal(resp.Bytes(), &rawList); err == nil {
			tasksList.Results = rawList
		}
	}

	for _, task := range tasksList.Results {
		if strings.Contains(task.Content, jiraKey) || strings.Contains(task.Description, jiraKey) {
			taskURL := strings.TrimSpace(task.URL)
			if taskURL == "" {
				taskURL = "https://app.todoist.com/app/task/" + task.ID
			}
			return &Ticket{
				ID:          task.ID,
				Title:       task.Content,
				Description: task.Description,
				Status:      task.status(),
				URL:         taskURL,
			}, nil
		}
	}

	return nil, fmt.Errorf("no todoist task found matching jira key %s", jiraKey)
}

// ListActiveTasks retrieves uncompleted tasks from Todoist.
func (t *Todoist) ListActiveTasks(ctx context.Context) ([]*Ticket, error) {
	if strings.TrimSpace(t.cfg.Token) == "" {
		return nil, fmt.Errorf("TODOIST_API_TOKEN is required")
	}

	resp, err := t.client.R().
		SetContext(ctx).
		Get("/tasks")
	if err != nil {
		return nil, err
	}
	if resp.IsStatusFailure() {
		return nil, fmt.Errorf("todoist GET tasks: %s: %s", resp.Status(), strings.TrimSpace(resp.String()))
	}

	var tasksList struct {
		Results []todoistTaskV1 `json:"results"`
	}
	if err := json.Unmarshal(resp.Bytes(), &tasksList); err != nil || len(tasksList.Results) == 0 {
		var rawList []todoistTaskV1
		if err := json.Unmarshal(resp.Bytes(), &rawList); err == nil {
			tasksList.Results = rawList
		}
	}

	projMap, _ := t.FetchProjectsMap(ctx)

	var out []*Ticket
	for _, task := range tasksList.Results {
		if task.Checked {
			continue
		}
		taskURL := strings.TrimSpace(task.URL)
		if taskURL == "" {
			taskURL = "https://app.todoist.com/app/task/" + task.ID
		}
		updatedAt := task.UpdatedAt
		if updatedAt == "" {
			updatedAt = task.CreatedAt
		}
		out = append(out, &Ticket{
			ID:          task.ID,
			Title:       task.Content,
			Description: task.Description,
			Status:      task.status(),
			URL:         taskURL,
			ProjectID:   task.ProjectID,
			ProjectName: projMap[task.ProjectID],
			UpdatedAt:   updatedAt,
		})
	}

	return out, nil
}

// ListActiveTasksForProjects retrieves uncompleted tasks from Todoist, filtered by project names or IDs.
func (t *Todoist) ListActiveTasksForProjects(ctx context.Context, projects []string) ([]*Ticket, error) {
	tasks, err := t.ListActiveTasks(ctx)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return tasks, nil
	}

	targetIDs := make(map[string]bool)
	for _, p := range projects {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		projID, err := t.GetProjectIDByName(ctx, p)
		if err == nil && projID != "" {
			targetIDs[projID] = true
		}
		targetIDs[p] = true
	}

	var filtered []*Ticket
	for _, tk := range tasks {
		if targetIDs[tk.ProjectID] {
			filtered = append(filtered, tk)
		}
	}
	return filtered, nil
}

// TodoistCompletedTask represents a completed item returned by Todoist.
type TodoistCompletedTask struct {
	ID          string   `json:"id"`
	Content     string   `json:"content"`
	Description string   `json:"description"`
	CompletedAt string   `json:"completed_at"`
	Labels      []string `json:"labels"`
	URL         string   `json:"url"`
}

// FetchProjectsMap returns a map of project_id -> project_name.
func (t *Todoist) FetchProjectsMap(ctx context.Context) (map[string]string, error) {
	if strings.TrimSpace(t.cfg.Token) == "" {
		return nil, fmt.Errorf("TODOIST_API_TOKEN is required")
	}

	resp, err := t.client.R().
		SetContext(ctx).
		Get("/projects")
	if err != nil {
		return nil, err
	}
	if resp.IsStatusFailure() {
		return nil, fmt.Errorf("todoist GET projects: %s: %s", resp.Status(), strings.TrimSpace(resp.String()))
	}

	var projectsList struct {
		Results []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal(resp.Bytes(), &projectsList); err != nil || len(projectsList.Results) == 0 {
		var rawList []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(resp.Bytes(), &rawList); err == nil {
			projectsList.Results = rawList
		}
	}

	out := make(map[string]string)
	for _, p := range projectsList.Results {
		if p.ID != "" {
			out[p.ID] = p.Name
		}
	}
	return out, nil
}

// GetProjectIDByName fetches all Todoist projects and returns the ID of the project matching name (case-insensitive).
func (t *Todoist) GetProjectIDByName(ctx context.Context, name string) (string, error) {
	pm, err := t.FetchProjectsMap(ctx)
	if err != nil {
		return "", err
	}

	for id, projName := range pm {
		if strings.EqualFold(strings.TrimSpace(projName), strings.TrimSpace(name)) {
			return id, nil
		}
	}

	return "", fmt.Errorf("project %q not found in Todoist", name)
}

// FetchCompletedTasks retrieves completed tasks for a given project.
func (t *Todoist) FetchCompletedTasks(ctx context.Context, projectID string) ([]TodoistCompletedTask, error) {
	if strings.TrimSpace(t.cfg.Token) == "" {
		return nil, fmt.Errorf("TODOIST_API_TOKEN is required")
	}

	req := t.client.R().SetContext(ctx)
	if projectID != "" {
		req.SetQueryParam("project_id", projectID)
	}

	resp, err := req.Get("/tasks/completed")
	if err != nil {
		return nil, err
	}
	if resp.IsStatusFailure() {
		// Fallback: If /tasks/completed is unavailable, try returning empty list without failing
		return nil, fmt.Errorf("todoist GET tasks/completed: %s: %s", resp.Status(), strings.TrimSpace(resp.String()))
	}

	var page struct {
		Items []struct {
			ID          string   `json:"id"`
			Content     string   `json:"content"`
			Description string   `json:"description"`
			CompletedAt string   `json:"completed_at"`
			Labels      []string `json:"labels"`
			URL         string   `json:"url"`
		} `json:"items"`
	}
	if err := json.Unmarshal(resp.Bytes(), &page); err != nil {
		// Try unmarshaling raw array if direct list returned
		var rawItems []struct {
			ID          string   `json:"id"`
			Content     string   `json:"content"`
			Description string   `json:"description"`
			CompletedAt string   `json:"completed_at"`
			Labels      []string `json:"labels"`
			URL         string   `json:"url"`
		}
		if err := json.Unmarshal(resp.Bytes(), &rawItems); err == nil {
			page.Items = rawItems
		}
	}

	out := make([]TodoistCompletedTask, len(page.Items))
	for i, item := range page.Items {
		out[i] = TodoistCompletedTask{
			ID:          item.ID,
			Content:     item.Content,
			Description: item.Description,
			CompletedAt: item.CompletedAt,
			Labels:      item.Labels,
			URL:         item.URL,
		}
	}

	return out, nil
}
