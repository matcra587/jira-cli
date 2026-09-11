package cmdutil

import (
	"encoding/json"
	"io"
	"reflect"
	"time"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/spf13/cobra"
)

// IssueRef aliases the canonical issue-identity type, which moved to the
// leaf package internal/envelope so renderers and command packages can
// share it without an import cycle. The alias keeps the many existing
// cmdutil.IssueRef call sites compiling; new code may use either name.
type IssueRef = envelope.IssueRef

// WriteEnvelope emits the standard envelope shape for a command with no
// warnings.
func WriteEnvelope(cmd *cobra.Command, command string, data any) error {
	return WriteEnvelopeWithWarnings(cmd, command, data, nil)
}

// WriteEnvelopeWithErrors emits an ok:false envelope while preserving a
// command's data payload. Use this when the command successfully gathered
// diagnostics but the diagnostics themselves mean the command is unhealthy
// (for example auth.status with an invalid profile).
func WriteEnvelopeWithErrors(cmd *cobra.Command, command string, data any, errorsOut []cli.Error) error {
	if len(errorsOut) == 0 {
		return WriteEnvelope(cmd, command, data)
	}
	if UsePlainOutput(cmd) {
		if err := cli.WriteCommandPlain(cmd.OutOrStdout(), command, data, PlainOptionsForCommand(cmd)...); err != nil {
			return err
		}
		return writePlainFailureDiagnostics(cmd.ErrOrStderr(), command, data, errorsOut)
	}
	exit := cli.ExitCode(errorsOut[0])
	env := cli.Envelope{
		OK: false,
		Meta: cli.Meta{
			Command:           command,
			ExitCode:          &exit,
			Timestamp:         time.Now().UTC().Format(time.RFC3339),
			RequestID:         cli.NewRequestID(),
			UpstreamRequestID: firstUpstreamRequestID(errorsOut),
		},
		Data:     data,
		Errors:   errorsOut,
		Warnings: []cli.Warning{},
	}
	// Machine mode: the failure envelope goes to stdout, the same stream as
	// success, so a consumer piping stdout gets a parseable ok:false result.
	return writeEnvelopeJSON(cmd, cmd.OutOrStdout(), env)
}

// firstUpstreamRequestID surfaces a Jira-side trace id from the error set
// into meta, so a consumer that only reads meta can still hand Atlassian
// support a correlatable id. The first error carrying one wins — the set
// is ordered worst-first, and a keyed multi-target run has one id per
// entry anyway.
func firstUpstreamRequestID(errorsOut []cli.Error) string {
	for _, e := range errorsOut {
		if e.UpstreamRequestID != "" {
			return e.UpstreamRequestID
		}
	}
	return ""
}

// WriteEnvelopeWithResponseAndErrors emits an ok:false envelope with both a
// preserved data payload and pagination metadata from resp.
func WriteEnvelopeWithResponseAndErrors(cmd *cobra.Command, command string, data any, resp *jira.Response, errorsOut []cli.Error) error {
	if resp == nil {
		return WriteEnvelopeWithErrors(cmd, command, data, errorsOut)
	}
	if len(errorsOut) == 0 {
		return WriteEnvelopeWithResponse(cmd, command, data, resp)
	}
	if UsePlainOutput(cmd) {
		if err := cli.WriteCommandPlain(cmd.OutOrStdout(), command, data, PlainOptionsForCommand(cmd)...); err != nil {
			return err
		}
		return writePlainFailureDiagnostics(cmd.ErrOrStderr(), command, data, errorsOut)
	}
	exit := cli.ExitCode(errorsOut[0])
	env := cli.Envelope{
		OK: false,
		Meta: cli.Meta{
			Command:           command,
			ExitCode:          &exit,
			Timestamp:         time.Now().UTC().Format(time.RFC3339),
			RequestID:         cli.NewRequestID(),
			UpstreamRequestID: resp.UpstreamRequestID(),
			Pagination:        paginationFromResponse(resp),
		},
		Data:     data,
		Errors:   errorsOut,
		Warnings: []cli.Warning{},
	}
	// Machine mode: the failure envelope goes to stdout, the same stream as
	// success, so a consumer piping stdout gets a parseable ok:false result.
	return writeEnvelopeJSON(cmd, cmd.OutOrStdout(), env)
}

