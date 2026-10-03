package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

type assembleData struct {
	Name string `json:"name"`
}

func TestAssembleDerivesOKFromErrors(t *testing.T) {
	tests := []struct {
		name     string
		errors   []envelope.Error
		exitCode int
		wantOK   bool
	}{
		{
			name:   "no errors is ok",
			errors: nil,
			wantOK: true,
		},
		{
			name:     "any error is not ok",
			errors:   []envelope.Error{{Code: "boom", Message: "m", Hint: "h"}},
			exitCode: 1,
			wantOK:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := output.Assemble(output.Inputs{
				Op:       "assembletest.op",
				Data:     assembleData{Name: "x"},
				Errors:   tt.errors,
				ExitCode: tt.exitCode,
				Now:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			})
			if res.Envelope.OK != tt.wantOK {
				t.Errorf("OK = %v, want %v (OK is derived from len(Errors), never passed directly)", res.Envelope.OK, tt.wantOK)
			}
		})
	}
}

func TestAssembleNormalizesNilSlicesToEmptyNeverNull(t *testing.T) {
	res := output.Assemble(output.Inputs{
		Op:   "assembletest.nilslices",
		Data: assembleData{Name: "x"},
		// Warnings and Errors both left nil.
		Now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})

	if res.Envelope.Errors == nil {
		t.Error("Errors is nil, want normalized to an empty (non-nil) slice")
	}
	if len(res.Envelope.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", res.Envelope.Errors)
	}
	if res.Envelope.Warnings == nil {
		t.Error("Warnings is nil, want normalized to an empty (non-nil) slice")
	}
	if len(res.Envelope.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty", res.Envelope.Warnings)
	}

	b, err := json.Marshal(res.Envelope)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(b)
	if strings.Contains(body, `"errors":null`) || strings.Contains(body, `"warnings":null`) {
		t.Errorf("marshaled envelope carries null for errors/warnings, want []: %s", body)
	}
	if !strings.Contains(body, `"errors":[]`) || !strings.Contains(body, `"warnings":[]`) {
		t.Errorf("marshaled envelope missing empty-array errors/warnings: %s", body)
	}
}

