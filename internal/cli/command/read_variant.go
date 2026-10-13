package command

import (
	"context"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// ReadSpec declares a read selected on an existing command path. Its result
// has its own registration and renderer; it never inherits mutation state.
// Nil Args inherits the path's positional declaration. Explicit Args may narrow
// its count bounds; positions inherit unless explicitly given and must match.
type ReadSpec[T any] struct {
	Op            string
	Args          *ArgSpec
	Identity      bool
	Keyed         bool
	Passthrough   bool
	Human         output.HumanRenderFunc
	Validate      func() error
	Run           func(context.Context, Invocation) (T, error)
	Warnings      func(T) []envelope.Warning
	RetainOnError func(T) bool
}

// ReadVariant selects a separately typed read through the shared owner.
type ReadVariant struct{ variant *boolVariant }

// LocalWriteSpec declares a separately typed local mutation. It has the same
// result and validation surface as ReadSpec; its class is constructor-owned.
type LocalWriteSpec[T any] ReadSpec[T]

// LocalWriteVariant selects a separately typed local mutation. New requires a
// preview flag on its command path, even when the default operation is a read.
type LocalWriteVariant struct{ variant *boolVariant }

// boolVariant retains the typed constructor's execution boundary. Its fields
// are private so callers cannot substitute an unregistered result or writer.
type boolVariant struct {
	flag     string
	meta     Meta
	check    func() error
	validate func() error
	run      func(*cobra.Command, Invocation, ArgSpec) error
	human    bool
}

// WhenBoolRead selects a separately registered read when flag is true. New
// verifies that the path registers a boolean flag whose default is false.
// The handler only returns typed data; this owner emits every result.
func WhenBoolRead[T any](flag string, spec ReadSpec[T]) *ReadVariant {
	return &ReadVariant{variant: whenBool(flag, spec, false)}
}

// WhenBoolLocalWrite selects a separately registered local mutation when flag
// is true. New checks the flag, output registration and preview requirement.
func WhenBoolLocalWrite[T any](flag string, spec LocalWriteSpec[T]) *LocalWriteVariant {
	return &LocalWriteVariant{variant: whenBool(flag, ReadSpec[T](spec), true)}
}

func whenBool[T any](flag string, spec ReadSpec[T], localWrite bool) *boolVariant {
	var args *ArgSpec
	if spec.Args != nil {
		copied := *spec.Args
		copied.Positions = slices.Clone(copied.Positions)
		args = &copied
	}
	return &boolVariant{
		flag: flag,
		meta: Meta{Op: spec.Op, Variant: "--" + flag, Args: args, Identity: spec.Identity, Keyed: spec.Keyed, Passthrough: spec.Passthrough, MutatesLocalState: localWrite},
		check: func() error {
			if spec.Run == nil {
				return fmt.Errorf("command: alternative %q has no Run", spec.Op)
			}
			return registeredOutput[T](spec.Op)
		},
		validate: spec.Validate,
		human:    spec.Human != nil,
		run: func(cmd *cobra.Command, inv Invocation, positions ArgSpec) error {
			data, err := spec.Run(cmd.Context(), inv)
			recordIssueKeys(cmd.Context(), data, dryRunFlag(cmd), positions, inv.Args, err)
			var warnings []envelope.Warning
			if spec.Warnings != nil {
				warnings = spec.Warnings(data)
			}
			if err != nil {
				if spec.RetainOnError != nil && spec.RetainOnError(data) {
					return &cli.ResultError{Err: err, Op: spec.Op, Data: data, Human: spec.Human, Warnings: warnings, Mutation: localWrite}
				}
				return err
			}
			return writeEnvelope(cmd, spec.Op, data, warnings, spec.Human, false)
		},
	}
}

// bind copies the declaration for one command. Restricting the alternative's arguments
// to the path's positional grammar preserves its native completion declaration.
func (v *boolVariant) bind(op string, pathArgs ArgSpec, variants []Meta) (*boolVariant, error) {
	if v == nil {
		return nil, nil
	}
	if v.check == nil || v.run == nil {
		return nil, fmt.Errorf("command: boolean alternative must use its typed constructor")
	}
	if err := v.check(); err != nil {
		return nil, err
	}
	if v.meta.Op == op {
		return nil, fmt.Errorf("command: alternative must declare its own operation, not %q", op)
	}
	for _, variant := range variants {
		if variant.Variant == v.meta.Variant {
			return nil, fmt.Errorf("command: alternative flag %q is already declared as a variant", v.meta.Variant)
		}
	}
	read := *v
	if read.meta.Args == nil {
		read.meta.Args = cloneMetas([]Meta{{Args: &pathArgs}})[0].Args
		return &read, nil
	}
	read.meta.Args = cloneMetas([]Meta{read.meta})[0].Args
	args := read.meta.Args
	if args.Positions == nil {
		args.Positions = slices.Clone(pathArgs.Positions)
	}
	validBounds := args.Min >= pathArgs.Min && args.Max >= AnyArgs && (args.Max == AnyArgs || args.Max >= args.Min)
	withinPath := pathArgs.Max == AnyArgs || args.Max != AnyArgs && args.Max <= pathArgs.Max
	if !validBounds || !withinPath || !slices.Equal(args.Positions, pathArgs.Positions) {
		return nil, fmt.Errorf("command: alternative %q may only narrow the path's argument bounds and must retain its completion positions", read.meta.Op)
	}
	return &read, nil
}

func (v *boolVariant) selected(cmd *cobra.Command) bool {
	selected, err := cmd.Flags().GetBool(v.flag)
	return err == nil && selected
}

func (v *boolVariant) checkFlag(cmd *cobra.Command) error {
	f := cmd.Flags().Lookup(v.flag)
	if f == nil || f.Value.Type() != "bool" || f.DefValue != "false" {
		return fmt.Errorf("command: alternative %q requires --%s as a boolean flag defaulting to false", v.meta.Op, v.flag)
	}
	return nil
}

var boolVariants = map[*cobra.Command]*boolVariant{}

func setBoolVariant(cmd *cobra.Command, read *boolVariant) {
	opMetasMu.Lock()
	defer opMetasMu.Unlock()
	boolVariants[cmd] = read
}

// SelectedReadMeta reports the active read alternative after flag parsing.
// SelectedVariantMeta handles both read and local-write alternatives. This
// read-only accessor retains the original read-specific lookup contract.
func SelectedReadMeta(cmd *cobra.Command) (Meta, bool) {
	meta, ok := SelectedVariantMeta(cmd)
	if !ok || meta.Class() != ClassRead {
		return Meta{}, false
	}
	return meta, true
}

// SelectedVariantMeta reports the active typed alternative after flag parsing,
// including failures before its handler runs. The returned metadata is a copy.
func SelectedVariantMeta(cmd *cobra.Command) (Meta, bool) {
	opMetasMu.RLock()
	read := boolVariants[cmd]
	opMetasMu.RUnlock()
	if read == nil || !read.selected(cmd) {
		return Meta{}, false
	}
	return cloneMetas([]Meta{read.meta})[0], true
}
