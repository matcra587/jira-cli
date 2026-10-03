package output

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestFilterFailureRetainsUnfilteredReceipt(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want error
	}{
		{name: "evaluation", expr: `., error("stop")`, want: ErrFilterFailed},
		{name: "output-limit", expr: `"too long"`, want: ErrFilterTooLarge},
		{name: "canceled", expr: `.`, want: context.Canceled},
		{name: "timeout", expr: `repeat(.) | empty`, want: context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filter, err := CompileFilter(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			res := Assemble(Inputs{
				Op: "fixture", Data: struct {
					Key string `json:"key"`
				}{Key: "KEY-900"}, Filter: filter,
				RequestID: "original-request",
				Warnings:  []envelope.Warning{{Type: "fixture", Message: "preserve this warning"}},
			})
			ctx := t.Context()
			switch tc.name {
			case "output-limit":
				old := maxFilterOutput
				maxFilterOutput = 1
				t.Cleanup(func() { maxFilterOutput = old })
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "timeout":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer cancel()
			}
			var stdout bytes.Buffer
			err = Render(ctx, &stdout, FormatJSON, res)
			var failed *FilterError
			if !errors.As(err, &failed) || !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want receipt and %v", err, tc.want)
			}
			if !reflect.DeepEqual(failed.Envelope, res.Envelope) {
				t.Fatalf("unfiltered receipt changed: got %+v, want %+v", failed.Envelope, res.Envelope)
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed evaluation wrote %q", stdout.String())
			}
		})
	}
}

type canceledFilterWriter struct{}

func (canceledFilterWriter) Write([]byte) (int, error) { return 0, context.Canceled }

func TestFilterWriterFailureIsNotAnUntouchedProjection(t *testing.T) {
	filter, err := CompileFilter(".")
	if err != nil {
		t.Fatal(err)
	}
	res := Assemble(Inputs{Op: "fixture", Data: "receipt", Filter: filter})
	err = Render(t.Context(), canceledFilterWriter{}, FormatJSON, res)
	var writeErr *WriteError
	var filterErr *FilterError
	if !errors.As(err, &writeErr) || errors.As(err, &filterErr) {
		t.Fatalf("destination failure = %v; must remain a WriteError without an untouched projection receipt", err)
	}
}
