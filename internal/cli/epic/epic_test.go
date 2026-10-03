package epic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type searchCall struct {
	jql, cursor string
	fields      []string
	size        int
}

type searchClient struct {
	core.JiraClient
	calls []searchCall
	read  func(context.Context, searchCall) (core.IssueSearchRead, error)
}

func (c *searchClient) SearchIssues(ctx context.Context, jql string, fields []string, cursor string, size int, _ ...string) (core.IssueSearchRead, error) {
	call := searchCall{jql: jql, fields: fields, cursor: cursor, size: size}
	c.calls = append(c.calls, call)
	return c.read(ctx, call)
}

func page(t *testing.T, raw string) core.IssueSearchRead {
	t.Helper()
	var out jira.SearchResults
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return core.IssueSearchRead{Results: &out}
}

func clientDeps(c *searchClient) Deps {
	return Deps{JiraClient: func() (core.JiraClient, error) { return c, nil }}
}

func TestEpicListPreservesSparsePagesFieldsAndFailurePrefix(t *testing.T) {
	t.Parallel()
	failure := errors.New("transport stopped")
	c := &searchClient{}
	c.read = func(_ context.Context, call searchCall) (core.IssueSearchRead, error) {
		if call.jql != epicJQL || !reflect.DeepEqual(call.fields, []string{"summary", "status"}) {
			return core.IssueSearchRead{}, fmt.Errorf("search scope/fields changed: %+v", call)
		}
		switch call.cursor {
		case "anchor":
			return page(t, `{"issues":[],"isLast":false,"nextPageToken":"next"}`), nil
		case "next":
			return page(t, `{"issues":[{"id":"1","key":"KEY-1","self":"https://example.invalid/1","fields":{"summary":null,"customfield_fixture":{"enabled":false}}}],"isLast":false,"nextPageToken":"last"}`), nil
		default:
			return core.IssueSearchRead{}, failure
		}
	}
	out, err := runList(t.Context(), clientDeps(c), options{query: epicJQL, cursor: "anchor", walk: command.SearchWalk{Limit: 2}})
	if !errors.Is(err, failure) || len(out.Epics) != 1 || out.Epics[0].Key != "KEY-1" || !out.Epics[0].Fields.Summary.Null || string(out.Epics[0].Fields.Extra["customfield_fixture"]) != `{"enabled":false}` || out.Pagination.Returned != 1 || out.Pagination.NextPageToken != "last" || out.Pagination.IsLast || out.Truncated || len(c.calls) != 3 || c.calls[2].size != 1 || out.JQL != epicJQL {
		t.Fatalf("validated search data/paging lost: %+v err=%v calls=%+v", out, err, c.calls)
	}
}

func TestEpicBoardCountsActualStatusesAndUnknowns(t *testing.T) {
	t.Parallel()
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			c := &searchClient{}
			c.read = func(_ context.Context, call searchCall) (core.IssueSearchRead, error) {
				if call.jql == epicJQL {
					return page(t, `{"issues":[{"key":"KEY-1","fields":{"summary":"One"}},{"key":"KEY-2"}],"isLast":true}`), nil
				}
				if !reflect.DeepEqual(call.fields, []string{"status"}) || call.size != command.MaxSearchPageSize {
					return core.IssueSearchRead{}, fmt.Errorf("child search fields/size changed: %+v", call)
				}
				switch call.jql {
				case `parent = "KEY-1"`:
					if call.cursor == "" {
						return page(t, `{"issues":[],"isLast":false,"nextPageToken":"children"}`), nil
					}
					return page(t, `{"issues":[{"key":"KEY-3","fields":{"status":{"name":"To Do"}}},{"key":"KEY-4","fields":{"status":{"name":"Review","statusCategory":{"name":"Done"}}}},{"key":"KEY-5","fields":{"status":{"name":"Review"}}}],"isLast":true}`), nil
				case `parent = "KEY-2"`:
					if unknown {
						return page(t, `{"issues":[{"key":"KEY-6"},{"key":"KEY-7","fields":{"status":null}},{"key":"KEY-8","fields":{"status":{"name":null}}},{"key":"KEY-9","fields":{"status":{"name":" "}}}],"isLast":true}`), nil
					}
					return page(t, `{"issues":[],"isLast":true}`), nil
				default:
					return core.IssueSearchRead{}, fmt.Errorf("unsafe or foreign child query: %q", call.jql)
				}
			}
			out, err := runBoard(t.Context(), clientDeps(c), options{query: epicJQL, walk: command.SearchWalk{Limit: 50}})
			want := map[string]int{"To Do": 1, "In Progress": 0, "Done": 0, "Review": 2}
			if err != nil || len(out.Epics) != 2 || !reflect.DeepEqual(out.Epics[0].Counts, want) || !reflect.DeepEqual(out.Totals, want) || out.Pagination.Returned != 2 || !out.Epics[0].Children.Complete || out.Epics[0].Children.Pagination.Returned != 3 || !out.Epics[1].Children.Attempted || out.Epics[1].Children.Complete == unknown || out.CountsComplete == unknown || (len(boardWarnings(out)) > 0) != unknown {
				t.Fatalf("status-name counts or completeness changed: %+v err=%v", out, err)
			}
			if unknown && (out.UnknownStatus != 4 || out.Epics[1].Children.UnknownStatus != 4 || out.Epics[1].Children.Pagination.Returned != 4) {
				t.Fatalf("unknown statuses silently discarded: %+v", out)
			}
		})
	}
}

