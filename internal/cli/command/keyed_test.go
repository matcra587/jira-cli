package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAddKeyedFlagsRegistersParallelismWithItsDefault(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	var keyed KeyedFlags
	AddKeyedFlags(cmd, &keyed)

	flag := cmd.Flags().Lookup(FlagParallelism)
	if flag == nil {
		t.Fatalf("--%s was not registered", FlagParallelism)
	}
	if flag.Hidden {
		t.Errorf("--%s is hidden; it is documented surface, not an alias", FlagParallelism)
	}
	if keyed.Parallelism != DefaultParallelism {
		t.Errorf("Parallelism = %d, want the default %d bound into the struct", keyed.Parallelism, DefaultParallelism)
	}
	// The bounds belong in the help text: a caller who guesses 100 should
	// be able to find the ceiling without running the command and reading
	// the refusal.
	if !strings.Contains(flag.Usage, "1-16") {
		t.Errorf("usage = %q, want it to state the accepted range", flag.Usage)
	}
}

func TestKeyedParallelismOverrideKeepsBoundsAndShortFlag(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "refresh"}
	var flags KeyedFlags
	AddKeyedFlags(cmd, &flags, WithParallelismDefault(1), WithParallelismShorthand("p"))
	if flags.Parallelism != 1 || cmd.Flags().Lookup(FlagParallelism).DefValue != "1" {
		t.Fatalf("refresh default=%d, want 1", flags.Parallelism)
	}
	if err := cmd.ParseFlags([]string{"-p", "16"}); err != nil || flags.Parallelism != 16 || flags.Validate() != nil {
		t.Fatalf("refresh -p16=%d, %v", flags.Parallelism, err)
	}
	if err := cmd.ParseFlags([]string{"--parallelism=17"}); err != nil || flags.Validate() == nil {
		t.Fatalf("refresh bound accepted=%d, %v", flags.Parallelism, err)
	}
}

func TestKeyedParallelismRejectsInvalidDefault(t *testing.T) {
	t.Parallel()
	for _, value := range []int{0, 17} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("AddKeyedFlags default=%d accepted invalid wiring", value)
				}
			}()
			AddKeyedFlags(&cobra.Command{Use: "refresh"}, &KeyedFlags{}, WithParallelismDefault(value))
		})
	}
}

func TestResourceFailureUsesSharedSeverityAndOriginalCauses(t *testing.T) {
	t.Parallel()
	missing := cli.NewCodedError("jira_not_found", "missing resource")
	storage := cli.NewCodedError(cli.CodeCacheIO, "storage failed")
	badBoard := cli.NewCodedError(cli.CodeBoardResponseInvalid, "board response invalid")
	badSearch := cli.NewCodedError(cli.CodeSearchResponseInvalid, "search response invalid")
	for _, cause := range []error{storage, badBoard, badSearch} {
		rows := []envelope.ResourceResult[int]{
			{Resource: "fields", Err: &envelope.KeyError{Code: "jira_not_found", Message: "missing"}},
			{Resource: "boards", Err: &envelope.KeyError{Code: cli.MapError(cause).Code, Message: "failed"}},
		}
		if err := KeyedFailure(rows, []error{missing, cause}); !errors.Is(err, cause) {
			t.Fatalf("KeyedFailure(resource %s)=%v, want original severe cause", rows[1].Err.Code, err)
		}
		rows[0].OK = true
		if err := KeyedFailure(rows, []error{nil, cause}); err != nil {
			t.Fatalf("KeyedFailure(mixed resources)=%v, want retained partial success", err)
		}
	}
	rows := []envelope.ResourceResult[int]{{Resource: "fields", Err: &envelope.KeyError{Code: cli.CodeCacheIO, Message: "first"}}, {Resource: "boards", Err: &envelope.KeyError{Code: cli.CodeBoardResponseInvalid, Message: "second"}}}
	if err := KeyedFailure(rows, []error{storage, badBoard}); !errors.Is(err, storage) {
		t.Fatalf("KeyedFailure(equal resource severity)=%v, want earliest original cause", err)
	}
}

