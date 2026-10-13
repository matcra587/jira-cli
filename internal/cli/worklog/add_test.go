package worklog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type worklogClient struct {
	core.JiraClient
	get func(context.Context, string, int, int) (core.WorklogRead, error)
	add func(context.Context, string, jira.AddWorklogRequest, bool) (core.WorklogWrite, error)
}

func (c *worklogClient) GetWorklogs(ctx context.Context, key string, start, size int) (core.WorklogRead, error) {
	return c.get(ctx, key, start, size)
}

func (c *worklogClient) AddWorklog(ctx context.Context, key string, req jira.AddWorklogRequest, notify bool) (core.WorklogWrite, error) {
	return c.add(ctx, key, req, notify)
}

func clientDeps(c *worklogClient) Deps {
	d := fixedDeps()
	d.JiraClient = func(ctx context.Context) (core.JiraClient, error) { return c, nil }
	return d
}

func storedWorklog(id string) *jira.Worklog {
	return &jira.Worklog{ID: jira.NullableOf(id), TimeSpentSeconds: jira.NullableOf(int64(60))}
}

func TestWorklogAddPreviewIsLocalAndInventsNoRecord(t *testing.T) {
	t.Parallel()
	deps := fixedDeps()
	deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
		t.Error("preview constructed a client")
		return nil, errors.New("preview must remain local")
	}
	var nowCalls int
	deps.Now = func() time.Time { nowCalls++; return time.Date(2026, 1, 2, 12, 30, 0, 0, time.UTC) }
	input := parsedInput(t, "--time-spent=1d", "--started=now")
	out, err := runAdd(t.Context(), deps, command.Invocation{DryRun: true, Args: []string{"KEY-2,KEY-1:2"}}, input, 2, false)
	if err != nil || !out.DryRun || out.Notify || out.WorkdaySeconds != 28800 || string(out.Payload) != `{"timeSpentSeconds":28800,"started":"2026-01-02T12:30:00.000+0000"}` || nowCalls != 1 || len(out.Results) != 2 || out.Results[0].Issue.Key != "KEY-2" || out.Results[1].Issue.Key != "KEY-1" {
		t.Fatalf("local preview lost exact request or target order: %+v %v now calls=%d", out, err, nowCalls)
	}
	for _, row := range out.Results {
		if !row.OK || row.Data == nil || row.Data.Applied || row.Data.Worklog != nil || row.Data.ReceiptError != nil || row.Err != nil {
			t.Fatalf("preview invented remote state: %+v", row)
		}
	}
}

func TestWorklogAddBoundedOrderedPartialWrites(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var mu sync.Mutex
	calls := map[string]int{}
	var active, peak atomic.Int32
	firstStarted, secondDone := make(chan struct{}), make(chan struct{})
	c := &worklogClient{add: func(ctx context.Context, key string, req jira.AddWorklogRequest, notify bool) (core.WorklogWrite, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		mu.Lock()
		calls[key]++
		mu.Unlock()
		if req.TimeSpentSeconds != 60 || req.Started != "2026-01-02T12:30:00.000+0000" || notify {
			return core.WorklogWrite{}, errors.New("request or notify changed")
		}
		if key == "KEY-1" {
			close(firstStarted)
			select {
			case <-secondDone:
			case <-ctx.Done():
				return core.WorklogWrite{}, ctx.Err()
			}
		}
		if key == "KEY-2" {
			select {
			case <-firstStarted:
			case <-ctx.Done():
				return core.WorklogWrite{}, ctx.Err()
			}
			close(secondDone)
			return core.WorklogWrite{}, cli.NewCodedError("jira_not_found", "missing issue")
		}
		return core.WorklogWrite{Acknowledged: true, Worklog: storedWorklog("100")}, nil
	}}
	out, err := runAdd(ctx, clientDeps(c), command.Invocation{Args: []string{"KEY-1:3", "KEY-1"}}, parsedInput(t, "--time-spent=1m"), 2, false)
	if err != nil || peak.Load() != 2 || len(out.Results) != 3 || !reflect.DeepEqual(calls, map[string]int{"KEY-1": 1, "KEY-2": 1, "KEY-3": 1}) {
		t.Fatalf("fanout bounds, dedup or no-replay changed: %+v %v peak=%d calls=%v", out, err, peak.Load(), calls)
	}
	for i, row := range out.Results {
		if row.Issue.Key != fmt.Sprintf("KEY-%d", i+1) || row.OK != (i != 1) {
			t.Fatalf("completion order replaced request order: %+v", out.Results)
		}
	}
	if out.Results[1].Data != nil || out.Results[1].Err.Code != "jira_not_found" || !out.Results[0].Data.Applied || out.Results[0].Data.Worklog.ID != "100" {
		t.Fatalf("partial outcomes collapsed: %+v", out.Results)
	}
}

func TestWorklogAddAcknowledgementSurvivesInvalidReceipt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		worklog *jira.Worklog
		err     error
	}{
		{"missing", nil, nil}, {"invalid identity", &jira.Worklog{}, nil}, {"malformed body", nil, errors.New("bad JSON")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			c := &worklogClient{add: func(context.Context, string, jira.AddWorklogRequest, bool) (core.WorklogWrite, error) {
				calls.Add(1)
				return core.WorklogWrite{Acknowledged: true, Worklog: tc.worklog}, tc.err
			}}
			out, err := runAdd(t.Context(), clientDeps(c), command.Invocation{Args: []string{"KEY-1"}}, parsedInput(t, "--time-spent=1m"), 1, true)
			if err != nil || calls.Load() != 1 || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Err != nil || !out.Results[0].Data.Applied || out.Results[0].Data.Worklog != nil || out.Results[0].Data.ReceiptError == nil || out.Results[0].Data.ReceiptError.Code != cli.CodeWorklogResponseInvalid {
				t.Fatalf("acknowledged creation became failure or invented receipt: %+v %v calls=%d", out, err, calls.Load())
			}
			warnings := addWarnings(out)
			if len(warnings) != 1 || warnings[0].Type != "worklog_receipt_incomplete" || warnings[0].Lossy {
				t.Fatalf("applied but incomplete receipt lacks inspection warning: %+v", warnings)
			}
		})
	}
}

