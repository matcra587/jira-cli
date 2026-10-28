package command

import (
	"fmt"
	"slices"
	"sync"

	"github.com/spf13/cobra"
)

// Class buckets an operation by what it touches — the axis-applicability
// map in the audit suite keys off it (which of the sixteen scorecard
// axes apply, which are asserted absent).
type Class string

const (
	// ClassRead reaches Jira without writing anything anywhere.
	ClassRead Class = "read"
	// ClassInteractive admits a terminal session whose explicit UI actions
	// may perform the declared Jira or local writes after review.
	ClassInteractive Class = "interactive"
	// ClassMutation writes to Jira.
	ClassMutation Class = "mutation"
	// ClassLocalState mutates local state only (config, cache, aliases).
	ClassLocalState Class = "local-state"
)

// Meta declares what a command DOES: the operation it emits and the
// contract-relevant facts about it. The audit inventory derives its
// member set from these declarations joined to the live tree — never
// from cobra shape alone, because cobra exposes commands, not which op
// a branch emits (a flag variant reusing an existing op is invisible to
// a tree walk). A command missing its declaration fails the inventory
// by name, so registration cannot be forgotten silently.
type Meta struct {
	// Screen marks a human-only terminal session with structured refusals.
	Screen bool
	// Op is the dotted operation name the envelope carries as
	// meta.command ("root", "issue.view").
	Op string
	// Variant names the flag or argument form that selects this op when
	// one command path emits several ("--count"). Empty for the path's
	// default form.
	Variant string
	// Args is the positional declaration retained by Spec construction.
	// Nil means a handwritten command has not supplied structured argument
	// metadata; Cobra's usage remains available without inventing arity.
	Args *ArgSpec
	// WritesJira marks operations that mutate Jira.
	WritesJira bool
	// MutatesLocalState marks operations that write local state only.
	MutatesLocalState bool
	// Identity declares that the op's Output struct carries a domain
	// identity slot (an envelope.Ref field such as IssueRef). The
	// inventory cross-checks the declaration against the registered
	// type in both directions, so a bare-string identity and an
	// undeclared ref slot are equally loud.
	Identity bool
	// Keyed marks multi-key operations returning ordered results[] —
	// gates the M3 axis.
	Keyed bool
	// Passthrough marks operations carrying a registered wire
	// passthrough boundary (issue view's fields) — gates the M10 axis.
	Passthrough bool
	// NoSuccessEnvelope, when non-empty, is the documented reason this
	// command's success emits no envelope at all. Two kinds declare it:
	// a group, which prints help rather than an
	// operation result, and a command whose output IS the bytes —
	// `auth token`, because a frame would carry the secret into logs and
	// filters, and `completion`, because a shell evals what it prints.
	// The inventory then requires the schema registration to be ABSENT —
	// exactness cuts both ways.
	NoSuccessEnvelope string
}

// Class derives the command class from the write flags. A command that
// writes Jira is a mutation regardless of any local side effects — the
// Jira write dominates for the safety axes (dry-run, preview).
func (m Meta) Class() Class {
	switch {
	case m.Screen:
		return ClassInteractive
	case m.WritesJira:
		return ClassMutation
	case m.MutatesLocalState:
		return ClassLocalState
	default:
		return ClassRead
	}
}

// opMetas keys declarations by command pointer: the tree owns identity,
// so two trees (production and a test fixture) never collide. Production
// construction is single-threaded, but parallel tests build fixture
// trees while others read the live tree's declarations — the same
// registry, concurrently — so access is locked.
var (
	opMetasMu sync.RWMutex
	opMetas   = map[*cobra.Command][]Meta{}
)

// SetMeta declares cmd's operation metadata: the default form first,
// then any flag/argument variants. Every violation panics — this is
// startup wiring, and a malformed declaration must never survive to a
// tree the inventory would then misread.
func SetMeta(cmd *cobra.Command, meta Meta, variants ...Meta) {
	if cmd == nil {
		panic("command: SetMeta called with a nil command")
	}
	opMetasMu.Lock()
	defer opMetasMu.Unlock()
	if _, dup := opMetas[cmd]; dup {
		panic(fmt.Sprintf("command: operation metadata for %q set twice", cmd.Name()))
	}
	all := append([]Meta{meta}, variants...)
	if meta.Variant != "" {
		panic(fmt.Sprintf("command: %q's first metadata entry must be the default form (Variant empty), got %q", cmd.Name(), meta.Variant))
	}
	seen := map[string]bool{}
	for i, m := range all {
		if m.Op == "" {
			panic(fmt.Sprintf("command: %q declares metadata without an operation name", cmd.Name()))
		}
		if i > 0 && m.Variant == "" {
			panic(fmt.Sprintf("command: %q declares a second default form for op %q — variants need a Variant", cmd.Name(), m.Op))
		}
		if seen[m.Variant] {
			panic(fmt.Sprintf("command: %q declares variant %q twice", cmd.Name(), m.Variant))
		}
		seen[m.Variant] = true
		if m.NoSuccessEnvelope != "" && (m.Identity || m.Keyed || m.Passthrough) {
			panic(fmt.Sprintf("command: %q op %q claims data-shape traits on an envelope-free operation", cmd.Name(), m.Op))
		}
	}
	opMetas[cmd] = cloneMetas(all)
}

// MetaFor returns cmd's declared metadata, default form first. The
// slice is a copy so callers cannot mutate the registry.
func MetaFor(cmd *cobra.Command) ([]Meta, bool) {
	opMetasMu.RLock()
	defer opMetasMu.RUnlock()
	ms, ok := opMetas[cmd]
	if !ok {
		return nil, false
	}
	return cloneMetas(ms), true
}

func cloneMetas(metas []Meta) []Meta {
	cloned := slices.Clone(metas)
	for i := range cloned {
		if cloned[i].Args != nil {
			args := *cloned[i].Args
			args.Positions = slices.Clone(args.Positions)
			cloned[i].Args = &args
		}
	}
	return cloned
}
