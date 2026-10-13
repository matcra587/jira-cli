package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gechr/clog"

	adapter "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestMoveHumanResumeQuotesOpaqueTaskID(t *testing.T) {
	for _, tt := range []struct {
		id   string
		want string
	}{
		{id: "100", want: "jira issue move status --wait -- 100"},
		{id: "--help", want: "jira issue move status --wait -- --help"},
		{id: "job with spaces", want: "jira issue move status --wait -- 'job with spaces'"},
		{id: "job '$VALUE'; echo injected", want: "jira issue move status --wait -- 'job '\"'\"'$VALUE'\"'\"'; echo injected'"},
	} {
		t.Run(tt.id, func(t *testing.T) {
			if err := adapter.ValidateBulkTaskID(tt.id); err != nil {
				t.Fatalf("fixture is not an accepted opaque task ID: %v", err)
			}
			var buf bytes.Buffer
			renderBulkState(output.NewView(&buf, clog.ColorNever), envelope.BulkTaskState{Task: &envelope.BulkTaskRef{ID: tt.id}})
			if !strings.Contains(buf.String(), tt.want) {
				t.Fatalf("resume command does not preserve one literal task argument: %q; want %q", buf.String(), tt.want)
			}
		})
	}
}

type fakeMoveClient struct {
	unusedJiraClient
	reads        []string
	posts        int
	polls        int
	request      jira.IssueBulkMovePayload
	identities   map[string]string
	submit       core.IssueMoveSubmission
	submitErr    error
	observations []*jira.BulkOperationProgress
	pollErr      error
	cancel       context.CancelFunc
	submitCancel context.CancelFunc
}

func (f *fakeMoveClient) GetIssue(_ context.Context, key string, _ []string, _ ...string) (core.IssueRead, error) {
	f.reads = append(f.reads, key)
	id := f.identities[key]
	if id == "" {
		return core.IssueRead{}, errors.New("no fake issue identity")
	}
	return core.IssueRead{Issue: &jira.IssueBean{ID: jira.NullableOf(id), Key: jira.NullableOf(key)}}, nil
}

func (f *fakeMoveClient) SubmitIssueMove(_ context.Context, request jira.IssueBulkMovePayload) (core.IssueMoveSubmission, error) {
	f.posts++
	f.request = request
	if f.submitCancel != nil {
		f.submitCancel()
	}
	return f.submit, f.submitErr
}

func (f *fakeMoveClient) GetBulkOperationProgress(_ context.Context, taskID string) (core.BulkOperationRead, error) {
	f.polls++
	if taskID != "100" {
		return core.BulkOperationRead{}, errors.New("unexpected task identity")
	}
	if f.polls <= len(f.observations) {
		if f.cancel != nil {
			f.cancel()
		}
		return core.BulkOperationRead{Progress: f.observations[f.polls-1]}, nil
	}
	return core.BulkOperationRead{}, f.pollErr
}