func TestWorklogAddTotalFailureRetainsOrderedReceiptsAndSeverity(t *testing.T) {
	t.Parallel()
	c := &worklogClient{add: func(_ context.Context, key string, _ jira.AddWorklogRequest, _ bool) (core.WorklogWrite, error) {
		if key == "KEY-2" {
			return core.WorklogWrite{}, cli.NewCodedError("jira_unauthorized", "credential refused")
		}
		return core.WorklogWrite{}, cli.NewCodedError("jira_not_found", "missing issue")
	}}
	out, err := runAdd(t.Context(), clientDeps(c), command.Invocation{Args: []string{"KEY-1:2"}}, parsedInput(t, "--time-spent=1m"), 2, true)
	if cli.MapError(err).Code != "jira_unauthorized" || len(out.Results) != 2 || out.Results[0].Err.Code != "jira_not_found" || out.Results[1].Err.Code != "jira_unauthorized" || len(out.Payload) == 0 {
		t.Fatalf("total failure lost receipts or used wrong severity: %+v %v", out, err)
	}
}

func TestWorklogAddRefusesUnsafeInputsBeforeClient(t *testing.T) {
	t.Parallel()
	deps := fixedDeps()
	deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
		t.Error("invalid input constructed client")
		return nil, errors.New("unexpected client")
	}
	for _, keys := range [][]string{{"KEY-1,foreign"}, {"KEY-1:1001"}, {"KEY-0"}} {
		if _, err := runAdd(t.Context(), deps, command.Invocation{Args: keys}, parsedInput(t, "--time-spent=1m"), 1, true); err == nil {
			t.Fatalf("invalid targets accepted: %v", keys)
		}
	}
	for _, args := range [][]string{{}, {"--time-spent=0m"}, {"--time-spent=1m", "--started="}} {
		if _, err := runAdd(t.Context(), deps, command.Invocation{Args: []string{"KEY-1"}}, parsedInput(t, args...), 1, true); err == nil {
			t.Fatalf("invalid time accepted: %v", args)
		}
	}
	deps.WorkdaySeconds = func() (int, error) { return 0, nil }
	if _, err := runAdd(t.Context(), deps, command.Invocation{Args: []string{"KEY-1"}}, parsedInput(t, "--time-spent=1m"), 1, true); err == nil {
		t.Fatal("invalid configured workday accepted")
	}
}

func TestWorklogCanceledBeforeConstruction(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	deps := fixedDeps()
	deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
		t.Error("canceled command constructed client")
		return nil, context.Canceled
	}
	if _, err := runAdd(ctx, deps, command.Invocation{Args: []string{"KEY-1"}}, parsedInput(t, "--time-spent=1m"), 1, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := runList(ctx, deps, []string{"KEY-1"}, listOptions{limit: 50, keyed: command.KeyedFlags{Parallelism: 1}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation lost: %v", err)
	}
}

func TestWorklogAddNativeRecordKeepsUnknownAndNullFields(t *testing.T) {
	t.Parallel()
	var record jira.Worklog
	if err := json.Unmarshal([]byte(`{"id":"100","author":null,"timeSpentSeconds":0,"future":{"markup":"<>&","n":9007199254740993}}`), &record); err != nil {
		t.Fatal(err)
	}
	c := &worklogClient{add: func(context.Context, string, jira.AddWorklogRequest, bool) (core.WorklogWrite, error) {
		return core.WorklogWrite{Acknowledged: true, Worklog: &record}, nil
	}}
	out, err := runAdd(t.Context(), clientDeps(c), command.Invocation{Args: []string{"KEY-1"}}, parsedInput(t, "--time-spent=1m"), 1, true)
	if err != nil || len(out.Results) != 1 || out.Results[0].Data == nil || out.Results[0].Data.Worklog == nil {
		t.Fatalf("native receipt absent: %+v %v", out, err)
	}
	got := out.Results[0].Data.Worklog.Fields
	if !got.Author.Null || !got.TimeSpentSeconds.Set || got.TimeSpentSeconds.Value != 0 || string(got.Extra["future"]) != `{"markup":"<>&","n":9007199254740993}` {
		t.Fatalf("native record facts lost: %+v", got)
	}
}

func TestWorklogAddCancellationRetainsAcknowledgement(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	c := &worklogClient{add: func(context.Context, string, jira.AddWorklogRequest, bool) (core.WorklogWrite, error) {
		calls.Add(1)
		cancel()
		return core.WorklogWrite{Acknowledged: true, Worklog: storedWorklog("100")}, nil
	}}
	out, err := runAdd(ctx, clientDeps(c), command.Invocation{Args: []string{"KEY-1:2"}}, parsedInput(t, "--time-spent=1m"), 1, true)
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 || len(out.Results) != 2 || !out.Results[0].OK || !out.Results[0].Data.Applied || out.Results[0].Data.Worklog.ID != "100" || out.Results[1].OK || out.Results[1].Err.Code != cli.CodeCanceled {
		t.Fatalf("cancellation lost acknowledgement or posted the next target: %+v %v calls=%d", out, err, calls.Load())
	}
}
