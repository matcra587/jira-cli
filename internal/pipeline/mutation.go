package pipeline

import (
	"errors"
	"fmt"
	"maps"
	"regexp"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli/adfmode"
)

// MutationInput is the realistic, command-facing input shape for the
// 5-stage pipeline. internal/cli/root/commands.go assembles one of these from
// flag values, --json-input, the resolved profile, and the active
// schema; pipeline.RunMutation orchestrates stages 1–5.
//
// Either Schema (preloaded, e.g., from cache) OR SchemaFetcher (lazy,
// triggers refresh-once on ErrSchemaUnknown) MAY be provided. When
// neither is set, stage 3 (field schema / screen validation) is
// skipped — stage 4 (customfield encoding) still runs and rejects
// malformed values regardless of schema availability.
//
// CURRENT INTEGRATION DEPTH: internal/cli/root/commands.go wires this struct
// with Mode + Fields + DryRun + ADFDoc. Stage 2 (ADF validation) is
// now load-bearing: ValidateDoc rejects unknown nodes, illegal marks,
// and unknown mark types before submission.
// Stage 3 (field schema / screen validation) remains opt-in via
// SchemaFetcher — wiring ProjectService into a SchemaFetcher closure
// to activate it is documented future work. Stages 1 (parse), 2
// (validate), 4 (customfield encoding), and 5 (dry-run vs submit
// gating) all carry production traffic; stage 3 is provided for
// future field-mapping work.
type MutationInput struct {
	Mode adfmode.Mode

	// Stage 1 — parse / shape. Set by the caller after pulling values
	// out of cobra flags + --json-input. Empty means parse succeeded.
	ParseError string

	// Stage 2 — ADF + compatibility. The Document the caller built
	// (typically via adf.FromMarkdown for --markdown, or
	// via --json-input for raw ADF). FieldCompat selects per-field
	// compatibility (inlineCard rules).
	ADFDoc      *adf.Document
	FieldCompat *adf.FieldCompatibility

	// NamedADFDocs holds additional ADF substructures keyed by their
	// field name (e.g. "description_adf"). All are validated through
	// ValidateDoc at stage 2 with the same mode as ADFDoc. This is the
	// hook used by issue create --json-input to catch garbage ADF in
	// payload subfields.
	NamedADFDocs map[string]adf.Document

	// MarkdownWarnings carries warnings produced when the caller
	// converted Markdown input to ADF (adf.FromMarkdownLossy). A lossy
	// conversion means user content was dropped before the document
	// even reached the pipeline. In strict mode any lossy warning here
	// aborts the mutation at stage 2; in best-effort mode the warnings
	// are surfaced and the partial document proceeds.
	MarkdownWarnings []adf.Warning

	// Stage 3 — field schema / screen. Either Schema (preloaded) or
	// SchemaFetcher (lazy with refresh-once + known-safe fallback).
	Schema        ScreenSchema
	SchemaFetcher SchemaFetcher
	// ScreenValidationExemptFields skips stage 3 only for native fields
	// that are already in Jira's API-ready shape. customfield_NNNNN keys
	// still go through screen validation so their schema-backed encoding
	// remains available in stage 4.
	ScreenValidationExemptFields map[string]bool

	// Stage 4 — customfield encoding. The Fields map is what the caller
	// would post to Jira's REST API. Each customfield_xxxxx value is
	// validated through pkg/jira/customfield.Registry.
	Fields map[string]any

	// Stage 5.
	DryRun bool
}

// MutationResult reports the outcome of one RunMutation call.
type MutationResult struct {
	Aborted      bool
	AbortedAt    Stage
	Submitted    bool
	PreviewReady bool
	// SchemaValidatedRemotely is true only when a configured SchemaFetcher
	// resolved Jira metadata and stage 3 validated at least one field against
	// it. A skipped stage or best-effort fallback leaves it false.
	SchemaValidatedRemotely bool

	// SubmitFields is the post-validation, post-encoding fields map the
	// caller should send to Jira. In strict-abort cases this is nil.
	SubmitFields map[string]any
	// SubmitADF is the post-validation ADF doc (after compatibility
	// passes). nil if no doc was supplied.
	SubmitADF *adf.Document
	// Warnings collected from every stage that ran. Non-empty even when
	// a later stage aborted — earlier-stage observations always survive.
	Warnings []adf.Warning
	Err      error
}