func moveObservation(t *testing.T, body string) *jira.BulkOperationProgress {
	t.Helper()
	var out jira.BulkOperationProgress
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func moveFixture() *fakeMoveClient {
	return &fakeMoveClient{identities: map[string]string{"KEY-1": "101", "KEY-2": "102"}, submit: core.IssueMoveSubmission{Acknowledged: true, Task: &jira.SubmittedBulkOperation{TaskID: jira.NullableOf("100")}}}
}

func moveInvocation(body string, keys ...string) command.Invocation {
	return command.Invocation{Args: keys, Stdin: strings.NewReader(body), NoInput: true}
}

func moveOptions(wait bool) moveFlags {
	return moveFlags{input: "-", mutation: command.MutationFlags{Force: true}, poll: bulkPollFlags{wait: wait, max: 3, interval: time.Nanosecond}}
}

func TestMovePreviewPreservesDestinationAssociations(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			input := `{"targetToSourcesMapping":{"Z,10002":{"issueIdsOrKeys":["KEY-1","KEY-3"]},"A,10001":{"issueIdsOrKeys":["KEY-2"]}}}`
			wantSources := []string{"KEY-2", "KEY-1", "KEY-3"}
			if remote {
				input = `{"targetToSourcesMapping":{"Z,10002":{"issueIdsOrKeys":["101","103"]},"A,10001":{"issueIdsOrKeys":["102"]}}}`
				wantSources = []string{"102", "101", "103"}
			}
			fake := moveFixture()
			fake.identities["KEY-3"] = "103"
			deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
			inv := moveInvocation(input, "KEY-3", "KEY-1", "KEY-2")
			inv.DryRun = true
			flags := moveOptions(false)
			flags.mutation.ValidateRemote = remote
			out, err := runMove(context.Background(), deps, inv, flags)
			if err != nil || !out.BindingsValidated || out.RemoteValidated != remote || fake.posts != 0 || fake.polls != 0 || len(out.Targets) != 3 || len(out.Results) != 3 {
				t.Fatalf("multi-destination preview failed: %+v %v", out, err)
			}
			for i, destination := range []string{"A,10001", "Z,10002", "Z,10002"} {
				if out.Targets[i].Source != wantSources[i] || out.Targets[i].Destination != destination {
					t.Fatalf("authored source-to-destination association changed: %+v", out.Targets)
				}
			}
			for i, destination := range []string{"Z,10002", "Z,10002", "A,10001"} {
				if out.Results[i].Issue.Key != inv.Args[i] || out.Results[i].Data.Destination != destination || out.Results[i].Data.Applied {
					t.Fatalf("positional destination or preview state changed: %+v", out.Results)
				}
			}
		})
	}
}

func moveClientDeps(f *fakeMoveClient) Deps {
	return Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return f, nil }}
}

func TestMoveLocalPreviewRetainsNativeRequestWithoutClient(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"KEY-1", "101"} {
		body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["` + source + `"],"inferFieldDefaults":false,"future":{"n":9007199254740993,"markup":"<>&","null":null,"empty":[]}}},"sendBulkNotification":false}`
		calls := 0
		deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
			calls++
			return nil, errors.New("unexpected client")
		}}
		inv := moveInvocation(body, "KEY-1")
		inv.DryRun = true
		out, err := runMove(t.Context(), deps, inv, moveOptions(true))
		if err != nil || calls != 0 || !out.DryRun || out.Acknowledged || out.Attempted || out.Task != nil || out.Polls != 0 || out.RemoteValidated || out.BindingsValidated != (source == "KEY-1") || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data.Applied {
			t.Fatalf("preview invented remote facts: %+v calls=%d err=%v", out, calls, err)
		}
		if !strings.Contains(string(out.Payload), `"n":9007199254740993,"markup":"<>&","null":null,"empty":[]`) {
			t.Fatalf("native input lost: %s", out.Payload)
		}
	}
}

func TestMovePayloadBindingRefusesForeignAndRepeatedSources(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-2"]}}}`,
		`{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1","KEY-2"]}}}`,
		`{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["101","KEY-1"]}}}`,
		`{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["999"]}}}`,
	} {
		fake := moveFixture()
		keys := []string{"KEY-1"}
		if strings.Contains(body, `"101","KEY-1"`) {
			keys = append(keys, "KEY-2")
		}
		_, err := runMove(t.Context(), moveClientDeps(fake), moveInvocation(body, keys...), moveOptions(false))
		if err == nil || fake.posts != 0 {
			t.Fatalf("unbound payload submitted: %s posts=%d err=%v", body, fake.posts, err)
		}
	}
}

