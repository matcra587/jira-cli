// Package discovery joins live command metadata with registered output shapes.
package discovery

import (
	"fmt"
	"slices"

	"github.com/matcra587/docent"
	docentcobra "github.com/matcra587/docent/cobra"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// Build walks the fully assembled tree. Constructors and credentials are never
// invoked. Docent owns framework facts; the host joins operation identities,
// schemas and output policy from their existing owners.
func Build(root *cobra.Command) (docent.Command, error) {
	if root == nil {
		return docent.Command{}, fmt.Errorf("discovery: missing command tree")
	}
	materializeHelp(root)
	tree := docentcobra.Tree(root)
	if err := enrich(root, &tree); err != nil {
		return docent.Command{}, err
	}
	tree.Extensions["contract_version"] = envelope.ContractVersion
	tree.Extensions["output_modes"] = slices.Clone(cli.OutputModeValues)
	tree.Extensions["argument_contract"] = "arguments retains declared min/max bounds (max=-1 means unbounded) and ordered positions; handwritten commands without a declaration retain usage only."
	tree.Extensions["output_contract"] = map[string]any{
		"json":        "Full invocation envelope. Operation output_schema describes data.",
		"compact":     "Bare typed data on success; full invocation envelope on failure.",
		"schema_refs": "Resolve local $refs against the extracted data.schema object.",
		"envelope":    envelope.SchemaOf(envelope.Envelope{}, nil),
		"error":       envelope.SchemaOf(envelope.Error{}, nil),
	}
	return tree.Clone(), nil
}

// Schema prepares the selected discovery artifact for the shared output path.
// Bare-root discovery and agent schema use the same default routing document.
func Schema(tree docent.Command, path string, shapes bool) (envelope.AgentSchemaOutput, error) {
	tree, err := selectSchema(tree, path, shapes)
	if err != nil {
		return envelope.AgentSchemaOutput{}, err
	}
	data, err := docent.MarshalSchema(tree)
	return envelope.AgentSchemaOutput{Schema: data}, err
}

func selectSchema(tree docent.Command, path string, shapes bool) (docent.Command, error) {
	if path != "" {
		found, ok := docent.FindByPath(tree, path)
		if !ok {
			found, ok = docent.FindByPath(tree, tree.Name+" "+path)
		}
		if !ok {
			err := cli.NewInputError(cli.CodeFlagValueInvalid, "unknown canonical command path: "+path)
			err.Field = "path"
			err.Suggestions = []string{"jira agent schema --output=json"}
			return docent.Command{}, err
		}
		tree = found.PoolShapes()
	} else if shapes {
		tree = tree.PoolShapes()
	} else {
		tree = tree.StripShapes()
		stripVariantSchemas(&tree)
	}
	// The version belongs on the emitted root, including selected subtrees.
	if tree.Extensions == nil {
		tree.Extensions = make(map[string]any)
	}
	tree.Extensions["contract_version"] = envelope.ContractVersion
	return tree, nil
}

func stripVariantSchemas(tree *docent.Command) {
	delete(tree.Extensions, "operation_schemas")
	for i := range tree.Children {
		stripVariantSchemas(&tree.Children[i])
	}
}

func materializeHelp(cmd *cobra.Command) {
	cmd.InitDefaultHelpFlag()
	for _, child := range cmd.Commands() {
		materializeHelp(child)
	}
}

func enrich(cmd *cobra.Command, tree *docent.Command) error {
	tree.Extensions = map[string]any{"usage": cmd.UseLine(), "long": cmd.Long, "examples": cmd.Example}
	if metas, ok := command.MetaFor(cmd); ok {
		tree.ReadOnly = true
		// Variants share a command path but remain explicit operation facts.
		// The default operation owns this node's output schema.
		operations := make([]Operation, 0, len(metas))
		variantSchemas := make(map[string]any)
		variantArgs := make(map[string]any)
		for i, meta := range metas {
			if meta.Class() != command.ClassRead {
				tree.ReadOnly = false
			}
			operation := Operation{Op: meta.Op, Variant: meta.Variant, Class: string(meta.Class()), NoSuccessEnvelope: meta.NoSuccessEnvelope}
			if i > 0 {
				if meta.NoSuccessEnvelope == "" {
					reg, exists := envelope.Registered(meta.Op)
					if !exists {
						return fmt.Errorf("discovery: %s has no output schema for %s", cmd.CommandPath(), meta.Op)
					}
					variantSchemas[meta.Op] = reg.Schema
					operation.HasOutputSchema = true
				}
				if meta.Args != nil {
					variantArgs[meta.Op] = map[string]any{"min": meta.Args.Min, "max": meta.Args.Max, "positions": slices.Clone(meta.Args.Positions)}
				}
			}
			operations = append(operations, operation)
			if i == 0 {
				if meta.Args != nil {
					tree.Extensions["arguments"] = meta.Args
				}
				if meta.NoSuccessEnvelope == "" {
					reg, exists := envelope.Registered(meta.Op)
					if !exists {
						return fmt.Errorf("discovery: %s has no output schema for %s", cmd.CommandPath(), meta.Op)
					}
					tree.OutputSchema = reg.Schema
				}
			}
		}
		tree.Extensions["operations"] = operations
		if len(variantSchemas) > 0 {
			// JSON-native containers keep Docent's defensive copying intact;
			// each operation has a different, registered output shape.
			tree.Extensions["operation_schemas"] = variantSchemas
		}
		if len(variantArgs) > 0 {
			tree.Extensions["operation_arguments"] = variantArgs
		}
	}
	byName := make(map[string]*cobra.Command, len(cmd.Commands()))
	for _, child := range cmd.Commands() {
		byName[child.Name()] = child
	}
	kept := make([]docent.Command, 0, len(tree.Children))
	for _, child := range tree.Children {
		actual := byName[child.Name]
		if actual.Hidden {
			continue
		}
		if err := enrich(actual, &child); err != nil {
			return err
		}
		kept = append(kept, child)
	}
	tree.Children = kept
	return nil
}

// Operation is host metadata that Cobra's command/flag tree cannot describe.
type Operation struct {
	Op                string `json:"op"`
	Variant           string `json:"variant,omitempty"`
	Class             string `json:"class"`
	NoSuccessEnvelope string `json:"no_success_envelope,omitempty"`
	HasOutputSchema   bool   `json:"has_output_schema,omitempty"`
}
