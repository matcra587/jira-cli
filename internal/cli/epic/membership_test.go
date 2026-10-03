package epic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type membershipClient struct {
	core.JiraClient
	get  func(context.Context, string, []string) (core.IssueRead, error)
	edit func(context.Context, string, jira.IssueWriteRequest, bool) (core.IssueEditWrite, error)
}

func (c *membershipClient) GetIssue(ctx context.Context, key string, fields []string, _ ...string) (core.IssueRead, error) {
	return c.get(ctx, key, fields)
}

func (c *membershipClient) EditIssue(ctx context.Context, key string, request jira.IssueWriteRequest, notify bool) (core.IssueEditWrite, error) {
	return c.edit(ctx, key, request, notify)
}

func membershipRead(t *testing.T, raw string) core.IssueRead {
	t.Helper()
	var issue jira.IssueBean
	if err := json.Unmarshal([]byte(raw), &issue); err != nil {
		t.Fatal(err)
	}
	return core.IssueRead{Issue: &issue}
}

func membershipDeps(c *membershipClient) Deps {
	return Deps{JiraClient: func() (core.JiraClient, error) { return c, nil }}
}

func membershipOpts() membershipOptions {
	return membershipOptions{keyed: command.KeyedFlags{Parallelism: 2}, mut: command.MutationFlags{Notify: true}}
}

func TestEpicMembershipLocalPreviewAndSelfRefusal(t *testing.T) {
	t.Parallel()
	deps := Deps{JiraClient: func() (core.JiraClient, error) {
		t.Error("local input/preview constructed a client")
		return nil, errors.New("must remain local")
	}}
	for _, parent := range []string{"KEY-100", ""} {
		out, err := runMembership(t.Context(), deps, command.Invocation{DryRun: true}, []string{"KEY-2,KEY-1:2"}, parent, membershipOpts())
		want := `{"fields":{"parent":null}}`
		if parent != "" {
			want = `{"fields":{"parent":{"key":"KEY-100"}}}`
		}
		if err != nil || !out.DryRun || out.Epic != parent || !out.Notify || string(out.Payload) != want || len(out.Results) != 2 || out.Results[0].Issue.Key != "KEY-2" || out.Results[1].Issue.Key != "KEY-1" {
			t.Fatalf("preview lost exact payload/order: %+v %v", out, err)
		}
		for _, row := range out.Results {
			if !row.OK || row.Data.Applied || row.Data.RemoteValidated || row.Data.Parent != nil || row.Data.AlreadyUnparented || row.Data.VerifyFailed {
				t.Fatalf("preview invented a remote outcome: %+v", row)
			}
		}
	}
	for _, input := range []string{"KEY-100", "KEY-99:101", "KEY-1:1001", "KEY-1,foreign"} {
		if _, err := runMembership(t.Context(), deps, command.Invocation{}, []string{input}, "KEY-100", membershipOpts()); err == nil {
			t.Fatalf("unsafe targets accepted: %s", input)
		}
	}
}

func TestEpicMembershipParentTypePreflightPreventsAllWrites(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{}`, `{"fields":null}`, `{"fields":{}}`,
		`{"fields":{"issuetype":{"name":"Epic"}}}`,
		`{"fields":{"issuetype":{"name":"Epic","subtask":true,"hierarchyLevel":1}}}`,
		`{"fields":{"issuetype":{"name":"Epic","subtask":false,"hierarchyLevel":0}}}`,
		`{"fields":{"issuetype":{"name":"Task","subtask":false}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			read := membershipRead(t, raw)
			c := &membershipClient{get: func(_ context.Context, key string, fields []string) (core.IssueRead, error) {
				if key != "KEY-100" || !reflect.DeepEqual(fields, []string{"issuetype"}) {
					return core.IssueRead{}, errors.New("parent preflight changed")
				}
				return read, nil
			}, edit: func(context.Context, string, jira.IssueWriteRequest, bool) (core.IssueEditWrite, error) {
				return core.IssueEditWrite{}, errors.New("write was reached")
			}}
			out, err := runMembership(t.Context(), membershipDeps(c), command.Invocation{}, []string{"KEY-1", "KEY-2"}, "KEY-100", membershipOpts())
			if err == nil || cli.MapError(err).Code != cli.CodeArgValueInvalid || len(out.Results) != 0 {
				t.Fatalf("parent refusal did not precede fanout: %+v %v", out, err)
			}
		})
	}
}

