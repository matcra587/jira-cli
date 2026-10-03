package jira

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gechr/x/human"
)

// worklogStartedLayout includes milliseconds and Jira's numeric UTC offset.
const worklogStartedLayout = "2006-01-02T15:04:05.000-0700"

var worklogStartedOffsetLayouts = []string{
	time.RFC3339Nano,
	worklogStartedLayout,
	"2006-01-02T15:04:05-0700",
}

var worklogStartedLocalLayouts = []string{
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// ParseWorklogStarted normalizes ISO timestamps, local dates/times, now,
// yesterday and elapsed durations followed by " ago". Explicit offsets are
// preserved; relative dates use the injected clock and location. A nil
// location selects time.Local. Relative duration days are 24 hours, whereas
// ParseWorklogDuration uses workdays for logged effort.
func ParseWorklogStarted(input string, now time.Time, loc *time.Location) (string, error) {
	if loc == nil {
		loc = time.Local
	}
	value := strings.TrimSpace(input)
	lower := strings.ToLower(value)
	var parsed time.Time
	switch lower {
	case "":
		return "", errors.New("started timestamp is empty")
	case "now":
		parsed = now.In(loc)
	case "yesterday":
		parsed = now.In(loc).AddDate(0, 0, -1)
	default:
		if rel, ok := strings.CutSuffix(lower, " ago"); ok {
			d, err := human.ParseDuration(strings.TrimSpace(rel))
			if err != nil {
				return "", fmt.Errorf("relative started %q: %w", input, err)
			}
			if d < 0 {
				return "", fmt.Errorf("relative started %q: duration must not be negative", input)
			}
			parsed = now.In(loc).Add(-d)
		} else {
			matched := false
			for _, layout := range worklogStartedOffsetLayouts {
				if t, err := time.Parse(layout, value); err == nil {
					parsed = t
					matched = true
					break
				}
			}
			if !matched {
				for _, layout := range worklogStartedLocalLayouts {
					if t, err := time.ParseInLocation(layout, value, loc); err == nil {
						parsed = t
						matched = true
						break
					}
				}
			}
			if !matched {
				return "", fmt.Errorf("started %q must be ISO-8601 (offset optional) or relative (now, yesterday, 2h ago)", input)
			}
		}
	}
	if parsed.Year() < 1 || parsed.Year() > 9999 {
		return "", errors.New("started timestamp must have a year from 0001 to 9999")
	}
	return parsed.Format(worklogStartedLayout), nil
}
