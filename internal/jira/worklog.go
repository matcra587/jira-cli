package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	xstrings "github.com/gechr/x/strings"
)

// DefaultWorkdaySeconds preserves the CLI's eight-hour Jira workday.
const DefaultWorkdaySeconds = int(8 * time.Hour / time.Second)

// ParseWorklogDuration converts integer d/h/m components into seconds.
// Unlike x/human.ParseDuration, d means a configured workday rather than
// 24 hours. Components may be adjacent or separated by spaces; arithmetic
// is checked before multiplication or addition. Nonpositive workdaySeconds
// selects the historical eight-hour default.
func ParseWorklogDuration(input string, workdaySeconds int) (int, error) {
	if workdaySeconds <= 0 {
		workdaySeconds = DefaultWorkdaySeconds
	}
	rest := strings.TrimSpace(input)
	if rest == "" {
		return 0, errors.New("time spent is required")
	}
	var seconds int
	for rest != "" {
		end := 0
		for end < len(rest) && xstrings.IsDigitChar(rune(rest[end])) {
			end++
		}
		if end == 0 || end == len(rest) {
			return 0, fmt.Errorf("time spent %q must use integer d, h or m components, such as 1d2h30m", input)
		}
		n, err := strconv.Atoi(rest[:end])
		if err != nil {
			return 0, fmt.Errorf("time spent %q is too large: %w", input, err)
		}
		var unit int
		switch rest[end] {
		case 'd':
			unit = workdaySeconds
		case 'h':
			unit = int(time.Hour / time.Second)
		case 'm':
			unit = int(time.Minute / time.Second)
		default:
			return 0, fmt.Errorf("time spent %q must use d, h or m units", input)
		}
		if n > (math.MaxInt-seconds)/unit {
			return 0, fmt.Errorf("time spent %q is too large", input)
		}
		seconds += n * unit
		rest = strings.TrimSpace(rest[end+1:])
	}
	if seconds <= 0 {
		return 0, errors.New("time spent must be positive")
	}
	return seconds, nil
}

// AddWorklogRequest contains caller-owned worklog members. A received Worklog
// also carries server-owned IDs, authors and timestamps, so it must never be
// reused as a POST body. Comment holds submission-ready ADF bytes.
type AddWorklogRequest struct {
	TimeSpentSeconds int             `json:"timeSpentSeconds"`
	Started          string          `json:"started,omitempty"`
	Comment          json.RawMessage `json:"comment,omitempty"`
	// Extra preserves caller-owned native properties, visibility and future
	// request members. The CLI removes its convenience input members first.
	Extra map[string]json.RawMessage `json:"-"`
}

func (v AddWorklogRequest) MarshalJSON() ([]byte, error) {
	type alias AddWorklogRequest
	return marshalMergeExtra(alias(v), v.Extra, []string{"timeSpentSeconds", "started", "comment"})
}

// ValidateIdentity refuses a missing identity rather than manufacturing a
// successful worklog receipt or an empty record in a list.
func (v Worklog) ValidateIdentity() error {
	if !v.ID.Set || v.ID.Null || v.ID.Value == "" || strings.TrimSpace(v.ID.Value) != v.ID.Value {
		return errors.New("worklog identity is missing, null or blank")
	}
	return nil
}

// ValidatePage checks the requested offset and returned facts before any row
// is exposed. An empty page beyond total is valid; an empty page before total
// remains inspectable so the walk can report lack of progress honestly.
func (v PageOfWorklogs) ValidatePage(startAt, maxResults int) error {
	if startAt < 0 || maxResults < 1 {
		return errors.New("worklog page request requires a nonnegative offset and positive size")
	}
	if !v.StartAt.Set || v.StartAt.Null || v.StartAt.Value != int64(startAt) {
		return errors.New("worklog page startAt does not match the requested offset")
	}
	if !v.MaxResults.Set || v.MaxResults.Null || v.MaxResults.Value < 0 {
		return errors.New("worklog page maxResults is missing, null or negative")
	}
	if !v.Total.Set || v.Total.Null || v.Total.Value < 0 {
		return errors.New("worklog page total is missing, null or negative")
	}
	if !v.Worklogs.Set || v.Worklogs.Null {
		return errors.New("worklog page worklogs must be an explicit array")
	}
	count := int64(len(v.Worklogs.Value))
	if count > int64(maxResults) || count > v.MaxResults.Value {
		return errors.New("worklog page exceeds the requested or declared size")
	}
	if count > 0 && (v.Total.Value < v.StartAt.Value || count > v.Total.Value-v.StartAt.Value) {
		return errors.New("worklog page rows exceed the declared total")
	}
	seen := make(map[string]struct{}, len(v.Worklogs.Value))
	for _, worklog := range v.Worklogs.Value {
		if err := worklog.ValidateIdentity(); err != nil {
			return err
		}
		if _, exists := seen[worklog.ID.Value]; exists {
			return fmt.Errorf("worklog page repeats identity %q", worklog.ID.Value)
		}
		seen[worklog.ID.Value] = struct{}{}
	}
	return nil
}
