package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gechr/clog"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/boardscope"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestDefaultBoardParserPreservesScopeAndUsesOnlyPublicCache(t *testing.T) {
	t.Parallel()
	const site = "https://example.invalid"
	const boardQuery = "project = BOARD OR labels = scope ORDER BY Rank ASC"
	for _, test := range []struct {
		name  string
		mine  bool
		args  []string
		want  string
		board bool
	}{
		{name: "list default board", args: []string{"--as-jql"}, want: "(project = BOARD OR labels = scope) ORDER BY Rank ASC", board: true},
		{name: "mine board ordering", mine: true, args: []string{"--as-jql"}, want: "(project = BOARD OR labels = scope) AND assignee = currentUser() ORDER BY Rank ASC", board: true},
		{name: "raw overrides board", args: []string{"--as-jql", "--jql=status = Done OR labels = urgent ORDER BY created DESC"}, want: "status = Done OR labels = urgent ORDER BY created DESC"},
		{name: "mine raw overrides board", mine: true, args: []string{"--as-jql", "--jql=status = Done OR labels = urgent ORDER BY created DESC"}, want: "assignee = currentUser() AND (status = Done OR labels = urgent) ORDER BY created DESC"},
		{name: "empty board selects project", args: []string{"--as-jql", "--board="}, want: `project in ("DEFAULT")`},
		{name: "explicit project narrows board", args: []string{"--as-jql", "--project=FLAGGED"}, want: `(project = BOARD OR labels = scope) AND project in ("FLAGGED") ORDER BY Rank ASC`, board: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &adapters.MemoryCache{}
			raw, _ := json.Marshal(boardscope.Resolution{ID: 42, Name: "Sprint", JQL: boardQuery})
			if err := store.Set(boardscope.CacheKey(site, "name", "Sprint"), raw); err != nil {
				t.Fatal(err)
			}
			lookups := 0
			deps := Deps{Preferences: func() (core.IssuePreferences, error) {
				return core.IssuePreferences{BaseURL: site, Account: "public@example.invalid", DefaultProject: "DEFAULT", DefaultIssueType: "Bug", DefaultBoard: "Sprint"}, nil
			}, CacheFor: func(actual, account string) (core.Cache, error) {
				lookups++
				if actual != site || account != "public@example.invalid" {
					t.Fatal("wrong public namespace")
				}
				return store, nil
			}, Cache: func(context.Context) (core.Cache, error) {
				t.Fatal("default opened credential-backed cache")
				return nil, nil
			}, JiraClient: func(context.Context) (core.JiraClient, error) {
				t.Fatal("default preview opened remote client")
				return nil, nil
			}}
			cmd := newList(deps)
			if test.mine {
				cmd = newMine(deps)
			}
			if err := cmd.ParseFlags(test.args); err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stdout)
			cmd.SetContext(cli.WithFormat(t.Context(), output.FormatJSON))
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatal(err)
			}
			var frame struct {
				OK     bool             `json:"ok"`
				Errors []envelope.Error `json:"errors"`
				Data   struct {
					JQL   string `json:"jql"`
					Board *struct {
						Precedence string `json:"precedence"`
						CachedAt   string `json:"cached_at"`
					} `json:"board"`
				} `json:"data"`
			}
			if json.Unmarshal(stdout.Bytes(), &frame) != nil || !frame.OK || len(frame.Errors) != 0 || frame.Data.JQL != test.want {
				t.Fatalf("default scope/order changed: %s", &stdout)
			}
			if test.board && (lookups != 1 || frame.Data.Board == nil || frame.Data.Board.Precedence != "default_board" || frame.Data.Board.CachedAt == "") {
				t.Fatal("default snapshot provenance missing")
			}
			if !test.board && (lookups != 0 || frame.Data.Board != nil) {
				t.Fatal("overridden board caused a lookup or retained provenance")
			}
		})
	}
}

func TestDefaultBoardMissingOrCanceledCannotBroadenAQuery(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"missing", "canceled", "zero ID", "competing selectors", "reverse without order"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			deps := Deps{Preferences: func() (core.IssuePreferences, error) {
				return core.IssuePreferences{BaseURL: "https://example.invalid", Account: "public@example.invalid", DefaultBoard: "Sprint"}, nil
			}, CacheFor: func(string, string) (core.Cache, error) {
				calls++
				if mode == "canceled" {
					cancel()
				}
				return &adapters.MemoryCache{}, nil
			}, JiraClient: func(context.Context) (core.JiraClient, error) {
				t.Fatal("missing/default scope reached remote client")
				return nil, nil
			}}
			cmd := newList(deps)
			args := []string{"--as-jql"}
			if mode == "zero ID" {
				args = append(args, "--board-id=0")
			}
			if mode == "competing selectors" {
				args = append(args, "--board=", "--board-id=42")
			}
			if mode == "reverse without order" {
				cmd = newMine(deps)
				args = append(args, "--reverse")
			}
			if err := cmd.ParseFlags(args); err != nil {
				t.Fatal(err)
			}
			cmd.SetContext(ctx)
			err := cmd.RunE(cmd, nil)
			if err == nil {
				t.Fatal("failed scope ran an unscoped query")
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cache cancellation was lost", err)
			}
			if mode == "missing" && cli.MapError(err).Code != cli.CodeBoardCacheUnavailable {
				t.Fatal("missing scope misclassified", err)
			}
			if (mode == "zero ID" || mode == "competing selectors" || mode == "reverse without order") && calls != 0 {
				t.Fatal("invalid flags opened cached scope")
			}
		})
	}
}

