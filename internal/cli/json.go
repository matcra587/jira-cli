package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/gechr/clog"
	clogtheme "github.com/gechr/clog/theme"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// Envelope is the machine-readable JSON output contract. ok is always
// emitted; a successful command sets ok:true and a failed command sets
// ok:false with data:null and a populated errors slice.
type Envelope struct {
	OK       bool      `json:"ok"`
	Meta     Meta      `json:"meta"`
	Data     any       `json:"data"`
	Errors   []Error   `json:"errors"`
	Warnings []Warning `json:"warnings"`
}

// Meta is the envelope's command-context block. Machine envelopes omit
// profile entirely — a command that must report a profile puts it in
// command-specific data. ExitCode is present only on failure envelopes.
//
// RequestID is a locally generated correlation id; UpstreamRequestID is
// Jira's own trace id (Atl-Traceid / X-ARequestId) for the exchange —
// the value to quote to Atlassian support — present whenever the command
// had a Jira response to read it from.
type Meta struct {
	Command           string      `json:"command"`
	ExitCode          *int        `json:"exit_code,omitempty"`
	Timestamp         string      `json:"timestamp"`
	RequestID         string      `json:"request_id,omitempty"`
	UpstreamRequestID string      `json:"upstream_request_id,omitempty"`
	Pagination        *Pagination `json:"pagination,omitempty"`
}

// Pagination is the JSON envelope's output-shape descriptor for paginated
// responses. The startAt/nextPageToken fields here describe the SHAPE the
// CLI emits — they are not cursor-management state in command code.
// pagination-exempt: output-shape only, not consumer-side cursor management.
//
// This is the ONE pagination shape and it lives in ONE place: meta.pagination
// on single-target reads, and results[].data.pagination (same shape) inside
// keyed multi-key results. isLast and nextCursor are the authoritative walk
// signals; total appears only when the endpoint reports a real total —
// token-paged endpoints (enhanced search) never do, so a fabricated 0 is
// never emitted.
type Pagination = envelope.Pagination

// KnownTotal wraps an authoritative total for Pagination.Total. Only call
// it with a value the endpoint actually reported.
//
//go:fix inline
func KnownTotal(total int) *int { return new(total) }

// Error is one structured failure entry in the envelope errors slice.
// type, code, message, hint, and retryable are always present; agents
// branch on code (stable snake_case), never on message. The remaining
// fields are optional context, omitted when empty.
//
// message and hint are disjoint. message states what failed — a
// diagnosis, not a wording contract; it may carry summarized upstream
// text. hint states what to do next — remediation; it may name
// commands, fields, or retry behavior. Remediation never belongs in
// message, and a diagnosis never belongs in hint.
type Error struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint"`
	Retryable bool   `json:"retryable"`

	// Flag / Field / Path scope a validation failure to an input;
	// Suggestions carries "did you mean" candidates for it, pre-formatted
	// as the caller would type them (e.g. "--output").
	Flag        string   `json:"flag,omitempty"`
	Field       string   `json:"field,omitempty"`
	Path        string   `json:"path,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`

	// HTTPStatus / RetryAfterSeconds carry transport metadata.
	HTTPStatus         int `json:"http_status,omitempty"`
	RetryAfterSeconds  int `json:"retry_after_seconds,omitempty"`
	RateLimitRemaining int `json:"rate_limit_remaining,omitempty"`

	// Provider / UpstreamCode / UpstreamStatus preserve a backend's own
	// failure identity. UpstreamCode stays empty when the provider
	// exposes no stable machine code (Jira does not).
	Provider       string `json:"provider,omitempty"`
	UpstreamCode   string `json:"upstream_code,omitempty"`
	UpstreamStatus int    `json:"upstream_status,omitempty"`
	// UpstreamRequestID is Jira's trace id for the failed exchange
	// (Atl-Traceid / X-ARequestId) — the value Atlassian support can
	// correlate, unlike the locally generated meta.request_id.
	UpstreamRequestID string `json:"upstream_request_id,omitempty"`

	// UpstreamMessages / UpstreamFieldErrors preserve Jira's
	// ErrorCollection body verbatim. Wording is not contractual; these
	// are diagnostic context, never a branch target.
	UpstreamMessages    []string          `json:"upstream_messages,omitempty"`
	UpstreamFieldErrors map[string]string `json:"upstream_field_errors,omitempty"`

	// Candidates carries structured disambiguation context — populated by
	// the watcher resolver when /user/search returns 2+ matches.
	Candidates []map[string]any `json:"candidates,omitempty"`
}

