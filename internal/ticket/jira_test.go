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

func TestJira_Fetch_rendersDescriptionAndCommentsAsMarkdown(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/PROJ-2":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":  "200",
				"key": "PROJ-2",
				"fields": map[string]any{
					"summary": "Spec",
					"description": map[string]any{
						"type": "doc", "version": 1,
						"content": []any{
							map[string]any{
								"type": "heading", "attrs": map[string]any{"level": float64(2)},
								"content": []any{map[string]any{"type": "text", "text": "Goals"}},
							},
							map[string]any{
								"type": "bulletList",
								"content": []any{
									map[string]any{
										"type": "listItem",
										"content": []any{
											map[string]any{
												"type": "paragraph",
												"content": []any{
													map[string]any{"type": "text", "text": "Ship "},
													map[string]any{
														"type":  "text",
														"text":  "v1",
														"marks": []any{map[string]any{"type": "strong"}},
													},
												},
											},
										},
									},
								},
							},
						},
					},
					"status": map[string]any{"name": "Open"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/PROJ-2/comment":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"comments": []map[string]any{
					{
						"id": "c1", "created": "2026-05-01T12:00:00.000+0000",
						"body": map[string]any{
							"type": "doc", "version": 1,
							"content": []any{
								map[string]any{
									"type": "paragraph",
									"content": []any{
										map[string]any{"type": "text", "text": "See "},
										map[string]any{
											"type": "text",
											"text": "docs",
											"marks": []any{
												map[string]any{
													"type":  "link",
													"attrs": map[string]any{"href": "https://docs.example/doc"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
				"startAt": 0, "maxResults": 50, "total": 1,
			})
		default:
			assert.Failf(t, "unexpected request", "unexpected %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	u := srv.URL
	host := u[len("http://"):]
	p := NewJira(zerolog.Nop(), JiraConfig{
		Email: "a@b.c", APIToken: "tok", Domain: host, HTTPScheme: "http",
	})
	got, err := p.Fetch(context.Background(), "PROJ-2")
	require.NoError(t, err)
	assert.Equal(t, "## Goals\n\n- Ship **v1**", got.Description)
	require.Len(t, got.Comments, 1)
	assert.Equal(t, "See [docs](https://docs.example/doc)", got.Comments[0].Content)
}

func TestJira_Fetch_success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/TASK-1":
			assert.NotEmpty(t, r.Header.Get("Authorization"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":  "10000",
				"key": "TASK-1",
				"fields": map[string]any{
					"summary": "Fix thing",
					"description": map[string]any{
						"type": "doc", "version": 1,
						"content": []any{
							map[string]any{
								"type": "paragraph",
								"content": []any{
									map[string]any{"type": "text", "text": "Do it"},
								},
							},
						},
					},
					"status": map[string]any{"name": "In Progress"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/TASK-1/comment":
			assert.Equal(t, "0", r.URL.Query().Get("startAt"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"comments": []map[string]any{
					{
						"id":      "101",
						"created": "2026-01-02T10:00:00.000+0000",
						"body": map[string]any{
							"type": "doc", "version": 1,
							"content": []any{
								map[string]any{
									"type": "paragraph",
									"content": []any{
										map[string]any{"type": "text", "text": "Note one"},
									},
								},
							},
						},
					},
				},
				"startAt":    0,
				"maxResults": 50,
				"total":      1,
			})
		default:
			assert.Failf(t, "unexpected request", "unexpected %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	u := srv.URL
	host := u[len("http://"):]
	p := NewJira(zerolog.Nop(), JiraConfig{
		Email:      "a@b.c",
		APIToken:   "tok",
		Domain:     host,
		HTTPScheme: "http",
	})
	got, err := p.Fetch(context.Background(), "TASK-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "TASK-1", got.ID)
	assert.Equal(t, "Fix thing", got.Title)
	assert.Equal(t, "Do it", got.Description)
	assert.Equal(t, "In Progress", got.Status)
	assert.Equal(t, "http://"+host+"/browse/TASK-1", got.URL)
	require.Len(t, got.Comments, 1)
	assert.Equal(t, "101", got.Comments[0].ID)
	assert.Equal(t, "Note one", got.Comments[0].Content)
	assert.Equal(t, "2026-01-02T10:00:00.000+0000", got.Comments[0].PostedAt)
}

func TestJira_Comment_success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/issue/TASK-9/comment" {
			assert.Failf(t, "unexpected request", "unexpected %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			assert.NoError(t, err, "decode body")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		doc, ok := body["body"].(map[string]any)
		if !ok {
			assert.Fail(t, "body is not a map[string]any")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		assert.Equal(t, "doc", doc["type"])
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"999"}`))
	}))
	t.Cleanup(srv.Close)

	u := srv.URL
	host := u[len("http://"):]
	p := NewJira(zerolog.Nop(), JiraConfig{
		Email:      "a@b.c",
		APIToken:   "tok",
		Domain:     host,
		HTTPScheme: "http",
	})
	err := p.Comment(context.Background(), "TASK-9", "hello there")
	require.NoError(t, err)
}

func TestJira_Fetch_missingToken(t *testing.T) {
	t.Parallel()
	p := NewJira(zerolog.Nop(), JiraConfig{
		Email:    "a@b.c",
		Domain:   "x.atlassian.net",
		APIToken: "",
	})
	_, err := p.Fetch(context.Background(), "X-1")
	require.Error(t, err)
}

func TestJira_CreateIssue_success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/issue" {
			assert.Failf(t, "unexpected request", "unexpected %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		err := json.NewDecoder(r.Body).Decode(&body)
		if !assert.NoError(t, err) {
			return
		}

		fields, ok := body["fields"].(map[string]any)
		if !assert.True(t, ok) {
			return
		}
		proj, _ := fields["project"].(map[string]any)
		assert.Equal(t, "DX", proj["key"])
		assert.Equal(t, "New Task", fields["summary"])

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "10050",
			"key":  "DX-100",
			"self": "http://example/rest/api/3/issue/10050",
		})
	}))
	t.Cleanup(srv.Close)

	u := srv.URL
	host := u[len("http://"):]
	p := NewJira(zerolog.Nop(), JiraConfig{
		Email:      "a@b.c",
		APIToken:   "tok",
		Domain:     host,
		HTTPScheme: "http",
	})
	got, err := p.CreateIssue(context.Background(), CreateIssueRequest{
		Project:     "DX",
		Summary:     "New Task",
		Description: "Task description",
		IssueType:   "Task",
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "DX-100", got.ID)
	assert.Equal(t, "New Task", got.Title)
	assert.Equal(t, "http://"+host+"/browse/DX-100", got.URL)
}

func TestJira_CreateIssue_withEpic(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/issue" {
			assert.Failf(t, "unexpected request", "unexpected %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		err := json.NewDecoder(r.Body).Decode(&body)
		if !assert.NoError(t, err) {
			return
		}

		fields, ok := body["fields"].(map[string]any)
		if !assert.True(t, ok) {
			return
		}
		parent, ok := fields["parent"].(map[string]any)
		if !assert.True(t, ok) {
			return
		}
		assert.Equal(t, "DX-50", parent["key"])

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "10051",
			"key":  "DX-101",
			"self": "http://example/rest/api/3/issue/10051",
		})
	}))
	t.Cleanup(srv.Close)

	u := srv.URL
	host := u[len("http://"):]
	p := NewJira(zerolog.Nop(), JiraConfig{
		Email:      "a@b.c",
		APIToken:   "tok",
		Domain:     host,
		HTTPScheme: "http",
	})
	got, err := p.CreateIssue(context.Background(), CreateIssueRequest{
		Project:     "DX",
		Summary:     "Child Task",
		EpicKey:     "DX-50",
		Description: "Child of epic",
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "DX-101", got.ID)
}

func TestJira_ListEpics_success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet &&
			(r.URL.Path == "/rest/api/3/search/jql" || r.URL.Path == "/rest/api/3/search") {
			jql := r.URL.Query().Get("jql")
			assert.Contains(t, jql, "assignee = currentUser()")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issues": []map[string]any{
					{
						"id":  "10",
						"key": "DX-10",
						"fields": map[string]any{
							"summary": "Core Infrastructure Epic",
							"status":  map[string]any{"name": "In Progress"},
						},
					},
					{
						"id":  "11",
						"key": "DX-11",
						"fields": map[string]any{
							"summary": "Agent Ticketing Flow Epic",
							"status":  map[string]any{"name": "In Progress"},
						},
					},
				},
				"total": 2,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	u := srv.URL
	host := u[len("http://"):]
	p := NewJira(zerolog.Nop(), JiraConfig{
		Email:      "a@b.c",
		APIToken:   "tok",
		Domain:     host,
		HTTPScheme: "http",
	})
	epics, err := p.ListEpics(context.Background())
	require.NoError(t, err)
	require.Len(t, epics, 2)
	assert.Equal(t, "DX-10", epics[0].ID)
	assert.Equal(t, "Core Infrastructure Epic", epics[0].Title)
	assert.Equal(t, "DX-11", epics[1].ID)
	assert.Equal(t, "Agent Ticketing Flow Epic", epics[1].Title)
}

func TestJira_ListAssignedTickets_success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet &&
			(r.URL.Path == "/rest/api/3/search/jql" || r.URL.Path == "/rest/api/3/search") {
			fields := r.URL.Query().Get("fields")
			assert.Contains(t, fields, "parent")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issues": []map[string]any{
					{
						"id":  "55",
						"key": "DX-55",
						"fields": map[string]any{
							"summary": "Fix auth token refresh",
							"status":  map[string]any{"name": "In Progress"},
							"parent": map[string]any{
								"key": "DX-10",
								"fields": map[string]any{
									"summary": "Core Infrastructure Epic",
								},
							},
						},
					},
				},
				"total": 1,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	u := srv.URL
	host := u[len("http://"):]
	p := NewJira(zerolog.Nop(), JiraConfig{
		Email:      "a@b.c",
		APIToken:   "tok",
		Domain:     host,
		HTTPScheme: "http",
	})
	tickets, err := p.ListAssignedTickets(context.Background())
	require.NoError(t, err)
	require.Len(t, tickets, 1)
	assert.Equal(t, "DX-55", tickets[0].ID)
	assert.Equal(t, "Fix auth token refresh", tickets[0].Title)
	assert.Equal(t, "DX-10", tickets[0].ParentID)
	assert.Equal(t, "Core Infrastructure Epic", tickets[0].ParentTitle)
}