func TestEpicMembershipBoundedOrderedPartialWrites(t *testing.T) {
	t.Parallel()
	parent := membershipRead(t, `{"fields":{"issuetype":{"name":"Renamed Epic","subtask":false,"hierarchyLevel":1}}}`)
	var mu sync.Mutex
	calls := map[string]int{}
	var active, peak atomic.Int32
	failure := cli.NewCodedError("jira_not_found", "missing child")
	firstStarted, secondFinished := make(chan struct{}), make(chan struct{})
	c := &membershipClient{get: func(context.Context, string, []string) (core.IssueRead, error) { return parent, nil }}
	c.edit = func(_ context.Context, key string, req jira.IssueWriteRequest, notify bool) (core.IssueEditWrite, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		mu.Lock()
		calls[key]++
		mu.Unlock()
		body, err := jira.MarshalNoEscape(req)
		if err != nil || string(body) != `{"fields":{"parent":{"key":"KEY-100"}}}` || notify {
			return core.IssueEditWrite{}, errors.New("parent payload/notify changed")
		}
		if key == "KEY-1" {
			close(firstStarted)
			<-secondFinished
		}
		if key == "KEY-2" {
			<-firstStarted
			close(secondFinished)
			return core.IssueEditWrite{}, failure
		}
		return core.IssueEditWrite{}, nil
	}
	o := membershipOpts()
	o.mut.Notify = false
	out, err := runMembership(t.Context(), membershipDeps(c), command.Invocation{}, []string{"KEY-1:3", "KEY-1"}, "KEY-100", o)
	if err != nil || peak.Load() != 2 || len(out.Results) != 3 || !reflect.DeepEqual(calls, map[string]int{"KEY-1": 1, "KEY-2": 1, "KEY-3": 1}) {
		t.Fatalf("fanout bounds/dedup/replay changed: %+v %v peak=%d calls=%v", out, err, peak.Load(), calls)
	}
	for i, row := range out.Results {
		if row.Issue.Key != fmt.Sprintf("KEY-%d", i+1) || row.OK != (i != 1) {
			t.Fatalf("results reordered: %+v", out.Results)
		}
	}
	if out.Results[1].Err == nil || out.Results[1].Err.Code != "jira_not_found" || out.Results[1].Data != nil || !out.Results[0].Data.Applied || !out.Results[0].Data.RemoteValidated {
		t.Fatalf("partial receipt lost failures or acknowledged writes: %+v", out)
	}
}

func TestEpicRemovalParentSafetyAndNoOp(t *testing.T) {
	t.Parallel()
	epic := membershipRead(t, `{"fields":{"issuetype":{"name":"Epic","subtask":false}}}`)
	task := membershipRead(t, `{"fields":{"issuetype":{"name":"Task","subtask":false,"hierarchyLevel":0}}}`)
	for _, tc := range []struct {
		name, raw string
		parent    core.IssueRead
		write, ok bool
		state     string
	}{
		{"epic", `{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":false}}}`, epic, true, true, ""},
		{"subtask", `{"fields":{"issuetype":{"subtask":true},"parent":{"key":"KEY-100"}}}`, epic, false, false, ""},
		{"missing type", `{"fields":{"parent":{"key":"KEY-100"}}}`, epic, false, false, ""},
		{"null type", `{"fields":{"parent":{"key":"KEY-100"},"issuetype":null}}`, epic, false, false, ""},
		{"missing subtask flag", `{"fields":{"parent":{"key":"KEY-100"},"issuetype":{}}}`, epic, false, false, ""},
		{"null subtask flag", `{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":null}}}`, epic, false, false, ""},
		{"task parent", `{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":false}}}`, task, false, false, ""},
		{"absent", `{"fields":{"issuetype":{"subtask":false}}}`, epic, false, true, "absent"},
		{"null", `{"fields":{"parent":null,"issuetype":{"subtask":false}}}`, epic, false, true, "null"},
		{"missing fields", `{}`, epic, false, false, ""},
		{"malformed parent", `{"fields":{"parent":{}}}`, epic, false, false, ""},
		{"scalar parent", `{"fields":{"parent":false}}`, epic, false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := membershipRead(t, tc.raw)
			writes := 0
			c := &membershipClient{get: func(_ context.Context, key string, _ []string) (core.IssueRead, error) {
				if key == "KEY-1" {
					return child, nil
				}
				return tc.parent, nil
			}, edit: func(_ context.Context, _ string, request jira.IssueWriteRequest, _ bool) (core.IssueEditWrite, error) {
				writes++
				body, err := jira.MarshalNoEscape(request)
				if err != nil || string(body) != `{"fields":{"parent":null}}` {
					return core.IssueEditWrite{}, errors.New("removal did not send explicit null")
				}
				return core.IssueEditWrite{}, nil
			}}
			out, err := runMembership(t.Context(), membershipDeps(c), command.Invocation{}, []string{"KEY-1"}, "", membershipOpts())
			if (err == nil) != tc.ok || len(out.Results) != 1 || out.Results[0].OK != tc.ok || (writes == 1) != tc.write {
				t.Fatalf("removal safety/no-op changed: %+v err=%v writes=%d", out, err, writes)
			}
			if tc.state != "" && (out.Results[0].Data.Parent.State != tc.state || !out.Results[0].Data.AlreadyUnparented || out.Results[0].Data.Applied) {
				t.Fatalf("unparented observation fabricated a write: %+v", out)
			}
		})
	}
}

