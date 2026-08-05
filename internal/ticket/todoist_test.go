package ticket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAPIv1Base = "/api/v1"

func TestTodoist_Fetch_success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/tasks/12345":
			assert.Equal(t, testAPIv1Base+"/tasks/12345", r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "12345",
				"content":     "Fix login",
				"description": "Details here",
				"checked":     false,
				"url":         "https://todoist.com/showTask?id=12345",
			})
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/comments":
			assert.Equal(t, "12345", r.URL.Query().Get("task_id"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{
						"id":         "c1",
						"task_id":    "12345",
						"project_id": "p1",
						"posted_at":  "2026-01-02T15:04:05Z",
						"content":    "First note",
						"is_deleted": false,
					},
				},
				"next_cursor": "",
			})
		default:
			assert.Failf(t, "unexpected request", "unexpected %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	got, err := p.Fetch(context.Background(), "12345")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "12345", got.ID)
	assert.Equal(t, "Fix login", got.Title)
	assert.Equal(t, "Details here", got.Description)
	assert.Equal(t, "https://todoist.com/showTask?id=12345", got.URL)
	require.Len(t, got.Comments, 1)
	assert.Equal(t, "c1", got.Comments[0].ID)
	assert.Equal(t, "First note", got.Comments[0].Content)
	assert.Equal(t, "2026-01-02T15:04:05Z", got.Comments[0].PostedAt)
	assert.Equal(t, "p1", got.Comments[0].ProjectID)
}

