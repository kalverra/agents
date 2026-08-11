// Package schedule installs, removes, and inspects macOS launchd jobs for automated activity reports.
package schedule

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/rs/zerolog"
)

// Interval is a single StartCalendarInterval entry in a launchd plist.
type Interval struct {
	Month      int
	MonthSet   bool
	Day        int
	DaySet     bool
	Weekday    int
	WeekdaySet bool
	Hour       int
	Minute     int
}

// Schedule holds the schedule spec for each report mode.
// Specs are small strings like "weekday:08:30", "fri:16:00", or "28:17:00".
type Schedule struct {
	Daily     string
	Weekly    string
	Monthly   string
	Quarterly string
	Finalize  string
}

// Scheduler writes and manages launchd plists.
type Scheduler struct {
	PlistDir    string
	LogDir      string
	BinaryPath  string
	LabelPrefix string
	Project     string
	Provider    string
	Model       string
	UID         string
	Logger      zerolog.Logger
	Schedule    Schedule
	Eval        bool
	Run         func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Status reports which activity jobs are currently loaded by launchd.
type Status struct {
	Daily     bool
	Weekly    bool
	Monthly   bool
	Quarterly bool
	Finalize  bool
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Binary}}</string>
        <string>activity</string>
        <string>review</string>
        <string>--mode</string>
        <string>{{.Mode}}</string>
        <string>--project</string>
        <string>{{.Project}}</string>
        {{- if .Provider}}
        <string>--provider</string>
        <string>{{.Provider}}</string>
        {{- end}}
        {{- if .Model}}
        <string>--model</string>
        <string>{{.Model}}</string>
        {{- end}}
        {{- if .Eval}}
        <string>--eval</string>
        {{- end}}
    </array>
    <key>StartCalendarInterval</key>
    <array>
        {{- range .Intervals}}
        <dict>
            {{- if .MonthSet}}
            <key>Month</key>
            <integer>{{.Month}}</integer>
            {{- end}}
            {{- if .DaySet}}
            <key>Day</key>
            <integer>{{.Day}}</integer>
            {{- end}}
            {{- if .WeekdaySet}}
            <key>Weekday</key>
            <integer>{{.Weekday}}</integer>
            {{- end}}
            <key>Hour</key>
            <integer>{{.Hour}}</integer>
            <key>Minute</key>
            <integer>{{.Minute}}</integer>
        </dict>
        {{- end}}
    </array>
    <key>StandardOutPath</key>
    <string>{{.LogDir}}/{{.Mode}}.log</string>
    <key>StandardErrorPath</key>
    <string>{{.LogDir}}/{{.Mode}}.err.log</string>
</dict>
</plist>
`

const finalizePlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Binary}}</string>
        <string>activity</string>
        <string>finalize</string>
    </array>
    <key>StartCalendarInterval</key>
    <array>
        {{- range .Intervals}}
        <dict>
            {{- if .WeekdaySet}}
            <key>Weekday</key>
            <integer>{{.Weekday}}</integer>
            {{- end}}
            <key>Hour</key>
            <integer>{{.Hour}}</integer>
            <key>Minute</key>
            <integer>{{.Minute}}</integer>
        </dict>
        {{- end}}
    </array>
    <key>StandardOutPath</key>
    <string>{{.LogDir}}/finalize.log</string>
    <key>StandardErrorPath</key>
    <string>{{.LogDir}}/finalize.err.log</string>
</dict>
</plist>
`

