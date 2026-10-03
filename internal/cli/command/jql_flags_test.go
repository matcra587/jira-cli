package command

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestJQLFilterFlagPreservesCSVAndSliceInterface(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	var values []string
	AddJQLFilterVar(cmd, &values, "status", "Filter statuses", FlagOpts{Terse: "Status"})
	if err := cmd.ParseFlags([]string{"--status=Open,Done", `--status="Needs, review"`}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(values, []string{"Open", "Done", "Needs, review"}) {
		t.Fatalf("CSV/repetition changed: %q", values)
	}
	flag := cmd.Flags().Lookup("status")
	if flag.Value.Type() != "stringSlice" {
		t.Fatal("flag type metadata changed")
	}
	value, ok := flag.Value.(pflag.SliceValue)
	if !ok || !slices.Equal(value.GetSlice(), values) {
		t.Fatal("pflag slice access lost")
	}
	if err := value.Append("Blocked"); err != nil || !slices.Equal(values, []string{"Open", "Done", "Needs, review", "Blocked"}) {
		t.Fatal("slice append changed")
	}
	if err := value.Replace([]string{"Ready"}); err != nil || !slices.Equal(values, []string{"Ready"}) {
		t.Fatal("slice replacement changed")
	}
	got, err := cmd.Flags().GetStringSlice("status")
	if err != nil || !slices.Equal(got, values) {
		t.Fatal("pflag typed access changed")
	}
}

func TestJQLFilterFlagRefusalsAreAtomic(t *testing.T) {
	for _, text := range []string{"", " ", "Open\n", "Open\nDone", "Open\r\nDone", "\"Open\nDone\"", "Open\x00", "Open\t", "Open\x7f"} {
		t.Run(text, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			var values []string
			AddJQLFilterVar(cmd, &values, "status", "Filter statuses", FlagOpts{Terse: "Status"})
			if err := cmd.ParseFlags([]string{"--status=Ready"}); err != nil {
				t.Fatal(err)
			}
			value := cmd.Flags().Lookup("status").Value.(pflag.SliceValue)
			for _, apply := range []func() error{
				func() error { return cmd.Flags().Set("status", text) },
				func() error { return value.Append(text) },
				func() error { return value.Replace([]string{"Done", text}) },
			} {
				if err := apply(); err == nil || !slices.Equal(values, []string{"Ready"}) {
					t.Fatalf("malformed CSV altered flags: %q, %v", values, err)
				}
			}
		})
	}
}
