package root

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func rootReadVariantFixture(t *testing.T, args *command.ArgSpec, validation error, calls *int) *cobra.Command {
	t.Helper()
	c, err := command.New(command.Spec[envelope.IssueCreateOutput]{
		Op: "issue.create", Use: "probe", Short: "Probe selected read errors", WritesJira: true,
		Args: command.ArgSpec{Max: command.AnyArgs},
		Flags: func(c *cobra.Command) {
			var mutation command.MutationFlags
			command.AddMutationFlags(c, &mutation)
			var inspect bool
			command.AddBoolVar(c, &inspect, "inspect", false, "Read instead of mutating", command.FlagOpts{})
		},
		Read: command.WhenBoolRead("inspect", command.ReadSpec[envelope.VersionOutput]{Op: "version", Args: args, Validate: func() error { return validation }, Run: func(context.Context, command.Invocation) (envelope.VersionOutput, error) {
			*calls++
			return envelope.VersionOutput{}, nil
		}}),
		Run: func(context.Context, command.Invocation) (envelope.IssueCreateOutput, error) {
			*calls++
			return envelope.IssueCreateOutput{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestReadVariantRootAttributesPreHandlerFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		arity      *command.ArgSpec
		validation error
		wantCode   string
	}{
		{name: "argument", args: []string{"probe", "--inspect", "--output=json"}, arity: &command.ArgSpec{Min: 1, Max: command.AnyArgs}, wantCode: cli.CodeArgCountInvalid},
		{name: "flag", args: []string{"probe", "--inspect", "--unknown-option", "--output=json"}, wantCode: cli.CodeFlagUnknown},
		{name: "input", args: []string{"probe", "--inspect", "--output=json"}, validation: cli.NewInputError(cli.CodeFlagValueInvalid, "invalid read context"), wantCode: cli.CodeFlagValueInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			calls := 0
			deps := &Deps{Detect: func() cli.Detection { return cli.Detection{} }}
			program, sink := newProgram(deps, &stdout, io.Discard)
			program.Root.AddCommand(rootReadVariantFixture(t, tc.arity, tc.validation, &calls))
			typeArgErrors(program.Root)
			exit := execute(t.Context(), program, sink, tc.args)
			var frame struct {
				OK     bool             `json:"ok"`
				Meta   envelope.Meta    `json:"meta"`
				Errors []envelope.Error `json:"errors"`
			}
			if json.Unmarshal(stdout.Bytes(), &frame) != nil || exit != cli.ExitValidation || frame.OK || frame.Meta.Command != "version" || len(frame.Errors) != 1 || frame.Errors[0].Code != tc.wantCode || calls != 0 {
				t.Fatalf("pre-handler read failure misattributed: exit=%d calls=%d frame=%s", exit, calls, stdout.Bytes())
			}
			if strings.Contains(frame.Errors[0].Hint, "verify mutation outcomes") {
				t.Fatal("read failure inherited mutation recovery instructions")
			}
		})
	}
}

func TestReadVariantRootUsesSelectedNoArgsClassification(t *testing.T) {
	calls := 0
	c := rootReadVariantFixture(t, &command.ArgSpec{}, nil, &calls)
	c.AddCommand(&cobra.Command{Use: "status", RunE: func(*cobra.Command, []string) error { return nil }})
	typeArgErrors(c)
	if err := c.Flags().Set("inspect", "true"); err != nil {
		t.Fatal(err)
	}
	err := c.Args(c, []string{"bogus"})
	if err == nil || cli.MapError(err).Code != cli.CodeCommandUnknown {
		t.Fatalf("selected NoArgs parent lost unknown-command classification: %v", err)
	}
	if err := c.Flags().Set("inspect", "false"); err != nil {
		t.Fatal(err)
	}
	if err := c.Args(c, []string{"literal"}); err != nil {
		t.Fatalf("default positional acceptance changed: %v", err)
	}
}

func TestReadVariantFailureSeedDoesNotRetainPreviousCommand(t *testing.T) {
	calls := 0
	c := rootReadVariantFixture(t, nil, nil, &calls)
	root := &cobra.Command{Use: "jira"}
	root.AddCommand(c)
	command.AddOutputFlags(root)
	sink := &failureSink{stdout: io.Discard, detect: func() cli.Detection { return cli.Detection{} }}
	sink.seed(root, []string{"probe", "--output=json"})
	if sink.command != c {
		t.Fatal("seed did not retain selected command")
	}
	sink.seed(root, []string{"not-a-command", "--output=json"})
	if sink.command != nil || sink.op != "root" {
		t.Fatal("unknown invocation retained previous operation")
	}
}

func TestReadVariantRootPreservesActualDefaultValidatorClassification(t *testing.T) {
	c, err := command.New(command.Spec[envelope.VersionOutput]{
		Op: "version", Use: "probe", Short: "Probe default validator attribution",
		Run: func(context.Context, command.Invocation) (envelope.VersionOutput, error) {
			return envelope.VersionOutput{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A host may supply its actual validator after shared construction.
	c.Args = cobra.ExactArgs(1)
	c.AddCommand(&cobra.Command{Use: "status", RunE: func(*cobra.Command, []string) error { return nil }})
	typeArgErrors(c)
	err = c.Args(c, []string{"one", "two"})
	if err == nil || cli.MapError(err).Code != cli.CodeArgCountInvalid {
		t.Fatalf("default argument miscount relabeled as unknown command: %v", err)
	}
}