func TestMoveRemotePreviewBindsNumericIDsWithoutSubmission(t *testing.T) {
	t.Parallel()
	fake := moveFixture()
	body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["102","101"]}}}`
	inv := moveInvocation(body, "KEY-1", "KEY-2")
	inv.DryRun = true
	options := moveOptions(true)
	options.mutation.ValidateRemote = true
	out, err := runMove(t.Context(), moveClientDeps(fake), inv, options)
	if err != nil || !out.BindingsValidated || !out.RemoteValidated || out.Attempted || fake.posts != 0 || fake.polls != 0 || !slices.Equal(fake.reads, []string{"KEY-1", "KEY-2"}) || out.Results[0].Issue.ID != "101" || out.Results[1].Issue.ID != "102" {
		t.Fatalf("remote preview lost binding or wrote: %+v fake=%+v err=%v", out, fake, err)
	}
}

func TestMoveAcknowledgementIsNotApplied(t *testing.T) {
	t.Parallel()
	fake := moveFixture()
	body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-2","KEY-1"]}},"future":{"n":9007199254740993}}`
	out, err := runMove(t.Context(), moveClientDeps(fake), moveInvocation(body, "KEY-1", "KEY-2"), moveOptions(false))
	if err != nil || fake.posts != 1 || fake.polls != 0 || !out.Acknowledged || out.Task == nil || out.Task.ID != "100" || out.Terminal || out.Progress != nil || len(out.Results) != 2 {
		t.Fatalf("acknowledgement changed: %+v fake=%+v err=%v", out, fake, err)
	}
	for i, row := range out.Results {
		if row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] || !row.OK || row.Data.Applied || row.Data.Outcome != "accepted" {
			t.Fatalf("receipt fabricated issue completion: %+v", row)
		}
	}
	request, err := jira.MarshalNoEscape(fake.request)
	if err != nil || string(request) != string(out.Payload) {
		t.Fatalf("published payload differs from submitted payload: %s %s %v", out.Payload, request, err)
	}
}

func TestMoveCompleteMixedOutcomeKeepsSuccessfulIssue(t *testing.T) {
	t.Parallel()
	fake := moveFixture()
	fake.observations = []*jira.BulkOperationProgress{
		moveObservation(t, `{"taskId":"100","status":"RUNNING","progressPercent":100}`),
		moveObservation(t, `{"taskId":"100","status":"COMPLETE","totalIssueCount":2,"invalidOrInaccessibleIssueCount":0,"processedAccessibleIssues":[101],"failedAccessibleIssues":{"102":["Destination unavailable"]},"progressPercent":100}`),
	}
	body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1","KEY-2"]}}}`
	out, err := runMove(t.Context(), moveClientDeps(fake), moveInvocation(body, "KEY-1", "KEY-2"), moveOptions(true))
	if err != nil || fake.posts != 1 || fake.polls != 2 || !out.Terminal || out.PollLimitReached || !out.Results[0].OK || !out.Results[0].Data.Applied || out.Results[1].OK || out.Results[1].Err.Code != cli.CodeBulkOperationFailed || out.Results[1].Err.Message != "Destination unavailable" {
		t.Fatalf("mixed outcome fabricated or discarded: %+v fake=%+v err=%v", out, fake, err)
	}
}