func writePlainFailureDiagnostics(stderr io.Writer, command string, data any, errorsOut []cli.Error) error {
	switch command {
	case "issue.view":
		return cli.WriteIssueViewFailureDiagnostics(stderr, data, errorsOut)
	default:
		return cli.WriteKeyedResultsFailureDiagnostics(stderr, data, errorsOut)
	}
}

// WriteEnvelopeWithRawWarnings emits the standard envelope shape with a
// caller-supplied list of free-form warning maps. Necessary for warnings
// whose schema (cache-truncated, rate-limit-during-paginate) carries
// fields outside the cli.Warning struct's Type/Message/Field/Path/etc.
// surface — see contracts/envelope-shapes.md.
func WriteEnvelopeWithRawWarnings(cmd *cobra.Command, command string, data any, warnings []map[string]any) error {
	return writeRawWarningEnvelope(cmd, command, data, warnings, nil)
}

// WriteEnvelopeWithPaginationAndRawWarnings is WriteEnvelopeWithRawWarnings
// plus a pagination block in the JSON meta. The bounded --all drain uses it:
// the drain knows its terminal state (isLast = not truncated, total = the
// drained count), but has no *jira.Response to derive it from, so the caller
// builds the pagination directly. Pagination rides only in the JSON envelope,
// matching the single-page WriteEnvelopeWithResponse path.
func WriteEnvelopeWithPaginationAndRawWarnings(cmd *cobra.Command, command string, data any, pagination *cli.Pagination, warnings []map[string]any) error {
	return writeRawWarningEnvelope(cmd, command, data, warnings, pagination)
}

func writeRawWarningEnvelope(cmd *cobra.Command, command string, data any, warnings []map[string]any, pagination *cli.Pagination) error {
	for _, cw := range collectedCommandWarnings(cmd) {
		warnings = append(warnings, map[string]any{
			"type":    cw.Type,
			"message": cw.Message,
			"lossy":   cw.Lossy,
		})
	}
	if UseCompactOutput(cmd) {
		// compact is the data payload without the envelope. Warnings have
		// no envelope to ride in, so fold any non-empty warning set into
		// the data so credential-cleanup and pagination notices stay
		// visible to agents (they would otherwise be silently dropped).
		// Pagination folds in the same way, matching the
		// WriteEnvelopeWithResponse compact path.
		if pagination != nil {
			if m, ok := dataAsMap(data); ok {
				m["pagination"] = pagination
				data = m
			}
		}
		return cli.WriteCompact(cmd.OutOrStdout(), FoldRawWarningsIntoData(data, warnings))
	}
	if UsePlainOutput(cmd) {
		if err := cli.WriteCommandPlain(cmd.OutOrStdout(), command, data, PlainOptionsForCommand(cmd)...); err != nil {
			return err
		}
		return MirrorADFWarningsToStderr(cmd.ErrOrStderr(), RawWarningsToCLI(warnings))
	}
	meta := map[string]any{
		"command":    command,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"request_id": cli.NewRequestID(),
	}
	if pagination != nil {
		meta["pagination"] = pagination
	}
	// The raw-warning path keeps its warnings as maps: they carry arbitrary
	// structured fields (rate-limit retry seconds, drain cut reasons) the typed
	// Warning struct does not model, so folding them into cli.Warning would
	// drop data. Build the envelope document as a map and route it through the
	// shared clog writer — same byte-shape as before, now with the write tracker
	// wrapper capturing broken-pipe / quota write failures.
	body := map[string]any{
		"ok":       true,
		"meta":     meta,
		"data":     data,
		"errors":   []any{},
		"warnings": rawWarningsOrEmpty(warnings),
	}
	if UseHumanJSONOutput(cmd) {
		return cli.WriteHumanJSON(cmd.OutOrStdout(), body, HumanJSONPrintTheme(cmd))
	}
	return cli.WriteEnvelopeDocument(cmd.OutOrStdout(), body)
}

// foldWarnings merges a non-empty warning slice into a compact-mode data
// payload so a correctness warning survives a mode that has no envelope
// to carry it. A map payload gets a "warnings" key alongside its existing
// fields; a non-map payload (slice or scalar) is wrapped as
// {"data": ..., "warnings": ...} so the warning is never silently
// dropped. An empty warning set returns the data unchanged.
func foldWarnings[T any](data any, warnings []T) any {
	if len(warnings) == 0 {
		return data
	}
	if m, ok := dataAsMap(data); ok {
		out := CopyAnyMap(m)
		out["warnings"] = warnings
		return out
	}
	return map[string]any{"data": data, "warnings": warnings}
}

