package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/attachmentfiles"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestMapError(t *testing.T) {
	t.Parallel()
	apiErr := func(status int) error { return &adapterjira.APIError{StatusCode: status} }
	tests := []struct {
		name     string
		err      error
		wantCode string
		wantExit int
	}{
		{name: "unauthorized", err: apiErr(401), wantCode: "jira_unauthorized", wantExit: ExitAuth},
		{name: "forbidden", err: apiErr(403), wantCode: "jira_forbidden", wantExit: ExitAuth},
		{name: "not found", err: apiErr(404), wantCode: "jira_not_found", wantExit: ExitNotFound},
		{name: "bad request", err: apiErr(400), wantCode: "jira_bad_request", wantExit: ExitValidation},
		{name: "precondition failed", err: apiErr(http.StatusPreconditionFailed), wantCode: CodeJiraPreconditionFailed, wantExit: ExitValidation},
		{name: "unmapped 4xx", err: apiErr(409), wantCode: "jira_request_failed", wantExit: ExitValidation},
		// 408 arrives as a Jira HTTP status here (via codeForStatus), a
		// different code path than context.DeadlineExceeded below — both
		// resolve to the same "timeout" code, but only this one exercises
		// codeForStatus's dedicated branch.
		{name: "request timeout status", err: apiErr(408), wantCode: "timeout", wantExit: ExitTimeout},
		{name: "rate limited", err: apiErr(429), wantCode: "jira_rate_limited", wantExit: ExitRateLimit},
		{name: "server error", err: apiErr(503), wantCode: "jira_server_error", wantExit: ExitServer},
		{name: "wrapped api error", err: fmt.Errorf("get issue: %w", apiErr(404)), wantCode: "jira_not_found", wantExit: ExitNotFound},
		{name: "malformed current account", err: fmt.Errorf("resolve: %w", adapterjira.ErrCurrentUserResponseInvalid), wantCode: CodeCurrentUserResponseInvalid, wantExit: ExitServer},
		{name: "canceled current account", err: errors.Join(adapterjira.ErrCurrentUserResponseInvalid, context.Canceled), wantCode: "canceled", wantExit: ExitCanceled},
		{name: "timed out current account", err: errors.Join(adapterjira.ErrCurrentUserResponseInvalid, context.DeadlineExceeded), wantCode: "timeout", wantExit: ExitTimeout},
		{name: "bulk operation receipt", err: fmt.Errorf("accepted move: %w", adapterjira.ErrBulkOperationResponseInvalid), wantCode: "bulk_operation_response_invalid", wantExit: ExitServer},
		{name: "JQL protocol failure", err: fmt.Errorf("parse: %w", adapterjira.ErrJQLResponseInvalid), wantCode: "jql_response_invalid", wantExit: ExitServer},
		{name: "invalid attachment input", err: fmt.Errorf("inspect: %w", attachmentfiles.ErrInvalid), wantCode: CodeAttachmentFileInvalid, wantExit: ExitValidation},
		{name: "uncertain attachment receipt", err: fmt.Errorf("upload: %w", core.ErrAttachmentResponseInvalid), wantCode: CodeAttachmentResponseInvalid, wantExit: ExitServer},
		{name: "uncertain comment receipt", err: fmt.Errorf("write: %w", core.ErrCommentResponseInvalid), wantCode: CodeCommentResponseInvalid, wantExit: ExitServer},
		{name: "uncertain transition receipt", err: fmt.Errorf("write: %w", core.ErrTransitionResponseInvalid), wantCode: CodeTransitionResponseInvalid, wantExit: ExitServer},
		{name: "canceled uncertain transition", err: errors.Join(core.ErrTransitionResponseInvalid, context.Canceled), wantCode: "canceled", wantExit: ExitCanceled},
		{name: "invalid transition discovery", err: fmt.Errorf("read: %w", core.ErrTransitionReadInvalid), wantCode: "transition_read_invalid", wantExit: ExitServer},
		{name: "canceled transition discovery", err: errors.Join(core.ErrTransitionReadInvalid, context.Canceled), wantCode: "canceled", wantExit: ExitCanceled},
		{name: "timed out transition discovery", err: errors.Join(core.ErrTransitionReadInvalid, context.DeadlineExceeded), wantCode: "timeout", wantExit: ExitTimeout},
		{name: "uncertain ranking receipt", err: fmt.Errorf("write: %w", core.ErrRankResponseInvalid), wantCode: CodeRankResponseInvalid, wantExit: ExitServer},
		{name: "uncertain issue deletion", err: fmt.Errorf("delete: %w", core.ErrIssueDeleteResponseInvalid), wantCode: CodeIssueDeleteResponseInvalid, wantExit: ExitServer},
		{name: "canceled uncertain issue deletion", err: errors.Join(core.ErrIssueDeleteResponseInvalid, context.Canceled), wantCode: "canceled", wantExit: ExitCanceled},
		{name: "timed out uncertain issue deletion", err: errors.Join(core.ErrIssueDeleteResponseInvalid, context.DeadlineExceeded), wantCode: "timeout", wantExit: ExitTimeout},
		{name: "uncertain remote-link receipt", err: fmt.Errorf("write: %w", core.ErrWeblinkResponseInvalid), wantCode: CodeWeblinkResponseInvalid, wantExit: ExitServer},
		{name: "canceled uncertain comment", err: errors.Join(core.ErrCommentResponseInvalid, context.Canceled), wantCode: "canceled", wantExit: ExitCanceled},
		{name: "uncertain attachment beats local input", err: errors.Join(core.ErrAttachmentResponseInvalid, attachmentfiles.ErrInvalid), wantCode: CodeAttachmentResponseInvalid, wantExit: ExitServer},
		{name: "canceled uncertain attachment", err: errors.Join(core.ErrAttachmentResponseInvalid, context.Canceled), wantCode: "canceled", wantExit: ExitCanceled},
		{name: "uncertain issue creation", err: fmt.Errorf("write: %w", core.ErrIssueCreateResponseInvalid), wantCode: CodeIssueCreateResponseInvalid, wantExit: ExitServer},
		{name: "failed creation followup", err: fmt.Errorf("write: %w", core.ErrIssueCreateFollowupFailed), wantCode: CodeIssueCreateFollowupFailed, wantExit: ExitServer},
		{name: "canceled issue creation", err: errors.Join(core.ErrIssueCreateResponseInvalid, context.Canceled), wantCode: "canceled", wantExit: ExitCanceled},
		{name: "expired creation followup", err: errors.Join(core.ErrIssueCreateFollowupFailed, context.DeadlineExceeded), wantCode: "timeout", wantExit: ExitTimeout},
		{name: "canceled", err: context.Canceled, wantCode: "canceled", wantExit: ExitCanceled},
		{name: "deadline", err: context.DeadlineExceeded, wantCode: "timeout", wantExit: ExitTimeout},
		{name: "wrapped cancellation", err: fmt.Errorf("get issue: %w", context.Canceled), wantCode: "canceled", wantExit: ExitCanceled},
		// A chain carrying both a typed API error and a cancellation
		// resolves to the cancellation — it is the truth of what happened.
		{name: "cancellation beats api error", err: errors.Join(apiErr(429), context.Canceled), wantCode: "canceled", wantExit: ExitCanceled},
		{name: "untyped fallback", err: errors.New("something odd"), wantCode: "validation_failed", wantExit: ExitValidation},
		{name: "unknown command", err: NewInputError(CodeCommandUnknown, `unknown command "verson"`), wantCode: "command_unknown", wantExit: ExitValidation},
		{name: "unknown flag", err: NewInputError(CodeFlagUnknown, "unknown flag: --outpt"), wantCode: "flag_unknown", wantExit: ExitValidation},
		{name: "foreign flag", err: NewInputError(CodeFlagForeign, "unknown flag: --template"), wantCode: "flag_foreign", wantExit: ExitValidation},
		{name: "wrapped input error", err: fmt.Errorf("dispatch: %w", NewInputError(CodeArgValueInvalid, "unknown shell")), wantCode: "arg_value_invalid", wantExit: ExitValidation},
		{name: "output write failure", err: &output.WriteError{Err: errors.New("broken pipe")}, wantCode: "output_write_failed", wantExit: ExitOutput},
		{name: "wrapped output write failure", err: fmt.Errorf("render: %w", &output.WriteError{Err: errors.New("broken pipe")}), wantCode: "output_write_failed", wantExit: ExitOutput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MapError(tt.err)
			if got.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", got.Code, tt.wantCode)
			}
			if got.Exit != tt.wantExit {
				t.Errorf("exit = %d, want %d", got.Exit, tt.wantExit)
			}
			if got.Exit != ExitCode(tt.err) {
				t.Errorf("ExitCode disagrees with MapError: %d vs %d", ExitCode(tt.err), got.Exit)
			}
			if got.Message == "" {
				t.Error("message is empty; the original error text must survive")
			}
		})
	}
}