func TestMovePollingFailureRetainsTaskAndLastObservation(t *testing.T) {
	t.Parallel()
	for _, canceled := range []bool{false, true} {
		fake := moveFixture()
		fake.observations = []*jira.BulkOperationProgress{moveObservation(t, `{"taskId":"100","status":"RUNNING","progressPercent":100}`)}
		cause := errors.New("progress unavailable")
		fake.pollErr = cause
		ctx, cancel := context.WithCancel(t.Context())
		if canceled {
			fake.cancel = cancel
			cause = context.Canceled
		}
		body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}}}`
		out, err := runMove(ctx, moveClientDeps(fake), moveInvocation(body, "KEY-1"), moveOptions(true))
		cancel()
		if !errors.Is(err, cause) || fake.posts != 1 || !out.Acknowledged || out.Task == nil || out.Task.ID != "100" || out.Progress == nil || out.Progress.Status.Value != "RUNNING" || out.Terminal || out.Results[0].Data.Applied || fake.polls != map[bool]int{true: 1, false: 2}[canceled] {
			t.Fatalf("interrupted move lost receipt or replayed: %+v fake=%+v err=%v", out, fake, err)
		}
	}
}

func TestMovePollingBoundDoesNotClaimCompletion(t *testing.T) {
	t.Parallel()
	fake := moveFixture()
	running := moveObservation(t, `{"taskId":"100","status":"RUNNING","progressPercent":100}`)
	fake.observations = []*jira.BulkOperationProgress{running, running, running}
	body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}}}`
	out, err := runMove(t.Context(), moveClientDeps(fake), moveInvocation(body, "KEY-1"), moveOptions(true))
	if err != nil || fake.posts != 1 || fake.polls != 3 || out.Polls != 3 || !out.PollLimitReached || out.Terminal || out.Results[0].Data.Applied {
		t.Fatalf("polling bound fabricated completion: %+v fake=%+v err=%v", out, fake, err)
	}
}

func TestMoveReceiptFailureKeepsAcknowledgementWithoutReplay(t *testing.T) {
	t.Parallel()
	fake := moveFixture()
	fake.submit.Task = nil
	fake.submitErr = errors.New("receipt interrupted")
	body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}}}`
	out, err := runMove(t.Context(), moveClientDeps(fake), moveInvocation(body, "KEY-1"), moveOptions(true))
	if err != nil || fake.posts != 1 || fake.polls != 0 || !out.Acknowledged || out.Task != nil || out.ReceiptError == nil || out.ReceiptError.Code != cli.CodeBulkOperationResponseInvalid || !out.Results[0].OK || out.Results[0].Data.Applied {
		t.Fatalf("receipt failure lost acceptance: %+v fake=%+v err=%v", out, fake, err)
	}
}

func TestMoveHeadlessRefusalDoesNotConstructClient(t *testing.T) {
	t.Parallel()
	fake := moveFixture()
	calls := 0
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { calls++; return fake, nil }}
	options := moveOptions(false)
	options.mutation.Force = false
	body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}}}`
	_, err := runMove(t.Context(), deps, moveInvocation(body, "KEY-1"), options)
	if err == nil || cli.MapError(err).Code != cli.CodeFlagRequired || calls != 0 || fake.posts != 0 {
		t.Fatalf("headless refusal bypassed local gate: calls=%d fake=%+v err=%v", calls, fake, err)
	}
}

func TestMoveDeclinedConfirmationIsCancellation(t *testing.T) {
	t.Parallel()
	fake := moveFixture()
	deps := moveClientDeps(fake)
	deps.Confirm = func(_ context.Context, _ prompt.Confirm) (bool, error) { return false, nil }
	options := moveOptions(false)
	options.mutation.Force = false
	body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}}}`
	inv := moveInvocation(body, "KEY-1")
	inv.Interactive = true
	inv.NoInput = false
	_, err := runMove(t.Context(), deps, inv, options)
	if err == nil || cli.MapError(err).Code != cli.CodeCanceled || fake.posts != 0 {
		t.Fatalf("declined move was not canceled: code=%s posts=%d err=%v", cli.MapError(err).Code, fake.posts, err)
	}
}

func TestMoveTerminalUnaccountedIssueIsNotApplied(t *testing.T) {
	t.Parallel()
	fake := moveFixture()
	fake.observations = []*jira.BulkOperationProgress{moveObservation(t, `{"taskId":"100","status":"COMPLETE","totalIssueCount":1,"invalidOrInaccessibleIssueCount":1,"processedAccessibleIssues":[],"progressPercent":100}`)}
	body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}}}`
	out, err := runMove(t.Context(), moveClientDeps(fake), moveInvocation(body, "KEY-1"), moveOptions(true))
	if err == nil || cli.MapError(err).Code != cli.CodeBulkOutcomeUnconfirmed || out.Results[0].OK || out.Results[0].Data != nil || !out.Terminal || fake.posts != 1 {
		t.Fatalf("unaccounted terminal issue marked moved: %+v err=%v", out, err)
	}
}

