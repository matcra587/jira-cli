package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func readVariantFixture(read *ReadVariant, flags func(*cobra.Command), run func(context.Context, Invocation) (envelope.IssueCreateOutput, error)) (*cobra.Command, error) {
	return New(Spec[envelope.IssueCreateOutput]{
		Op: "issue.create", Use: "probe", Short: "Probe typed read selection", WritesJira: true,
		Flags: func(c *cobra.Command) {
			var mutation MutationFlags
			AddMutationFlags(c, &mutation)
			if flags != nil {
				flags(c)
			}
		},
		Read: read, Run: run,
	})
}

func registerReadVariantFlag(c *cobra.Command) {
	var inspect bool
	AddBoolVar(c, &inspect, "inspect", false, "Read instead of mutating", FlagOpts{})
}

func TestReadVariantSelectsSeparatelyTypedOperation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		selected bool
		op       string
		reads    int
		writes   int
	}{
		{name: "default", op: "issue.create", writes: 1},
		{name: "read", selected: true, op: "version", reads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reads, writes := 0, 0
			read := WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "version", Run: func(_ context.Context, inv Invocation) (envelope.VersionOutput, error) {
				reads++
				data, err := io.ReadAll(inv.Stdin)
				if err != nil || string(data) != "native input" || inv.Version != "fixture-version" {
					t.Fatalf("invocation facts lost: %q %s %v", data, inv.Version, err)
				}
				return envelope.VersionOutput{Version: "native-read"}, nil
			}})
			c, err := readVariantFixture(read, registerReadVariantFlag, func(context.Context, Invocation) (envelope.IssueCreateOutput, error) {
				writes++
				return envelope.IssueCreateOutput{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			c.Version = "fixture-version"
			c.SetIn(strings.NewReader("native input"))
			var stdout bytes.Buffer
			c.SetOut(&stdout)
			c.SetErr(io.Discard)
			if tc.selected {
				c.SetArgs([]string{"--inspect"})
			} else {
				c.SetArgs(nil)
			}
			if err := c.ExecuteContext(cli.WithFormat(t.Context(), output.FormatJSON)); err != nil {
				t.Fatal(err)
			}
			var frame struct {
				OK   bool            `json:"ok"`
				Meta envelope.Meta   `json:"meta"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil || !frame.OK {
				t.Fatalf("missing typed result: %s %v", stdout.Bytes(), err)
			}
			if frame.Meta.Command != tc.op || reads != tc.reads || writes != tc.writes {
				t.Fatalf("wrong operation or dispatch: %s reads=%d writes=%d", frame.Meta.Command, reads, writes)
			}
			meta, active := SelectedReadMeta(c)
			if active != tc.selected || tc.selected && (meta.Op != "version" || meta.Class() != ClassRead) {
				t.Fatalf("read classification changed: %+v %v", meta, active)
			}
			if tc.selected && (bytes.Contains(frame.Data, []byte(`"dry_run"`)) || !bytes.Contains(frame.Data, []byte(`"version":"native-read"`))) {
				t.Fatal("read emitted the mutation output schema")
			}
		})
	}
}

func TestReadVariantRetainsReadReceiptAndOriginalCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("read interrupted")
	data := envelope.VersionOutput{Version: "observed"}
	c, err := readVariantFixture(WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{
		Op: "version", Run: func(context.Context, Invocation) (envelope.VersionOutput, error) { return data, cause },
		RetainOnError: func(v envelope.VersionOutput) bool { return v.Version != "" },
		Warnings: func(envelope.VersionOutput) []envelope.Warning {
			return []envelope.Warning{{Type: "observation", Message: "retained read"}}
		},
		Human: func(*output.View, envelope.Envelope) error { return nil },
	}), registerReadVariantFlag, func(context.Context, Invocation) (envelope.IssueCreateOutput, error) {
		t.Fatal("mutation handler ran")
		return envelope.IssueCreateOutput{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Flags().Set("inspect", "true"); err != nil {
		t.Fatal(err)
	}
	c.SetContext(t.Context())
	err = c.RunE(c, nil)
	var retained *cli.ResultError
	if !errors.Is(err, cause) || !errors.As(err, &retained) || retained.Op != "version" || retained.Mutation || retained.Human == nil || len(retained.Warnings) != 1 {
		t.Fatalf("read failure lost identity/classification/cause: %+v %v", retained, err)
	}
	observed, ok := retained.Data.(envelope.VersionOutput)
	if !ok || observed.Version != "observed" {
		t.Fatal("read receipt retyped as mutation data")
	}
}

func TestReadVariantValidationPrecedesBothHandlers(t *testing.T) {
	t.Parallel()
	refusal := errors.New("read input invalid")
	calls := 0
	c, err := readVariantFixture(WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "version", Validate: func() error { return refusal }, Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
		calls++
		return envelope.VersionOutput{}, nil
	}}), registerReadVariantFlag, func(context.Context, Invocation) (envelope.IssueCreateOutput, error) {
		calls++
		return envelope.IssueCreateOutput{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Flags().Set("inspect", "true"); err != nil {
		t.Fatal(err)
	}
	c.SetContext(t.Context())
	if err := c.RunE(c, nil); !errors.Is(err, refusal) || calls != 0 {
		t.Fatalf("refusal dispatched a handler: calls=%d err=%v", calls, err)
	}
}

func TestReadVariantChecksWiringBeforeInvocation(t *testing.T) {
	t.Parallel()
	good := func(context.Context, Invocation) (envelope.VersionOutput, error) {
		return envelope.VersionOutput{}, nil
	}
	for _, tc := range []struct {
		name  string
		read  *ReadVariant
		flags func(*cobra.Command)
	}{
		{name: "zero-value", read: &ReadVariant{}, flags: registerReadVariantFlag},
		{name: "unregistered", read: WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "missing.read.registration", Run: good}), flags: registerReadVariantFlag},
		{name: "wrong-type", read: WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "config.list", Run: good}), flags: registerReadVariantFlag},
		{name: "missing-handler", read: WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "version"}), flags: registerReadVariantFlag},
		{name: "missing-flag", read: WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "version", Run: good})},
		{name: "string-flag", read: WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "version", Run: good}), flags: func(c *cobra.Command) {
			var value string
			AddStringVar(c, &value, "inspect", "false", "Wrong type", FlagOpts{})
		}},
		{name: "default-true", read: WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "version", Run: good}), flags: func(c *cobra.Command) {
			var value bool
			AddBoolVar(c, &value, "inspect", true, "Wrong default", FlagOpts{})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := readVariantFixture(tc.read, tc.flags, func(context.Context, Invocation) (envelope.IssueCreateOutput, error) {
				t.Fatal("construction ran handler")
				return envelope.IssueCreateOutput{}, nil
			})
			if err == nil || c != nil {
				t.Fatalf("invalid wiring accepted: %v %v", c, err)
			}
		})
	}
}

func TestReadVariantRefusesIncompatiblePositionalGrammar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args ArgSpec
	}{
		{name: "lower-minimum", args: ArgSpec{Min: 0, Max: 2}},
		{name: "higher-maximum", args: ArgSpec{Min: 1, Max: 3}},
		{name: "unbounded-maximum", args: ArgSpec{Min: 1, Max: AnyArgs}},
		{name: "invalid-maximum", args: ArgSpec{Min: 1, Max: -2}},
		{name: "inverted-bounds", args: ArgSpec{Min: 2, Max: 1}},
		{name: "different-predictor", args: ArgSpec{Min: 1, Max: 2, Positions: []Arg{{Name: "KEY", Predictor: "project"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := New(Spec[envelope.IssueCreateOutput]{
				Op: "issue.create", Use: "probe KEY", Short: "Probe incompatible read arguments", WritesJira: true,
				Args: ArgSpec{Min: 1, Max: 2, Positions: []Arg{{Name: "KEY", Predictor: "issuekey"}}},
				Flags: func(c *cobra.Command) {
					var mutation MutationFlags
					AddMutationFlags(c, &mutation)
					registerReadVariantFlag(c)
				},
				Read: WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "version", Args: &tc.args, Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
					return envelope.VersionOutput{}, nil
				}}),
				Run: func(context.Context, Invocation) (envelope.IssueCreateOutput, error) {
					return envelope.IssueCreateOutput{}, nil
				},
			})
			if err == nil || c != nil || !strings.Contains(err.Error(), "may only narrow") {
				t.Fatalf("incompatible read grammar accepted: command=%v err=%v", c, err)
			}
		})
	}
}

func TestReadVariantRequiresMatchingKeyedSurface(t *testing.T) {
	t.Parallel()
	t.Run("read-without-keyed-flags", func(t *testing.T) {
		t.Parallel()
		read := WhenBoolRead("inspect", ReadSpec[envelope.IssueEditOutput]{
			Op: "issue.edit", Keyed: true,
			Run: func(context.Context, Invocation) (envelope.IssueEditOutput, error) {
				return envelope.IssueEditOutput{}, nil
			},
		})
		c, err := readVariantFixture(read, registerReadVariantFlag, func(context.Context, Invocation) (envelope.IssueCreateOutput, error) {
			return envelope.IssueCreateOutput{}, nil
		})
		if err == nil || c != nil || !strings.Contains(err.Error(), "keyed") {
			t.Fatalf("keyed read without shared keyed surface accepted: %v %v", c, err)
		}
	})
	t.Run("read-denies-keyed-flags", func(t *testing.T) {
		t.Parallel()
		c, err := New(Spec[envelope.IssueEditOutput]{
			Op: "issue.edit", Use: "probe", Short: "Probe keyed read declaration", WritesJira: true, Keyed: true,
			Flags: func(c *cobra.Command) {
				var mutation MutationFlags
				AddMutationFlags(c, &mutation)
				var keyed KeyedFlags
				AddKeyedFlags(c, &keyed)
				registerReadVariantFlag(c)
			},
			Read: WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "version", Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
				return envelope.VersionOutput{}, nil
			}}),
			Run: func(context.Context, Invocation) (envelope.IssueEditOutput, error) {
				return envelope.IssueEditOutput{}, nil
			},
		})
		if err == nil || c != nil || !strings.Contains(err.Error(), "keyed") {
			t.Fatalf("read denied the shared keyed surface: %v %v", c, err)
		}
	})
}

func TestReadVariantPreservesNativeCompletionBounds(t *testing.T) {
	t.Parallel()
	c, err := New(Spec[envelope.IssueCreateOutput]{
		Op: "issue.create", Use: "probe KEY", Short: "Probe native completion bounds", WritesJira: true,
		Args: ArgSpec{Max: 2, Positions: []Arg{{Name: "KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			var mutation MutationFlags
			AddMutationFlags(c, &mutation)
			registerReadVariantFlag(c)
		},
		Read: WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "version", Args: &ArgSpec{Max: 1}, Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
			return envelope.VersionOutput{}, nil
		}}),
		Run: func(context.Context, Invocation) (envelope.IssueCreateOutput, error) {
			return envelope.IssueCreateOutput{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "jira"}
	root.AddCommand(c)
	subs := clibcobra.Subcommands(root)
	if len(subs) != 1 || !subs[0].HasMaxPositionalArgs || subs[0].MaxPositionalArgs != 2 || !reflect.DeepEqual(subs[0].DynamicArgs, []string{"issuekey"}) {
		t.Fatalf("native completion lost default bounds or predictors: %+v", subs)
	}
}

func TestReadVariantKeepsContextAndIndependentHumanRenderer(t *testing.T) {
	t.Parallel()
	type contextKey struct{}
	readValidated, rendered := false, false
	cause := errors.New("caller canceled read")
	read := WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{
		Op: "version", Validate: func() error { readValidated = true; return nil },
		Run: func(ctx context.Context, inv Invocation) (envelope.VersionOutput, error) {
			if !readValidated || ctx.Value(contextKey{}) != "caller" || !errors.Is(context.Cause(ctx), cause) || !inv.DryRun {
				t.Fatal("read lost validation, context or invocation facts")
			}
			return envelope.VersionOutput{Version: "human-read"}, nil
		},
		Human: func(view *output.View, frame envelope.Envelope) error {
			data, ok := frame.Data.(envelope.VersionOutput)
			if !ok || data.Version != "human-read" || frame.Meta.Command != "version" {
				t.Fatal("human renderer received default operation data")
			}
			rendered = true
			view.Line(data.Version)
			return nil
		},
	})
	c, err := New(Spec[envelope.IssueCreateOutput]{
		Op: "issue.create", Use: "probe", Short: "Probe independent human read", WritesJira: true,
		Flags: func(c *cobra.Command) {
			var mutation MutationFlags
			AddMutationFlags(c, &mutation)
			registerReadVariantFlag(c)
		},
		Read: read, Validate: func() error { t.Fatal("default validation ran"); return nil },
		Human: func(*output.View, envelope.Envelope) error { t.Fatal("default renderer ran"); return nil },
		Run: func(context.Context, Invocation) (envelope.IssueCreateOutput, error) {
			t.Fatal("default handler ran")
			return envelope.IssueCreateOutput{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"--inspect", "--dry-run"})
	ctx, cancel := context.WithCancelCause(context.WithValue(t.Context(), contextKey{}, "caller"))
	cancel(cause)
	if err := c.ExecuteContext(cli.WithFormat(ctx, output.FormatHuman)); err != nil {
		t.Fatal(err)
	}
	if !rendered || !strings.Contains(stdout.String(), "human-read") {
		t.Fatal("read human output was not emitted on its configured destination")
	}
}

func TestReadVariantInheritedNoArgsRetainsCobraIdentity(t *testing.T) {
	t.Parallel()
	c, err := readVariantFixture(WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "version", Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
		return envelope.VersionOutput{}, nil
	}}), registerReadVariantFlag, func(context.Context, Invocation) (envelope.IssueCreateOutput, error) {
		return envelope.IssueCreateOutput{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(c.Args).Pointer() != reflect.ValueOf(cobra.PositionalArgs(cobra.NoArgs)).Pointer() {
		t.Fatal("inherited NoArgs was wrapped, breaking command/argument attribution")
	}
}

func TestReadVariantArgumentsAndMetadataStayIndependent(t *testing.T) {
	t.Parallel()
	args := ArgSpec{Min: 1, Max: AnyArgs, Positions: []Arg{{Name: "KEY", Predictor: "issuekey"}}}
	read := WhenBoolRead("inspect", ReadSpec[envelope.VersionOutput]{Op: "version", Args: &args, Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
		return envelope.VersionOutput{}, nil
	}})
	args.Min = 9
	args.Positions[0].Name = "changed"
	c, err := New(Spec[envelope.IssueCreateOutput]{
		Op: "issue.create", Use: "probe KEY...", Short: "Probe read arity", WritesJira: true,
		Args: ArgSpec{Max: AnyArgs, Positions: []Arg{{Name: "KEY", Predictor: "issuekey"}}}, Read: read,
		Flags: func(c *cobra.Command) {
			var mutation MutationFlags
			AddMutationFlags(c, &mutation)
			registerReadVariantFlag(c)
		},
		Run: func(context.Context, Invocation) (envelope.IssueCreateOutput, error) {
			return envelope.IssueCreateOutput{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Args(c, nil) != nil || c.Args(c, []string{"KEY-1"}) != nil {
		t.Fatal("default variadic arity changed")
	}
	if err := c.Flags().Set("inspect", "true"); err != nil {
		t.Fatal(err)
	}
	if c.Args(c, nil) == nil || c.Args(c, []string{"KEY-1"}) != nil {
		t.Fatal("selected read arity ignored")
	}
	meta, ok := SelectedReadMeta(c)
	if !ok || meta.Args.Min != 1 || meta.Args.Positions[0].Name != "KEY" {
		t.Fatalf("read declaration aliases caller storage: %+v", meta)
	}
	meta.Args.Min = 4
	meta.Args.Positions[0].Name = "caller mutation"
	again, _ := SelectedReadMeta(c)
	if again.Args.Min != 1 || again.Args.Positions[0].Name != "KEY" {
		t.Fatal("returned metadata mutates selected declaration")
	}
}
