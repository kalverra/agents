package activity

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseTimeframe converts a timeframe string like "1d", "7d", "30d", "today", "yesterday", "week", "month", "quarter", or "24h" into a start time relative to now.
func ParseTimeframe(tf string, now time.Time) (time.Time, error) {
	tf = strings.ToLower(strings.TrimSpace(tf))
	if tf == "" {
		tf = "7d"
	}

	switch tf {
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), nil
	case "yesterday":
		y := now.AddDate(0, 0, -1)
		return time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, now.Location()), nil
	case "week":
		return now.Add(-7 * 24 * time.Hour), nil
	case "month":
		return now.Add(-30 * 24 * time.Hour), nil
	case "quarter":
		return now.Add(-90 * 24 * time.Hour), nil
	}

	if before, ok := strings.CutSuffix(tf, "d"); ok {
		daysStr := before
		days, err := strconv.Atoi(daysStr)
		if err == nil && days > 0 {
			return now.Add(time.Duration(-days) * 24 * time.Hour), nil
		}
	}

	dur, err := time.ParseDuration(tf)
	if err == nil && dur > 0 {
		return now.Add(-dur), nil
	}

	return time.Time{}, fmt.Errorf(
		"unsupported timeframe format: %q (use 1d, 7d, 30d, today, yesterday, week, month, quarter, or duration like 24h)",
		tf,
	)
}
