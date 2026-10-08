package envelope_test

// No t.Parallel here: tests reset the process-wide registry. Individual tests
// may exercise concurrent readers after publishing their own declarations.

import (
	"slices"
	"strings"
	"sync"
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

func TestRegistrationCapturesOverridesBeforeSchemaDemand(t *testing.T) {
	envelope.ResetRegistryForTest()
	choices := []string{"original"}
	example := map[string]any{"name": "original"}
	examples := []any{example}
	name := map[string]any{"description": "original", "enum": choices, "examples": examples}
	overrides := map[string]any{"properties": map[string]any{"name": name}}
	envelope.Register("registrytest.captured", registryOutput{}, overrides)

	name["description"] = "changed"
	choices[0] = "changed"
	example["name"] = "changed"
	examples[0] = "changed"
	delete(overrides, "properties")

	sample, ok := envelope.OutputSample("registrytest.captured")
	if _, typed := sample.(registryOutput); !ok || !typed {
		t.Fatal("declaration lookup lost the registered output type")
	}
	if _, ok := envelope.OutputSample("registrytest.missing"); ok {
		t.Fatal("declaration lookup invented an output type")
	}
	reg, ok := envelope.Registered("registrytest.captured")
	if !ok {
		t.Fatal("registration missing")
	}
	properties, _ := reg.Schema["properties"].(map[string]any)
	field, _ := properties["name"].(map[string]any)
	values, _ := field["enum"].([]string)
	items, _ := field["examples"].([]any)
	if field["type"] != "string" || field["description"] != "original" || !slices.Equal(values, []string{"original"}) || len(items) != 1 {
		t.Fatal("caller mutations changed the captured schema")
	}
	item, _ := items[0].(map[string]any)
	if item["name"] != "original" {
		t.Fatal("nested schema examples still alias authored overrides")
	}
}

func TestRegisteredConcurrentFirstReadPreservesSchema(t *testing.T) {
	envelope.ResetRegistryForTest()
	envelope.Register("registrytest.concurrent", registryOutput{}, map[string]any{"description": "concurrent output"})
	var readers sync.WaitGroup
	for range 32 {
		readers.Go(func() {
			reg, ok := envelope.Registered("registrytest.concurrent")
			if !ok || reg.Op != "registrytest.concurrent" || reg.Schema["description"] != "concurrent output" || reg.Schema["type"] != "object" {
				t.Error("concurrent lookup lost the schema or registration identity")
			}
			properties, _ := reg.Schema["properties"].(map[string]any)
			name, _ := properties["name"].(map[string]any)
			if name["type"] != "string" {
				t.Error("concurrent lookup published an incomplete derived schema")
			}
			snapshot := envelope.Snapshot()
			if len(snapshot) != 1 || snapshot[0].Op != reg.Op || snapshot[0].Schema["description"] != "concurrent output" {
				t.Error("concurrent snapshot disagrees with lookup")
			}
		})
	}
	readers.Wait()
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