// RunMutation executes stages 1–5 with realistic inputs. Designed to
// be the single call site for every mutation command.
func RunMutation(in MutationInput) MutationResult {
	res := MutationResult{}

	// --- Stage 1: parse / shape ---
	if in.ParseError != "" {
		res.Aborted = true
		res.AbortedAt = StageParse
		res.Err = errors.New(in.ParseError)
		return res
	}

	// --- Stage 2: ADF validation + compatibility ---
	// Markdown-conversion warnings come from before the pipeline (the
	// caller ran adf.FromMarkdownLossy). Surface them on every path; in
	// strict mode a lossy conversion aborts before submission so dropped
	// user content never reaches Jira silently.
	res.Warnings = append(res.Warnings, in.MarkdownWarnings...)
	if in.Mode == adfmode.ModeStrict {
		for _, w := range in.MarkdownWarnings {
			if w.Lossy {
				res.Aborted = true
				res.AbortedAt = StageADF
				// Typed so the error mapper emits the source-mapped
				// message with a remediation hint, never an empty one.
				res.Err = adf.LossyConversionError{Warning: w}
				return res
			}
		}
	}

	doc := in.ADFDoc
	if doc != nil {
		// Normalize away losslessly-invalid constructs (e.g. empty text
		// nodes) BEFORE validation and submission. Jira rejects the whole
		// document with an opaque INVALID_INPUT for these, yet they carry no
		// renderable content, so the repair runs in every mode and the
		// cleaned doc is what gets validated and sent.
		normalized, normWarnings := adf.Normalize(*doc)
		res.Warnings = append(res.Warnings, normWarnings...)
		doc = &normalized

		// ValidateDoc enforces root shape (always) and per-mode node/mark
		// rules. Runs before ApplyCompatibility so unknown nodes are
		// caught at the validation stage, not on the wire.
		warnings, err := adf.ValidateDoc(*doc, in.Mode)
		res.Warnings = append(res.Warnings, warnings...)
		if err != nil {
			res.Aborted = true
			res.AbortedAt = StageADF
			res.Err = err
			return res
		}

		if in.FieldCompat != nil {
			converted, compWarnings, compErr := adf.ApplyCompatibility(*doc, *in.FieldCompat, in.Mode)
			res.Warnings = append(res.Warnings, compWarnings...)
			if compErr != nil {
				res.Aborted = true
				res.AbortedAt = StageADF
				res.Err = compErr
				return res
			}
			doc = &converted
		}
	}
	res.SubmitADF = doc

	// Stage 2b: validate any additional named ADF subfields (e.g.
	// description_adf in issue create --json-input payloads). Each is
	// run through ValidateDoc with the same mode. FieldCompat is not
	// applied here — these are opaque payload subfields, not the primary
	// document.
	for field, namedDoc := range in.NamedADFDocs {
		warnings, err := adf.ValidateDoc(namedDoc, in.Mode)
		// Attach field context to warnings so the user sees which key failed.
		for i := range warnings {
			if warnings[i].Field == "" {
				warnings[i].Field = field
			}
		}
		res.Warnings = append(res.Warnings, warnings...)
		if err != nil {
			res.Aborted = true
			res.AbortedAt = StageADF
			res.Err = err
			return res
		}
	}

	// --- Stage 3: field schema / screen ---
	// Flatten contract-level custom_fields into top-level
	// customfield_NNNNN keys BEFORE screen validation so nested and raw
	// custom fields share one namespace and one validation path. A
	// malformed wrapper or a colliding key is fatal at this boundary.
	flatFields, flattenErr := FlattenCustomFields(in.Fields)
	if flattenErr != nil {
		res.Aborted = true
		res.AbortedAt = StageFieldSchema
		res.Err = flattenErr
		return res
	}
	schemaFields, exemptFields := splitScreenValidationFields(flatFields, in.ScreenValidationExemptFields)

	schema := in.Schema
	var fields map[string]any
	if len(schemaFields) == 0 {
		fields = mergeScreenValidationExemptFields(map[string]any{}, exemptFields)
	} else if in.SchemaFetcher != nil {
		// Resolve via fetcher; refresh once on a transient unknown.
		s, _, err := ResolveScreenSchemaStrict(in.SchemaFetcher)
		switch {
		case err == nil:
			schema = s
			validated, warnings, err := ValidateFields(schemaFields, schema, in.Mode)
			res.Warnings = append(res.Warnings, warnings...)
			if err != nil {
				res.Aborted = true
				res.AbortedAt = StageFieldSchema
				res.Err = err
				return res
			}
			res.SchemaValidatedRemotely = true
			fields = mergeScreenValidationExemptFields(validated, exemptFields)
		case errors.Is(err, ErrSchemaNotFound):
			// A 404 / unknown project or issue type. This is a definite
			// user error, never transient — fatal in every mode. The
			// known-safe fallback must not mask a typo'd target.
			res.Aborted = true
			res.AbortedAt = StageFieldSchema
			res.Err = fmt.Errorf("screen schema unavailable: project or issue type not found: %w", err)
			return res
		case errors.Is(err, ErrSchemaUnknown) && in.Mode == adfmode.ModeBestEffort:
			// Best-effort + a transient miss: strip to the known-safe
			// field set so an unresolved screen never lets an unverified
			// field through.
			f, warnings := ApplyKnownSafeFallback(schemaFields)
			res.Warnings = append(res.Warnings, warnings...)
			fields = mergeScreenValidationExemptFields(f, exemptFields)
		case errors.Is(err, ErrSchemaUnknown):
			// Strict + a transient miss (no createmeta access, transport
			// failure, timeout). The CLI cannot tell which custom fields
			// are off-screen, so forwarding them would let unvalidated
			// fields reach Jira. Strict mode aborts: a missing schema is
			// fatal in strict mode. The underlying transport cause is
			// preserved in the error.
			res.Aborted = true
			res.AbortedAt = StageFieldSchema
			res.Err = fmt.Errorf("screen schema could not be resolved in strict mode: %w", err)
			return res
		default:
			res.Aborted = true
			res.AbortedAt = StageFieldSchema
			res.Err = err
			return res
		}
	} else if schema.ValidFields != nil {
		// Preloaded schema (non-nil ValidFields means caller actually
		// supplied one; empty map vs nil distinguishes "valid empty
		// schema rejects everything" from "no schema, skip stage 3").
		validated, warnings, err := ValidateFields(schemaFields, schema, in.Mode)
		res.Warnings = append(res.Warnings, warnings...)
		if err != nil {
			res.Aborted = true
			res.AbortedAt = StageFieldSchema
			res.Err = err
			return res
		}
		fields = mergeScreenValidationExemptFields(validated, exemptFields)
	} else {
		// No schema provided — caller intentionally bypassed stage 3.
		// Stage 4 still runs; the customfield registry will reject any
		// malformed values regardless of screen membership.
		fields = flatFields
	}

	// --- Stage 4: customfield registry ---
	encoded, warnings, err := encodeCustomFields(fields, schema, in.Mode)
	res.Warnings = append(res.Warnings, warnings...)
	if err != nil {
		res.Aborted = true
		res.AbortedAt = StageCustomField
		res.Err = err
		return res
	}
	res.SubmitFields = encoded

	// --- Stage 5: dry-run preview / live submit ---
	if in.DryRun {
		res.PreviewReady = true
		return res
	}
	res.Submitted = true
	return res
}

