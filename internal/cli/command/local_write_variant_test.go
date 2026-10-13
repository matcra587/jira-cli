package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func localWriteFixture(variant *LocalWriteVariant, flags func(*cobra.Command), run func(context.Context, Invocation) (envelope.VersionOutput, error)) (*cobra.Command, error) {
	return New(Spec[envelope.VersionOutput]{Op: "version", Use: "probe", Short: "Probe typed local write", LocalWrite: variant, Flags: flags, Run: run})
}

func localWriteFlags(c *cobra.Command) {
	var store bool
	AddBoolVar(c, &store, "store", false, "Save local state", FlagOpts{})
	AddDryRunFlag(c)
}

func TestLocalWriteVariantSelectsTypedResultAndInvocationPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		args             []string
		selected, dryRun bool
	}{
		{name: "default"},
		{name: "explicit false", args: []string{"--store=false"}},
		{name: "live", args: []string{"--store"}, selected: true},
		{name: "preview", args: []string{"--store", "--dry-run"}, selected: true, dryRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			type contextKey struct{}
			caller := context.WithValue(t.Context(), contextKey{}, "caller")
			reads, writes := 0, 0
			c, err := localWriteFixture(WhenBoolLocalWrite("store", LocalWriteSpec[envelope.ConfigSetOutput]{
				Op: "config.set", Run: func(ctx context.Context, inv Invocation) (envelope.ConfigSetOutput, error) {
					writes++
					if ctx.Value(contextKey{}) != "caller" || inv.DryRun != tc.dryRun {
						t.Fatal("local write lost preview invocation")
					}
					return envelope.ConfigSetOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}, Key: "read_only", Value: "true"}, nil
				},
			}), localWriteFlags, func(context.Context, Invocation) (envelope.VersionOutput, error) {
				reads++
				return envelope.VersionOutput{Version: "native-read"}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			c.SetOut(&stdout)
			c.SetErr(io.Discard)
			c.SetArgs(tc.args)
			if err := c.ExecuteContext(cli.WithFormat(caller, output.FormatJSON)); err != nil {
				t.Fatal(err)
			}
			var frame struct {
				Meta envelope.Meta  `json:"meta"`
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
				t.Fatal(err)
			}
			meta, selected := SelectedVariantMeta(c)
			if selected != tc.selected {
				t.Fatal("wrong alternative selection")
			}
			if tc.selected {
				if reads != 0 || writes != 1 || frame.Meta.Command != "config.set" || frame.Data["dry_run"] != tc.dryRun || frame.Data["key"] != "read_only" || meta.Class() != ClassLocalState {
					t.Fatalf("wrong local write dispatch: %+v reads=%d writes=%d", frame, reads, writes)
				}
				if _, read := SelectedReadMeta(c); read {
					t.Fatal("local mutation attributed as read")
				}
			} else if reads != 1 || writes != 0 || frame.Meta.Command != "version" || frame.Data["dry_run"] != nil || frame.Data["version"] != "native-read" {
				t.Fatalf("default read changed: %+v", frame)
			}
			if reflect.ValueOf(c.Args).Pointer() != reflect.ValueOf(cobra.PositionalArgs(cobra.NoArgs)).Pointer() {
				t.Fatal("inherited NoArgs identity wrapped")
			}
		})
	}
}