// Warning is a non-fatal best-effort diagnostic emitted alongside data.
// lossy is mandatory and serialized even when false; consumers branch on
// it without nil checks. field/path/node_type/mark_type are optional
// context fields.
type Warning struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Field    string `json:"field,omitempty"`
	Path     string `json:"path,omitempty"`
	NodeType string `json:"node_type,omitempty"`
	MarkType string `json:"mark_type,omitempty"`
	Lossy    bool   `json:"lossy"`
}

// WarningSource is anything that can describe itself as a cli.Warning. The
// pkg/adf Warning satisfies this; we keep cli ignorant of pkg/adf to avoid
// the import cycle (pkg/adf -> cli -> pkg/adf via the plain renderer).
type WarningSource interface {
	WarningType() string
	WarningMessage() string
	WarningField() string
	WarningPath() string
	WarningNodeType() string
	WarningMarkType() string
	WarningIsLossy() bool
}

// WarningFrom adapts any WarningSource into a cli.Warning ready for the
// envelope. Used by command code converting pkg/adf warnings.
func WarningFrom(src WarningSource) Warning {
	return Warning{
		Type:     src.WarningType(),
		Message:  src.WarningMessage(),
		Field:    src.WarningField(),
		Path:     src.WarningPath(),
		NodeType: src.WarningNodeType(),
		MarkType: src.WarningMarkType(),
		Lossy:    src.WarningIsLossy(),
	}
}

// WriteEnvelope serializes a full JSON envelope to w. A clog encode or
// write failure is surfaced to the caller rather than silently dropped.
func WriteEnvelope(w io.Writer, env Envelope) error {
	// An active --jq filter replaces the envelope bytes with the filter's
	// results (see WriteEnvelopeDocument).
	if JQEnabled() {
		return writeJQ(w, env)
	}
	return writeJSON(w, env, clog.JSONFlat, clog.ColorNever, nil)
}

// WriteEnvelopeDocument serializes a pre-built envelope document (a value that
// already carries the ok/meta/data/errors/warnings shape — typically a map)
// through the same clog flat path cli.WriteEnvelope uses, so a broken-pipe or
// quota write failure surfaces via writeTracker instead of being swallowed. It
// exists for the raw-warning path, whose warnings carry arbitrary structured
// fields the typed Warning struct does not model, so the document cannot be
// funneled through the typed Envelope without dropping data.
func WriteEnvelopeDocument(w io.Writer, doc any) error {
	// An active --jq filter replaces the envelope bytes with the filter's
	// results — success and failure envelopes alike, so an agent's error
	// branch can filter too (the exit code is unaffected; it rides the
	// returned command error, not the printed bytes).
	if JQEnabled() {
		return writeJQ(w, doc)
	}
	return writeJSON(w, doc, clog.JSONFlat, clog.ColorNever, nil)
}

// WriteCompact serializes the JSON data payload to w without the
// envelope wrapper, with null-valued keys dropped recursively so the
// agent-facing payload stays lean. json and human modes keep the full,
// stable schema; compact is the deliberately lossy, token-economical
// view, so in compact an absent key means the value was null. A clog
// encode or write failure is surfaced to the caller rather than silently
// dropped.
func WriteCompact(w io.Writer, data any) error {
	// Under --jq the filter runs over what compact mode emits: the
	// null-stripped data document, not the envelope.
	if JQEnabled() {
		return writeJQ(w, stripNulls(data))
	}
	return writeJSON(w, stripNulls(data), clog.JSONFlat, clog.ColorNever, nil)
}

