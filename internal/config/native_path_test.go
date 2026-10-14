package config

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/matcra587/jira-cli/internal/nativepath"
)

func TestNativeConfigurationPathOnlyAdmitsInitialAbsence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		cause   error
		missing bool
	}{
		{"initial object missing", errors.Join(nativepath.ErrMissing, os.ErrNotExist), true},
		{"existing object", nil, false},
		{"metadata resolution missing", &os.PathError{Op: "resolve path metadata", Path: "selected", Err: os.ErrNotExist}, false},
		{"close missing", errors.Join(errors.New("close metadata handle"), os.ErrNotExist), false},
		{"canceled metadata", errors.Join(context.Canceled, os.ErrNotExist), false},
		{"permission refusal", os.ErrPermission, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			got, err := resolveNativeConfigurationPath("selected", func(path string) (string, error) {
				calls++
				if path != "selected" {
					t.Fatalf("resolver selected another input: %q", path)
				}
				return "canonical", tc.cause
			})
			if calls != 1 {
				t.Fatalf("resolver was replayed: calls=%d", calls)
			}
			if tc.missing {
				if got != "selected" || err != nil {
					t.Fatalf("initial absence could not prepare creation: path=%q error=%v", got, err)
				}
				return
			}
			if got != "canonical" || err != tc.cause { //nolint:errorlint // This owner must preserve the exact error object.
				t.Fatalf("existing-object outcome was replaced by absence: path=%q error=%v want=%v", got, err, tc.cause)
			}
		})
	}
}