// dataAsMap views an envelope data payload as a string-keyed map so compact
// folds (warnings, pagination) can merge keys in. Maps pass through; a typed
// Output struct (internal/envelope) round-trips through JSON so tags,
// omitempty, and embedding fold exactly as the wire shape does. Non-object
// payloads (arrays, scalars) report false.
func dataAsMap(data any) (map[string]any, bool) {
	if m, ok := data.(map[string]any); ok {
		return m, true
	}
	rv := reflect.ValueOf(data)
	for rv.IsValid() && rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		return nil, false
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return out, true
}

// FoldRawWarningsIntoData folds a raw map-shaped warning slice into a
// compact payload. See foldWarnings.
func FoldRawWarningsIntoData(data any, warnings []map[string]any) any {
	return foldWarnings(data, warnings)
}

// FoldWarningsIntoData folds a typed cli.Warning slice into a compact
// payload. See foldWarnings.
func FoldWarningsIntoData(data any, warnings []cli.Warning) any {
	return foldWarnings(data, warnings)
}

func rawWarningsOrEmpty(w []map[string]any) []map[string]any {
	if w == nil {
		return []map[string]any{}
	}
	return w
}

// RawWarningsToCLI converts free-form map-shaped warnings into typed
// cli.Warning values, dropping empty entries.
func RawWarningsToCLI(warnings []map[string]any) []cli.Warning {
	out := make([]cli.Warning, 0, len(warnings))
	for _, warning := range warnings {
		if len(warning) == 0 {
			continue
		}
		out = append(out, cli.Warning{
			Type:    stringField(warning, "type"),
			Message: rawWarningMessage(warning),
			Field:   stringField(warning, "field"),
			Path:    stringField(warning, "path"),
			Lossy:   boolField(warning, "lossy"),
		})
	}
	return out
}

func rawWarningMessage(warning map[string]any) string {
	for _, key := range []string{"message", "remediation", "reason", "type"} {
		if value := stringField(warning, key); value != "" {
			return value
		}
	}
	return "warning"
}

// WriteEnvelopeWithWarnings is the warning-emitting envelope entry
// point — every command emitting structured warnings (typically from
// pipeline.RunMutation) calls this so warnings travel in the envelope
// under JSON mode and mirror to stderr under TTY/--plain (via the
// route helper).
func WriteEnvelopeWithWarnings(cmd *cobra.Command, command string, data any, warnings []adf.Warning) error {
	cliWarnings := make([]cli.Warning, 0, len(warnings))
	for _, w := range warnings {
		cliWarnings = append(cliWarnings, cli.WarningFrom(w))
	}
	cliWarnings = append(cliWarnings, collectedCommandWarnings(cmd)...)
	if UseCompactOutput(cmd) {
		// compact has no envelope; fold warnings into the data so a failed
		// credential cleanup or other correctness notice is not lost.
		return cli.WriteCompact(cmd.OutOrStdout(), FoldWarningsIntoData(data, cliWarnings))
	}
	if UsePlainOutput(cmd) {
		// Data on stdout, warnings on stderr as clog WRN.
		if err := cli.WriteCommandPlain(cmd.OutOrStdout(), command, data, PlainOptionsForCommand(cmd)...); err != nil {
			return err
		}
		return MirrorADFWarningsToStderr(cmd.ErrOrStderr(), cliWarnings)
	}
	env := cli.Envelope{
		OK: true,
		Meta: cli.Meta{
			Command:   command,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			RequestID: cli.NewRequestID(),
		},
		Data:     data,
		Errors:   []cli.Error{},
		Warnings: cliWarnings,
	}
	if env.Warnings == nil {
		env.Warnings = []cli.Warning{}
	}
	return writeEnvelopeJSON(cmd, cmd.OutOrStdout(), env)
}

// WriteEnvelopeWithResponse emits the standard envelope shape with
// pagination derived from an HTTP response and no pipeline warnings.
func WriteEnvelopeWithResponse(cmd *cobra.Command, command string, data any, resp *jira.Response) error {
	return WriteEnvelopeWithResponseAndWarnings(cmd, command, data, resp, nil)
}

