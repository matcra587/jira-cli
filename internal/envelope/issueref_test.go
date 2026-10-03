package envelope_test

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// pins the compile-time contract: IssueRef satisfies Ref (the marker
// interface every identity-slot type must implement).
var _ envelope.Ref = envelope.IssueRef{}

func TestIssueRefStringRendersKey(t *testing.T) {
	tests := []struct {
		name string
		ref  envelope.IssueRef
		want string
	}{
		{
			name: "key only, id and self absent",
			ref:  envelope.IssueRef{Key: "PROJ-1"},
			want: "PROJ-1",
		},
		{
			name: "id and self present, String still renders the key",
			ref:  envelope.IssueRef{ID: "10001", Key: "PROJ-2", Self: "https://example.atlassian.net/rest/api/3/issue/10001"},
			want: "PROJ-2",
		},
		{
			name: "empty ref renders the empty string",
			ref:  envelope.IssueRef{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIssueRefJSONOmitsEmptyIDAndSelfButAlwaysCarriesKey(t *testing.T) {
	tests := []struct {
		name string
		ref  envelope.IssueRef
		want string
	}{
		{
			name: "key only",
			ref:  envelope.IssueRef{Key: "PROJ-1"},
			want: `{"key":"PROJ-1"}`,
		},
		{
			name: "empty key still emits the key field",
			ref:  envelope.IssueRef{},
			want: `{"key":""}`,
		},
		{
			name: "id, key, and self all present",
			ref:  envelope.IssueRef{ID: "10001", Key: "PROJ-1", Self: "https://example.invalid/issue/10001"},
			want: `{"id":"10001","key":"PROJ-1","self":"https://example.invalid/issue/10001"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.ref)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tt.want {
				t.Errorf("marshal(%+v) = %s, want %s", tt.ref, b, tt.want)
			}
		})
	}
}