func TestTodoist_Fetch_commentsEmpty(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/tasks/1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "1", "content": "Solo", "checked": false,
			})
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/comments":
			assert.Equal(t, "1", r.URL.Query().Get("task_id"))
			_, _ = w.Write([]byte(`{"results":[],"next_cursor":null}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	got, err := p.Fetch(context.Background(), "1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got.Comments)
}

func TestTodoist_Fetch_commentsPagination(t *testing.T) {
	t.Parallel()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/tasks/1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "1", "content": "T", "checked": false})
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/comments":
			calls++
			switch calls {
			case 1:
				assert.Empty(t, r.URL.Query().Get("cursor"))
				_ = json.NewEncoder(w).Encode(map[string]any{
					"results": []map[string]any{
						{"id": "a", "content": "one", "posted_at": "2026-01-01T00:00:00Z", "is_deleted": false},
					},
					"next_cursor": "c2",
				})
			case 2:
				assert.Equal(t, "c2", r.URL.Query().Get("cursor"))
				_ = json.NewEncoder(w).Encode(map[string]any{
					"results": []map[string]any{
						{"id": "b", "content": "two", "posted_at": "2026-01-02T00:00:00Z", "is_deleted": false},
					},
					"next_cursor": "",
				})
			default:
				assert.Failf(t, "unexpected comments page", "unexpected comments page %d", calls)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	got, err := p.Fetch(context.Background(), "1")
	require.NoError(t, err)
	require.Len(t, got.Comments, 2)
	assert.Equal(t, "one", got.Comments[0].Content)
	assert.Equal(t, "two", got.Comments[1].Content)
}

func TestTodoist_Fetch_commentsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/tasks/1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "1", "content": "x", "checked": false})
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/comments":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`err`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	_, err := p.Fetch(context.Background(), "1")
	require.Error(t, err)
}

func TestTodoist_Fetch_notFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	_, err := p.Fetch(context.Background(), "999")
	require.Error(t, err)
}

func TestTodoist_Fetch_synthesizesURLWhenMissing(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/tasks/abcXYZ":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "abcXYZ", "content": "No URL field", "checked": false,
			})
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/comments":
			_, _ = w.Write([]byte(`{"results":[],"next_cursor":""}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	got, err := p.Fetch(context.Background(), "abcXYZ")
	require.NoError(t, err)
	assert.Equal(t, "https://app.todoist.com/app/task/abcXYZ", got.URL)
}

func TestTodoist_Comment_success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, testAPIv1Base+"/comments", r.URL.Path)
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var body map[string]any
		err := json.NewDecoder(r.Body).Decode(&body)
		if !assert.NoError(t, err, "decode body") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		assert.Equal(t, "12345", body["task_id"])
		assert.Equal(t, "done", body["content"])

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"c1","task_id":"12345","content":"done"}`))
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	require.NoError(t, p.Comment(context.Background(), "12345", "done"))
}

func TestTodoist_Comment_errorStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	err := p.Comment(context.Background(), "12345", "x")
	require.Error(t, err)
}

func TestTodoist_SearchTasksByJiraKey_success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/tasks" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{
						"id":          "777",
						"content":     "[DX-100] Implement search feature",
						"description": "Synced from Jira DX-100",
						"checked":     false,
					},
					{
						"id":      "888",
						"content": "Unrelated task",
						"checked": false,
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	got, err := p.SearchTasksByJiraKey(context.Background(), "DX-100")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "777", got.ID)
	assert.Equal(t, "[DX-100] Implement search feature", got.Title)
}

func TestTodoist_CreateTask_success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == testAPIv1Base+"/tasks" {
			var body map[string]any
			err := json.NewDecoder(r.Body).Decode(&body)
			if !assert.NoError(t, err) {
				return
			}

			assert.Equal(t, "New Todoist task", body["content"])
			assert.Equal(t, "Task description", body["description"])

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "999",
				"content":     "New Todoist task",
				"description": "Task description",
				"checked":     false,
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	got, err := p.CreateTask(context.Background(), CreateTaskRequest{
		Title:       "New Todoist task",
		Description: "Task description",
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "999", got.ID)
	assert.Equal(t, "New Todoist task", got.Title)
}

func TestTodoist_GetWorkProjectID(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/projects" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": "p-personal", "name": "Personal"},
					{"id": "p-work", "name": "Work"},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	projectID, err := p.GetProjectIDByName(context.Background(), "Work")
	require.NoError(t, err)
	assert.Equal(t, "p-work", projectID)
}

func TestTodoist_FetchCompletedTasks(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/tasks/completed" {
			assert.Equal(t, "p-work", r.URL.Query().Get("project_id"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":           "task-101",
						"content":      "Done ticket [DX-4553](https://jira.com/DX-4553)",
						"description":  "Completed subtask",
						"completed_at": "2026-08-02T12:00:00Z",
						"labels":       []string{"jira-sync"},
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	tasks, err := p.FetchCompletedTasks(context.Background(), "p-work")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "task-101", tasks[0].ID)
	assert.Equal(t, []string{"jira-sync"}, tasks[0].Labels)
}

func TestTodoist_ListActiveTasks_success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/tasks" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{
						"id":          "111",
						"content":     "Personal task 1",
						"description": "Desc 1",
						"checked":     false,
						"url":         "https://app.todoist.com/app/task/111",
					},
					{
						"id":          "222",
						"content":     "Personal task 2",
						"description": "Desc 2",
						"checked":     false,
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})
	tasks, err := p.ListActiveTasks(context.Background())
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	assert.Equal(t, "111", tasks[0].ID)
	assert.Equal(t, "Personal task 1", tasks[0].Title)
	assert.Equal(t, "222", tasks[1].ID)
	assert.Equal(t, "https://app.todoist.com/app/task/222", tasks[1].URL)
}

func TestTodoist_ListActiveTasksForProjects(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": "proj_123", "name": "Work"},
					{"id": "proj_456", "name": "Personal"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == testAPIv1Base+"/tasks":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{
						"id":         "111",
						"project_id": "proj_123",
						"content":    "Work task",
						"checked":    false,
					},
					{
						"id":         "222",
						"project_id": "proj_456",
						"content":    "Personal task",
						"checked":    false,
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	p := NewTodoist(zerolog.Nop(), TodoistConfig{Token: "tok", BaseURL: srv.URL + testAPIv1Base})

	// Filtering by project name "Work" should return only task 111
	tasks, err := p.ListActiveTasksForProjects(context.Background(), []string{"Work"})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "111", tasks[0].ID)
	assert.Equal(t, "Work task", tasks[0].Title)
}