// TestWithoutParallelismRegistersNothingAndPinsSequential is `issue
// rank`'s case: a keyed command whose keys MUST run one at a time gets
// no flag at all, and its KeyedFlags still answers the question an
// executor asks it.
func TestWithoutParallelismRegistersNothingAndPinsSequential(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	var keyed KeyedFlags
	AddKeyedFlags(cmd, &keyed, WithoutParallelism("anchored moves are order-sensitive"))

	if cmd.Flags().Lookup(FlagParallelism) != nil {
		t.Errorf("--%s was registered on a command that opted out", FlagParallelism)
	}
	if keyed.Parallelism != MinParallelism {
		t.Errorf("Parallelism = %d, want it pinned to %d so an executor reads a usable width", keyed.Parallelism, MinParallelism)
	}
	if err := keyed.Validate(); err != nil {
		t.Errorf("Validate on an opted-out command = %v, want nil", err)
	}
}

// TestWithoutParallelismDemandsAReason keeps the opt-out from becoming a
// silent default: the whole point of an explicit one is that somebody
// stated why.
func TestWithoutParallelismDemandsAReason(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("AddKeyedFlags accepted an opt-out with no reason")
		}
	}()
	AddKeyedFlags(&cobra.Command{Use: "x"}, &KeyedFlags{}, WithoutParallelism(""))
}

func TestKeyedFlagsValidateBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		value   int
		wantErr bool
	}{
		{name: "below the floor", value: 0, wantErr: true},
		{name: "negative", value: -1, wantErr: true},
		{name: "the floor", value: MinParallelism},
		{name: "the default", value: DefaultParallelism},
		{name: "the ceiling", value: MaxParallelism},
		{name: "above the ceiling", value: MaxParallelism + 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := KeyedFlags{Parallelism: tc.value}.Validate()
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Validate(%d) = %v, want nil", tc.value, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate(%d) = nil, want a refusal — clamping would leave the caller with a wrong expectation", tc.value)
			}
			var input *cli.InputError
			if !errors.As(err, &input) {
				t.Fatalf("Validate(%d) = %T, want a typed input error", tc.value, err)
			}
			if input.Code != cli.CodeFlagValueInvalid {
				t.Errorf("code = %q, want %q", input.Code, cli.CodeFlagValueInvalid)
			}
			if input.Field != FlagParallelism {
				t.Errorf("field = %q, want the offending flag", input.Field)
			}
		})
	}
}

// keyedSpec is a minimal Spec over an op that IS registered — the keyed
// cross-check runs inside New, so a fixture has to get far enough into
// it to reach the check.
func keyedSpec(keyed bool, flags func(*cobra.Command)) Spec[envelope.IssueLinkListOutput] {
	return Spec[envelope.IssueLinkListOutput]{
		Op:    "issue.link.list",
		Use:   "list KEY",
		Short: "fixture",
		Keyed: keyed,
		Flags: flags,
		Run: func(context.Context, Invocation) (envelope.IssueLinkListOutput, error) {
			return envelope.IssueLinkListOutput{}, nil
		},
	}
}

// TestNewRefusesKeyedWithoutTheSharedSurface and its sibling below pin
// the invariant AddKeyedFlags exists to keep: the Spec's Keyed
// declaration and the shared surface are two statements about one fact,
// and a command that makes only one of them is a wiring mistake either
// way round.
func TestNewRefusesKeyedWithoutTheSharedSurface(t *testing.T) {
	_, err := New(keyedSpec(true, nil))
	if err == nil {
		t.Fatal("New accepted a Keyed command that never called AddKeyedFlags")
	}
	if !strings.Contains(err.Error(), "AddKeyedFlags") {
		t.Errorf("error = %v, want it to name the call that is missing", err)
	}
}

func TestNewRefusesTheSharedSurfaceWithoutKeyed(t *testing.T) {
	_, err := New(keyedSpec(false, func(c *cobra.Command) {
		AddKeyedFlags(c, &KeyedFlags{})
	}))
	if err == nil {
		t.Fatal("New accepted a command that binds --parallelism while declaring a single result")
	}
	if !strings.Contains(err.Error(), "Keyed") {
		t.Errorf("error = %v, want it to name the declaration that is missing", err)
	}
}

func TestNewAcceptsAKeyedCommandThatDeclaresBoth(t *testing.T) {
	cmd, err := New(keyedSpec(true, func(c *cobra.Command) {
		AddKeyedFlags(c, &KeyedFlags{})
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if cmd.Flags().Lookup(FlagParallelism) == nil {
		t.Errorf("--%s missing from the built command", FlagParallelism)
	}
}
