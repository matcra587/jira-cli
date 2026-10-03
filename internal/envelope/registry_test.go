package envelope_test

// No t.Parallel here: the registry map is deliberately unsynchronized
// (registration is single-threaded startup wiring), so its tests must
// not run concurrently with each other.

import (
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

type registryOutput struct {
	Name  string `json:"name"`
	Extra string `json:"extra,omitempty"`
}

func TestRegisterPublishesDerivedSchemaWithOverrides(t *testing.T) {
	// Registration panics on duplicates by design; reset so a
	// repeated in-process run (-count=N) starts clean.
	envelope.ResetRegistryForTest()
	// The override rides the production Register path — this is the
	// registry consuming SchemaOf's merge escape hatch, pinned.
	envelope.Register("registrytest.overridden", registryOutput{}, map[string]any{
		"properties": map[string]any{
			"name": map[string]any{"description": "the display name"},
		},
	})

	reg, ok := envelope.Registered("registrytest.overridden")
	if !ok {
		t.Fatal("registration not found")
	}
	props, _ := reg.Schema["properties"].(map[string]any)
	name, _ := props["name"].(map[string]any)
	if name["type"] != "string" {
		t.Errorf("derived type lost under override: %v", name)
	}
	if name["description"] != "the display name" {
		t.Errorf("override prose missing: %v", name)
	}
	if _, sample := reg.Sample.(registryOutput); !sample {
		t.Errorf("Sample = %T, want the registered struct kept reflectable", reg.Sample)
	}
}

func TestRegisteredUnknownOp(t *testing.T) {
	if _, ok := envelope.Registered("registrytest.never"); ok {
		t.Error("Registered invented an entry for an unregistered op")
	}
}

func TestSnapshotIsSortedAndContainsRegistrations(t *testing.T) {
	// Registration panics on duplicates by design; reset so a
	// repeated in-process run (-count=N) starts clean.
	envelope.ResetRegistryForTest()
	envelope.Register("registrytest.snap-b", registryOutput{}, nil)
	envelope.Register("registrytest.snap-a", registryOutput{}, nil)

	snap := envelope.Snapshot()
	ops := make([]string, 0, len(snap))
	for _, reg := range snap {
		ops = append(ops, reg.Op)
	}
	if !slices.IsSorted(ops) {
		t.Errorf("Snapshot not sorted: %v", ops)
	}
	for _, want := range []string{"registrytest.snap-a", "registrytest.snap-b"} {
		if !slices.Contains(ops, want) {
			t.Errorf("Snapshot missing %q", want)
		}
	}
}

func TestRegisterRejectsContractViolations(t *testing.T) {
	// Registration panics on duplicates by design; reset so a
	// repeated in-process run (-count=N) starts clean.
	envelope.ResetRegistryForTest()
	tests := []struct {
		name     string
		register func()
		wantPart string
	}{
		{
			name:     "empty op",
			register: func() { envelope.Register("", registryOutput{}, nil) },
			wantPart: "empty operation name",
		},
		{
			name:     "nil sample",
			register: func() { envelope.Register("registrytest.nil", nil, nil) },
			wantPart: "struct Output sample",
		},
		{
			name:     "non-struct sample",
			register: func() { envelope.Register("registrytest.scalar", "bare string", nil) },
			wantPart: "struct Output sample",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("Register accepted a contract violation")
				}
				if msg, ok := r.(string); !ok || !strings.Contains(msg, tt.wantPart) {
					t.Errorf("panic = %v, want mention of %q", r, tt.wantPart)
				}
			}()
			tt.register()
		})
	}
}

func TestRegisterRejectsDuplicateOp(t *testing.T) {
	// Registration panics on duplicates by design; reset so a
	// repeated in-process run (-count=N) starts clean.
	envelope.ResetRegistryForTest()
	envelope.Register("registrytest.dup", registryOutput{}, nil)
	defer func() {
		if recover() == nil {
			t.Fatal("Register accepted a duplicate operation")
		}
	}()
	envelope.Register("registrytest.dup", registryOutput{}, nil)
}