// WriteEnvelopeWithResponseAndWarnings is the warning-emitting
// envelope entry point for commands that BOTH have a paginated/HTTP
// response AND need to surface
// pipeline warnings (e.g., live-submit issue create / edit / comment /
// worklog where the pipeline has already validated and produced
// best-effort warnings before the API call). Mirrors
// WriteEnvelopeWithWarnings's TTY routing for plain mode so the data
// stays on stdout and warnings mirror to stderr as clog WRN lines.
func WriteEnvelopeWithResponseAndWarnings(cmd *cobra.Command, command string, data any, resp *jira.Response, warnings []adf.Warning) error {
	if resp == nil {
		// WriteEnvelopeWithWarnings collects the credential warnings itself.
		return WriteEnvelopeWithWarnings(cmd, command, data, warnings)
	}
	cliWarnings := make([]cli.Warning, 0, len(warnings))
	for _, w := range warnings {
		cliWarnings = append(cliWarnings, cli.WarningFrom(w))
	}
	cliWarnings = append(cliWarnings, collectedCommandWarnings(cmd)...)
	if UseCompactOutput(cmd) {
		if m, ok := dataAsMap(data); ok {
			if pagination := paginationFromResponse(resp); pagination != nil {
				m["pagination"] = pagination
			}
			return cli.WriteCompact(cmd.OutOrStdout(), FoldWarningsIntoData(m, cliWarnings))
		}
		return cli.WriteCompact(cmd.OutOrStdout(), data)
	}
	if UsePlainOutput(cmd) {
		if err := cli.WriteCommandPlain(cmd.OutOrStdout(), command, data, PlainOptionsForCommand(cmd)...); err != nil {
			return err
		}
		return MirrorADFWarningsToStderr(cmd.ErrOrStderr(), cliWarnings)
	}
	env := cli.Envelope{
		OK: true,
		Meta: cli.Meta{
			Command:           command,
			Timestamp:         time.Now().UTC().Format(time.RFC3339),
			RequestID:         cli.NewRequestID(),
			UpstreamRequestID: resp.UpstreamRequestID(),
			Pagination:        paginationFromResponse(resp),
		},
		Data:     data,
		Errors:   []cli.Error{},
		Warnings: cliWarnings,
	}
	if env.Warnings == nil {
		env.Warnings = []cli.Warning{}
	}
	return writeEnvelopeJSON(cmd, cmd.OutOrStdout(), env)
}

func writeEnvelopeJSON(cmd *cobra.Command, w io.Writer, env cli.Envelope) error {
	if UseHumanJSONOutput(cmd) {
		return cli.WriteHumanJSON(w, env, HumanJSONPrintTheme(cmd))
	}
	return cli.WriteEnvelope(w, env)
}

// MirrorADFWarningsToStderr is a thin wrapper around cli.RouteWarnings
// for the plain-mode warning-only path used by both
// WriteEnvelopeWithWarnings and WriteEnvelopeWithResponseAndWarnings.
func MirrorADFWarningsToStderr(stderr io.Writer, warnings []cli.Warning) error {
	if len(warnings) == 0 || stderr == nil {
		return nil
	}
	return cli.RouteWarnings(cli.RouteOptions{
		Stderr:   stderr,
		Stdout:   io.Discard, // data was already written above
		Mode:     cli.RoutePlain,
		Command:  "",
		Data:     map[string]any{}, // no-op data, we only want the WRN lines
		Warnings: warnings,
	})
}

// paginationFromResponse derives the envelope pagination block from an HTTP
// response, or nil when resp is nil or carries no pagination signal at all —
// a mutation response would otherwise emit a zero-value block that reads as
// "empty last page".
func paginationFromResponse(resp *jira.Response) *cli.Pagination {
	if resp == nil {
		return nil
	}
	if resp.MaxResults <= 0 && !resp.TotalKnown && resp.NextPageToken == "" { // pagination-exempt: signal presence check, no cursor arithmetic
		return nil
	}
	p := &cli.Pagination{
		StartAt:    resp.StartAt, // pagination-exempt: output-shape, not consumer cursor
		MaxResults: resp.MaxResults,
		IsLast:     resp.NextCursor() == "",
		NextCursor: resp.NextCursor(),
	}
	if resp.TotalKnown {
		p.Total = new(resp.Total)
	}
	return p
}

// PaginationFromResponse is the exported form for commands that need to
// build the canonical pagination block themselves (drains, client-side
// windows) before handing it to WriteEnvelopeWithPagination.
func PaginationFromResponse(resp *jira.Response) *cli.Pagination {
	return paginationFromResponse(resp)
}