func splitScreenValidationFields(fields map[string]any, exempt map[string]bool) (map[string]any, map[string]any) {
	if len(fields) == 0 {
		return map[string]any{}, map[string]any{}
	}
	validated := make(map[string]any, len(fields))
	skipped := map[string]any{}
	for key, value := range fields {
		if exempt[key] && !customfieldIDPattern.MatchString(key) {
			skipped[key] = value
			continue
		}
		validated[key] = value
	}
	return validated, skipped
}

func mergeScreenValidationExemptFields(fields, exempt map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+len(exempt))
	maps.Copy(out, fields)
	maps.Copy(out, exempt)
	return out
}

// customfieldIDPattern matches Jira customfield ID keys (customfield_NNNN).
var customfieldIDPattern = regexp.MustCompile(`^customfield_\d+$`)

// encodeCustomFields runs stage 4: it validates and encodes every
// customfield_NNNNN value in fields against the type the screen schema
// declares for it, then routes the outcome through the single shared
// CustomFieldDropPolicy so create/edit/clone/move all behave the same.
//
//   - When the schema declares a known schema.custom type for the
//     field, the customfield registry validates and encodes the value.
//     A malformed value is fatal in strict mode and dropped with a
//     warning in best-effort mode.
//   - When no type is known (no schema, or a marketplace/vendor field
//     the registry cannot map), the value is forwarded opaquely with a
//     warning — never silently dropped.
//
// Native (non customfield_NNNNN) keys pass straight through: they are
// not custom fields and stage 3 already screen-validated them.
func encodeCustomFields(fields map[string]any, schema ScreenSchema, mode adfmode.Mode) (map[string]any, []adf.Warning, error) {
	if len(fields) == 0 {
		return fields, nil, nil
	}
	out := make(map[string]any, len(fields))
	var warnings []adf.Warning
	for k, v := range fields {
		if !customfieldIDPattern.MatchString(k) {
			// Native field — not a custom field. Pass through.
			out[k] = v
			continue
		}
		fieldType := fieldTypeFor(schema, k)
		encoded, knownType, encErr := encodeCustomFieldByType(fieldType, v)
		if !knownType {
			// Unknown / unmappable type — opaque pass-through with a
			// warning, per the shared policy.
			decision := CustomFieldDropPolicy(k, "", false, mode)
			warnings = append(warnings, adf.Warning{
				Type:    "customfield_unknown_type",
				Message: decision.Warning,
				Field:   k,
				Lossy:   false,
			})
			out[k] = v
			continue
		}
		if encErr != nil {
			decision := CustomFieldDropPolicy(k, fieldType, true, mode)
			if decision.Fatal {
				return nil, warnings, &CustomFieldError{Field: k, Reason: encErr.Error()}
			}
			warnings = append(warnings, adf.Warning{
				Type:    "customfield_invalid",
				Message: decision.Warning + ": " + encErr.Error(),
				Field:   k,
				Lossy:   true,
			})
			continue
		}
		out[k] = encoded
	}
	return out, warnings, nil
}

// CustomFieldError is the typed error returned by stage 4.
type CustomFieldError struct {
	Field  string
	Reason string
}

func (e *CustomFieldError) Error() string {
	if e == nil {
		return "<nil customfield error>"
	}
	return "customfield: " + e.Field + ": " + e.Reason
}
