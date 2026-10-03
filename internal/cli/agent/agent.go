// Package agent serves credential-free runtime discovery through typed results.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/matcra587/docent"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/discovery"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

const (
	flagPath        = "path"
	flagShapes      = "shapes"
	pathTerse       = "Command subtree"
	pathPlaceholder = "command"
)

// Deps supplies local discovery sources and an export publication boundary.
// It carries no Jira client or credential store.
type Deps struct {
	Tree        func() (docent.Command, error)
	Guides      *docent.GuideSet
	WriteGuides ExportWriter
}

func New(deps Deps) *cobra.Command {
	group := &cobra.Command{Use: "agent", Short: "Discover the CLI contract without credentials", Args: cobra.NoArgs}
	var path string
	var shapes bool
	group.AddCommand(command.MustNew(command.Spec[envelope.AgentSchemaOutput]{
		Op: "agent.schema", Use: "schema", Short: "Discover commands, flags and output shapes",
		Passthrough: true, // the schema artifact retains JSON Schema and Docent vocabulary
		Long: "Read the fully assembled command tree without authentication or network access. " +
			"The full tree shows shape markers; --path selects a command subtree with shapes, " +
			"and --shapes embeds shapes throughout the tree. --path and --shapes are mutually exclusive. " +
			"Paths use canonical space-separated names, with or without the jira prefix. Hidden flags and commands are omitted.\n\n" +
			"extensions.usage gives positional syntax. extensions.arguments retains declared min/max bounds " +
			"and ordered positions; max=-1 means an unbounded argument list.\n\n" +
			"JSON mode carries the artifact at .data.schema; compact mode at .schema. " +
			"Local $refs resolve against that extracted schema object. The artifact is deterministic; invocation metadata varies normally. " +
			"Human mode prints the same artifact as formatted JSON.",
		Example: "$ jira agent schema --output=json\n\n" +
			"$ jira agent schema --path 'api post' --jq '.data.schema'\n\n" +
			"$ jira agent schema --shapes --output=compact",
		Flags: func(c *cobra.Command) {
			command.AddStringVar(c, &path, flagPath, "", "Select a canonical command subtree with output shapes", command.FlagOpts{Terse: pathTerse, Placeholder: pathPlaceholder})
			command.AddBoolVar(c, &shapes, flagShapes, false, "Embed output shapes throughout the full tree", command.FlagOpts{Terse: "Embed all shapes"})
			c.MarkFlagsMutuallyExclusive(flagPath, flagShapes)
		},
		Human: renderSchema,
		Run: func(_ context.Context, _ command.Invocation) (envelope.AgentSchemaOutput, error) {
			tree, err := deps.Tree()
			if err != nil {
				return envelope.AgentSchemaOutput{}, err
			}
			return discovery.Schema(tree, path, shapes)
		},
	}))
	group.AddCommand(newGuide(deps.Guides, command.Spec[envelope.GuideOutput]{Op: "agent.guide", Human: renderGuide}))
	group.AddCommand(newExport(deps.Guides, deps.WriteGuides))
	group.AddCommand(newADFMatrix())
	group.AddCommand(newFieldTypes())
	return group
}

// SealPaths runs after assembly so help and shell completion include discovery
// itself and every later-mounted command. The values come from the same tree.
func SealPaths(group *cobra.Command, tree docent.Command) {
	var values, descriptions []string
	var walk func(docent.Command)
	walk = func(node docent.Command) {
		values = append(values, node.Path)
		descriptions = append(descriptions, node.Description)
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(tree)
	for _, child := range group.Commands() {
		if child.Name() == "schema" {
			clibcobra.Extend(child.Flags().Lookup(flagPath), clibcobra.FlagExtra{Terse: pathTerse, Placeholder: pathPlaceholder, Enum: values, EnumTerse: descriptions})
		}
	}
}

func renderSchema(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.AgentSchemaOutput)
	if !ok {
		return fmt.Errorf("agent: renderer received %T, want AgentSchemaOutput", env.Data)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, out.Schema, "", "  "); err != nil {
		return fmt.Errorf("agent: format schema: %w", err)
	}
	for _, line := range strings.Split(formatted.String(), "\n") {
		v.Line(cli.Text(line))
	}
	return nil
}