func TestMapErrorFieldIsDeterministic(t *testing.T) {
	t.Parallel()
	err := &adapterjira.APIError{
		StatusCode:  400,
		FieldErrors: map[string]string{"summary": "too long", "assignee": "unknown", "priority": "bad"},
	}
	for range 20 {
		if got := MapError(err).Field; got != "assignee" {
			t.Fatalf("field = %q, want the first sorted key %q", got, "assignee")
		}
	}
}

// TestRegistryIsComplete keeps the fail-safe branch in newError from ever
// shipping: every code the mapper can produce has a row, and every row
// carries a non-empty hint (an error that resolves must say what to do).
func TestRegistryIsComplete(t *testing.T) {
	t.Parallel()
	statuses := []int{400, 401, 403, 404, 405, 409, 412, 429, 500, 502, 503}
	codes := map[string]bool{"canceled": true, "timeout": true, "validation_failed": true, "output_write_failed": true, CodeRedirectRefused: true, CodeAPIResponseIncomplete: true}
	for _, err := range []error{attachmentfiles.ErrInvalid, core.ErrAttachmentResponseInvalid, core.ErrCommentResponseInvalid, core.ErrTransitionResponseInvalid, core.ErrTransitionReadInvalid, core.ErrWeblinkResponseInvalid, core.ErrRankResponseInvalid, core.ErrIssueDeleteResponseInvalid, core.ErrIssueCreateResponseInvalid, core.ErrIssueCreateFollowupFailed} {
		codes[MapError(err).Code] = true
	}
	for _, s := range statuses {
		codes[codeForStatus(s)] = true
	}
	// The input codes reach the mapper through the InputError adapter;
	// the shared list keeps a new code from shipping without its row.
	for _, c := range inputCodes {
		codes[c] = true
	}
	// Codes the CLI diagnoses itself reach the mapper through
	// CodedError, which exists because not every self-diagnosed failure
	// is a validation failure — "not logged in" exits 1, not 3.
	for _, c := range codedCodes {
		codes[c] = true
	}
	for code := range codes {
		s, ok := registry[code]
		if !ok {
			t.Errorf("code %q reachable from the mapper has no registry row", code)
			continue
		}
		if s.hint == "" {
			t.Errorf("code %q has no hint; every code must carry one", code)
		}
		if s.exit == ExitOK {
			t.Errorf("code %q maps to exit 0; an error can never exit clean", code)
		}
	}
	for code := range registry {
		if !codes[code] {
			t.Errorf("registry row %q is unreachable from the mapper", code)
		}
	}
}