func TestLocalWriteVariantRetainsMutationReceiptAndOriginalCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("publication outcome unknown")
	c, err := localWriteFixture(WhenBoolLocalWrite("store", LocalWriteSpec[envelope.ConfigSetOutput]{
		Op: "config.set", Run: func(context.Context, Invocation) (envelope.ConfigSetOutput, error) {
			return envelope.ConfigSetOutput{Key: "read_only"}, cause
		},
		RetainOnError: func(out envelope.ConfigSetOutput) bool { return out.Key != "" },
		Warnings: func(envelope.ConfigSetOutput) []envelope.Warning {
			return []envelope.Warning{{Type: "uncertain", Message: "inspect publication"}}
		},
		Human: func(*output.View, envelope.Envelope) error { return nil },
	}), localWriteFlags, func(context.Context, Invocation) (envelope.VersionOutput, error) {
		t.Fatal("default read ran")
		return envelope.VersionOutput{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Flags().Set("store", "true"); err != nil {
		t.Fatal(err)
	}
	c.SetContext(t.Context())
	err = c.RunE(c, nil)
	var retained *cli.ResultError
	if !errors.Is(err, cause) || !errors.As(err, &retained) || !retained.Mutation || retained.Op != "config.set" || retained.Human == nil || len(retained.Warnings) != 1 {
		t.Fatalf("local mutation lost receipt/class/cause: %+v %v", retained, err)
	}
	if data, ok := retained.Data.(envelope.ConfigSetOutput); !ok || data.Key != "read_only" {
		t.Fatal("local mutation receipt retyped")
	}
}

func TestLocalWriteVariantConstructionRequiresPreviewAndTypedWiring(t *testing.T) {
	t.Parallel()
	good := func(context.Context, Invocation) (envelope.ConfigSetOutput, error) {
		return envelope.ConfigSetOutput{}, nil
	}
	for _, tc := range []struct {
		name    string
		variant *LocalWriteVariant
		flags   func(*cobra.Command)
	}{
		{name: "zero", variant: &LocalWriteVariant{}, flags: localWriteFlags},
		{name: "missing registration", variant: WhenBoolLocalWrite("store", LocalWriteSpec[envelope.ConfigSetOutput]{Op: "missing.local", Run: good}), flags: localWriteFlags},
		{name: "wrong output", variant: WhenBoolLocalWrite("store", LocalWriteSpec[envelope.ConfigSetOutput]{Op: "version", Run: good}), flags: localWriteFlags},
		{name: "missing handler", variant: WhenBoolLocalWrite("store", LocalWriteSpec[envelope.ConfigSetOutput]{Op: "config.set"}), flags: localWriteFlags},
		{name: "missing flag", variant: WhenBoolLocalWrite("store", LocalWriteSpec[envelope.ConfigSetOutput]{Op: "config.set", Run: good}), flags: AddDryRunFlag},
		{name: "missing preview", variant: WhenBoolLocalWrite("store", LocalWriteSpec[envelope.ConfigSetOutput]{Op: "config.set", Run: good}), flags: func(c *cobra.Command) { var store bool; AddBoolVar(c, &store, "store", false, "Store", FlagOpts{}) }},
		{name: "wrong preview type", variant: WhenBoolLocalWrite("store", LocalWriteSpec[envelope.ConfigSetOutput]{Op: "config.set", Run: good}), flags: func(c *cobra.Command) {
			var store bool
			var preview string
			AddBoolVar(c, &store, "store", false, "Store", FlagOpts{})
			AddStringVar(c, &preview, "dry-run", "false", "Wrong preview type", FlagOpts{})
		}},
		{name: "default true", variant: WhenBoolLocalWrite("store", LocalWriteSpec[envelope.ConfigSetOutput]{Op: "config.set", Run: good}), flags: func(c *cobra.Command) {
			var store bool
			AddBoolVar(c, &store, "store", true, "Store", FlagOpts{})
			AddDryRunFlag(c)
		}},
		{name: "wrong flag type", variant: WhenBoolLocalWrite("store", LocalWriteSpec[envelope.ConfigSetOutput]{Op: "config.set", Run: good}), flags: func(c *cobra.Command) {
			var store string
			AddStringVar(c, &store, "store", "false", "Store", FlagOpts{})
			AddDryRunFlag(c)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := localWriteFixture(tc.variant, tc.flags, func(context.Context, Invocation) (envelope.VersionOutput, error) {
				t.Fatal("constructor entered handler")
				return envelope.VersionOutput{}, nil
			})
			if err == nil || c != nil {
				t.Fatalf("invalid local write wiring accepted: %v %v", c, err)
			}
		})
	}
}
