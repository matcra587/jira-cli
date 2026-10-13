package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/jira"
)

const descriptionSource = `{"type":"doc","version":1,"future":{"large":9007199254740993,"spelling":"\u003c\/"},"content":[{"type":"paragraph","content":[{"type":"text","text":"alpha"}]},{"type":"futureNode","attrs":{"raw":"<>&"}}]}`

type descriptionClient struct {
	site, account, apiBaseURL string
	calls                     int
	read                      func(context.Context, string, []string) (IssueRead, error)
}

func (c *descriptionClient) JiraScope() JiraScope {
	route := c.apiBaseURL
	if route == "" {
		route = c.site + "/"
	}
	return JiraScope{Site: c.site, Account: c.account, APIBaseURL: route}
}

func (c *descriptionClient) BaseURL() string { return c.site }
func (c *descriptionClient) Account() string { return c.account }
func (c *descriptionClient) GetIssue(ctx context.Context, key string, fields []string, expand ...string) (IssueRead, error) {
	c.calls++
	return c.read(ctx, key, fields)
}

func descriptionIssue(t *testing.T, description string) *jira.IssueBean {
	t.Helper()
	var issue jira.IssueBean
	if err := json.Unmarshal([]byte(`{"id":"123456","key":"KEY-1","fields":{"updated":"2026-10-13T12:00:00.000+0000","description":`+description+`}}`), &issue); err != nil {
		t.Fatal(err)
	}
	return &issue
}

func descriptionReader(t *testing.T, issue *jira.IssueBean) *descriptionClient {
	t.Helper()
	return &descriptionClient{site: "https://jira.example.test", account: "reader@example.test", read: func(_ context.Context, _ string, fields []string) (IssueRead, error) {
		if !slices.Equal(fields, []string{"description", "updated"}) {
			t.Fatal("editing did not request explicit native description/updated fields", fields)
		}
		return IssueRead{Issue: issue, Retry: RetryInfo{Attempts: 2}}, nil
	}}
}

func TestIssueDescriptionPreparationPreservesNativeBytesAndIsolation(t *testing.T) {
	t.Parallel()
	for _, native := range []string{descriptionSource, `null`, `{"type":"doc","version":1,"content":[]}`} {
		t.Run(native, func(t *testing.T) {
			t.Parallel()
			issue := descriptionIssue(t, native)
			client := descriptionReader(t, issue)
			session, retry, err := PrepareIssueDescription(t.Context(), client, "KEY-1")
			if err != nil || retry.Attempts != 2 || client.calls != 1 {
				t.Fatal("native prepare failed", err)
			}
			snapshot := session.Snapshot()
			if snapshot.Version != 1 || snapshot.IssueID != "123456" || snapshot.IssueKey != "KEY-1" || snapshot.Site != client.site || snapshot.Account != client.account || string(snapshot.Description) != native {
				t.Fatal("native source or binding changed", snapshot)
			}
			original := bytes.Clone(snapshot.Description)
			snapshot.Description[0] = 'x'
			issue.Fields.Value.Description.Value = json.RawMessage(`null`)
			got, err := session.Apply(strings.ReplaceAll(session.Markdown(), "\n", "\r\n"))
			if err != nil || !bytes.Equal(got, original) || !bytes.Equal(session.Snapshot().Description, original) {
				t.Fatal("session aliased input/output or changed untouched bytes", err)
			}
			reopened, err := OpenIssueDescriptionSession(session.Snapshot())
			if err != nil || reopened.Markdown() != session.Markdown() {
				t.Fatal("reopened native source changed anchors", err)
			}
		})
	}
}

func TestIssueDescriptionPreparationRefusesIncompleteNativeState(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"nil", "absent ID", "null ID", "blank ID", "absent key", "null key", "blank key", "absent fields", "null fields", "absent description", "invalid ADF", "wrong version", "duplicate root"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			issue := descriptionIssue(t, descriptionSource)
			switch name {
			case "nil":
				issue = nil
			case "absent ID":
				issue.ID.Set = false
			case "null ID":
				issue.ID.Null = true
			case "blank ID":
				issue.ID.Value = " "
			case "absent key":
				issue.Key.Set = false
			case "null key":
				issue.Key.Null = true
			case "blank key":
				issue.Key.Value = " "
			case "absent fields":
				issue.Fields.Set = false
			case "null fields":
				issue.Fields.Null = true
			case "absent description":
				issue.Fields.Value.Description.Set = false
			case "invalid ADF":
				issue.Fields.Value.Description = jira.NullableOf(json.RawMessage(`{"type":"paragraph"}`))
			case "wrong version":
				issue.Fields.Value.Description = jira.NullableOf(json.RawMessage(`{"type":"doc","version":2,"content":[]}`))
			case "duplicate root":
				issue.Fields.Value.Description = jira.NullableOf(json.RawMessage(`{"type":"doc","version":1,"version":1,"content":[]}`))
			}
			session, _, err := PrepareIssueDescription(t.Context(), descriptionReader(t, issue), "KEY-1")
			if session != nil || !errors.Is(err, ErrIssueDescriptionInvalid) {
				t.Fatal("incomplete or contradictory native source became editable", err)
			}
		})
	}
}