func TestMapErrorNilIsZero(t *testing.T) {
	t.Parallel()
	if got := MapError(nil); !reflect.DeepEqual(got, Classification{}) {
		t.Fatalf("MapError(nil) = %+v, want zero Error", got)
	}
}

// TestMapErrorCarriesOffenderAndSuggestions pins the input-error adapter's
// data half: the offender and the candidates must survive mapping, or the
// envelope's field and suggestions would silently go empty.
func TestMapErrorCarriesOffenderAndSuggestions(t *testing.T) {
	t.Parallel()
	in := NewInputError(CodeFlagUnknown, "unknown flag: --outpt")
	in.Field = "outpt"
	in.Suggestions = []string{"--output"}
	got := MapError(fmt.Errorf("dispatch: %w", in))
	if got.Field != "outpt" {
		t.Errorf("field = %q, want %q", got.Field, "outpt")
	}
	if !reflect.DeepEqual(got.Suggestions, []string{"--output"}) {
		t.Errorf("suggestions = %v, want [--output]", got.Suggestions)
	}
}

// TestInputCodesStayValidation pins the exit-stability half of the input
// taxonomy: every input code classifies as validation (exit 3), the same
// exit the collapsed validation_failed carried — the codes sharpen the
// class without moving the published exit contract.
func TestInputCodesStayValidation(t *testing.T) {
	t.Parallel()
	for _, code := range inputCodes {
		if got := MapError(NewInputError(code, "boom")).Exit; got != ExitValidation {
			t.Errorf("code %q exit = %d, want %d", code, got, ExitValidation)
		}
	}
}