// Install writes plists and loads launchd jobs for each configured schedule.
func (s *Scheduler) Install(ctx context.Context) error {
	if err := os.MkdirAll(s.PlistDir, 0750); err != nil {
		return fmt.Errorf("creating plist dir: %w", err)
	}
	if err := os.MkdirAll(s.LogDir, 0750); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}

	modes := []struct {
		mode string
		spec string
	}{
		{"daily", s.Schedule.Daily},
		{"weekly", s.Schedule.Weekly},
		{"monthly", s.Schedule.Monthly},
		{"quarterly", s.Schedule.Quarterly},
	}

	var errs []error
	for _, m := range modes {
		if strings.TrimSpace(m.spec) == "" {
			s.Logger.Debug().Str("mode", m.mode).Msg("no schedule configured; skipping")
			continue
		}
		if err := s.installMode(ctx, m.mode, m.spec); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.mode, err))
		}
	}

	if strings.TrimSpace(s.Schedule.Finalize) != "" {
		if err := s.installFinalize(ctx, s.Schedule.Finalize); err != nil {
			errs = append(errs, fmt.Errorf("finalize: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("install errors: %v", errs)
	}
	return nil
}

func (s *Scheduler) installMode(ctx context.Context, mode, spec string) error {
	label := s.LabelPrefix + "-" + mode
	plistPath := filepath.Join(s.PlistDir, label+".plist")

	intervals, err := ParseSchedule(mode, spec)
	if err != nil {
		return fmt.Errorf("parsing schedule: %w", err)
	}

	if _, err := s.bootout(ctx, label); err != nil {
		s.Logger.Debug().Err(err).Str("label", label).Msg("bootout before install failed (likely not loaded)")
	}

	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return fmt.Errorf("parsing plist template: %w", err)
	}

	f, err := os.Create(plistPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("creating plist: %w", err)
	}
	defer f.Close() //nolint:errcheck

	if err := tmpl.Execute(f, map[string]any{
		"Label":     label,
		"Binary":    s.BinaryPath,
		"Mode":      mode,
		"Project":   s.Project,
		"Provider":  s.Provider,
		"Model":     s.Model,
		"Eval":      s.Eval,
		"Intervals": intervals,
		"LogDir":    s.LogDir,
	}); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	if _, err := s.run(ctx, "launchctl", "bootstrap", "gui/"+s.UID, plistPath); err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}
	if _, err := s.run(ctx, "launchctl", "enable", "gui/"+s.UID+"/"+label); err != nil {
		s.Logger.Debug().Err(err).Str("label", label).Msg("enable after bootstrap failed")
	}
	return nil
}

func (s *Scheduler) installFinalize(ctx context.Context, spec string) error {
	label := s.LabelPrefix + "-finalize"
	plistPath := filepath.Join(s.PlistDir, label+".plist")

	intervals, err := ParseFinalizeSchedule(spec)
	if err != nil {
		return fmt.Errorf("parsing finalize schedule: %w", err)
	}

	if _, err := s.bootout(ctx, label); err != nil {
		s.Logger.Debug().Err(err).Str("label", label).Msg("bootout before install failed (likely not loaded)")
	}

	tmpl, err := template.New("finalize_plist").Parse(finalizePlistTemplate)
	if err != nil {
		return fmt.Errorf("parsing finalize plist template: %w", err)
	}

	f, err := os.Create(plistPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("creating finalize plist: %w", err)
	}
	defer f.Close() //nolint:errcheck

	if err := tmpl.Execute(f, map[string]any{
		"Label":     label,
		"Binary":    s.BinaryPath,
		"Intervals": intervals,
		"LogDir":    s.LogDir,
	}); err != nil {
		return fmt.Errorf("writing finalize plist: %w", err)
	}

	if _, err := s.run(ctx, "launchctl", "bootstrap", "gui/"+s.UID, plistPath); err != nil {
		return fmt.Errorf("bootstrap finalize failed: %w", err)
	}
	if _, err := s.run(ctx, "launchctl", "enable", "gui/"+s.UID+"/"+label); err != nil {
		s.Logger.Debug().Err(err).Str("label", label).Msg("enable after bootstrap finalize failed")
	}
	return nil
}

// Uninstall unloads and removes launchd jobs for each configured schedule.
func (s *Scheduler) Uninstall(ctx context.Context) error {
	modes := []string{"daily", "weekly", "monthly", "quarterly", "finalize"}
	var errs []error
	for _, mode := range modes {
		label := s.LabelPrefix + "-" + mode
		if _, err := s.bootout(ctx, label); err != nil {
			errs = append(errs, fmt.Errorf("%s bootout: %w", mode, err))
		}
		plistPath := filepath.Join(s.PlistDir, label+".plist")
		if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("%s remove plist: %w", mode, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("uninstall errors: %v", errs)
	}
	return nil
}

// Status asks launchd which activity jobs are currently loaded.
func (s *Scheduler) Status(ctx context.Context) (Status, error) {
	out, err := s.run(ctx, "launchctl", "print", "gui/"+s.UID)
	if err != nil {
		return Status{}, fmt.Errorf("launchctl print: %w", err)
	}
	outStr := string(out)
	return Status{
		Daily:     strings.Contains(outStr, s.LabelPrefix+"-daily"),
		Weekly:    strings.Contains(outStr, s.LabelPrefix+"-weekly"),
		Monthly:   strings.Contains(outStr, s.LabelPrefix+"-monthly"),
		Quarterly: strings.Contains(outStr, s.LabelPrefix+"-quarterly"),
		Finalize:  strings.Contains(outStr, s.LabelPrefix+"-finalize"),
	}, nil
}

func (s *Scheduler) bootout(ctx context.Context, label string) ([]byte, error) {
	return s.run(ctx, "launchctl", "bootout", "gui/"+s.UID+"/"+label)
}

func (s *Scheduler) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if s.Run != nil {
		return s.Run(ctx, name, args...)
	}
	return nil, fmt.Errorf("no command runner configured")
}

