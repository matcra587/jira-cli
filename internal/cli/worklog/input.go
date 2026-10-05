package worklog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/jira"
)

type inputFlags struct {
	body                          command.BodyFlags
	jsonInput, timeSpent, started string
	timeSet, startedSet           bool
	cmd                           *cobra.Command
}

func payloadAliases() []command.HiddenAlias {
	return append(command.ADFRouteAliases(), command.HiddenAlias{Flag: "comment-markdown", Canonical: command.FlagMarkdown})
}

func (f *inputFlags) register(cmd *cobra.Command) {
	f.cmd = cmd
	command.AddBodyFlags(cmd, &f.body, command.WithoutADFRoute())
	command.AddStringVar(cmd, &f.jsonInput, command.FlagJSONInput, "", "Read a whole native worklog request or legacy time_spent/comment_markdown object; - reads standard input", command.FlagOpts{Terse: "Worklog payload", Placeholder: "path"})
	command.AddStringVar(cmd, &f.timeSpent, "time-spent", "", "Integer effort in d/h/m units; replaces effort from JSON input", command.FlagOpts{Terse: "Logged effort", Placeholder: "duration"})
	command.AddStringVar(cmd, &f.started, "started", "", "ISO date/time or now, yesterday, duration ago; replaces the JSON start time", command.FlagOpts{Terse: "Start time", Placeholder: "time"})
	command.AddHiddenAliases(cmd, payloadAliases())
}

func (f *inputFlags) validate() error {
	if err := f.body.ResolveAliases(f.cmd.Flags()); err != nil {
		return err
	}
	if err := command.ResolveHiddenAliases(f.cmd.Flags(), payloadAliases()); err != nil {
		return err
	}
	if err := command.ValidateJSONInput(f.cmd.Flags()); err != nil {
		return err
	}
	if err := f.body.Conflict(); err != nil {
		return err
	}
	if f.jsonInput != "" && f.body.Given() {
		return cli.NewInputError(cli.CodeFlagConflict, "--json-input conflicts with --markdown and --markdown-file; choose one comment route")
	}
	f.timeSet = f.cmd.Flags().Changed("time-spent")
	f.startedSet = f.cmd.Flags().Changed("started")
	return nil
}

func inputError(field string, err error) error {
	e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf("--%s: %v", field, err))
	e.Field = field
	return e
}

var worklogInputMembers = [...]string{"timeSpentSeconds", "timeSpent", "time_spent", "started", "comment", "comment_markdown"}

func stringMember(fields map[string]json.RawMessage, key string) (string, error) {
	var value jira.Nullable[string]
	if err := json.Unmarshal(fields[key], &value); err != nil {
		return "", err
	}
	if value.Null {
		return "", fmt.Errorf("%s must be a string, not null", key)
	}
	return value.Value, nil
}

func (f *inputFlags) compose(ctx context.Context, deps Deps, inv command.Invocation, day int) (jira.AddWorklogRequest, string, error) {
	req := jira.AddWorklogRequest{}
	members := make(map[string]json.RawMessage)
	if f.jsonInput != "" {
		data, err := command.ReadInputFileContext(ctx, command.FlagJSONInput, f.jsonInput, inv.Stdin, inv.StdinIsTerminal)
		if err != nil {
			return req, "", err
		}
		members, err = jira.DecodeNativeObject(data, worklogInputMembers[:])
		if err != nil {
			return req, "", inputError(command.FlagJSONInput, err)
		}
	}
	effort := f.timeSpent
	if !f.timeSet {
		count := 0
		for _, key := range []string{"timeSpentSeconds", "timeSpent", "time_spent"} {
			if _, ok := members[key]; !ok {
				continue
			}
			count++
			if key == "timeSpentSeconds" {
				var seconds jira.Nullable[int]
				if err := json.Unmarshal(members[key], &seconds); err != nil || seconds.Null {
					return req, "", inputError(command.FlagJSONInput, errors.New("timeSpentSeconds must be an integer, not null"))
				}
				req.TimeSpentSeconds = seconds.Value
			} else {
				value, err := stringMember(members, key)
				if err != nil {
					return req, "", inputError(command.FlagJSONInput, err)
				}
				effort = value
			}
		}
		if count > 1 {
			return req, "", inputError(command.FlagJSONInput, errors.New("choose one of timeSpentSeconds, timeSpent or time_spent"))
		}
	}
	if f.timeSet || effort != "" {
		seconds, err := jira.ParseWorklogDuration(effort, day)
		if err != nil {
			return req, "", inputError("time-spent", err)
		}
		req.TimeSpentSeconds = seconds
	}
	if req.TimeSpentSeconds <= 0 {
		return req, "", inputError("time-spent", errors.New("positive time spent is required; provide --time-spent or JSON effort"))
	}
	started := f.started
	if !f.startedSet {
		if _, ok := members["started"]; ok {
			value, err := stringMember(members, "started")
			if err != nil {
				return req, "", inputError(command.FlagJSONInput, err)
			}
			started = value
		} else {
			started = "now"
		}
	}
	var err error
	req.Started, err = jira.ParseWorklogStarted(started, deps.now(), deps.Location)
	if err != nil {
		return req, "", inputError("started", err)
	}
	route := ""
	comment, hasComment := members["comment"]
	_, hasMarkdown := members["comment_markdown"]
	if hasComment && hasMarkdown {
		return req, "", inputError(command.FlagJSONInput, errors.New("comment and comment_markdown conflict"))
	}
	if hasComment {
		if !bytes.Equal(bytes.TrimSpace(comment), []byte("null")) {
			if err := adf.Validate(comment); err != nil {
				return req, "", inputError(command.FlagJSONInput, fmt.Errorf("comment must be ADF: %w", err))
			}
		}
		req.Comment = comment
		route = command.RouteADF
	} else if hasMarkdown {
		value, err := stringMember(members, "comment_markdown")
		if err != nil {
			return req, "", inputError(command.FlagJSONInput, err)
		}
		body, err := command.ConvertMarkdown(command.FlagJSONInput, value)
		if err != nil {
			return req, "", err
		}
		req.Comment = body.ADF
		route = body.Route
	} else {
		body, err := f.body.Resolve(ctx, inv.Stdin, inv.StdinIsTerminal)
		if err != nil {
			return req, "", err
		}
		req.Comment = body.ADF
		route = body.Route
	}
	for _, key := range worklogInputMembers {
		delete(members, key)
	}
	req.Extra = members
	return req, route, nil
}