// TestNewErrorUnknownCodeFailsSafe pins newError's own defensive branch
// (a code with no registry row is a programmer error, unreachable from
// MapError today — every code MapError can produce has a row, per
// TestRegistryIsComplete). It fails safe to the validation exit and
// preserves the original error's message, but carries no hint: the
// fail-safe path never invents one from a registry row that isn't there.
func TestNewErrorUnknownCodeFailsSafe(t *testing.T) {
	t.Parallel()
	err := errors.New("boom")
	got := newError("no_such_code", err)
	want := Classification{Code: "no_such_code", Message: "boom", Exit: ExitValidation}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("newError(%q, err) = %+v, want %+v", "no_such_code", got, want)
	}
}

// TestMapErrorCarriesRetryFacts pins the machine-readable half of the
// rate-limit contract: the hint tells a human to wait out the pause, and
// the envelope owes an agent the actual numbers.
func TestMapErrorCarriesRetryFacts(t *testing.T) {
	t.Parallel()
	apiErr := &adapterjira.APIError{
		StatusCode:        http.StatusTooManyRequests,
		RetryAfterSeconds: 42,
		Retry:             core.RetryInfo{Attempts: 3, Waited: 1500 * time.Millisecond},
	}
	got := MapError(fmt.Errorf("wrapped: %w", apiErr))

	if got.Code != "jira_rate_limited" {
		t.Fatalf("code = %q", got.Code)
	}
	if got.RetryAfterSeconds != 42 || got.Attempts != 3 || got.Waited != 1500*time.Millisecond {
		t.Errorf("retry facts dropped: after=%d attempts=%d waited=%v", got.RetryAfterSeconds, got.Attempts, got.Waited)
	}
}

// TestTransportSentinelsKeepTheirOwnCodes pins the mapping, not just
// the registry row.
//
// The completeness test above only asks that every listed code HAS a
// row — a code can sit in the list, carry a perfect hint, and never be
// produced, because the mapper case that reaches it was dropped. Then
// the failure quietly falls back to "validation_failed" and its generic
// advice, which is precisely the outcome these codes exist to avoid.
func TestTransportSentinelsKeepTheirOwnCodes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "cleartext scheme",
			err:  fmt.Errorf("jira client: http is %w — token in the clear", adapterjira.ErrInsecureScheme),
			want: CodeInsecureScheme,
		},
		{
			name: "read-only",
			err:  fmt.Errorf("blocked: %w", adapterjira.ErrReadOnly),
			want: CodeReadOnlyBlocked,
		},
		{
			name: "unsafe path",
			err:  fmt.Errorf("bad segment: %w", adapterjira.ErrUnsafePath),
			want: CodePathInvalid,
		},
		{
			name: "unsafe redirect",
			err:  fmt.Errorf("request failed: %w", adapterjira.ErrUnsafeRedirect),
			want: CodeRedirectRefused,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MapError(tt.err)
			if got.Code != tt.want {
				t.Errorf("code = %q, want %q — the sentinel fell through to the generic row", got.Code, tt.want)
			}
			if got.Hint != registry[tt.want].hint {
				t.Errorf("hint = %q, want the row's own hint", got.Hint)
			}
		})
	}
}
