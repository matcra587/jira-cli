package envelope

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"sync"
)

// Registration ties one operation name to the exported Output struct its
// command emits and the schema published for it. Sample keeps the
// reflectable declaration alive so cross-cutting guardrails (the audit
// inventory) can verify type-level contracts — the identity-slot Ref
// rule — straight off the type instead of re-deriving them from the
// schema, which would be checking SchemaOf against itself.
type Registration struct {
	Op     string
	Sample any
	Schema map[string]any
}

// registrations is the schema registry: one entry per operation,
// registered beside the Output struct's definition as commands land.
// Registration is single-threaded startup wiring (the same posture as
// the human-renderer registry), so the map is not synchronized.
// registrations is written only at startup (init-time registration) but
// read whenever a command renders. A mutex, not because the contract
// permits concurrent registration — it does not — but because a
// package-level map that is written at all must not be a data race for
// the readers, and tests legitimately re-register under -count>1.
var (
	registryMu    sync.RWMutex
	registrations = map[string]Registration{}
)

// Register publishes op's Output struct and its derived schema. The
// sample must be a struct value — the contract's no-`any` law lives
// here: an operation cannot promise an unshaped payload. overrides is
// the SchemaOf escape hatch for prose and refinements the type system
// cannot express. Every violation panics: registration runs at
// startup, and a half-registered contract must never reach a command.
func Register(op string, sample any, overrides map[string]any) {
	if op == "" {
		panic("envelope: Register called with an empty operation name")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registrations[op]; dup {
		panic(fmt.Sprintf("envelope: operation %q registered twice", op))
	}
	t := reflect.TypeOf(sample)
	if t == nil || t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("envelope: operation %q must register a struct Output sample, got %T", op, sample))
	}
	registrations[op] = Registration{Op: op, Sample: sample, Schema: SchemaOf(sample, overrides)}
}

// Registered returns op's registration when one exists.
func Registered(op string) (Registration, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	reg, ok := registrations[op]
	return reg, ok
}

// Snapshot returns every registration sorted by operation name — the
// enumeration seam the audit inventory joins against the command tree,
// so a registered op no command emits is as visible as a command with
// no registration.
func Snapshot() []Registration {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Registration, 0, len(registrations))
	for _, reg := range registrations {
		out = append(out, reg)
	}
	slices.SortFunc(out, func(a, b Registration) int { return cmp.Compare(a.Op, b.Op) })
	return out
}
