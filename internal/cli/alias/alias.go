// Package alias manages literal local command expansions. The composition
// root supplies validation against the assembled command tree; these handlers
// cannot construct a Jira client or execute an expansion.
package alias

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	xmaps "github.com/gechr/x/maps"
	"github.com/gechr/x/shell"
	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// Deps validates names and complete alias chains against the actual tree.
type Deps struct {
	ValidateName func(string) error
	Validate     func(map[string]string, string) error
}

// New returns the alias command group. Validation is a required construction
// dependency, rather than a guessed copy of the root command inventory.
func New(deps Deps) *cobra.Command {
	if deps.ValidateName == nil || deps.Validate == nil {
		panic("alias: command-tree validation is required")
	}
	cmd := &cobra.Command{
		Use: "alias", Short: "Manage local command shortcuts", Args: cobra.NoArgs,
		Long: "Store, inspect, remove or import command shortcuts in the selected config file. " +
			"Management is local and needs no Jira credentials. Expansions use POSIX shell quoting " +
			"without running a shell. Built-in commands take precedence; cyclic chains are refused.",
		Example: "$ jira alias set mine 'issue list --assignee me'\n$ jira mine --output=json\n$ jira alias list",
	}
	command.SetMeta(cmd, command.Meta{Op: "alias", NoSuccessEnvelope: "a bare group prints help, not an operation result"})
	cmd.AddCommand(newSet(deps), newList(), newDelete(), newImport(deps))
	return cmd
}

// OpenFile uses the shared file-only snapshot owner without loading effective
// environment settings or credentials. Dispatch uses the same read boundary.
func OpenFile(ctx context.Context) (*config.AliasFile, error) {
	file, err := config.OpenAliasFile(ctx, "")
	if err != nil {
		return nil, fileError(err)
	}
	return file, nil
}

func fileError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	code := cli.CodeConfigInvalid
	if errors.Is(err, config.ErrFileChanged) {
		code = cli.CodeConfigChanged
	} else if pathError, ok := errors.AsType[*os.PathError](err); ok && pathError != nil {
		code = cli.CodeLocalIO
	}
	input := cli.NewInputError(code, err.Error())
	input.Field = cli.FlagConfig
	return input
}

func saveFile(ctx context.Context, file *config.AliasFile, aliases map[string]string) (bool, error) {
	changed, err := file.SaveAliases(ctx, aliases)
	if err != nil {
		return false, fileError(fmt.Errorf("save aliases: %w; inspect the config file before retrying", err))
	}
	return changed, nil
}

func newList() *cobra.Command {
	return command.MustNew(command.Spec[envelope.AliasListOutput]{
		Op: "alias.list", Use: "list", Aliases: []string{"ls"}, Short: "List stored aliases",
		Long: "Inspect literal names and expansions from the selected config file. " +
			"This does not validate or execute expansions, so malformed shortcuts remain inspectable.",
		Example: "$ jira alias list\n$ jira alias list --output=json",
		Human:   renderList,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.AliasListOutput, error) {
			file, err := OpenFile(ctx)
			if err != nil {
				return envelope.AliasListOutput{}, err
			}
			aliases := file.Aliases()
			return envelope.AliasListOutput{Path: file.Path(), Aliases: aliases, Count: len(aliases)}, nil
		},
	})
}

func newSet(deps Deps) *cobra.Command {
	return command.MustNew(command.Spec[envelope.AliasSetOutput]{
		Op: "alias.set", MutatesLocalState: true, Use: "set NAME EXPANSION...", Short: "Create or replace an alias",
		Long: "Validate and store a command shortcut. A single expansion argument is stored verbatim; " +
			"multiple arguments are individually shell-quoted for a faithful argv round trip. " +
			"Quote literal '#' characters: unquoted '#' starts a shell comment. " +
			"Existing names are replaced without a prompt. --dry-run validates and previews without writing.",
		Example: "$ jira alias set mine 'issue list --assignee me'\n$ jira alias set mine -- issue list --jql 'project = DEMO'\n$ jira alias set mine 'issue list' --dry-run",
		Args:    command.ArgSpec{Min: 2, Max: command.AnyArgs, Positions: []command.Arg{{Name: "NAME"}, {Name: "EXPANSION"}}},
		Flags:   command.AddDryRunFlag,
		Human:   renderSet,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.AliasSetOutput, error) {
			var zero envelope.AliasSetOutput
			name := inv.Args[0]
			if err := deps.ValidateName(name); err != nil {
				return zero, err
			}
			file, err := OpenFile(ctx)
			if err != nil {
				return zero, err
			}
			aliases := file.Aliases()
			out := envelope.AliasSetOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}, Path: file.Path(), Name: name, Expansion: storeExpansion(inv.Args[1:])}
			if previous, ok := aliases[name]; ok {
				out.Previous = &previous
			}
			aliases[name] = out.Expansion
			if err := deps.Validate(aliases, name); err != nil {
				return zero, err
			}
			if !inv.DryRun {
				out.Changed, err = saveFile(ctx, file, aliases)
				if err != nil {
					return zero, err
				}
			}
			return out, nil
		},
	})
}

func newDelete() *cobra.Command {
	var flags command.MutationFlags
	return command.MustNew(command.Spec[envelope.AliasDeleteOutput]{
		Op: "alias.delete", MutatesLocalState: true, Use: "delete NAME", Aliases: []string{"del", "rm"}, Short: "Remove a stored alias",
		Long: "Remove one local shortcut. A live delete requires --force in headless, agent or --no-input mode. " +
			"An interactive terminal proceeds without a prompt. --dry-run previews without writing. " +
			"A missing name reports deleted=false and leaves the file untouched.",
		Example: "$ jira alias delete mine --force\n$ jira alias delete mine --dry-run",
		Args:    command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "NAME", Predictor: "alias"}}},
		Flags:   func(cmd *cobra.Command) { command.AddMutationFlags(cmd, &flags, command.WithForce()) },
		Human:   renderDelete,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.AliasDeleteOutput, error) {
			var zero envelope.AliasDeleteOutput
			if !inv.DryRun && !flags.Force && !inv.Interactive {
				err := cli.NewInputError(cli.CodeFlagRequired, "alias delete requires --force in headless, agent or --no-input mode")
				err.Field = command.FlagForce
				return zero, err
			}
			file, err := OpenFile(ctx)
			if err != nil {
				return zero, err
			}
			aliases := file.Aliases()
			out := envelope.AliasDeleteOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}, Path: file.Path(), Name: inv.Args[0]}
			if previous, ok := aliases[out.Name]; ok {
				out.Previous, out.Deleted = &previous, true
			}
			delete(aliases, out.Name)
			if !inv.DryRun {
				out.Changed, err = saveFile(ctx, file, aliases)
				if err != nil {
					return zero, err
				}
			}
			return out, nil
		},
	})
}

func storeExpansion(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return strings.Join(xslices.Map(args, shell.Quote), " ")
}

func sortedNames(table map[string]string) []string {
	names := xmaps.Keys(table)
	slices.Sort(names)
	return names
}
