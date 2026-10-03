package alias

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const flagClobber = "clobber"

func newImport(deps Deps) *cobra.Command {
	var clobber bool
	return command.MustNew(command.Spec[envelope.AliasImportOutput]{
		Op: "alias.import", MutatesLocalState: true, Use: "import [FILE|-]", Short: "Merge aliases from YAML",
		Long: "Read one YAML mapping of string names to string expansions from a file or standard input. " +
			"Existing aliases are kept unless --clobber is set. Proposed chains are validated together, " +
			"independently of YAML key order. Invalid entries, cycles and invalid dependants are skipped with reasons. " +
			"Malformed YAML, duplicate keys and non-string values refuse the entire import. " +
			"--dry-run validates and previews the merge without writing or requiring credentials.",
		Example: "$ jira alias import aliases.yaml\n$ jira alias import - --clobber --dry-run",
		Args:    command.ArgSpec{Max: 1, Positions: []command.Arg{{Name: "FILE"}}},
		Flags: func(cmd *cobra.Command) {
			command.AddDryRunFlag(cmd)
			command.AddBoolVar(cmd, &clobber, flagClobber, false, "Replace existing names", command.FlagOpts{Terse: "Replace existing aliases"})
		},
		Human: renderImport,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.AliasImportOutput, error) {
			var zero envelope.AliasImportOutput
			path := command.StdinPath
			if len(inv.Args) != 0 {
				path = inv.Args[0]
			}
			data, err := command.ReadArgumentFileContext(ctx, "FILE", path, inv.Stdin, inv.StdinIsTerminal)
			if err != nil {
				return zero, err
			}
			incoming, err := parseImport(data)
			if err != nil {
				input := cli.NewInputError(cli.CodeAliasInvalid, "invalid alias YAML: "+err.Error())
				input.Field = "FILE"
				return zero, input
			}
			file, err := OpenFile(ctx)
			if err != nil {
				return zero, err
			}
			merged, accepted, skipped := mergeImport(deps, file.Aliases(), incoming, clobber)
			out := envelope.AliasImportOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}, Path: file.Path(), Imported: len(accepted), Aliases: accepted, Skipped: skipped}
			if !inv.DryRun {
				out.Changed, err = saveFile(ctx, file, merged)
				if err != nil {
					return zero, err
				}
			}
			return out, nil
		},
	})
}

// importText rejects YAML's scalar-to-string coercion for expansions.
// Keys remain native strings: the pinned merge decoder tracks explicit keys
// as strings and would otherwise overwrite them with merged named-string keys.
type importText string

func (text *importText) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return fmt.Errorf("line %d: alias names and expansions must be strings", node.Line)
	}
	*text = importText(node.Value)
	return nil
}

func parseImport(data []byte) (map[string]string, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("expected a mapping of alias names to expansions")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("only one YAML document is accepted")
	}
	// YAML null bypasses UnmarshalYAML, and native string keys permit scalar
	// coercion. Check the graph, including anchor and merge targets, first.
	seen := map[*yaml.Node]bool{}
	var checkStrings func(*yaml.Node) error
	checkStrings = func(node *yaml.Node) error {
		if node == nil || seen[node] {
			return nil
		}
		seen[node] = true
		if node.Kind == yaml.ScalarNode && node.Tag != "!!str" && (node.Tag != "!!merge" || node.Value != "<<") {
			return fmt.Errorf("line %d: alias names and expansions must be strings", node.Line)
		}
		if err := checkStrings(node.Alias); err != nil {
			return err
		}
		for _, child := range node.Content {
			if err := checkStrings(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := checkStrings(&document); err != nil {
		return nil, err
	}
	var imported map[string]importText
	if err := document.Decode(&imported); err != nil {
		return nil, err
	}
	table := make(map[string]string, len(imported))
	for name, expansion := range imported {
		table[name] = string(expansion)
	}
	return table, nil
}

// mergeImport validates the full proposed graph before excluding invalid
// entries. Subsequent passes reject dependants that would otherwise refer to
// an excluded entry; clobber failures restore the original definition.
func mergeImport(deps Deps, existing, incoming map[string]string, clobber bool) (map[string]string, []string, map[string]string) {
	merged := maps.Clone(existing)
	accepted := map[string]string{}
	skipped := map[string]string{}
	for _, name := range sortedNames(incoming) {
		if err := deps.ValidateName(name); err != nil {
			skipped[name] = err.Error()
			continue
		}
		if _, exists := existing[name]; exists && !clobber {
			skipped[name] = "name already exists; use --clobber to replace it"
			continue
		}
		merged[name], accepted[name] = incoming[name], incoming[name]
	}
	for {
		invalid := map[string]string{}
		for _, name := range sortedNames(accepted) {
			if err := deps.Validate(merged, name); err != nil {
				invalid[name] = err.Error()
			}
		}
		if len(invalid) == 0 {
			break
		}
		for name, reason := range invalid {
			skipped[name] = reason
			delete(accepted, name)
			if original, exists := existing[name]; exists {
				merged[name] = original
			} else {
				delete(merged, name)
			}
		}
	}
	return merged, sortedNames(accepted), skipped
}
