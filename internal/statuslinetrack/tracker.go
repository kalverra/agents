// Package statuslinetrack provides tracking capabilities for the status line.
package statuslinetrack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kalverra/agents/internal/paths"
)

func sanitizeKey(key string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '_'
		}
		return r
	}, key)
}

// ActiveTask is a running background task from the statusline payload.
type ActiveTask struct {
	Key  string
	Name string
	Sort int
}

// Entry is a tracked task with a persisted start time.
type Entry struct {
	Key       string
	Name      string
	StartedAt time.Time
}

// SessionState tracks the persisted session activity and cache details.
type SessionState struct {
	LastActiveAt time.Time `json:"last_active_at"`
	HasCache     bool      `json:"has_cache"`
	CacheTokens  *int      `json:"cache_tokens,omitempty"`
}

type fileState struct {
	Tasks        map[string]taskRecord `json:"tasks"`
	LastActiveAt time.Time             `json:"last_active_at"`
	CacheTokens  *int                  `json:"cache_tokens,omitempty"`
	HasCache     bool                  `json:"has_cache,omitempty"`
}

type taskRecord struct {
	StartedAt time.Time `json:"started_at"`
	Name      string    `json:"name"`
}

// DefaultRoot returns the directory for per-session tracker files.
func DefaultRoot() string {
	return filepath.Join(paths.StateDir(), "statusline-tracker")
}

// Sync updates persisted task start times for sessionID and returns active entries in sort order.
func Sync(root, sessionID string, active []ActiveTask, now time.Time) ([]Entry, error) {
	if sessionID == "" {
		sessionID = "unknown"
	}
	sessionID = sanitizeKey(sessionID)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}

	path := filepath.Join(root, sessionID+".json")
	state, err := loadState(path)
	if err != nil {
		return nil, err
	}
	if state.Tasks == nil {
		state.Tasks = make(map[string]taskRecord)
	}

	activeKeys := make(map[string]struct{}, len(active))
	for _, task := range active {
		activeKeys[task.Key] = struct{}{}
		rec, exists := state.Tasks[task.Key]
		if !exists {
			rec = taskRecord{StartedAt: now, Name: task.Name}
		} else if task.Name != "" {
			rec.Name = task.Name
		}
		state.Tasks[task.Key] = rec
	}

	for key := range state.Tasks {
		if _, ok := activeKeys[key]; !ok {
			delete(state.Tasks, key)
		}
	}

	if err := saveState(path, state); err != nil {
		return nil, err
	}

	sort.Slice(active, func(i, j int) bool {
		return active[i].Sort < active[j].Sort
	})

	entries := make([]Entry, 0, len(active))
	for _, task := range active {
		rec := state.Tasks[task.Key]
		entries = append(entries, Entry{
			Key:       task.Key,
			Name:      rec.Name,
			StartedAt: rec.StartedAt,
		})
	}
	return entries, nil
}

// SyncSessionState updates and returns the persisted session cache state.
func SyncSessionState(
	root, sessionID string,
	cacheTokens *int,
	isActiveTurn bool,
	now time.Time,
	transcriptPath string,
) (SessionState, error) {
	if sessionID == "" {
		sessionID = "unknown"
	}
	sessionID = sanitizeKey(sessionID)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return SessionState{}, err
	}

	path := filepath.Join(root, sessionID+".json")
	state, err := loadState(path)
	if err != nil {
		return SessionState{}, err
	}

	if cacheTokens != nil && *cacheTokens > 0 {
		state.HasCache = true
		toks := *cacheTokens
		state.CacheTokens = &toks
	}

	if transcriptPath != "" {
		if info, statErr := os.Stat(transcriptPath); statErr == nil {
			modTime := info.ModTime()
			if modTime.After(state.LastActiveAt) {
				state.LastActiveAt = modTime
			}
		}
	}

	if isActiveTurn {
		state.LastActiveAt = now
	} else if state.LastActiveAt.IsZero() {
		state.LastActiveAt = now
	}

	if err := saveState(path, state); err != nil {
		return SessionState{}, err
	}

	return SessionState{
		LastActiveAt: state.LastActiveAt,
		HasCache:     state.HasCache,
		CacheTokens:  state.CacheTokens,
	}, nil
}

func loadState(path string) (fileState, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is under controlled tracker root
	if err != nil {
		if os.IsNotExist(err) {
			return fileState{Tasks: map[string]taskRecord{}}, nil
		}
		return fileState{}, err
	}
	var state fileState
	if err := json.Unmarshal(data, &state); err != nil {
		return fileState{}, fmt.Errorf("parse tracker %s: %w", path, err)
	}
	if state.Tasks == nil {
		state.Tasks = map[string]taskRecord{}
	}
	return state, nil
}

func saveState(path string, state fileState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