func TestEpicBoardRetainsPartialChildCountsAndUnattemptedRows(t *testing.T) {
	t.Parallel()
	c := &searchClient{}
	failure := cli.NewCodedError(cli.CodeSearchResponseInvalid, "bad page")
	c.read = func(_ context.Context, call searchCall) (core.IssueSearchRead, error) {
		if call.jql == epicJQL {
			return page(t, `{"issues":[{"key":"KEY-1"},{"key":"KEY-2"}],"isLast":true}`), nil
		}
		if call.cursor == "" {
			return page(t, `{"issues":[{"key":"KEY-3","fields":{"status":{"name":"Done"}}}],"isLast":false,"nextPageToken":"next"}`), nil
		}
		return core.IssueSearchRead{}, failure
	}
	out, err := runBoard(t.Context(), clientDeps(c), options{query: epicJQL, walk: command.SearchWalk{Limit: 50}})
	if !errors.Is(err, failure) || len(c.calls) != 3 || out.CountsComplete || out.Totals["Done"] != 1 || out.Epics[0].Counts["Done"] != 1 || out.Epics[0].Children.Complete || out.Epics[0].Children.Pagination.NextPageToken != "next" || out.Epics[0].Children.Pagination.Returned != 1 || out.Epics[0].Children.Error == nil || out.Epics[0].Children.Error.Code != cli.CodeSearchResponseInvalid || out.Epics[1].Counts != nil || out.Epics[1].Children.Attempted {
		t.Fatalf("partial/failed/pending counts misreported: %+v err=%v calls=%d", out, err, len(c.calls))
	}
}

func TestEpicBoardChildCapsAndUnboundedCompletion(t *testing.T) {
	t.Parallel()
	for _, unbounded := range []bool{false, true} {
		t.Run(fmt.Sprint(unbounded), func(t *testing.T) {
			c := &searchClient{}
			children := 0
			c.read = func(_ context.Context, call searchCall) (core.IssueSearchRead, error) {
				if call.jql == epicJQL {
					return page(t, `{"issues":[{"key":"KEY-1"}],"isLast":true}`), nil
				}
				children++
				return page(t, fmt.Sprintf(`{"issues":[{"key":"KEY-%d","fields":{"status":{"name":"Done"}}}],"isLast":%t,"nextPageToken":"child-%d"}`, children+1, children == command.MaxSearchPages+1, children)), nil
			}
			out, err := runBoard(t.Context(), clientDeps(c), options{query: epicJQL, walk: command.SearchWalk{All: true, Unbounded: unbounded}})
			want := command.MaxSearchPages
			if unbounded {
				want++
			}
			if err != nil || children != want || out.Totals["Done"] != want || out.CountsComplete != unbounded || out.Epics[0].Children.Truncated == unbounded || out.Epics[0].Children.Complete != unbounded || (!unbounded && out.Epics[0].Children.TruncatedReason != envelope.TruncatedReasonMaxPages) {
				t.Fatalf("child bound or escape ignored: %+v err=%v requests=%d", out, err, children)
			}
		})
	}
}

func TestEpicBoardLimitedEpicSelectionIsIncomplete(t *testing.T) {
	t.Parallel()
	c := &searchClient{read: func(_ context.Context, call searchCall) (core.IssueSearchRead, error) {
		if call.jql == epicJQL {
			return page(t, `{"issues":[{"key":"KEY-1"}],"isLast":false,"nextPageToken":"more-epics"}`), nil
		}
		return page(t, `{"issues":[],"isLast":true}`), nil
	}}
	out, err := runBoard(t.Context(), clientDeps(c), options{query: epicJQL, walk: command.SearchWalk{Limit: 1}})
	if err != nil || out.CountsComplete || out.Truncated || !out.Epics[0].Children.Complete || out.Pagination.NextPageToken != "more-epics" || len(boardWarnings(out)) != 1 {
		t.Fatalf("bounded epic selection claimed exhaustive totals: %+v err=%v", out, err)
	}
}

