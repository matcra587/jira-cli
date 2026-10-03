package discovery

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/version"
)

func TestBuildSchemaOwnsShapesAndOmitsHiddenSurface(t *testing.T) {
	root := &cobra.Command{Use: "jira", Short: "Fixture CLI"}
	command.AddOutputFlags(root)
	root.AddCommand(version.New(), &cobra.Command{Use: "hidden", Hidden: true})
	tree, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Children) != 1 || tree.Children[0].Name != "version" || !tree.Children[0].ReadOnly {
		t.Fatal("schema included hidden commands or lost the declared read class")
	}
	for _, flag := range tree.Flags {
		if flag.Name == "json" || flag.Name == "compact" || flag.Name == "human" {
			t.Fatal("schema advertised a hidden compatibility flag")
		}
	}
	leaf := &tree.Children[0]
	help := false
	for _, flag := range leaf.Flags {
		help = help || flag.Name == "help"
	}
	if !help {
		t.Fatal("lazy framework help flag disappeared from the schema")
	}
	props := leaf.OutputSchema["properties"].(map[string]any)
	if props["version"] == nil || props["go_version"] == nil || props["platform"] == nil {
		t.Fatal("schema does not describe the registered version output")
	}
	props["invented"] = map[string]any{"type": "string"}
	leaf.Extensions["long"] = "changed by consumer"
	again, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if again.Children[0].OutputSchema["properties"].(map[string]any)["invented"] != nil || again.Children[0].Extensions["long"] == "changed by consumer" {
		t.Fatal("discovery result aliases its registry or command tree")
	}
}