func TestCreateMetadataContextDefaultsResolveBeforeClient(t *testing.T) {
	t.Parallel()
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured", true: "flag override"}[explicit], func(t *testing.T) {
			t.Parallel()
			project, typeName := "DEFAULT", "Task"
			if explicit {
				project, typeName = "FLAGGED", "Bug"
			}
			reads := 0
			client := &inputMetadataClient{project: func(_ context.Context, requested string) (core.IssueCreateProjectRead, error) {
				reads++
				if requested != project {
					t.Fatal("project context ignored override/default")
				}
				return core.IssueCreateProjectRead{Native: []byte(`{"id":"10402","key":"` + project + `"}`)}, nil
			}, types: func(_ context.Context, actual string, offset, size int) (core.IssueMetadataRead, error) {
				reads++
				if actual != "10402" {
					t.Fatal("type lookup used unresolved project")
				}
				return inputPage("issueTypes", 0, 1, `{"id":"10002","name":"`+typeName+`"}`), nil
			}, fields: func(_ context.Context, actual, issueType string, offset, size int) (core.IssueMetadataRead, error) {
				reads++
				if actual != "10402" || issueType != "10002" {
					t.Fatal("field lookup used unresolved context")
				}
				return inputPage("fields", 0, 0), nil
			}}
			deps := inputMetadataDeps(client)
			deps.Preferences = func() (core.IssuePreferences, error) {
				return core.IssuePreferences{DefaultProject: "DEFAULT", DefaultIssueType: "Task"}, nil
			}
			cmd := newCreate(deps)
			args := []string{"--generate-json"}
			if explicit {
				args = append(args, "--project="+project, "--type="+typeName)
			}
			if err := cmd.ParseFlags(args); err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stdout)
			cmd.SetContext(cli.WithFormat(t.Context(), output.FormatJSON))
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatal(err)
			}
			if reads != 3 || !strings.Contains(stdout.String(), `"complete":true`) || !strings.Contains(stdout.String(), `"10002"`) {
				t.Fatal("default metadata context was not verified through native reads")
			}
		})
	}
}

type defaultScopedListClient struct {
	*fakeListClient
	account string
}

func (c *defaultScopedListClient) Account() string { return c.account }

func TestDefaultBoardLiveBindingRefusesForeignSiteAccountAndUnknownIdentity(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"foreign site", "foreign account", "unknown account"} {
		for _, branch := range []string{"search", "count", "web"} {
			t.Run(mode+"/"+branch, func(t *testing.T) {
				t.Parallel()
				store := &adapters.MemoryCache{}
				if err := store.Set("v2:board:https://example.invalid:name:Sprint", []byte(`{"id":42,"name":"Sprint","jql":"project = BOARD"}`)); err != nil {
					t.Fatal(err)
				}
				base := &fakeListClient{baseURL: "https://example.invalid", pages: onePage(&jira.SearchResults{IsLast: jira.NullableOf(true), Issues: jira.NullableOf([]jira.IssueBean{})})}
				client := core.JiraClient(&defaultScopedListClient{fakeListClient: base, account: "public@example.invalid"})
				if mode == "foreign site" {
					base.baseURL = "https://foreign.invalid"
				}
				if mode == "foreign account" {
					client = &defaultScopedListClient{fakeListClient: base, account: "foreign@example.invalid"}
				}
				if mode == "unknown account" {
					client = base
				}
				deps := Deps{Preferences: func() (core.IssuePreferences, error) {
					return core.IssuePreferences{BaseURL: "https://example.invalid", Account: "public@example.invalid", DefaultBoard: "Sprint"}, nil
				}, CacheFor: func(string, string) (core.Cache, error) { return store, nil }, JiraClient: func(context.Context) (core.JiraClient, error) { return client, nil }}
				cmd, _, findErr := New(deps).Find([]string{"list"})
				if findErr != nil {
					t.Fatal(findErr)
				}
				if branch != "search" {
					if err := cmd.ParseFlags([]string{"--" + branch}); err != nil {
						t.Fatal(err)
					}
				}
				var stdout bytes.Buffer
				cmd.SetOut(&stdout)
				cmd.SetContext(cli.WithFormat(t.Context(), output.FormatJSON))
				err := cmd.RunE(cmd, nil)
				if err == nil || cli.MapError(err).Code != cli.CodeConfigInvalid || len(base.calls) != 0 || len(base.countCalls) != 0 || stdout.Len() != 0 {
					t.Fatal("cached public scope reached a differently bound live route", mode, branch, err)
				}
			})
		}
	}
}