func TestMoveStatusPollingNeverSubmits(t *testing.T) {
	t.Parallel()
	fake := moveFixture()
	fake.observations = []*jira.BulkOperationProgress{moveObservation(t, `{"taskId":"100","status":"FAILED"}`)}
	state := envelope.BulkTaskState{Task: &envelope.BulkTaskRef{ID: "100"}}
	err := pollBulk(t.Context(), fake, &state, bulkPollFlags{wait: true, max: 3, interval: time.Nanosecond})
	if err != nil || fake.posts != 0 || fake.polls != 1 || !state.Terminal || state.Progress.Status.Value != "FAILED" {
		t.Fatalf("task observation submitted a new job or failed a valid read: %+v fake=%+v err=%v", state, fake, err)
	}
}

func TestMovePollingRefusesMalformedObservations(t *testing.T) {
	for _, body := range []string{"", `{"taskId":"OTHER","status":"RUNNING"}`, `{"taskId":"100"}`, `{"taskId":"100","status":"COMPLETE","totalIssueCount":2,"invalidOrInaccessibleIssueCount":0,"processedAccessibleIssues":[]}`} {
		t.Run(body, func(t *testing.T) {
			fake := moveFixture()
			valid := moveObservation(t, `{"taskId":"100","status":"RUNNING","progressPercent":40}`)
			var invalid *jira.BulkOperationProgress
			if body != "" {
				invalid = moveObservation(t, body)
			}
			fake.observations = []*jira.BulkOperationProgress{valid, invalid}
			state := envelope.BulkTaskState{Task: &envelope.BulkTaskRef{ID: "100"}}
			err := pollBulk(t.Context(), fake, &state, bulkPollFlags{wait: true, max: 3, interval: time.Nanosecond})
			if err == nil || cli.MapError(err).Code != cli.CodeBulkOperationResponseInvalid || state.Task.ID != "100" || state.Progress != valid || state.Polls != 2 || state.Terminal || state.PollLimitReached || fake.posts != 0 {
				t.Fatalf("malformed observation erased prior task facts: state=%+v err=%v", state, err)
			}
		})
	}
}

func TestMoveCancellationAfterAcceptanceRetainsTask(t *testing.T) {
	t.Parallel()
	fake := moveFixture()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fake.submitCancel = cancel
	body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}}}`
	out, err := runMove(ctx, moveClientDeps(fake), moveInvocation(body, "KEY-1"), moveOptions(false))
	if !errors.Is(err, context.Canceled) || !out.Acknowledged || out.Task == nil || out.Task.ID != "100" || fake.posts != 1 || fake.polls != 0 || out.Results[0].Data.Applied {
		t.Fatalf("post-acceptance cancellation lost task or fabricated completion: %+v fake=%+v err=%v", out, fake, err)
	}
}

func TestMoveExplicitFailureWithoutReasonRemainsFailure(t *testing.T) {
	t.Parallel()
	fake := moveFixture()
	fake.observations = []*jira.BulkOperationProgress{moveObservation(t, `{"taskId":"100","status":"COMPLETE","totalIssueCount":1,"invalidOrInaccessibleIssueCount":0,"processedAccessibleIssues":[],"failedAccessibleIssues":{"101":null},"progressPercent":100}`)}
	body := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}}}`
	out, err := runMove(t.Context(), moveClientDeps(fake), moveInvocation(body, "KEY-1"), moveOptions(true))
	if err == nil || cli.MapError(err).Code != cli.CodeBulkOperationFailed || out.Results[0].OK || out.Results[0].Err.Message == "" {
		t.Fatalf("explicit native failure was lost because its reason was null: %+v err=%v", out, err)
	}
}