func TestIssueDescriptionMergeRetainsFreshNativeStateAndImmutableID(t *testing.T) {
	t.Parallel()
	client := descriptionReader(t, descriptionIssue(t, descriptionSource))
	session, _, err := PrepareIssueDescription(t.Context(), client, "KEY-1")
	if err != nil {
		t.Fatal(err)
	}
	current := strings.Replace(descriptionSource, `9007199254740993`, `9007199254740995`, 1)
	client.read = func(_ context.Context, key string, fields []string) (IssueRead, error) {
		if key != "123456" || !slices.Equal(fields, []string{"description", "updated"}) {
			t.Fatal("merge did not read immutable issue ID", key, fields)
		}
		issue := descriptionIssue(t, current)
		issue.Key = jira.NullableOf("MOVED-2")
		return IssueRead{Issue: issue, Retry: RetryInfo{Attempts: 3}}, nil
	}
	merged, err := session.Merge(t.Context(), client, strings.Replace(session.Markdown(), "alpha", "authored", 1))
	if err != nil || !merged.Changed || merged.Current.IssueKey != "MOVED-2" || merged.Retry.Attempts != 3 || !bytes.Contains(merged.Description, []byte(`9007199254740995`)) || !bytes.Contains(merged.Description, []byte(`"raw":"<>&"`)) || !bytes.Contains(merged.Description, []byte(`"text":"authored"`)) {
		t.Fatal("merge lost current native bytes, local edit, identity or transport facts", err, string(merged.Description))
	}
	if string(session.Snapshot().Description) != descriptionSource {
		t.Fatal("merge changed original snapshot")
	}
}

func TestIssueDescriptionMergeScopeInputAndCancellationRefusals(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"site", "account", "api route", "nil client", "invalid edit", "canceled before", "canceled read", "scope changed during read", "wrong issue", "null current fields"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := descriptionReader(t, descriptionIssue(t, descriptionSource))
			session, _, err := PrepareIssueDescription(t.Context(), client, "KEY-1")
			if err != nil {
				t.Fatal(err)
			}
			client.calls = 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			markdown := session.Markdown()
			var selected IssueDescriptionClient = client
			want := ErrIssueDescriptionScopeChanged
			switch name {
			case "site":
				client.site = "https://other.example.test"
			case "account":
				client.account = "other@example.test"
			case "api route":
				client.apiBaseURL = "https://api.atlassian.com/ex/jira/other-cloud/"
			case "nil client":
				selected = nil
			case "invalid edit":
				markdown = strings.Replace(markdown, "protected", "editable", 1)
				want = nil
			case "canceled before":
				cancel()
				want = context.Canceled
			case "canceled read":
				client.read = func(context.Context, string, []string) (IssueRead, error) {
					cancel()
					return IssueRead{Issue: descriptionIssue(t, descriptionSource)}, nil
				}
				want = context.Canceled
			case "scope changed during read":
				client.read = func(context.Context, string, []string) (IssueRead, error) {
					client.account = "other@example.test"
					return IssueRead{Issue: descriptionIssue(t, descriptionSource)}, nil
				}
			case "wrong issue":
				client.read = func(context.Context, string, []string) (IssueRead, error) {
					issue := descriptionIssue(t, descriptionSource)
					issue.ID = jira.NullableOf("654321")
					return IssueRead{Issue: issue}, nil
				}
			case "null current fields":
				client.read = func(context.Context, string, []string) (IssueRead, error) {
					issue := descriptionIssue(t, descriptionSource)
					issue.Fields.Null = true
					return IssueRead{Issue: issue}, nil
				}
				want = ErrIssueDescriptionInvalid
			}
			merged, err := session.Merge(ctx, selected, markdown)
			if err == nil || merged.Description != nil || merged.Current.Version != 0 || want != nil && !errors.Is(err, want) {
				t.Fatal("refusal returned a candidate or wrong cause", err)
			}
			if (name == "site" || name == "account" || name == "api route" || name == "nil client" || name == "invalid edit" || name == "canceled before") && client.calls != 0 {
				t.Fatal("invalid scope/edit or cancellation entered remote read")
			}
		})
	}
}

