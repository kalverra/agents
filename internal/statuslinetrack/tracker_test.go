package statuslinetrack

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSync_TracksStartTimeAcrossCalls(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	session := "sess-1"
	start := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)
	later := start.Add(2*time.Minute + 15*time.Second)

	active := []ActiveTask{
		{Key: "0", Name: "go test ./...", Sort: 0},
		{Key: "1", Name: "npm run build", Sort: 1},
	}

	first, err := Sync(root, session, active, start)
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.Equal(t, "go test ./...", first[0].Name)
	require.Equal(t, start, first[0].StartedAt)

	second, err := Sync(root, session, active, later)
	require.NoError(t, err)
	require.Equal(t, start, second[0].StartedAt)
	require.Equal(t, start, second[1].StartedAt)
	require.Equal(t, later.Sub(start), later.Sub(second[0].StartedAt))
}

func TestSync_DropsFinishedTasks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	session := "sess-2"
	now := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	_, err := Sync(root, session, []ActiveTask{
		{Key: "0", Name: "go test ./...", Sort: 0},
		{Key: "1", Name: "npm run build", Sort: 1},
	}, now)
	require.NoError(t, err)

	remaining, err := Sync(root, session, []ActiveTask{
		{Key: "1", Name: "npm run build", Sort: 1},
	}, now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	require.Equal(t, "1", remaining[0].Key)
}

func TestSyncSessionState_InitialAndCacheTokens(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	session := "sess-cache-1"
	start := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)
	toks := 20857

	state, err := SyncSessionState(root, session, &toks, true, start, "")
	require.NoError(t, err)
	require.Equal(t, start, state.LastActiveAt)
	require.True(t, state.HasCache)
	require.NotNil(t, state.CacheTokens)
	require.Equal(t, 20857, *state.CacheTokens)
}

func TestSyncSessionState_IdleDoesNotAdvanceLastActiveAt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	session := "sess-cache-2"
	start := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)
	twoHoursLater := start.Add(2 * time.Hour)
	toks := 15000

	// Initial active turn
	_, err := SyncSessionState(root, session, &toks, true, start, "")
	require.NoError(t, err)

	// Idle statusline check 2 hours later
	state, err := SyncSessionState(root, session, nil, false, twoHoursLater, "")
	require.NoError(t, err)
	// LastActiveAt must remain start, not advanced to twoHoursLater
	require.Equal(t, start, state.LastActiveAt)
	require.True(t, state.HasCache)
	require.Equal(t, 15000, *state.CacheTokens)
}

func TestSyncSessionState_TranscriptModTime(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	session := "sess-cache-3"
	start := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)
	toks := 5000

	// Create a dummy transcript file
	transcriptFile := filepath.Join(t.TempDir(), "transcript.jsonl")
	require.NoError(t, os.WriteFile(transcriptFile, []byte(`{"turn":1}`), 0o600))
	transcriptTime := start.Add(10 * time.Minute)
	require.NoError(t, os.Chtimes(transcriptFile, transcriptTime, transcriptTime))

	// Initial call
	_, err := SyncSessionState(root, session, &toks, true, start, "")
	require.NoError(t, err)

	// Call with transcriptPath that has newer mtime
	state, err := SyncSessionState(root, session, nil, false, start.Add(15*time.Minute), transcriptFile)
	require.NoError(t, err)
	require.Equal(t, transcriptTime.UTC(), state.LastActiveAt.UTC())
}

func TestSync_PreservesSessionCacheState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	session := "sess-interleaved"
	start := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)
	toks := 88000

	// 1. Sync session state
	sessState, err := SyncSessionState(root, session, &toks, true, start, "")
	require.NoError(t, err)
	require.True(t, sessState.HasCache)

	// 2. Sync background tasks
	tasks := []ActiveTask{{Key: "0", Name: "go test", Sort: 0}}
	entries, err := Sync(root, session, tasks, start.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// 3. Re-sync session state idle - verify tasks and cache are both intact
	sessState2, err := SyncSessionState(root, session, nil, false, start.Add(2*time.Minute), "")
	require.NoError(t, err)
	require.True(t, sessState2.HasCache)
	require.Equal(t, 88000, *sessState2.CacheTokens)
	require.Equal(t, start, sessState2.LastActiveAt)

	// Verify tasks still intact
	entries2, err := Sync(root, session, tasks, start.Add(3*time.Minute))
	require.NoError(t, err)
	require.Len(t, entries2, 1)
	require.Equal(t, "go test", entries2[0].Name)
}

func TestSync_SanitizesSessionKeyWithSlashes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	session := "cwd:/home/tester/Projects/testrig"
	now := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	tasks := []ActiveTask{{Key: "0", Name: "go test", Sort: 0}}
	entries, err := Sync(root, session, tasks, now)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	toks := 1000
	sessState, err := SyncSessionState(root, session, &toks, true, now, "")
	require.NoError(t, err)
	require.True(t, sessState.HasCache)
}
