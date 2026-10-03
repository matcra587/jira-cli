package jira

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestBulkMoveNativeTargetBindingAndFidelity(t *testing.T) {
	t.Parallel()
	const input = `{"sendBulkNotification":null,"targetToSourcesMapping":{"Z,10001":{"issueIdsOrKeys":["KEY-2"],"future":{"n":9007199254740993,"markup":"<>&","empty":[],"null":null}},"A,10002,KEY-9":{"issueIdsOrKeys":["KEY-1","10003"],"inferFieldDefaults":false}},"future":false}`
	request, err := ParseIssueMovePayload([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	targets, err := request.MoveTargets()
	if err != nil || !slices.Equal(targets, []string{"KEY-1", "10003", "KEY-2"}) || !request.SendBulkNotification.Null || string(request.Extra["future"]) != "false" {
		t.Fatalf("native target binding changed: targets=%v request=%+v err=%v", targets, request, err)
	}
	encoded, err := MarshalNoEscape(request)
	if err != nil || !strings.Contains(string(encoded), `"sendBulkNotification":null`) || !strings.Contains(string(encoded), `"future":{"n":9007199254740993,"markup":"<>&","empty":[],"null":null}`) || !strings.Contains(string(encoded), `"future":false`) {
		t.Fatalf("native request values lost or coerced: %s %v", encoded, err)
	}
}

func TestBulkMoveRefusesAmbiguousTargets(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{}`, `{"targetToSourcesMapping":null}`, `{"targetToSourcesMapping":{}}`,
		`null`, `[]`, `{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":["KEY-1"]}}} {}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":["KEY-1"]}},"targetToSourcesMapping":{"B,10002":{"issueIdsOrKeys":["KEY-2"]}}}`,
		`{"targetToSourcesMapping":{"A":{"issueIdsOrKeys":["KEY-1"]}}}`, `{"targetToSourcesMapping":{"A,10001,KEY-9,extra":{"issueIdsOrKeys":["KEY-1"]}}}`,
		`{"targetToSourcesMapping":{",10001":{"issueIdsOrKeys":["KEY-1"]}}}`,
		`{"targetToSourcesMapping":{"A, 10001":{"issueIdsOrKeys":["KEY-1"]}}}`,
		`{"targetToSourcesMapping":{"A,10001":null}}`,
		`{"targetToSourcesMapping":{"A,10001":[]}}`,
		`{"targetToSourcesMapping":{"A,10001":{}}}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":null}}}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":[]}}}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":[null]}}}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":["KEY-1"," KEY-2"]}}}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":["KEY-1","KEY-1"]}}}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":["KEY-1"]},"B,10002":{"issueIdsOrKeys":["KEY-1"]}}}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":["KEY-1"],"issueIdsOrKeys":["KEY-2"]}}}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":["KEY-1"],"\u0069ssueIdsOrKeys":["KEY-2"]}}}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":["KEY-1"],"IssueIdsOrKeys":["KEY-2"]}}}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":["KEY-1"]}},"TargetToSourcesMapping":{"B,10002":{"issueIdsOrKeys":["KEY-2"]}}}`,
		`{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":["KEY-1"]}},"SendBulkNotification":false}`,
	} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseIssueMovePayload([]byte(input)); err == nil {
				t.Fatalf("unsafe move target declaration accepted by parser: %s", input)
			}
		})
	}
}

func TestBulkMoveExplicitIssueLimit(t *testing.T) {
	t.Parallel()
	for _, count := range []int{1000, 1001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			t.Parallel()
			sources := make([]string, count)
			for i := range sources {
				sources[i] = fmt.Sprintf("KEY-%d", i+1)
			}
			group, err := json.Marshal(map[string]any{"issueIdsOrKeys": sources})
			if err != nil {
				t.Fatal(err)
			}
			request := IssueBulkMovePayload{TargetToSourcesMapping: NullableOf(map[string]json.RawMessage{"A,10001": group})}
			targets, err := request.MoveTargets()
			if count == 1000 && (err != nil || len(targets) != count) {
				t.Fatalf("maximum legal request refused: count=%d %v", len(targets), err)
			}
			if count == 1001 && (err == nil || targets != nil) {
				t.Fatalf("over-limit request accepted: count=%d %v", len(targets), err)
			}
		})
	}
}

func TestBulkMoveRefusesRepeatedDestinationMappings(t *testing.T) {
	t.Parallel()
	for _, duplicate := range []string{`"A,10001"`, `"\u0041,10001"`} {
		input := `{"targetToSourcesMapping":{"A,10001":{"issueIdsOrKeys":["KEY-1"]},` + duplicate + `:{"issueIdsOrKeys":["KEY-2"]}}}`
		if _, err := ParseIssueMovePayload([]byte(input)); err == nil {
			t.Fatalf("duplicate destination silently discarded earlier targets: %s", input)
		}
	}
}

func TestBulkMoveSeamRefusesModeledExtraMembers(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"targetToSourcesMapping", "TargetToSourcesMapping", "sendBulkNotification", "SendBulkNotification"} {
		request := IssueBulkMovePayload{
			TargetToSourcesMapping: NullableOf(map[string]json.RawMessage{"A,10001": json.RawMessage(`{"issueIdsOrKeys":["KEY-1"]}`)}),
			Extra:                  map[string]json.RawMessage{key: json.RawMessage(`false`)},
		}
		if targets, err := request.MoveTargets(); err == nil || targets != nil {
			t.Fatalf("hand-built request bypassed native spelling guard: key=%q targets=%v err=%v", key, targets, err)
		}
	}
}