func TestIssueDescriptionMergeDistinguishesNativeNullAndEmptyDocument(t *testing.T) {
	t.Parallel()
	for _, source := range []string{`null`, `{"type":"doc","version":1,"content":[]}`, descriptionSource} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			client := descriptionReader(t, descriptionIssue(t, source))
			session, _, err := PrepareIssueDescription(t.Context(), client, "KEY-1")
			if err != nil {
				t.Fatal(err)
			}
			client.read = func(context.Context, string, []string) (IssueRead, error) {
				return IssueRead{Issue: descriptionIssue(t, `null`)}, nil
			}
			unchanged, err := session.Merge(t.Context(), client, session.Markdown())
			if err != nil || unchanged.Changed || string(unchanged.Description) != "null" {
				t.Fatal("unchanged edit did not retain current native null", err)
			}
			changed, err := session.Merge(t.Context(), client, session.Markdown()+"\nnew content\n")
			if source == "null" {
				if err != nil || !changed.Changed || !bytes.Contains(changed.Description, []byte("new content")) {
					t.Fatal("null source could not author first description", err)
				}
			} else {
				var failure *adf.EditError
				if !errors.As(err, &failure) || failure.Kind != adf.EditMergeConflict || changed.Description != nil {
					t.Fatal("remote clear was silently overwritten", err)
				}
			}
		})
	}
}

func TestIssueDescriptionNullWhitespaceDoesNotInventAnEmptyDocument(t *testing.T) {
	t.Parallel()
	client := descriptionReader(t, descriptionIssue(t, "null"))
	session, _, err := PrepareIssueDescription(t.Context(), client, "KEY-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, markdown := range []string{"", "\n", "\r\n", " \n\t\n"} {
		candidate, err := session.Apply(markdown)
		if err != nil || string(candidate) != "null" {
			t.Fatal("a blank editor save invented an empty native document", err, string(candidate))
		}
	}
}

func TestIssueDescriptionZeroSessionAndContradictoryNullRefuse(t *testing.T) {
	t.Parallel()
	issue := descriptionIssue(t, "null")
	issue.Fields.Value.Description.Null = false
	issue.Fields.Value.Description.Value = json.RawMessage("null")
	session, _, err := PrepareIssueDescription(t.Context(), descriptionReader(t, issue), "KEY-1")
	if session != nil || !errors.Is(err, ErrIssueDescriptionInvalid) {
		t.Fatal("populated raw null overrode the native value marker", err)
	}
	var zero IssueDescriptionSession
	if candidate, err := zero.Apply(""); candidate != nil || !errors.Is(err, ErrIssueDescriptionInvalid) {
		t.Fatal("unprepared session became usable", err)
	}
}

func TestIssueDescriptionPreparationRefusesScopeAndCancellationBeforeRead(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"blank account", "userinfo", "relative site", "query", "fragment", "canceled", "blank key"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := descriptionReader(t, descriptionIssue(t, descriptionSource))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			key := "KEY-1"
			switch name {
			case "blank account":
				client.account = " "
			case "userinfo":
				client.site = (&url.URL{Scheme: "https", Host: "jira.example.test", User: url.UserPassword("reader", "synthetic")}).String()
			case "relative site":
				client.site = "jira.example.test"
			case "query":
				client.site += "?untrusted=1"
			case "fragment":
				client.site += "#untrusted"
			case "canceled":
				cancel()
			case "blank key":
				key = " "
			}
			session, _, err := PrepareIssueDescription(ctx, client, key)
			if session != nil || err == nil || client.calls != 0 {
				t.Fatal("invalid scope/input or cancellation entered remote preparation", err)
			}
		})
	}
}

func TestIssueDescriptionReopenValidatesProtocolAndOwnsSourceBuffer(t *testing.T) {
	t.Parallel()
	client := descriptionReader(t, descriptionIssue(t, descriptionSource))
	session, _, err := PrepareIssueDescription(t.Context(), client, "KEY-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"version", "missing route", "missing ID", "missing key", "route credentials", "route scheme", "missing preimage", "contradicted preimage"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			snapshot := session.Snapshot()
			switch name {
			case "version":
				snapshot.Version = 2
			case "missing route":
				snapshot.APIBaseURL = ""
			case "missing ID":
				snapshot.IssueID = " "
			case "missing key":
				snapshot.IssueKey = " "
			case "route credentials":
				snapshot.APIBaseURL = (&url.URL{Scheme: "https", Host: "jira.example.test", Path: "/", User: url.UserPassword("reader", "synthetic")}).String()
			case "route scheme":
				snapshot.APIBaseURL = "file://jira.example.test/"
			case "missing preimage":
				snapshot.DescriptionBytes = nil
			case "contradicted preimage":
				snapshot.DescriptionBytes = []byte("null")
			}
			opened, err := OpenIssueDescriptionSession(snapshot)
			if opened != nil || !errors.Is(err, ErrIssueDescriptionInvalid) {
				t.Fatal("untrusted snapshot protocol or routing identity became usable", err)
			}
		})
	}
	snapshot := session.Snapshot()
	opened, err := OpenIssueDescriptionSession(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Description[0] = 'x'
	snapshot.DescriptionBytes[0] = 'x'
	if got, err := opened.Apply(opened.Markdown()); err != nil || string(got) != descriptionSource {
		t.Fatal("reopened session aliased caller-owned native source", err)
	}
}