// ParseSchedule converts a schedule spec into launchd StartCalendarInterval entries.
func ParseSchedule(mode, spec string) ([]Interval, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	parts := strings.Split(spec, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return nil, fmt.Errorf("schedule spec %q must be <day>:<HH:MM> or <day>:<HH>:<MM>", spec)
	}

	daySpec := parts[0]
	var timeParts []string
	if len(parts) == 2 {
		timeParts = strings.Split(parts[1], ":")
	} else {
		timeParts = parts[1:]
	}
	if len(timeParts) != 2 {
		return nil, fmt.Errorf("schedule spec %q has invalid time", spec)
	}
	hour, err := strconv.Atoi(timeParts[0])
	if err != nil || hour < 0 || hour > 23 {
		return nil, fmt.Errorf("schedule spec %q has invalid hour", spec)
	}
	minute, err := strconv.Atoi(timeParts[1])
	if err != nil || minute < 0 || minute > 59 {
		return nil, fmt.Errorf("schedule spec %q has invalid minute", spec)
	}

	daySpec = strings.ToLower(daySpec)

	var intervals []Interval
	switch daySpec {
	case "weekday":
		for wd := 1; wd <= 5; wd++ {
			intervals = append(intervals, Interval{Weekday: wd, WeekdaySet: true, Hour: hour, Minute: minute})
		}
	case "mon":
		intervals = append(intervals, Interval{Weekday: 1, WeekdaySet: true, Hour: hour, Minute: minute})
	case "tue":
		intervals = append(intervals, Interval{Weekday: 2, WeekdaySet: true, Hour: hour, Minute: minute})
	case "wed":
		intervals = append(intervals, Interval{Weekday: 3, WeekdaySet: true, Hour: hour, Minute: minute})
	case "thu":
		intervals = append(intervals, Interval{Weekday: 4, WeekdaySet: true, Hour: hour, Minute: minute})
	case "fri":
		intervals = append(intervals, Interval{Weekday: 5, WeekdaySet: true, Hour: hour, Minute: minute})
	case "sat":
		intervals = append(intervals, Interval{Weekday: 6, WeekdaySet: true, Hour: hour, Minute: minute})
	case "sun":
		intervals = append(intervals, Interval{Weekday: 0, WeekdaySet: true, Hour: hour, Minute: minute})
	default:
		day, err := strconv.Atoi(daySpec)
		if err != nil || day < 1 || day > 31 {
			return nil, fmt.Errorf("schedule spec %q has invalid day %q", spec, daySpec)
		}
		intervals = append(intervals, Interval{Day: day, DaySet: true, Hour: hour, Minute: minute})
	}

	if mode == "quarterly" && daySpec != "" && daySpec != "weekday" {
		// Replicate the day interval across quarter-end months.
		expanded := make([]Interval, 0, 4)
		for _, m := range []int{3, 6, 9, 12} {
			for _, iv := range intervals {
				expanded = append(
					expanded,
					Interval{
						Month:    m,
						MonthSet: true,
						Day:      iv.Day,
						DaySet:   iv.DaySet,
						Hour:     iv.Hour,
						Minute:   iv.Minute,
					},
				)
			}
		}
		return expanded, nil
	}

	return intervals, nil
}

// ParseFinalizeSchedule generates intervals for running every N minutes during business hours (09:00-17:59) on weekdays.
// Spec format: "weekday-bh:HH:MM" where HH:MM is the interval (e.g. "weekday-bh:00:30" = every 30 minutes).
func ParseFinalizeSchedule(spec string) ([]Interval, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	parts := strings.Split(spec, ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("finalize schedule spec %q must be weekday-bh:HH:MM", spec)
	}

	if parts[0] != "weekday-bh" {
		return nil, fmt.Errorf("finalize schedule spec must start with 'weekday-bh', got: %q", parts[0])
	}

	hourInt, err := strconv.Atoi(parts[1])
	if err != nil || hourInt < 0 {
		return nil, fmt.Errorf("invalid hour interval: %v", err)
	}
	minInt, err := strconv.Atoi(parts[2])
	if err != nil || minInt < 0 {
		return nil, fmt.Errorf("invalid minute interval: %v", err)
	}

	totalMinInterval := hourInt*60 + minInt
	if totalMinInterval <= 0 {
		return nil, fmt.Errorf("interval must be greater than 0")
	}

	var intervals []Interval
	for wd := 1; wd <= 5; wd++ {
		for h := 9; h <= 17; h++ {
			for m := 0; m < 60; m += totalMinInterval {
				intervals = append(intervals, Interval{
					Weekday:    wd,
					WeekdaySet: true,
					Hour:       h,
					Minute:     m,
				})
			}
		}
	}
	return intervals, nil
}
