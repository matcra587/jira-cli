package root

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gechr/x/shell"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	aliascmd "github.com/matcra587/jira-cli/internal/cli/alias"
)

func builtinCommand(root *cobra.Command, name string) bool {
	for _, cmd := range root.Commands() {
		if cmd.Name() == name || slices.Contains(cmd.Aliases, name) {
			return true
		}
	}
	return false
}

func validateAliasName(root *cobra.Command, name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return aliasError(name, "alias name is required")
	case strings.HasPrefix(name, "-"):
		return aliasError(name, "alias name cannot start with '-'")
	case !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl):
		return aliasError(name, "alias name must be valid text without controls")
	case builtinCommand(root, name):
		return aliasError(name, "alias name conflicts with a built-in command")
	default:
		return nil
	}
}

func validateAlias(root *cobra.Command, table map[string]string, name string) error {
	if err := validateAliasName(root, name); err != nil {
		return err
	}
	expanded, err := expandAliases(root, table, []string{name})
	if err != nil {
		return err
	}
	if err := checkCommandPath(root, expanded); err != nil {
		return aliasError(name, err.Error())
	}
	return nil
}

// prepareAliasArgs reads only local file settings, and only for a leading
// command that is not built in. Help, version and completion need no aliases.
func prepareAliasArgs(ctx context.Context, root *cobra.Command, args []string) ([]string, error) {
	name := firstPositional(root, args)
	if name == "" || builtinCommand(root, name) {
		return args, nil
	}
	file, err := aliascmd.OpenFile(ctx)
	if err != nil {
		return nil, err
	}
	return expandAliases(root, file.Aliases(), args)
}

// expandAliases expands a chain without invoking a shell. Prefix flags and
// trailing arguments retain their order through every expansion. Collecting
// fragments avoids repeatedly copying growing argv slices for a long chain.
func expandAliases(root *cobra.Command, table map[string]string, args []string) ([]string, error) {
	type fragment struct{ before, after []string }
	var fragments []fragment
	seen := map[string]bool{}
	original := args
	for {
		name, index := firstPositionalIndex(root, args)
		if index < 0 || name == "" || builtinCommand(root, name) {
			break
		}
		expansion, ok := table[name]
		if !ok {
			if len(fragments) == 0 {
				return original, nil
			}
			return nil, aliasError(name, "expansion names no built-in command or stored alias")
		}
		if err := validateAliasName(root, name); err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, aliasError(name, "alias expansion contains a cycle")
		}
		seen[name] = true
		parsed, err := shell.Split(expansion)
		if err != nil {
			return nil, aliasError(name, "expansion has invalid shell quoting: "+err.Error())
		}
		commandName := firstPositional(root, parsed)
		if commandName == "" {
			return nil, aliasError(name, "expansion has no command before '--'")
		}
		fragments = append(fragments, fragment{before: args[:index], after: args[index+1:]})
		args = parsed
	}
	if len(fragments) == 0 {
		return original, nil
	}
	length := len(args)
	for _, f := range fragments {
		length += len(f.before) + len(f.after)
	}
	out := make([]string, 0, length)
	for _, f := range fragments {
		out = append(out, f.before...)
	}
	out = append(out, args...)
	for _, f := range slices.Backward(fragments) {
		out = append(out, f.after...)
	}
	return out, nil
}

func aliasError(name, reason string) *cli.InputError {
	err := cli.NewInputError(cli.CodeAliasInvalid, fmt.Sprintf("alias %q: %s", name, reason))
	err.Field = name
	return err
}
