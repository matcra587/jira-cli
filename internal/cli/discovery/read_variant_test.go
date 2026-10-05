package discovery

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestReadVariantDiscoveryPublishesOwnSchemaWithoutRoutingBloat(t *testing.T) {
	c, err := command.New(command.Spec[envelope.IssueCreateOutput]{
		Op: "issue.create", Use: "probe", Short: "Probe typed variant discovery", WritesJira: true,
		Args: command.ArgSpec{Max: command.AnyArgs},
		Flags: func(c *cobra.Command) {
			var mutation command.MutationFlags
			command.AddMutationFlags(c, &mutation)
			var inspect bool
			command.AddBoolVar(c, &inspect, "inspect", false, "Read instead of mutating", command.FlagOpts{})
		},
		Read: command.WhenBoolRead("inspect", command.ReadSpec[envelope.VersionOutput]{Op: "version", Args: &command.ArgSpec{Min: 1, Max: command.AnyArgs}, Run: func(context.Context, command.Invocation) (envelope.VersionOutput, error) {
			return envelope.VersionOutput{}, nil
		}}),
		Run: func(context.Context, command.Invocation) (envelope.IssueCreateOutput, error) {
			return envelope.IssueCreateOutput{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "jira", Short: "Fixture CLI"}
	root.AddCommand(c)
	command.AddOutputFlags(root)
	tree, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Children) != 1 || tree.Children[0].ReadOnly {
		t.Fatal("mixed command falsely declared wholly read-only")
	}
	leaf := tree.Children[0]
	ops := leaf.Extensions["operations"].([]Operation)
	if len(ops) != 2 || ops[1].Op != "version" || ops[1].Class != "read" || ops[1].Variant != "--inspect" || !ops[1].HasOutputSchema {
		t.Fatalf("read operation facts absent: %+v", ops)
	}
	native := leaf.Extensions["operation_schemas"].(map[string]any)["version"].(map[string]any)
	props := native["properties"].(map[string]any)
	if props["version"] == nil || props["dry_run"] != nil {
		t.Fatal("variant advertised mutation schema")
	}
	args := leaf.Extensions["operation_arguments"].(map[string]any)["version"].(map[string]any)
	if args["min"] != 1 || args["max"] != command.AnyArgs {
		t.Fatal("variant argument constraints not discoverable")
	}
	route, err := Schema(tree, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var routing map[string]any
	if json.Unmarshal(route.Schema, &routing) != nil {
		t.Fatal("routing schema is invalid JSON")
	}
	children, ok := routing["children"].([]any)
	if !ok || len(children) != 1 {
		t.Fatal("routing document lost command children")
	}
	routeExtension := children[0].(map[string]any)["extensions"].(map[string]any)
	if routeExtension["operation_schemas"] != nil {
		t.Fatal("bare routing document leaked full variant schemas")
	}
	routeOps := routeExtension["operations"].([]any)
	if routeOps[1].(map[string]any)["has_output_schema"] != true {
		t.Fatal("routing omitted variant shape marker")
	}
	full, err := Schema(tree, "probe", false)
	if err != nil {
		t.Fatal(err)
	}
	var selected map[string]any
	if json.Unmarshal(full.Schema, &selected) != nil {
		t.Fatal("selected schema is invalid JSON")
	}
	extension := selected["extensions"].(map[string]any)
	if extension["operation_schemas"] == nil {
		t.Fatal("selected command omitted read output schema")
	}
	// Emission must not remove full shapes from the caller's tree.
	if tree.Children[0].Extensions["operation_schemas"] == nil {
		t.Fatal("routing emission mutated full tree")
	}
	props["invented"] = map[string]any{"type": "string"}
	again, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if again.Children[0].Extensions["operation_schemas"].(map[string]any)["version"].(map[string]any)["properties"].(map[string]any)["invented"] != nil {
		t.Fatal("variant schema aliases registration")
	}
}
