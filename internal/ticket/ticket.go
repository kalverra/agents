// Package ticket abstracts issue trackers for session workflows.
package ticket

import "context"

// Ticket is a normalized task/issue for display and SessionGoal text.
type Ticket struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status,omitempty"`
	URL         string `json:"url,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	Sprint      string `json:"sprint,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`

	ParentTitle string        `json:"parent_title,omitempty"`
	Comments    []TaskComment `json:"comments,omitempty"`
}

// Provider fetches tasks and posts follow-up comments.
type Provider interface {
	Fetch(ctx context.Context, id string) (*Ticket, error)
	Comment(ctx context.Context, id string, body string) error
}
