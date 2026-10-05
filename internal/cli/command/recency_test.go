package command_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/spf13/cobra"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestIssueRecencyObservesTypedRequestsAndJoinedResultsBeforeRendering(t *testing.T) {
	t.Parallel()
	original := cli.NewCodedError("jira_unauthorized", "original read failure")
	for _, tc := range []struct {
		name                                                  string
		dryRun, invalid, failedWrite, failedRead, failedInput bool
	}{
		{name: "success"},
		{name: "preview", dryRun: true},
		{name: "preflight", invalid: true},
		{name: "failed read", failedRead: true},
		{name: "handler validation", failedInput: true},
		{name: "output failure", failedWrite: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var recorded [][]string
			ctx := command.WithIssueKeyRecorder(cli.WithFormat(t.Context(), output.FormatJSON), func(keys []string) {
				recorded = append(recorded, slices.Clone(keys))
			})
			var preview bool
			cmd := command.MustNew(command.Spec[envelope.IssueViewOutput]{
				Op: "issue.view", Use: "fixture ISSUE_KEY", Short: "Read a fixture",
				Args: command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
				Flags: func(c *cobra.Command) {
					command.AddBoolVar(c, &preview, cli.FlagDryRun, false, "Preview", command.FlagOpts{})
				},
				Validate: func() error {
					if tc.invalid {
						return original
					}
					return nil
				},
				Run: func(context.Context, command.Invocation) (envelope.IssueViewOutput, error) {
					if tc.failedInput {
						return envelope.IssueViewOutput{}, cli.NewInputError(cli.CodeArgValueInvalid, "invalid native input")
					}
					out := envelope.IssueViewOutput{Issue: envelope.IssueView{IssueRef: envelope.IssueRef{Key: "DEMO-9"}}}
					if tc.failedRead {
						return out, original
					}
					return out, nil
				},
			})
			var stdout bytes.Buffer
			cmd.SetContext(ctx)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stdout)
			if tc.failedWrite {
				cmd.SetOut(brokenWriter{})
			}
			args := []string{"DEMO-1:3"}
			if tc.dryRun {
				args = append(args, "--dry-run")
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if tc.failedInput {
				if cli.MapError(err).Code != cli.CodeArgValueInvalid {
					t.Fatalf("handler input error replaced: %v", err)
				}
			} else if tc.invalid || tc.failedRead {
				if !errors.Is(err, original) {
					t.Fatalf("original cause replaced: %v", err)
				}
			} else if tc.failedWrite {
				if err == nil {
					t.Fatal("broken writer succeeded")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.dryRun || tc.invalid || tc.failedInput {
				if len(recorded) != 0 {
					t.Fatalf("preview/preflight observed keys: %v", recorded)
				}
			} else if len(recorded) != 1 || !slices.Equal(recorded[0], []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-9"}) {
				t.Fatalf("observed=%v, want one joined observation with expanded requests and actual result", recorded)
			}
		})
	}
}

func TestIssueRecencyJoinedResponseSurvivesSuccessfulRow(t *testing.T) {
	t.Parallel()
	response := &adapterjira.APIError{StatusCode: http.StatusBadRequest}
	rows := envelope.KeyedResults[envelope.IssueTransitionData]{Results: []envelope.KeyedResult[envelope.IssueTransitionData]{
		{Issue: envelope.IssueRef{Key: "DEMO-1"}, OK: true, Data: &envelope.IssueTransitionData{}},
		{Issue: envelope.IssueRef{Key: "DEMO-2"}, Err: &envelope.KeyError{Code: "jira_bad_request", Message: response.Error()}},
	}}
	if err := command.KeyedFailure(rows.Results, []error{nil, response}); err != nil {
		t.Fatalf("response observation changed partial-success policy: %v", err)
	}
	if !rows.HasJiraResponse() {
		t.Fatal("successful first row discarded the later joined native response")
	}
	raw, err := json.Marshal(rows)
	if err != nil || bytes.Contains(raw, []byte("JiraResponse")) || bytes.Contains(raw, []byte("jira_response")) {
		t.Fatalf("internal joined response fact leaked into public output: err=%v output=%s", err, raw)
	}
}

func TestIssueRecencyRetainsEveryJoinedNativeResponseBeforeChoosingFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		nativeIndex int
	}{
		{name: "local first", nativeIndex: 1},
		{name: "native first", nativeIndex: 0},
		{name: "local only", nativeIndex: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			keys := []string{"DEMO-1", "DEMO-2"}
			var recorded []string
			ctx := command.WithIssueKeyRecorder(cli.WithFormat(t.Context(), output.FormatJSON), func(keys []string) {
				recorded = slices.Clone(keys)
			})
			raw := []error{cli.NewInputError(cli.CodeTransitionScreenRequired, "screen required"), cli.NewInputError(cli.CodeTransitionScreenRequired, "screen required")}
			if tc.nativeIndex >= 0 {
				raw[tc.nativeIndex] = &adapterjira.APIError{StatusCode: http.StatusBadRequest}
			}
			cmd := command.MustNew(command.Spec[envelope.IssueTransitionOutput]{
				Op: "issue.transition", Use: "fixture ISSUE_KEY ISSUE_KEY", Short: "Join fixture outcomes",
				Args: command.ArgSpec{Min: 2, Max: 2, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}, {Name: "ISSUE_KEY", Predictor: "issuekey"}}},
				Run: func(context.Context, command.Invocation) (envelope.IssueTransitionOutput, error) {
					var out envelope.IssueTransitionOutput
					for i, err := range raw {
						class := cli.MapError(err)
						out.Results = append(out.Results, envelope.KeyedResult[envelope.IssueTransitionData]{Issue: envelope.IssueRef{Key: keys[i]}, Err: &envelope.KeyError{Code: class.Code, Message: class.Message, Hint: class.Hint}})
					}
					return out, command.KeyedFailure(out.Results, raw)
				},
			})
			var stdout bytes.Buffer
			cmd.SetContext(ctx)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stdout)
			cmd.SetArgs(keys)
			err := cmd.Execute()
			if !errors.Is(err, raw[0]) || cli.MapError(err).Code != cli.MapError(raw[0]).Code {
				t.Fatalf("joined observation changed the original winning error: %v", err)
			}
			if tc.nativeIndex >= 0 && !slices.Equal(recorded, []string{"DEMO-1", "DEMO-2", "DEMO-1", "DEMO-2"}) || tc.nativeIndex < 0 && len(recorded) != 0 {
				t.Fatalf("joined native observation depends on the selected error: recorded=%v native index=%d", recorded, tc.nativeIndex)
			}
		})
	}
}
