package command

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestOptionalStringTracksActualParserPresenceAndTreeIsolation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		args  []string
		value string
		given bool
	}{
		{name: "omitted"},
		{name: "explicit empty", args: []string{"--cloud-id="}, given: true},
		{name: "spaced value", args: []string{"--cloud-id", "fixture-cloud"}, value: "fixture-cloud", given: true},
		{name: "attached value", args: []string{"--cloud-id=fixture-cloud"}, value: "fixture-cloud", given: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var option OptionalString
			cmd := &cobra.Command{}
			AddOptionalStringVar(cmd, &option, "cloud-id", "Cloud routing", FlagOpts{Terse: "Cloud scope", Placeholder: "cloud-id"})
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			value, given := option.Value()
			if value != tc.value || given != tc.given {
				t.Fatal("parser presence collapsed into string value")
			}
			var other OptionalString
			another := &cobra.Command{}
			AddOptionalStringVar(another, &other, "cloud-id", "Cloud routing", FlagOpts{Terse: "Cloud scope", Placeholder: "cloud-id"})
			if value, given := other.Value(); value != "" || given {
				t.Fatal("another tree inherited option state")
			}
		})
	}
}
