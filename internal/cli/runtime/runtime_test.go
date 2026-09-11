package runtime_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/runtime"
)

// TestRuntimeNewAppliesDefaults verifies a zero-Option runtime is usable:
// every IO stream is non-nil so production code never has to nil-check
// them.
func TestRuntimeNewAppliesDefaults(t *testing.T) {
	rt, err := runtime.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if rt.Stdout() == nil || rt.Stderr() == nil || rt.Stdin() == nil {
		t.Fatal("New left an IO stream nil")
	}
}

// TestRuntimeOptionsOverrideInputs verifies each IO Option threads its
// value onto the Runtime.
func TestRuntimeOptionsOverrideInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("piped")

	rt, err := runtime.New(
		runtime.WithStdout(&stdout),
		runtime.WithStderr(&stderr),
		runtime.WithStdin(stdin),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if rt.Stdout() != &stdout || rt.Stderr() != &stderr || rt.Stdin() != stdin {
		t.Error("IO options did not thread onto runtime")
	}
}

// TestRuntimeRejectsInvalidOptions verifies New surfaces an error instead
// of building a runtime with a nil stream.
func TestRuntimeRejectsInvalidOptions(t *testing.T) {
	cases := map[string]runtime.Option{
		"nil stdout": runtime.WithStdout(nil),
		"nil stderr": runtime.WithStderr(nil),
		"nil stdin":  runtime.WithStdin(nil),
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := runtime.New(opt); err == nil {
				t.Errorf("New(%s) = nil error; want rejection", name)
			}
		})
	}
}

// TestRuntimeDoesNotStoreContext is a structural guarantee: the Runtime
// type must not carry a context.Context field. The root context belongs
// to main and flows through ExecuteContext, never parked on runtime.
func TestRuntimeDoesNotStoreContext(t *testing.T) {
	rt, err := runtime.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	typ := reflect.TypeOf(*rt)
	for field := range typ.Fields() {
		ft := field.Type
		if ft.String() == "context.Context" {
			t.Errorf("Runtime field %q stores a context.Context; runtime must not hold the root context", field.Name)
		}
		if ft.Kind() == reflect.Interface && ft.Name() == "Context" {
			t.Errorf("Runtime field %q stores a Context interface; runtime must not hold the root context", field.Name)
		}
	}
}
