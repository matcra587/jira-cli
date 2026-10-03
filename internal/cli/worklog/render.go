package worklog

import (
	"fmt"
	"strconv"

	"github.com/gechr/primer/table"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func renderAdd(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.WorklogAddOutput)
	if !ok {
		return fmt.Errorf("worklog.add: expected WorklogAddOutput, got %T", env.Data)
	}
	for _, r := range out.Results {
		key := cli.Entity(r.Issue.Key)
		switch {
		case !r.OK:
			v.Line(key + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
		case out.DryRun:
			v.Line("Would add a worklog to " + key + ".")
		case r.Data.ReceiptError != nil:
			v.Line(key + ": worklog created; receipt incomplete.")
			v.Hint(r.Data.ReceiptError.Hint)
		default:
			v.Line(key + ": worklog " + cli.Entity(r.Data.Worklog.ID) + " created.")
		}
	}
	if out.DryRun {
		v.Line(cli.Text("Payload: " + string(out.Payload)))
		v.Hint("Nothing was sent. Drop --dry-run to post this request.")
	}
	return nil
}

func renderList(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.WorklogListOutput)
	if !ok {
		return fmt.Errorf("worklog.list: expected WorklogListOutput, got %T", env.Data)
	}
	for _, r := range out.Results {
		v.Section(r.Issue.Key)
		if !r.OK {
			v.Line(cli.Text(r.Err.Message))
			v.Hint(r.Err.Hint)
			continue
		}
		data := r.Data
		if len(data.Worklogs) == 0 {
			if data.Complete {
				v.Line("No worklogs in this page.")
			} else {
				v.Line("No worklogs returned; the read is incomplete.")
			}
		} else {
			output.Table(v, cli.ResolvedTheme(), worklogColumns(), data.Worklogs)
		}
		v.Line(fmt.Sprintf("Returned %d; Jira reports %d total.", data.Pagination.Returned, data.Pagination.Total))
		if !data.Complete {
			message := "More worklogs remain."
			if data.TruncatedReason != "" {
				message = fmt.Sprintf("The read stopped (%s).", data.TruncatedReason)
			}
			v.Hint(fmt.Sprintf("%s Inspect pagination; resume with --start=%d.", message, data.Pagination.StartAt+data.Pagination.Returned))
		}
		if data.ReadError != nil {
			v.Hint(data.ReadError.Hint)
		}
	}
	return nil
}

func worklogColumns() []table.Column[envelope.WorklogView] {
	return []table.Column[envelope.WorklogView]{
		{Name: "id", Header: "ID", Render: func(w envelope.WorklogView, _ *table.RenderContext) table.Cell {
			return table.StyledCell(cli.Entity(w.ID), cli.Text(w.ID))
		}},
		{Name: "seconds", Header: "SECONDS", Render: func(w envelope.WorklogView, _ *table.RenderContext) table.Cell {
			s := "—"
			if w.Fields.TimeSpentSeconds.Set && !w.Fields.TimeSpentSeconds.Null {
				s = strconv.FormatInt(w.Fields.TimeSpentSeconds.Value, 10)
			}
			return table.TextCell(s)
		}},
		{Name: "author", Header: "AUTHOR", Flex: true, Render: func(w envelope.WorklogView, _ *table.RenderContext) table.Cell {
			return table.TextCell(cli.OrDash(cli.Text(w.Fields.Author.Value.DisplayName.Value)))
		}},
		{Name: "started", Header: "STARTED", Render: func(w envelope.WorklogView, _ *table.RenderContext) table.Cell {
			return table.TextCell(cli.OrDash(cli.Time(cli.Text(w.Fields.Started.Value))))
		}},
	}
}