func TestAssembleSuccessEnvelopeOmitsExitCode(t *testing.T) {
	res := output.Assemble(output.Inputs{
		Op:   "assembletest.success",
		Data: assembleData{Name: "x"},
		Now:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if res.Envelope.Meta.ExitCode != nil {
		t.Errorf("Meta.ExitCode = %v, want nil on a success envelope (absence means success)", *res.Envelope.Meta.ExitCode)
	}
}

func TestAssembleFailureEnvelopeCarriesExitCode(t *testing.T) {
	res := output.Assemble(output.Inputs{
		Op:       "assembletest.failure",
		Data:     assembleData{Name: "x"},
		Errors:   []envelope.Error{{Code: "boom", Message: "m", Hint: "h"}},
		ExitCode: 7,
		Now:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if res.Envelope.Meta.ExitCode == nil {
		t.Fatal("Meta.ExitCode is nil, want a pointer to the failure exit code")
	}
	if *res.Envelope.Meta.ExitCode != 7 {
		t.Errorf("Meta.ExitCode = %d, want 7", *res.Envelope.Meta.ExitCode)
	}
}

// TestAssemblePanicsOnFailureWithZeroExitCode pins the deliberate panic: a
// failure Inputs (non-empty Errors) with ExitCode == 0 would tell an agent
// reading exit_code that the command succeeded, so Assemble refuses to
// build that envelope at all rather than let it through as plausible.
func TestAssemblePanicsOnFailureWithZeroExitCode(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Assemble did not panic on a failure Inputs with ExitCode == 0")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "exit code 0") {
			t.Errorf("panic = %v, want a message mentioning exit code 0", r)
		}
	}()
	output.Assemble(output.Inputs{
		Op:     "assembletest.badexit",
		Data:   assembleData{Name: "x"},
		Errors: []envelope.Error{{Code: "boom", Message: "m", Hint: "h"}},
		// ExitCode deliberately left at its zero value.
		Now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
}

func TestAssembleMetaFields(t *testing.T) {
	// Now is fed in a non-UTC location; Assemble must normalize to UTC
	// before formatting, per its RFC3339 timestamp contract.
	loc := time.FixedZone("UTC-5", -5*60*60)
	now := time.Date(2026, 6, 15, 10, 30, 0, 0, loc)

	res := output.Assemble(output.Inputs{
		Op:        "assembletest.meta",
		Data:      assembleData{Name: "x"},
		Now:       now,
		RequestID: "deadbeefdeadbeefdeadbeefdeadbeef",
	})

	if res.Envelope.Meta.Command != "assembletest.meta" {
		t.Errorf("Meta.Command = %q, want %q", res.Envelope.Meta.Command, "assembletest.meta")
	}
	if res.Envelope.Meta.ContractVersion != envelope.ContractVersion {
		t.Errorf("Meta.ContractVersion = %q, want %q", res.Envelope.Meta.ContractVersion, envelope.ContractVersion)
	}
	wantTS := "2026-06-15T15:30:00Z"
	if res.Envelope.Meta.Timestamp != wantTS {
		t.Errorf("Meta.Timestamp = %q, want %q (Now normalized to UTC)", res.Envelope.Meta.Timestamp, wantTS)
	}
	if res.Envelope.Meta.RequestID != "deadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("Meta.RequestID = %q, want the injected id verbatim", res.Envelope.Meta.RequestID)
	}
}

func TestAssembleCarriesDataThrough(t *testing.T) {
	res := output.Assemble(output.Inputs{
		Op:   "assembletest.data",
		Data: assembleData{Name: "carried"},
		Now:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	got, ok := res.Envelope.Data.(assembleData)
	if !ok {
		t.Fatalf("Envelope.Data = %#v (%T), want the assembleData value passed in", res.Envelope.Data, res.Envelope.Data)
	}
	if got.Name != "carried" {
		t.Errorf("Envelope.Data.Name = %q, want %q", got.Name, "carried")
	}
}

func TestNewRequestIDIs32CharHexAndVariesPerCall(t *testing.T) {
	a := output.NewRequestID()
	b := output.NewRequestID()

	for _, id := range []string{a, b} {
		if len(id) != 32 {
			t.Errorf("NewRequestID() = %q, want length 32, got %d", id, len(id))
		}
		for _, r := range id {
			if !strings.ContainsRune("0123456789abcdef", r) {
				t.Errorf("NewRequestID() = %q, want lowercase hex only, found %q", id, r)
			}
		}
	}
	if a == b {
		t.Errorf("NewRequestID() returned the same id twice: %q", a)
	}
}

// TestAssembleDropsTheFilterOnFailure exercises the drop DIRECTLY, by
// handing Assemble both a filter and errors.
//
// The end-to-end test that claimed this rule never reached it: every
// failure it used (unknown command, bad flag) happens BEFORE the filter
// is resolved onto the context, and the failure sink assembles without
// one — so deleting the guard entirely left that test green. Nothing
// supplied both halves at once, which is the only state the guard exists
// to handle.
func TestAssembleDropsTheFilterOnFailure(t *testing.T) {
	t.Parallel()
	filter, err := output.CompileFilter(".")
	if err != nil {
		t.Fatal(err)
	}

	failure := output.Assemble(output.Inputs{
		Op:        "probe",
		Errors:    []envelope.Error{{Code: "validation_failed", Message: "boom"}},
		ExitCode:  3,
		Now:       time.Unix(0, 0),
		RequestID: "r",
		Filter:    filter,
	})
	if failure.Filter != nil {
		t.Error("a failure envelope kept its filter; an expression written for the success shape would run over the error frame and can hide it")
	}

	// The same inputs minus the errors must KEEP the filter, so the guard
	// is a failure check rather than a blanket disable.
	success := output.Assemble(output.Inputs{
		Op:        "probe",
		Data:      map[string]any{"a": 1},
		Now:       time.Unix(0, 0),
		RequestID: "r",
		Filter:    filter,
	})
	if success.Filter == nil {
		t.Error("a success envelope lost its filter; --jq would silently do nothing")
	}

	// A success carrying warnings is still a success and still filters.
	warned := output.Assemble(output.Inputs{
		Op:        "probe",
		Data:      map[string]any{"a": 1},
		Warnings:  []envelope.Warning{{Type: "t", Message: "m"}},
		Now:       time.Unix(0, 0),
		RequestID: "r",
		Filter:    filter,
	})
	if warned.Filter == nil {
		t.Error("a warned success lost its filter; warnings are not failures")
	}
}

// TestRenderJSONRefusesToFilterAFailure pins the renderer's own check.
// Result is exported, so a caller can build one directly with both a
// failure envelope and a filter, bypassing Assemble entirely. Compact
// always checked; JSON did not.
func TestRenderJSONRefusesToFilterAFailure(t *testing.T) {
	t.Parallel()
	filter, err := output.CompileFilter(".errors[0].code")
	if err != nil {
		t.Fatal(err)
	}
	res := output.Result{
		Envelope: envelope.Envelope{
			OK:       false,
			Errors:   []envelope.Error{{Code: "validation_failed", Message: "boom"}},
			Warnings: []envelope.Warning{},
		},
		Filter: filter,
	}
	for _, format := range []output.Format{output.FormatJSON, output.FormatCompact} {
		t.Run(string(format), func(t *testing.T) {
			var buf bytes.Buffer
			if err := output.Render(t.Context(), &buf, format, res); err != nil {
				t.Fatalf("render: %v", err)
			}
			if !strings.Contains(buf.String(), `"ok":false`) {
				t.Errorf("%s filtered an error frame: %s", format, buf.String())
			}
		})
	}
}
