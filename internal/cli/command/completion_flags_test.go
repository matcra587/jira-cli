package command_test

import (
	"slices"
	"testing"
	"time"

	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/clib/complete"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestValuedFlagsPreserveCompletionMetadata(t *testing.T) {
	t.Parallel()
	opts := command.FlagOpts{Terse: "Choices", Placeholder: "choice", Shorthand: "c", Complete: "predictor=choice,comma", Hint: "file", Enum: []string{"first", "second"}, EnumTerse: []string{"First choice", "Second choice"}, EnumDefault: "second", Forward: true}
	cases := []struct {
		name     string
		register func(*cobra.Command)
	}{
		{name: "string", register: func(cmd *cobra.Command) {
			var value string
			command.AddStringVar(cmd, &value, "choice", "", "Choose", opts)
		}},
		{name: "slice", register: func(cmd *cobra.Command) {
			var value []string
			command.AddStringSliceVar(cmd, &value, "choice", nil, "Choose", opts)
		}},
		{name: "array", register: func(cmd *cobra.Command) {
			var value []string
			command.AddStringArrayVar(cmd, &value, "choice", nil, "Choose", opts)
		}},
		{name: "int", register: func(cmd *cobra.Command) { var value int; command.AddIntVar(cmd, &value, "choice", 0, "Choose", opts) }},
		{name: "int64", register: func(cmd *cobra.Command) {
			var value int64
			command.AddInt64Var(cmd, &value, "choice", 0, "Choose", opts)
		}},
		{name: "duration", register: func(cmd *cobra.Command) {
			var value time.Duration
			command.AddDurationVar(cmd, &value, "choice", 0, "Choose", opts)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := &cobra.Command{Use: "fixture"}
			tc.register(cmd)
			meta := clibcobra.FlagMeta(cmd)
			if len(meta) != 1 {
				t.Fatalf("metadata = %#v", meta)
			}
			got := meta[0]
			if got.Terse != opts.Terse || got.Placeholder != opts.Placeholder || got.Short != opts.Shorthand || got.Complete != opts.Complete || got.ValueHint != opts.Hint || !slices.Equal(got.Enum, opts.Enum) || !slices.Equal(got.EnumTerse, opts.EnumTerse) || got.EnumDefault != opts.EnumDefault {
				t.Fatalf("metadata discarded: %#v", got)
			}
			var spec complete.Spec
			complete.ApplyMeta(&spec, &got)
			spec.LongFlag = got.Name
			spec.HasArg = got.HasArg
			gen := &complete.Generator{Specs: []complete.Spec{spec}}
			command.ForwardCompletion(cmd, gen)
			projected := gen.Specs[0]
			if !projected.HasArg || !projected.Forward || !projected.CommaList || projected.Dynamic != "choice" || projected.ValueHint != "file" || len(projected.ValueDescs) != 2 {
				t.Fatalf("completion metadata lost: %#v", projected)
			}
		})
	}
}

func TestCompletionMetadataPreservesCSVAndLiteralParsing(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "fixture"}
	var csv, literal []string
	opts := command.FlagOpts{Terse: "Values", Complete: "predictor=choice"}
	command.AddStringSliceVar(cmd, &csv, "csv", nil, "CSV values", opts)
	command.AddStringArrayVar(cmd, &literal, "literal", nil, "Literal values", opts)
	if err := cmd.ParseFlags([]string{"--csv", "first,second", "--csv", `"space, comma"`, "--literal", "first,second", "--literal", "space, comma"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(csv, []string{"first", "second", "space, comma"}) || !slices.Equal(literal, []string{"first,second", "space, comma"}) {
		t.Fatalf("csv=%#v literal=%#v", csv, literal)
	}
}