func TestDefaultBoardMatchingLiveBindingRetainsProvenanceAndSingleClient(t *testing.T) {
	t.Parallel()
	for _, branch := range []string{"search", "count", "web", "canceled factory"} {
		t.Run(branch, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store := &adapters.MemoryCache{}
			if err := store.Set("v2:board:https://example.invalid:name:Sprint", []byte(`{"id":42,"name":"Sprint","jql":"project = BOARD"}`)); err != nil {
				t.Fatal(err)
			}
			base := &fakeListClient{baseURL: "https://EXAMPLE.invalid/", pages: onePage(&jira.SearchResults{IsLast: jira.NullableOf(true), Issues: jira.NullableOf([]jira.IssueBean{})}), countRead: core.IssueCountRead{Count: 7}}
			client := &defaultScopedListClient{fakeListClient: base, account: "PUBLIC@example.invalid"}
			constructs := 0
			deps := Deps{Preferences: func() (core.IssuePreferences, error) {
				return core.IssuePreferences{BaseURL: "https://example.invalid", Account: "public@example.invalid", DefaultBoard: "Sprint"}, nil
			}, CacheFor: func(string, string) (core.Cache, error) { return store, nil }, JiraClient: func(context.Context) (core.JiraClient, error) {
				constructs++
				if branch == "canceled factory" {
					cancel()
				}
				return client, nil
			}}
			cmd, _, err := New(deps).Find([]string{"list"})
			if err != nil {
				t.Fatal(err)
			}
			if branch == "count" || branch == "web" {
				if err := cmd.ParseFlags([]string{"--" + branch}); err != nil {
					t.Fatal(err)
				}
			}
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetContext(cli.WithFormat(ctx, output.FormatJSON))
			err = cmd.RunE(cmd, nil)
			if branch == "canceled factory" {
				if !errors.Is(err, context.Canceled) || len(base.calls) != 0 || len(base.countCalls) != 0 || stdout.Len() != 0 {
					t.Fatal("binding factory cancellation entered a live route", err)
				}
				return
			}
			var frame struct {
				OK   bool                     `json:"ok"`
				Data envelope.IssueListOutput `json:"data"`
			}
			if err != nil || constructs != 1 || json.Unmarshal(stdout.Bytes(), &frame) != nil || !frame.OK || frame.Data.Board == nil || frame.Data.Board.Precedence != "default_board" || frame.Data.Board.CachedAt == nil || frame.Data.Board.JQL != "project = BOARD" {
				t.Fatal("matching scope lost provenance or reconstructed its checked client", branch, err, stdout.String())
			}
			if branch == "search" && len(base.calls) != 1 || branch == "count" && (len(base.countCalls) != 1 || frame.Data.Count == nil || frame.Data.Count.Value != 7) || branch == "web" && (frame.Data.WebURL == "" || len(base.calls) != 0 || len(base.countCalls) != 0) {
				t.Fatal("matching scope entered the wrong live branch", branch)
			}
		})
	}
}

func TestDefaultBoardHumanWebRetainsSingleURLLine(t *testing.T) {
	stamp := time.Unix(1700000000, 0).UTC()
	const link = "https://example.invalid/issues/?jql=project+%3D+BOARD"
	out := envelope.IssueListOutput{WebLink: envelope.WebLink{WebURL: link}, Board: &envelope.IssueBoardView{ID: 42, Name: "Sprint", JQL: "project = BOARD", Precedence: "default_board", CachedAt: &stamp}}
	if got := renderIssueList(t, out, clog.ColorNever, "", countEstimate{}); got != "Open in browser: "+link+"\n" {
		t.Fatal("configured board metadata changed the existing single browser URL line", got)
	}
}

func TestDefaultBoardHumanCountRetainsSingleResultLine(t *testing.T) {
	stamp := time.Unix(1700000000, 0).UTC()
	out := envelope.IssueListOutput{Count: &envelope.IssueCountEstimate{Value: 7, Approximate: true}, Board: &envelope.IssueBoardView{ID: 42, Name: "Sprint", JQL: "project = BOARD", Precedence: "default_board", CachedAt: &stamp}}
	if got := renderIssueList(t, out, clog.ColorNever, "", countEstimate{}); got != "~7 issues match.\n" {
		t.Fatal("configured board metadata changed the existing single count result line", got)
	}
}