func TestEpicMembershipVerificationNeverTurnsAppliedWriteIntoFailure(t *testing.T) {
	t.Parallel()
	epic := membershipRead(t, `{"fields":{"issuetype":{"name":"Epic","subtask":false}}}`)
	for _, raw := range []string{`{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":false}}}`, `{"fields":{"parent":{"key":"KEY-200"}}}`, `{"fields":{}}`, `{"fields":{"parent":[]}}`, "transport"} {
		t.Run(raw, func(t *testing.T) {
			var observed core.IssueRead
			if raw != "transport" {
				observed = membershipRead(t, raw)
			}
			writes := 0
			c := &membershipClient{get: func(_ context.Context, key string, _ []string) (core.IssueRead, error) {
				if key == "KEY-100" {
					return epic, nil
				}
				if raw == "transport" {
					return core.IssueRead{}, errors.New("readback unavailable")
				}
				return observed, nil
			}, edit: func(context.Context, string, jira.IssueWriteRequest, bool) (core.IssueEditWrite, error) {
				writes++
				return core.IssueEditWrite{}, nil
			}}
			o := membershipOpts()
			o.mut.Verify = true
			out, err := runMembership(t.Context(), membershipDeps(c), command.Invocation{}, []string{"KEY-1"}, "KEY-100", o)
			data := out.Results[0].Data
			wantFailure := raw != `{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":false}}}`
			if err != nil || writes != 1 || !out.Results[0].OK || !data.Applied || data.VerifyFailed != wantFailure || (data.VerificationError != nil) != wantFailure || (len(membershipWarnings(out)) > 0) != wantFailure {
				t.Fatalf("readback lied about write outcome: %+v err=%v writes=%d", out, err, writes)
			}
		})
	}
}

func TestEpicMembershipRemotePreviewAndCancellation(t *testing.T) {
	t.Parallel()
	epic := membershipRead(t, `{"fields":{"issuetype":{"name":"Epic","subtask":false}}}`)
	child := membershipRead(t, `{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":false}}}`)
	var reads, writes atomic.Int32
	c := &membershipClient{get: func(ctx context.Context, key string, _ []string) (core.IssueRead, error) {
		reads.Add(1)
		if err := ctx.Err(); err != nil {
			return core.IssueRead{}, err
		}
		if key == "KEY-100" {
			return epic, nil
		}
		return child, nil
	}, edit: func(context.Context, string, jira.IssueWriteRequest, bool) (core.IssueEditWrite, error) {
		writes.Add(1)
		return core.IssueEditWrite{}, nil
	}}
	o := membershipOpts()
	o.mut.ValidateRemote, o.mut.Verify = true, true
	for _, parent := range []string{"KEY-100", ""} {
		out, err := runMembership(t.Context(), membershipDeps(c), command.Invocation{DryRun: true}, []string{"KEY-1"}, parent, o)
		if err != nil || writes.Load() != 0 || !out.Results[0].Data.RemoteValidated || out.Results[0].Data.Applied || out.Results[0].Data.VerifyFailed {
			t.Fatalf("remote preview wrote or verified a nonexistent write: %+v %v", out, err)
		}
	}
	if reads.Load() != 3 {
		t.Fatalf("remote checks skipped or duplicated: %d", reads.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := runMembership(ctx, membershipDeps(c), command.Invocation{}, []string{"KEY-1"}, "", o)
	if !errors.Is(err, context.Canceled) || writes.Load() != 0 || reads.Load() != 3 || len(out.Results) != 1 || out.Results[0].Err.Code != cli.CodeCanceled {
		t.Fatalf("canceled targets fabricated success or reached HTTP: %+v %v", out, err)
	}
}

func TestEpicRemovalVerificationPreservesAbsentParent(t *testing.T) {
	t.Parallel()
	epic := membershipRead(t, `{"fields":{"issuetype":{"name":"Epic","subtask":false}}}`)
	child := membershipRead(t, `{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":false}}}`)
	removed := membershipRead(t, `{"fields":{"summary":"Owned child"}}`)
	missing := membershipRead(t, `{"key":"KEY-1"}`)
	written := false
	c := &membershipClient{get: func(_ context.Context, key string, fields []string) (core.IssueRead, error) {
		if key == "KEY-100" {
			return epic, nil
		}
		if !written {
			return child, nil
		}
		// Observed Jira behavior: an unset parent alone has no fields object.
		if reflect.DeepEqual(fields, []string{"parent", "summary"}) {
			return removed, nil
		}
		return missing, nil
	}, edit: func(context.Context, string, jira.IssueWriteRequest, bool) (core.IssueEditWrite, error) {
		written = true
		return core.IssueEditWrite{}, nil
	}}
	o := membershipOpts()
	o.mut.Verify = true
	out, err := runMembership(t.Context(), membershipDeps(c), command.Invocation{}, []string{"KEY-1"}, "", o)
	if err != nil || len(out.Results) != 1 || !out.Results[0].OK || !out.Results[0].Data.Applied || out.Results[0].Data.VerifyFailed || out.Results[0].Data.Parent == nil || out.Results[0].Data.Parent.State != "absent" {
		t.Fatalf("unset-parent response was not reliably verified: %+v %v", out, err)
	}
}
