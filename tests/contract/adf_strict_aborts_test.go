package contract

import (
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli/adfmode"
)

// In strict mode, ANY lossy ADF transformation (e.g., inlineCard
// degraded to text+link in best-effort) MUST abort with a typed
// validation error rather than silently completing.
//
// The test exercises the pkg/adf strict-vs-best-effort boundary directly
// — a higher-level CLI integration test would still abort with exit 3,
// but that level is covered by separate pipeline tests.
func TestStrictAbortsOnLossyInlineCardDegrade(t *testing.T) {
	doc, _, err := adf.Parse([]byte(`{
		"type": "doc", "version": 1, "content": [
			{"type": "paragraph", "content": [
				{"type": "inlineCard", "attrs": {"url": "https://example.com/x"}}
			]}
		]
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	schema := adf.FieldCompatibility{Field: "description", InlineCardSupported: false}

	_, _, err = adf.ApplyCompatibility(doc, schema, adfmode.ModeStrict)
	if err == nil {
		t.Fatal("strict mode must abort on lossy inlineCard, got nil")
	}
	// Strict aborts with the typed CompatibilityError so the calling CLI
	// command can map it to exit 3 (validation).
	if _, ok := errors.AsType[*adf.CompatibilityError](err); !ok {
		t.Fatalf("expected *adf.CompatibilityError, got %T: %v", err, err)
	}
}

// The SAME doc that aborts strict MUST succeed in best-effort with a
// warning. Together these prove the dual-mode contract.
func TestBestEffortDegradesWhereStrictAborts(t *testing.T) {
	doc, _, err := adf.Parse([]byte(`{
		"type": "doc", "version": 1, "content": [
			{"type": "paragraph", "content": [
				{"type": "inlineCard", "attrs": {"url": "https://example.com/x"}}
			]}
		]
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	schema := adf.FieldCompatibility{Field: "description", InlineCardSupported: false}

	_, warnings, err := adf.ApplyCompatibility(doc, schema, adfmode.ModeBestEffort)
	if err != nil {
		t.Fatalf("best-effort must not abort, got %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d", len(warnings))
	}
	if !warnings[0].Lossy {
		t.Fatalf("warning must be lossy=true")
	}
}
