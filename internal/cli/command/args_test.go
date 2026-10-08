package command

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestNoArgsKeepsCobrasOwnValue pins the identity two separate
// mechanisms depend on.
//
// root.isNoArgs compares against cobra.NoArgs by FUNCTION POINTER to
// tell a mistyped subcommand from an argument miscount, and clib
// recovers a positional limit by regex-matching cobra's own error
// wording. A wrapper — however transparent — defeats both silently.
// Verified against cobra: storing the value preserves identity, a
// forwarding closure does not.
func TestNoArgsKeepsCobrasOwnValue(t *testing.T) {
	t.Parallel()
	got := ArgSpec{}.validator()
	if got == nil {
		t.Fatal("the zero ArgSpec produced no validator")
	}
	if reflect.ValueOf(got).Pointer() != reflect.ValueOf(cobra.PositionalArgs(cobra.NoArgs)).Pointer() {
		t.Error("the zero ArgSpec did not hand back cobra.NoArgs itself; " +
			"every runnable parent will report an argument miscount for a mistyped verb")
	}
}

// TestValidatorDelegatesEveryArityToCobra pins that the messages come
// from cobra, because clib parses them to recover the positional limit.
func TestValidatorDelegatesEveryArityToCobra(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		spec ArgSpec
		args []string
		want string
	}{
		{name: "exact", spec: ArgSpec{Min: 1, Max: 1}, args: []string{"a", "b"}, want: "accepts 1 arg(s), received 2"},
		{name: "maximum", spec: ArgSpec{Min: 0, Max: 2}, args: []string{"a", "b", "c"}, want: "accepts at most 2 arg(s), received 3"},
		{name: "range", spec: ArgSpec{Min: 1, Max: 2}, args: []string{"a", "b", "c"}, want: "accepts between 1 and 2 arg(s), received 3"},
		{name: "minimum", spec: ArgSpec{Min: 2, Max: AnyArgs}, args: []string{"a"}, want: "requires at least 2 arg(s), only received 1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.spec.validator()(&cobra.Command{}, tt.args)
			if err == nil {
				t.Fatalf("%v accepted %d arguments", tt.spec, len(tt.args))
			}
			if err.Error() != tt.want {
				t.Errorf("message = %q, want cobra's own %q — clib parses this to recover the positional limit", err.Error(), tt.want)
			}
		})
	}
}

// TestArbitraryArgsAcceptsAnything pins the unbounded case.
func TestArbitraryArgsAcceptsAnything(t *testing.T) {
	t.Parallel()
	if err := (ArgSpec{Min: 0, Max: AnyArgs}).validator()(&cobra.Command{}, []string{"a", "b", "c"}); err != nil {
		t.Errorf("an unbounded spec rejected arguments: %v", err)
	}
}

// TestAnnotationNeverEmitsABlankSlot pins the rule clib enforces: a
// blank entry in the CSV breaks `jira completion <shell>` for EVERY
// shell, because clib validates each entry as shell-safe and rejects
// the empty string.
//
// Positions without a predictor use an explicit empty predictor. This keeps
// later predictors in their declared slots and prevents a trailing argument
// from reusing an earlier predictor.
func TestAnnotationNeverEmitsABlankSlot(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		spec ArgSpec
		want string
	}{
		{name: "none declared", spec: ArgSpec{Positions: []Arg{{Name: "A"}, {Name: "B"}}}, want: ""},
		{name: "one", spec: ArgSpec{Positions: []Arg{{Name: "KEY", Predictor: "issuekey"}}}, want: "dynamic-args='issuekey'"},
		{name: "trailing blank suppressed", spec: ArgSpec{Positions: []Arg{{Name: "KEY", Predictor: "issuekey"}, {Name: "N"}}}, want: "dynamic-args='issuekey,none'"},
		{name: "two", spec: ArgSpec{Positions: []Arg{{Name: "K", Predictor: "configkey"}, {Name: "V", Predictor: "configvalue"}}}, want: "dynamic-args='configkey,configvalue'"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.spec.annotation()
			if err != nil {
				t.Fatalf("annotation: %v", err)
			}
			if got != tt.want {
				t.Errorf("annotation = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, ",'") || strings.Contains(got, ",,") || strings.Contains(got, "='") && strings.Contains(got, "=','") {
				t.Errorf("annotation carries a blank slot: %q", got)
			}
		})
	}
}

// A blank interior position retains its slot without shifting later predictors.
func TestInteriorBlankPredictorRetainsItsSlot(t *testing.T) {
	t.Parallel()
	got, err := (ArgSpec{Positions: []Arg{{Name: "A"}, {Name: "B", Predictor: "issuekey"}}}).annotation()
	if err != nil || got != "dynamic-args='none,issuekey'" {
		t.Fatalf("annotation=%q err=%v", got, err)
	}
}

func TestUncompletedPositionalDoesNotRepeatEarlierPredictor(t *testing.T) {
	got, err := (ArgSpec{Max: 2, Positions: []Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}, {Name: "COMMENT_ID"}}}).annotation()
	if err != nil || got != "dynamic-args='issuekey,none'" {
		t.Fatalf("annotation=%q err=%v; identifier must suppress issue-key completion", got, err)
	}
}
