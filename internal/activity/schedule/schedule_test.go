package schedule_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedule "github.com/kalverra/agents/internal/activity/schedule"
)

func TestParseSchedule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mode    string
		spec    string
		wantLen int
		want    []schedule.Interval
	}{
		{
			name:    "daily weekday",
			mode:    "daily",
			spec:    "weekday:08:30",
			wantLen: 5,
		},
		{
			name:    "weekly fri",
			mode:    "weekly",
			spec:    "fri:16:00",
			wantLen: 1,
			want:    []schedule.Interval{{Weekday: 5, WeekdaySet: true, Hour: 16, Minute: 0}},
		},
		{
			name:    "monthly day",
			mode:    "monthly",
			spec:    "28:17:00",
			wantLen: 1,
			want:    []schedule.Interval{{Day: 28, DaySet: true, Hour: 17, Minute: 0}},
		},
		{
			name:    "quarterly day",
			mode:    "quarterly",
			spec:    "28:17:00",
			wantLen: 4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			intervals, err := schedule.ParseSchedule(tc.mode, tc.spec)
			require.NoError(t, err)
			require.Len(t, intervals, tc.wantLen)
			if len(tc.want) > 0 {
				assert.Equal(t, tc.want[0], intervals[0])
			}
		})
	}
}

func TestParseSchedule_Invalid(t *testing.T) {
	t.Parallel()

	_, err := schedule.ParseSchedule("daily", "bad")
	require.Error(t, err)
}

func TestScheduler_Install(t *testing.T) {
	t.Parallel()

	plistDir := t.TempDir()
	logDir := t.TempDir()
	binPath := "/Users/me/.agents/agents"

	var calls []string
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, fmt.Sprint(args))
		return nil, nil
	}

	s := &schedule.Scheduler{
		PlistDir:    plistDir,
		LogDir:      logDir,
		BinaryPath:  binPath,
		LabelPrefix: "com.test.activity",
		UID:         "501",
		Project:     "Work",
		Logger:      zerolog.New(nil),
		Schedule: schedule.Schedule{
			Daily:     "weekday:08:30",
			Weekly:    "fri:16:00",
			Monthly:   "28:17:00",
			Quarterly: "28:17:00",
		},
		Run: run,
	}

	require.NoError(t, s.Install(context.Background()))

	for _, mode := range []string{"daily", "weekly", "monthly", "quarterly"} {
		assert.FileExists(t, filepath.Join(plistDir, "com.test.activity-"+mode+".plist"))
	}
	require.Len(t, calls, 12) // bootout + bootstrap + enable per mode
}

func TestScheduler_Install_WithProviderModel(t *testing.T) {
	t.Parallel()

	plistDir := t.TempDir()
	logDir := t.TempDir()
	run := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, nil
	}

	s := &schedule.Scheduler{
		PlistDir:    plistDir,
		LogDir:      logDir,
		BinaryPath:  "/Users/me/.agents/agents",
		LabelPrefix: "com.test.activity",
		UID:         "501",
		Project:     "Work",
		Provider:    "llama",
		Model:       "Qwen/Qwen2.5-Coder-32B-Instruct-GGUF:Q4_K_M",
		Logger:      zerolog.New(nil),
		Schedule: schedule.Schedule{
			Daily: "weekday:08:30",
		},
		Run: run,
	}

	require.NoError(t, s.Install(context.Background()))
	content, err := os.ReadFile(filepath.Join(plistDir, "com.test.activity-daily.plist")) //nolint:gosec
	require.NoError(t, err)
	assert.Contains(t, string(content), "<?xml version=\"1.0\" encoding=\"UTF-8\"?>")
	assert.NotContains(t, string(content), "&lt;?xml")
	assert.Contains(t, string(content), "<string>--provider</string>")
	assert.Contains(t, string(content), "<string>llama</string>")
	assert.Contains(t, string(content), "<string>--model</string>")
	assert.Contains(t, string(content), "Qwen/Qwen2.5-Coder-32B-Instruct-GGUF:Q4_K_M")
}

func TestScheduler_Uninstall(t *testing.T) {
	t.Parallel()

	plistDir := t.TempDir()
	var calls []string
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, fmt.Sprint(args))
		return nil, nil
	}

	s := &schedule.Scheduler{
		PlistDir:    plistDir,
		LabelPrefix: "com.test.activity",
		UID:         "501",
		Logger:      zerolog.New(nil),
		Schedule: schedule.Schedule{
			Daily: "weekday:08:30",
		},
		Run: run,
	}

	path := filepath.Join(plistDir, "com.test.activity-daily.plist")
	require.NoError(t, os.WriteFile(path, []byte("plist"), 0600))

	require.NoError(t, s.Uninstall(context.Background()))
	assert.NoFileExists(t, path)
	require.Len(t, calls, 4) // one bootout per mode
}

func TestScheduler_Status(t *testing.T) {
	t.Parallel()

	run := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("com.test.activity-daily\ncom.test.activity-weekly\ncom.other\n"), nil
	}

	s := &schedule.Scheduler{
		LabelPrefix: "com.test.activity",
		UID:         "501",
		Logger:      zerolog.New(nil),
		Run:         run,
	}

	status, err := s.Status(context.Background())
	require.NoError(t, err)
	assert.True(t, status.Daily)
	assert.True(t, status.Weekly)
	assert.False(t, status.Monthly)
	assert.False(t, status.Quarterly)
}
