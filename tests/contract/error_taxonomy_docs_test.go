package contract

// Doc <-> code lockstep for the error taxonomy: the agent guide's core
// contract must document every exit code the mapper can produce and every
// field the error struct can emit. The field list is derived from
// cli.Error's json tags by reflection, so adding a field to the struct
// without documenting it fails here — and vice versa the exit table cannot
// silently drift from ExitCode.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func coreContractText(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "internal", "agentguides", "guides", "core-contract.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read core-contract.md: %v", err)
	}
	return string(raw)
}

// TestCoreContractDocumentsEveryExitCode pins the exit table to the full
// 0-8 set the code emits, including the local-output failure contract.
func TestCoreContractDocumentsEveryExitCode(t *testing.T) {
	doc := coreContractText(t)
	for _, code := range []string{"`0`", "`1`", "`2`", "`3`", "`4`", "`5`", "`6`", "`7`", "`8`"} {
		if !strings.Contains(doc, code) {
			t.Errorf("exit-code list is missing %s", code)
		}
	}
	for _, marker := range []string{
		"`code=canceled`",
		"`code=output_write_failed`",
		"`code=read_only`",
		"`code=timeout`",
		"`retryable=false`",
		"`type=io`",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("contract does not document %s", marker)
		}
	}
}

// TestCoreContractDocumentsEveryErrorField derives the emittable envelope
// error fields from cli.Error's json tags and requires each to appear in
// the contract's errors[] field list.
func TestCoreContractDocumentsEveryErrorField(t *testing.T) {
	doc := coreContractText(t)
	typ := reflect.TypeFor[cli.Error]()
	for field := range typ.Fields() {
		tag := field.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("error field %q is emitted by cli.Error but not documented in core-contract.md", name)
		}
	}
}