func TestEpicBoardUnsafeParentKeyRetainsLocalDiagnostic(t *testing.T) {
	t.Parallel()
	c := &searchClient{read: func(_ context.Context, call searchCall) (core.IssueSearchRead, error) {
		if call.jql == epicJQL {
			return page(t, `{"issues":[{"key":"KEY-1"},{"key":"KEY-\n2"},{"key":"KEY-3"}],"isLast":true}`), nil
		}
		if call.jql != `parent = "KEY-1"` {
			return core.IssueSearchRead{}, fmt.Errorf("unsafe parent reached Jira: %q", call.jql)
		}
		return page(t, `{"issues":[],"isLast":true}`), nil
	}}
	out, err := runBoard(t.Context(), clientDeps(c), options{query: epicJQL, walk: command.SearchWalk{Limit: 50}})
	if cli.MapError(err).Code != cli.CodeSearchResponseInvalid || len(c.calls) != 2 || len(out.Epics) != 3 || out.CountsComplete || !out.Epics[0].Children.Complete || out.Epics[1].Counts != nil || out.Epics[1].Children.Attempted || out.Epics[1].Children.Error == nil || out.Epics[1].Children.Error.Code != cli.CodeSearchResponseInvalid || out.Epics[2].Counts != nil || out.Epics[2].Children.Attempted || out.Epics[2].Children.Error != nil {
		t.Fatalf("unsafe-key refusal lost row ownership or initiated a read: %+v err=%v", out, err)
	}
}

func TestEpicBoardCancellationStopsChildReads(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := &searchClient{read: func(_ context.Context, _ searchCall) (core.IssueSearchRead, error) {
		cancel()
		return page(t, `{"issues":[{"key":"KEY-1"}],"isLast":true}`), nil
	}}
	out, err := runBoard(ctx, clientDeps(c), options{query: epicJQL, walk: command.SearchWalk{Limit: 50}})
	if !errors.Is(err, context.Canceled) || len(c.calls) != 1 || len(out.Epics) != 1 || out.Epics[0].Children.Attempted || out.CountsComplete {
		t.Fatalf("cancellation initiated child reads: %+v err=%v", out, err)
	}
}

func TestEpicQueriesQuoteProjectsAndRejectControlsBeforeClient(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "board"} {
		t.Run(verb, func(t *testing.T) {
			for _, tc := range []struct {
				args []string
				want string
				code string
			}{
				{args: []string{"--project=KEY", `--project=A" OR project = "B`}, want: `issuetype = Epic AND project in ("KEY", "A\" OR project = \"B")`},
				{args: []string{"--project= "}, code: cli.CodeFlagValueInvalid},
				{args: []string{"--project=K\nEY"}, code: cli.CodeFlagValueInvalid},
				{args: []string{"--limit=0"}, code: cli.CodeFlagValueInvalid},
				{args: []string{"--unbounded"}, code: cli.CodeFlagConflict},
				{args: []string{"--all", "--limit=1"}, code: cli.CodeFlagConflict},
			} {
				clients := 0
				deps := Deps{JiraClient: func() (core.JiraClient, error) { clients++; return nil, errors.New("unexpected client") }}
				cmd := New(deps)
				cmd.SetArgs(append([]string{verb}, tc.args...))
				cmd.SilenceUsage, cmd.SilenceErrors = true, true
				cmd.SetOut(&strings.Builder{})
				cmd.SetErr(&strings.Builder{})
				err := cmd.ExecuteContext(t.Context())
				if tc.code != "" && (err == nil || cli.MapError(err).Code != tc.code || clients != 0) {
					t.Fatalf("invalid flags reached client: args=%q err=%v clients=%d", tc.args, err, clients)
				}
				if tc.want != "" {
					o := options{projects: []string{"KEY", `A" OR project = "B`}, walk: command.SearchWalk{Limit: 50}, cmd: cmd.Commands()[0]}
					if err := o.validate(); err != nil || o.query != tc.want {
						t.Fatalf("project broke epic scope: query=%q want=%q err=%v", o.query, tc.want, err)
					}
				}
			}
		})
	}
}
