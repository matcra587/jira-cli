package core

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestIssueCreationFollowupsRequireUsableNativeOutcomes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{name: "identity-only seam"},
		{name: "no followup", body: `{"id":"9001","key":"NEW-1","future":null}`},
		{name: "successful transition", body: `{"transition":{"status":200,"warningCollection":{"warnings":["fixture warning"]}}}`},
		{name: "successful watchers", body: `{"watchers":{"status":201,"future":false}}`},
		{name: "failed transition", body: `{"transition":{"status":400}}`, want: ErrIssueCreateFollowupFailed},
		{name: "failed watchers", body: `{"watchers":{"status":403}}`, want: ErrIssueCreateFollowupFailed},
		{name: "both failed", body: `{"transition":{"status":400},"watchers":{"status":500}}`, want: ErrIssueCreateFollowupFailed},
		{name: "null receipt", body: `null`, want: ErrIssueCreateResponseInvalid},
		{name: "array receipt", body: `[]`, want: ErrIssueCreateResponseInvalid},
		{name: "trailing receipt", body: `{} {}`, want: ErrIssueCreateResponseInvalid},
		{name: "null transition", body: `{"transition":null}`, want: ErrIssueCreateResponseInvalid},
		{name: "missing status", body: `{"watchers":{}}`, want: ErrIssueCreateResponseInvalid},
		{name: "null status", body: `{"transition":{"status":null}}`, want: ErrIssueCreateResponseInvalid},
		{name: "zero status", body: `{"transition":{"status":0}}`, want: ErrIssueCreateResponseInvalid},
		{name: "oversized status", body: `{"transition":{"status":600}}`, want: ErrIssueCreateResponseInvalid},
		{name: "string status", body: `{"transition":{"status":"200"}}`, want: ErrIssueCreateResponseInvalid},
		{name: "fractional status", body: `{"transition":{"status":200.5}}`, want: ErrIssueCreateResponseInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateIssueCreateFollowups(json.RawMessage(tc.body))
			if (err == nil) != (tc.want == nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("followup classification = %v, want %v", err, tc.want)
			}
		})
	}
}
