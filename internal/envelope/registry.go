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

// Registration validates declarations at startup. Schema derivation is lazy:
// ordinary command construction checks types without needing output schemas.
// Each factory runs once, including under concurrent discovery readers.
type registration struct {
	declaration Registration
	schema      func() map[string]any
}

func (r registration) resolved() Registration {
	out := r.declaration
	out.Schema = r.schema()
	return out
}

// The mutex protects registry publication and lookup, not schema derivation.
var (
	registryMu    sync.RWMutex
	registrations = map[string]registration{}
)

// Register publishes op's Output struct and a lazy schema factory. The
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
	// Capture authored refinements now so deferring derivation does not let
	// later caller mutations change the published contract.
	refinements := cloneSchema(overrides)
	registrations[op] = registration{
		declaration: Registration{Op: op, Sample: sample},
		schema:      sync.OnceValue(func() map[string]any { return SchemaOf(sample, refinements) }),
	}
}

// OutputSample returns the declared output sample without deriving its schema.
// Command constructors use it to reject missing or mismatched registrations.
func OutputSample(op string) (any, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	reg, ok := registrations[op]
	return reg.declaration.Sample, ok
}

// Registered returns op's registration, deriving its schema on first demand.
func Registered(op string) (Registration, bool) {
	registryMu.RLock()
	reg, ok := registrations[op]
	registryMu.RUnlock()
	if !ok {
		return Registration{}, false
	}
	return reg.resolved(), true
}

// Snapshot returns every registration sorted by operation name — the
// enumeration seam the audit inventory joins against the command tree,
// so a registered op no command emits is as visible as a command with
// no registration.
func Snapshot() []Registration {
	registryMu.RLock()
	entries := make([]registration, 0, len(registrations))
	for _, reg := range registrations {
		entries = append(entries, reg)
	}
	registryMu.RUnlock()
	out := make([]Registration, 0, len(entries))
	for _, reg := range entries {
		out = append(out, reg.resolved())
	}
	slices.SortFunc(out, func(a, b Registration) int { return cmp.Compare(a.Op, b.Op) })
	return out
}
