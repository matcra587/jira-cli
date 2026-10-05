package command

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestCSVFlagRejectsDiscardedOrNormalizedLineEndings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, text string }{
		{name: "later record", text: "first\nsecond"},
		{name: "trailing newline", text: "first\n"},
		{name: "leading blank", text: "\nfirst"},
		{name: "crlf records", text: "first\r\nsecond"},
		{name: "quoted crlf", text: "\"first\r\nsecond\""},
		{name: "trailing carriage return", text: "first\r"},
		{name: "unparsed malformed record", text: "first\n\"unterminated"},
	} {
		for _, alias := range []bool{false, true} {
			name := tc.name + "/canonical"
			if alias {
				name = tc.name + "/alias"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				cmd := &cobra.Command{Use: "test"}
				var values []string
				AddStringSliceVar(cmd, &values, "values", nil, "Choose values", FlagOpts{Terse: "Values"})
				flag := "values"
				if alias {
					AddHiddenAliases(cmd, []HiddenAlias{{Flag: "old-values", Canonical: "values"}})
					flag = "old-values"
				}
				if err := cmd.Flags().Set(flag, "ready"); err != nil {
					t.Fatal(err)
				}
				before := slices.Clone(cmd.Flags().Lookup(flag).Value.(pflag.SliceValue).GetSlice())
				if err := cmd.Flags().Set(flag, tc.text); err == nil {
					t.Fatalf("CSV silently discarded or normalized authored input %q: %q", tc.text, cmd.Flags().Lookup(flag).Value.(pflag.SliceValue).GetSlice())
				}
				if !slices.Equal(before, cmd.Flags().Lookup(flag).Value.(pflag.SliceValue).GetSlice()) {
					t.Fatal("refused CSV changed existing values")
				}
			})
		}
	}
}

func TestCSVFlagPreservesLiteralQuotedValuesAndSliceContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{name: "quoted newline", text: "\"first\nsecond\"", want: []string{"first\nsecond"}},
		{name: "quoted carriage return", text: "\"first\r\"", want: []string{"first\r"}},
		{name: "comma and quotes", text: `"first,second","quote""value"`, want: []string{"first,second", `quote"value`}},
		{name: "empty value", text: `"",last,`, want: []string{"", "last", ""}},
		{name: "clear default", text: "", want: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := &cobra.Command{Use: "test"}
			var values []string
			AddStringSliceVar(cmd, &values, "values", []string{"default"}, "Choose values", FlagOpts{Terse: "Values", Shorthand: "v"})
			if err := cmd.ParseFlags([]string{"-v", tc.text}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(values, tc.want) {
				t.Fatalf("quoted CSV changed: got %q want %q", values, tc.want)
			}
			flag := cmd.Flags().Lookup("values")
			if flag.Value.Type() != "stringSlice" {
				t.Fatal("slice type changed")
			}
			value, ok := flag.Value.(pflag.SliceValue)
			if !ok {
				t.Fatal("slice interface lost")
			}
			got, err := cmd.Flags().GetStringSlice("values")
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("typed slice lookup changed: %q %v", got, err)
			}
			if err := cmd.Flags().Set("values", "next,last"); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(values, append(slices.Clone(tc.want), "next", "last")) {
				t.Fatal("repeat or first-default behavior changed")
			}
			if err := value.Append("literal,comma"); err != nil {
				t.Fatal(err)
			}
			if values[len(values)-1] != "literal,comma" {
				t.Fatal("literal append reparsed CSV")
			}
			if err := value.Replace([]string{"literal,newline\nvalue"}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(values, []string{"literal,newline\nvalue"}) {
				t.Fatal("literal replacement reparsed CSV")
			}
		})
	}
}

func TestCSVHiddenAliasPreservesQuotedValuesWhenFolded(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	var values []string
	AddStringSliceVar(cmd, &values, "values", nil, "Choose values", FlagOpts{Terse: "Values"})
	aliases := []HiddenAlias{{Flag: "old-values", Canonical: "values"}}
	AddHiddenAliases(cmd, aliases)
	if err := cmd.ParseFlags([]string{"--old-values", "\"first\nsecond\"", "--old-values", `third,"four,five"`}); err != nil {
		t.Fatal(err)
	}
	if err := ResolveHiddenAliases(cmd.Flags(), aliases); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(values, []string{"first\nsecond", "third", "four,five"}) {
		t.Fatalf("alias folding lost authored CSV: %q", values)
	}
}
