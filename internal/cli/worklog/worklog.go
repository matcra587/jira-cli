// Package worklog implements issue worklog reads and creation.
package worklog

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type Deps struct {
	JiraClient     func() (core.JiraClient, error)
	WorkdaySeconds func() (int, error)
	Now            func() time.Time
	Location       *time.Location
}

func New(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use: "worklog", Short: "Log and review time on issues", Args: cobra.NoArgs,
		Long:    "Read worklog records or add time to explicit issues. Integer d/h/m durations use workday_seconds; 1d defaults to eight hours. Native comments retain ADF.",
		Example: "$ jira worklog list KEY-123 --output=json\n\n# Preview logged effort\n$ jira worklog add KEY-123 --time-spent 2h30m --dry-run",
	}
	cmd.AddCommand(newList(deps), newAdd(deps))
	return cmd
}

func (d Deps) workday() (int, error) {
	if d.WorkdaySeconds != nil {
		return d.WorkdaySeconds()
	}
	return jira.DefaultWorkdaySeconds, nil
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}