// stripNulls returns data with every null-valued map entry removed,
// recursively. It normalizes the value through encoding/json first so
// typed structs, maps, and slices are all handled the same way. Empty
// arrays and objects are kept — an empty collection is meaningful, a null
// is not. On any marshal/decode error the original data is returned
// unchanged, so compact never fails closed on a serializable payload.
func stripNulls(data any) any {
	if data == nil {
		return data
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return data
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return data
	}
	return pruneNulls(generic)
}

// pruneNulls walks a generic JSON value (the shape produced by
// json.Decode into any) and deletes null-valued keys at every depth.
func pruneNulls(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if val == nil {
				delete(t, k)
				continue
			}
			t[k] = pruneNulls(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = pruneNulls(val)
		}
		return t
	default:
		return v
	}
}

// WriteHumanJSON serializes JSON through clog's pretty printer for
// endpoints whose human mode still has a structured JSON contract.
// printTheme, when non-nil, retints the syntax highlighting to match the
// user's resolved theme and terminal background, so highlighted JSON stays
// readable on a light terminal just as entity colors do; nil keeps clog's
// built-in dark palette.
//
// Color follows the resolved --color mode (resolvedColorMode): never strips the
// syntax highlighting, always forces it even when stdout is piped. The machine
// modes stay byte-clean because they encode through WriteEnvelope/WriteCompact,
// which force ColorNever — only this human-mode JSON path is mode-aware.
func WriteHumanJSON(w io.Writer, data any, printTheme *clogtheme.Theme) error {
	return writeJSON(w, data, clog.JSONPretty, resolvedColorMode, printTheme)
}

// writeJSON encodes data through clog's JSON printer in the given mode.
//
// A note on the mode, because the names invite a dangerous mix-up:
// clog.JSONFlat here is a JSONPrintMode meaning "no indentation, one line" —
// it does NOT flatten keys. clog separately has style.JSONModeFlat, which
// dot-flattens nested keys (a -> {b} becomes "a.b"); jira-cli never sets it,
// so the envelope's key structure is always preserved verbatim. Don't conflate
// the two: enabling key flattening would silently reshape the machine envelope
// that agents and the slack-cli sibling depend on.
func writeJSON(w io.Writer, data any, mode clog.JSONPrintMode, color clog.ColorMode, printTheme *clogtheme.Theme) error {
	logger, ew := newPrintLogger(w, color, printTheme)
	logger.Print().Mode(mode).JSON(data)
	if err := ew.firstError(); err != nil {
		return NewOutputError(err)
	}
	return nil
}

// WriteHumanTOML renders data as syntax-highlighted TOML through clog's
// printer — the TOML counterpart of WriteHumanJSON, for endpoints whose
// human mode shows config-file-shaped content. The same retinting and
// color-mode rules apply: printTheme (when non-nil) matches the highlight
// palette to the user's resolved theme, and color follows the resolved
// --color mode while machine modes stay byte-clean via WriteEnvelope.
func WriteHumanTOML(w io.Writer, data any, printTheme *clogtheme.Theme) error {
	logger, ew := newPrintLogger(w, resolvedColorMode, printTheme)
	logger.Print().TOML(data)
	if err := ew.firstError(); err != nil {
		return NewOutputError(err)
	}
	return nil
}

// newPrintLogger builds the throwaway clog logger the printer paths share,
// wrapping w so a Print() call that ignores its own write result still
// surfaces a broken-pipe or quota failure to the caller.
func newPrintLogger(w io.Writer, color clog.ColorMode, printTheme *clogtheme.Theme) (*clog.Logger, *writeTracker) {
	tracker, out := newTrackedWriter(w)
	logger := clog.New(clog.NewOutput(out, color))
	if printTheme != nil {
		logger.SetTheme(clogtheme.Single(printTheme))
	}
	return logger, tracker
}

// NewRequestID returns a 32-character hex request id. crypto/rand is the
// source; on the practically-impossible read failure it falls back to a
// timestamp seed, still hex-encoded and zero-padded so the id keeps its
// fixed 32-char shape for any consumer that parses it.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%032x", time.Now().UnixNano())
}
