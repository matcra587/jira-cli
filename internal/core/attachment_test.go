package core

import (
	"errors"
	"testing"

	xslices "github.com/gechr/x/slices"

	"github.com/matcra587/jira-cli/internal/jira"
)

func TestAttachmentReceiptRequiresDistinctIdentitiesForEveryInput(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		ids      []jira.Nullable[string]
		expected int
		valid    bool
	}{
		{"complete", []jira.Nullable[string]{jira.NullableOf("10042"), jira.NullableOf("10043")}, 2, true},
		{"opaque-identity", []jira.Nullable[string]{jira.NullableOf("future-id")}, 1, true},
		{"no-input", nil, 0, false},
		{"no-receipt", nil, 1, false},
		{"partial", []jira.Nullable[string]{jira.NullableOf("10042")}, 2, false},
		{"extra", []jira.Nullable[string]{jira.NullableOf("10042"), jira.NullableOf("10043")}, 1, false},
		{"absent", []jira.Nullable[string]{{}}, 1, false},
		{"null", []jira.Nullable[string]{jira.NullableNull[string]()}, 1, false},
		{"empty", []jira.Nullable[string]{jira.NullableOf("")}, 1, false},
		{"repeated", []jira.Nullable[string]{jira.NullableOf("10042"), jira.NullableOf("10042")}, 2, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			attachments := xslices.Map(test.ids, func(id jira.Nullable[string]) jira.Attachment {
				return jira.Attachment{ID: id}
			})
			err := ValidateAttachmentReceipt(attachments, test.expected)
			if test.valid && err != nil || !test.valid && !errors.Is(err, ErrAttachmentResponseInvalid) {
				t.Fatalf("receipt validity=%t was misreported: %v", test.valid, err)
			}
		})
	}
}
