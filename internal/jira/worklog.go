package jira

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"unicode"

	"github.com/matcra587/jira-cli/internal/adf"
)

// WorklogService reads and writes the work-log entries on an issue.
type WorklogService interface {
	List(context.Context, string, *ListOptions) ([]*Worklog, *Response, error)
	Add(context.Context, string, *WorklogAddRequest) (*Worklog, *Response, error)
	Delete(context.Context, string, string) (*Response, error)
}

type worklogService struct {
	client *Client
}

// WorklogAddRequest is the body for logging work. TimeSpentSeconds is required
// and positive. DryRun is local-only (`json:"-"`) — it short-circuits Add to a
// synthetic result and never reaches Jira.
type WorklogAddRequest struct {
	TimeSpentSeconds int           `json:"timeSpentSeconds"`
	Started          string        `json:"started,omitempty"`
	Comment          *adf.Document `json:"comment,omitempty"`
	DryRun           bool          `json:"-"`
}

// NewWorklogService constructs a WorklogService bound to the given client.
func NewWorklogService(client *Client) WorklogService {
	return &worklogService{client: client}
}

// List returns the issue's worklog entries. Unlike enhanced search, this
// endpoint reports its own offset paging fields, so the Response is stamped with
// a known Total and an IsLast computed from startAt + len versus total.
func (s *worklogService) List(ctx context.Context, issueKey string, opts *ListOptions) ([]*Worklog, *Response, error) {
	path := RESTPath("issue", issueKey, "worklog")
	if opts != nil {
		path = withQuery(path, opts.QueryValues())
	}
	req, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}
	var result struct {
		Worklogs   []*Worklog `json:"worklogs"`
		StartAt    int        `json:"startAt"`
		MaxResults int        `json:"maxResults"`
		Total      int        `json:"total"`
	}
	resp, err := s.client.Do(req, &result)
	if resp != nil {
		resp.StartAt = result.StartAt
		resp.MaxResults = result.MaxResults
		resp.Total = result.Total
		resp.TotalKnown = true
		resp.IsLast = result.StartAt+len(result.Worklogs) >= result.Total
	}
	return result.Worklogs, resp, err
}

// Add logs work against the issue. Under DryRun it returns a synthetic worklog
// without contacting Jira, so a preview path renders a plausible result while
// honoring the local-only contract of --dry-run.
func (s *worklogService) Add(ctx context.Context, issueKey string, reqBody *WorklogAddRequest) (*Worklog, *Response, error) {
	if reqBody == nil || reqBody.TimeSpentSeconds <= 0 {
		return nil, nil, errors.New("positive time spent is required")
	}
	if reqBody.DryRun {
		return &Worklog{ID: new("DRY-RUN"), TimeSpentSeconds: new(reqBody.TimeSpentSeconds)}, &Response{IsLast: true}, nil
	}
	req, err := s.client.NewRequest(ctx, http.MethodPost, RESTPath("issue", issueKey, "worklog"), reqBody)
	if err != nil {
		return nil, nil, err
	}
	var worklog Worklog
	resp, err := s.client.Do(req, &worklog)
	return &worklog, resp, err
}

// Delete removes a single worklog entry by id.
func (s *worklogService) Delete(ctx context.Context, issueKey, worklogID string) (*Response, error) {
	req, err := s.client.NewRequest(ctx, http.MethodDelete, RESTPath("issue", issueKey, "worklog", worklogID), nil)
	if err != nil {
		return nil, err
	}
	return s.client.Do(req, nil)
}

// ParseDuration converts a Jira time expression ("2h", "1d 4h", "30m") to
// seconds. It is deliberately not x/human.ParseDuration: Jira's "d" is a
// configurable work day (workdaySeconds, defaulting to DefaultWorkdaySeconds),
// not a fixed 24 hours, so a shared parser would compute the wrong total for
// day units. Only d/h/m are accepted; the value must be positive.
func ParseDuration(input string, workdaySeconds int) (int, error) {
	if workdaySeconds <= 0 {
		workdaySeconds = DefaultWorkdaySeconds
	}
	total := 0
	i := 0
	for i < len(input) {
		if input[i] == '-' {
			return 0, errors.New("duration must be positive")
		}
		start := i
		for i < len(input) && unicode.IsDigit(rune(input[i])) {
			i++
		}
		if start == i || i >= len(input) {
			return 0, errors.New("invalid duration")
		}
		n, err := strconv.Atoi(input[start:i])
		if err != nil {
			return 0, errors.New("invalid duration")
		}
		unit := input[i]
		i++
		switch unit {
		case 'd':
			total += n * workdaySeconds
		case 'h':
			total += n * 3600
		case 'm':
			total += n * 60
		default:
			return 0, errors.New("unsupported duration unit")
		}
	}
	if total <= 0 {
		return 0, errors.New("duration must be positive")
	}
	return total, nil
}

// DefaultWorkdaySeconds is Jira's default work day (8 hours), the fallback
// ParseDuration uses to expand day units when no instance-configured length is
// supplied.
const DefaultWorkdaySeconds = 28800