func TestBulkOperationProgressNativeFactsAndTerminalStates(t *testing.T) {
	t.Parallel()
	const input = `{"taskId":"100","status":"COMPLETE","totalIssueCount":4,"invalidOrInaccessibleIssueCount":1,"processedAccessibleIssues":[9007199254740993,10002],"failedAccessibleIssues":{"10003":["Unavailable destination"]},"progressPercent":100,"created":9007199254740993,"started":"2026-01-02T12:30:00Z","updated":null,"submittedBy":null,"future":{"n":9007199254740993,"markup":"<>&"}}`
	var progress BulkOperationProgress
	if err := json.Unmarshal([]byte(input), &progress); err != nil {
		t.Fatal(err)
	}
	if err := progress.ValidateTask("100"); err != nil || !progress.Terminal() || progress.ProcessedAccessibleIssues.Value[0] != 9007199254740993 || !progress.Updated.Null || !progress.SubmittedBy.Null || string(progress.Created.Value) != "9007199254740993" || string(progress.Started.Value) != `"2026-01-02T12:30:00Z"` || progress.FailedAccessibleIssues.Value["10003"][0] != "Unavailable destination" {
		t.Fatalf("complete mixed outcome or raw timestamps changed: %+v %v", progress, err)
	}
	encoded, err := MarshalNoEscape(progress)
	if err != nil || !strings.Contains(string(encoded), `"created":9007199254740993`) || !strings.Contains(string(encoded), `"updated":null`) || !strings.Contains(string(encoded), `"future":{"n":9007199254740993,"markup":"<>&"}`) {
		t.Fatalf("native progress values lost: %s %v", encoded, err)
	}
	for _, state := range []string{"ENQUEUED", "RUNNING", "COMPLETE", "FAILED", "CANCEL_REQUESTED", "CANCELLED", "DEAD"} { //nolint:misspell // Pin Jira's native status spelling independently.
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			var observation BulkOperationProgress
			body := `{"taskId":"100","status":"` + state + `","totalIssueCount":0,"invalidOrInaccessibleIssueCount":0,"processedAccessibleIssues":[]}`
			if err := json.Unmarshal([]byte(body), &observation); err != nil {
				t.Fatal(err)
			}
			wantTerminal := state == "COMPLETE" || state == "FAILED" || state == "CANCELLED" || state == "DEAD" //nolint:misspell // Preserve the native spelling in the independent expectation.
			if err := observation.ValidateTask("100"); err != nil || observation.Terminal() != wantTerminal {
				t.Fatalf("job state misclassified: %s terminal=%t %v", state, observation.Terminal(), err)
			}
		})
	}
}

func TestBulkOperationProgressRefusesContradictoryReceipts(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{}`, `{"taskId":null,"status":"RUNNING"}`, `{"taskId":"other","status":"RUNNING"}`,
		`{"taskId":"100"}`, `{"taskId":"100","status":null}`, `{"taskId":"100","status":"complete"}`,
		`{"taskId":"100","status":"RUNNING","progressPercent":-1}`,
		`{"taskId":"100","status":"RUNNING","progressPercent":101}`,
		`{"taskId":"100","status":"RUNNING","totalIssueCount":-1}`,
		`{"taskId":"100","status":"RUNNING","invalidOrInaccessibleIssueCount":-1}`,
		`{"taskId":"100","status":"RUNNING","processedAccessibleIssues":[0]}`,
		`{"taskId":"100","status":"RUNNING","processedAccessibleIssues":[1,1]}`,
		`{"taskId":"100","status":"RUNNING","failedAccessibleIssues":{"01":["reason"]}}`,
		`{"taskId":"100","status":"RUNNING","processedAccessibleIssues":[1],"failedAccessibleIssues":{"1":["reason"]}}`,
		`{"taskId":"100","status":"RUNNING","processedAccessibleIssues":[1],"totalIssueCount":0}`,
		`{"taskId":"100","status":"RUNNING","processedAccessibleIssues":[1],"invalidOrInaccessibleIssueCount":9223372036854775807}`,
		`{"taskId":"100","status":"COMPLETE"}`,
		`{"taskId":"100","status":"COMPLETE","totalIssueCount":1,"invalidOrInaccessibleIssueCount":0,"processedAccessibleIssues":null}`,
		`{"taskId":"100","status":"COMPLETE","totalIssueCount":2,"invalidOrInaccessibleIssueCount":0,"processedAccessibleIssues":[1]}`,
		`{"taskId":"100","status":"COMPLETE","totalIssueCount":1,"invalidOrInaccessibleIssueCount":0,"processedAccessibleIssues":[1],"progressPercent":99}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			var progress BulkOperationProgress
			if err := json.Unmarshal([]byte(body), &progress); err != nil {
				t.Fatal(err)
			}
			if err := progress.ValidateTask("100"); err == nil {
				t.Fatal("contradictory bulk progress accepted")
			}
		})
	}
}

func TestBulkOperationTaskIdentityRequired(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{}`, `{"taskId":null}`, `{"taskId":""}`, `{"taskId":" "}`, `{"taskId":"100 "}`} {
		var task SubmittedBulkOperation
		if err := json.Unmarshal([]byte(body), &task); err != nil {
			t.Fatal(err)
		}
		if err := task.ValidateIdentity(); err == nil {
			t.Fatalf("missing/blank task identity accepted: %s", body)
		}
	}
}
