package cli

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image/color"
	"io"
	"reflect"
	"regexp"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	ansi "github.com/charmbracelet/x/ansi"
	clibtheme "github.com/gechr/clib/theme"
	"github.com/gechr/clog"
	cloglink "github.com/gechr/clog/field/hyperlink"
	clogstyle "github.com/gechr/clog/style"
	"github.com/gechr/primer/table"
	termansi "github.com/gechr/x/ansi"
	"github.com/gechr/x/human"
	xmaps "github.com/gechr/x/maps"
	xslices "github.com/gechr/x/slices"
	xstrings "github.com/gechr/x/strings"
	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/browser"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/pill"
)

// PlainOption configures one aspect of a plain-text render; the WithPlain*
// constructors below build them, and WriteCommandPlain applies them in order.
type PlainOption func(*plainConfig)

type plainConfig struct {
	baseURL   string
	debug     bool
	threads   int
	termWidth int
	// tty is the resolved styling switch, not raw terminal detection: it gates
	// every ANSI style and OSC 8 hyperlink the renderer emits. cmdutil folds the
	// --color mode into it (WithPlainTTY(StyleEnabled(det.IsTTY))), so
	// --color=always sets it off a pipe and --color=never clears it on a real
	// terminal.
	tty     bool
	theme   *clibtheme.Theme
	columns []string
	tsv     bool
	// command is the envelope op the render belongs to, stashed by
	// WriteCommandPlain so completion lines can pick their level (a
	// successful Jira mutation logs at LevelSuccess, reads at Info).
	command string
	// pager permits paging long document output on the terminal. cmdutil
	// folds the whole policy gate into it (--no-pager off, human TTY, no
	// agent detected, prompts allowed); the renderer still verifies the
	// document actually overflows the real terminal before paging.
	pager bool
	// elapsed is the command's measured blocking time (Spin, fanout),
	// attached to the completion line; clog's default 1s DurationMinimum
	// hides it for fast calls. Zero means nothing blocking ran.
	elapsed time.Duration
	// resultKey is the multi-key entry this render belongs to. Child
	// renderers own identification: block renderers fold it into their
	// header, and the generic renderer emits it only when the data does
	// not already carry the value — so a key is never printed twice.
	resultKey string
	// statusPillWidth is the visible width every status pill in the current
	// table pads its label to, so the filled backgrounds form a uniform block
	// instead of ragged per-status widths. Zero means no padding (single-row
	// renders, or the width was not precomputed).
	statusPillWidth int
	// graphemeWidth selects grapheme-cluster width measurement for table
	// alignment. Grapheme-clustering terminals (mode 2027 — Ghostty, kitty,
	// WezTerm, foot) draw a ZWJ emoji sequence as one 2-cell glyph where
	// per-rune wcwidth counts 4, so rows measured with the wrong method
	// render with drifted columns.
	graphemeWidth bool
}

// WithPlainBaseURL sets the profile base URL the renderer needs to turn issue
// keys into browse hyperlinks; an empty value leaves keys as plain text.
func WithPlainBaseURL(baseURL string) PlainOption {
	return func(cfg *plainConfig) {
		cfg.baseURL = strings.TrimSpace(baseURL)
	}
}

// WithPlainDebug restores the operational diagnostics (raw JQL, timing) that
// the preview and count renderers otherwise suppress.
func WithPlainDebug(debug bool) PlainOption {
	return func(cfg *plainConfig) {
		cfg.debug = debug
	}
}

// WithPlainParallel marks the render as running under the fan-out executor,
// defaulting the reported thread count when the caller set none.
func WithPlainParallel(parallel bool) PlainOption {
	return func(cfg *plainConfig) {
		if parallel && cfg.threads == 0 {
			cfg.threads = 2
		}
	}
}

// WithPlainThreads sets the fan-out thread count surfaced on the completion
// line.
func WithPlainThreads(threads int) PlainOption {
	return func(cfg *plainConfig) {
		cfg.threads = threads
	}
}

// WithPlainTermWidth sets the terminal width the issue-list table wraps to.
func WithPlainTermWidth(width int) PlainOption {
	return func(cfg *plainConfig) {
		cfg.termWidth = width
	}
}

// WithPlainTTY sets the resolved styling switch — whether the renderer emits
// ANSI styles and OSC 8 hyperlinks. Callers pass the --color decision folded
// over TTY detection (cli.StyleEnabled), not raw terminal state.
func WithPlainTTY(tty bool) PlainOption {
	return func(cfg *plainConfig) {
		cfg.tty = tty
	}
}

// WithPlainTheme sets the color theme for styled output; a nil theme renders
// bare.
func WithPlainTheme(theme *clibtheme.Theme) PlainOption {
	return func(cfg *plainConfig) {
		cfg.theme = theme
	}
}

// WithPlainPager permits paging long document output. Callers pass the
// resolved policy gate; the renderer adds the only check that belongs to
// it — whether the rendered document overflows the terminal.
func WithPlainPager(pager bool) PlainOption {
	return func(cfg *plainConfig) {
		cfg.pager = pager
	}
}

// WithPlainElapsed carries the command's measured blocking time (Spin, the
// fanout executor) into the completion line. Renderers attach it without a
// minimum override, so clog's default 1s DurationMinimum keeps fast calls
// clean and only genuinely slow round-trips grow an elapsed field.
func WithPlainElapsed(elapsed time.Duration) PlainOption {
	return func(cfg *plainConfig) {
		cfg.elapsed = elapsed
	}
}

// WithPlainGraphemeWidth selects grapheme-cluster width measurement for
// table alignment, for terminals that draw grapheme clusters (mode 2027)
// as single glyphs. The default per-rune wcwidth measurement misaligns
// rows containing ZWJ or VS16 emoji sequences on such terminals.
func WithPlainGraphemeWidth(grapheme bool) PlainOption {
	return func(cfg *plainConfig) {
		cfg.graphemeWidth = grapheme
	}
}

// WithPlainResultKey names the multi-key entry the rendered data belongs
// to, making identification the child renderer's job instead of a
// duplicated heading above it.
func WithPlainResultKey(key string) PlainOption {
	return func(cfg *plainConfig) {
		cfg.resultKey = strings.TrimSpace(key)
	}
}

// WithPlainColumns selects and orders the issue-list columns for human and
// TSV output. An empty slice keeps the default column set.
func WithPlainColumns(columns []string) PlainOption {
	return func(cfg *plainConfig) {
		cfg.columns = columns
	}
}

// WithPlainTSV renders the issue list as tab-separated values instead of the
// styled table — one header line plus one line per issue, no ANSI or box.
func WithPlainTSV(tsv bool) PlainOption {
	return func(cfg *plainConfig) {
		cfg.tsv = tsv
	}
}

// newPlainLogger builds the per-command clog logger for human output.
// OmitEmpty drops nil/empty-string/empty-collection fields so a renderer
// never prints a field irrelevant to the active backend (e.g. an empty
// onepassword_account on a keyring profile). Omission policy, decided once:
// global OmitZero stays rejected even now that clog supports per-line
// overrides — most fields here render through the generic map path, where a
// meaningful false (valid=false) or zero (count=0) would be silently
// dropped. Per-line OmitZero is entry-scoped, so it fits only lines whose
// every field is noise at zero (the --debug error line in root.go uses it);
// a mixed line gates its noisy field conditionally instead (detail on the
// issue-list line).
//
// SetSmartQuotes picks the first delimiter pair not present in the value
// (", then ', then `), so a summary containing double quotes renders
// single-quoted instead of backslash-escaped. NumberGrouped renders counts
// and totals at or above 1000 with digit grouping; compact (1.2K) is
// declined — this is a data surface and exact values matter.
//
// The output takes the resolved --color mode (resolvedColorMode) rather than a
// hardcoded ColorAuto: this is a stdout surface clog.Default() (stderr) does not
// govern, so --color=always/never would otherwise be ignored here.
func newPlainLogger(w io.Writer) *clog.Logger {
	logger := clog.New(clog.NewOutput(w, resolvedColorMode))
	logger.SetOmitEmpty(true)
	logger.SetHyperlinkFallback(cloglink.FallbackText)
	logger.SetSmartQuotes(true)
	logger.SetNumberFormat(clog.NumberGrouped)
	logger.SetStyles(plainLoggerStyles())
	return logger
}

// newPlainLoggerAt returns the boundary logger with its minimum level
// pinned to level, for renderers whose whole surface is a single severity
// (the warning mirror, the failure-diagnostics summaries): the pin makes
// the surface's severity explicit and keeps anything below it from ever
// leaking through this logger.
func newPlainLoggerAt(w io.Writer, level clog.Level) *clog.Logger {
	logger := newPlainLogger(w)
	logger.SetLevel(level)
	return logger
}

// plainLoggerStyles keeps backtick delimiters in styled output
// (BacktickKeep): plain renderers emit grid-aligned table rows and padded
// columns, and clog's default backtick rendering drops the two delimiter
// characters of a `code` span — two visible cells gone per span, shifting
// every column after it. Keep mode styles the span but leaves the width
// exactly as written.
func plainLoggerStyles() *clogstyle.Config {
	return &clogstyle.Config{BacktickMode: clogstyle.BacktickKeep}
}

// WritePlain renders arbitrary data through the generic field renderer, the
// fallback for internal payloads with no command-specific renderer in
// WriteCommandPlain.
func WritePlain(w io.Writer, data any) error {
	return withTrackedWriter(w, func(out io.Writer) error {
		return writeGenericPlain(newPlainLogger(out), defaultPlainConfig(), "result", data)
	})
}

// WriteCommandPlain routes a command's data to the plain renderer that
// owns its human output. Per-command renderers in the plain_*.go files
// own the field order, human-size and time formatting for one command
// group; writeGenericPlain remains the fallback for low-risk internal
// data that has no dedicated renderer. The command set is closed, so an
// explicit switch is the whole dispatch — there is deliberately no
// parallel renderer registry.
func WriteCommandPlain(w io.Writer, command string, data any, opts ...PlainOption) error {
	return withTrackedWriter(w, func(out io.Writer) error {
		return writeCommandPlain(out, command, data, opts...)
	})
}

func writeCommandPlain(w io.Writer, command string, data any, opts ...PlainOption) error {
	// Human-mode render boundary: every renderer below pulls its display
	// strings from this Jira-controlled payload, so string VALUES are
	// sanitized once here — one systematic boundary instead of
	// per-renderer whack-a-mole, and a future renderer cannot reopen the
	// hole. Keys are left untouched (renderers look fields up by literal
	// name), and no legitimate logic value (status category, color name,
	// issue key) carries control bytes, so sanitizing values is
	// display-safe. A typed payload (e.g. issue view's *jira.Issue, the
	// issue-list rows) bypasses this map walk; those are covered at their
	// own extraction boundaries — formatHumanField for the table, and
	// stringFromMap / issueDescriptionPlain for the bespoke map renderers.
	data = sanitizePlainData(data)
	cfg := defaultPlainConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	cfg.command = command
	logger := newPlainLogger(w)
	if command != "issue.view" && len(keyedResultRows(data)) > 0 {
		return WriteKeyedResultsPlain(w, command, data, opts...)
	}
	switch command {
	case "config.get":
		return writeConfigGetPlain(w, data, cfg)
	case "config.profile":
		return writeConfigProfilePlain(w, data, cfg)
	case "issue.attachment.list":
		return WriteAttachmentListPlain(w, command, data, opts...)
	case "issue.list":
		return writeIssueListPlain(logger, data, cfg)
	case "issue.list.jql", "jql.build":
		return writeJQLPreviewPlain(logger, data, cfg)
	case "issue.list.count", "search.count":
		return writeCountPlain(logger, data, cfg)
	case "jql.validate":
		return writeValidatePlain(logger, data, cfg)
	case "jql.reference":
		return writeReferencePlain(logger, data, cfg)
	case "issue.view":
		return WriteIssueViewPlain(w, command, data, opts...)
	case "issue.transitions":
		return WriteIssueTransitionsPlain(w, command, data, opts...)
	case "auth.status":
		return writeAuthStatusPlain(logger, data, cfg)
	case "issue.comment.list":
		return WriteCommentListPlain(w, command, data, opts...)
	case "issue.watchers.list":
		return WriteWatcherListPlain(w, command, data, opts...)
	case "issue.link.list":
		return WriteLinkListPlain(w, command, data, opts...)
	case "issue.link.types":
		return WriteLinkTypesPlain(w, command, data, opts...)
	case "boards.list":
		return WriteBoardListPlain(w, command, data, opts...)
	case "alias.list":
		return WriteAliasListPlain(w, command, data, opts...)
	case "user.search":
		return writeUserSearchPlain(logger, data, cfg)
	case "release.notes":
		return writeReleaseNotesPlain(w, data, cfg)
	default:
		return writeGenericPlain(logger, cfg, messageForCommand(command, data), data)
	}
}

// sanitizePlainData deep-sanitizes every string VALUE in a plain-render
// payload through SanitizeTerminalBlock (multi-line content such as
// descriptions and comment bodies keeps its layout; ANSI escapes and
// other control bytes are dropped). It rebuilds maps and slices rather
// than mutating the caller's data, walks the JSON-ish shapes the
// renderers consume (map[string]any, []any, []map[string]any,
// []string), and leaves typed values untouched — those reach the
// terminal via formatHumanField, which sanitizes on extraction.
func sanitizePlainData(value any) any {
	switch v := value.(type) {
	case string:
		return SanitizeTerminalBlock(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			out[key] = sanitizePlainData(child)
		}
		return out
	case []any:
		return xslices.Map(v, sanitizePlainData)
	case []map[string]any:
		return xslices.Map(v, func(child map[string]any) map[string]any {
			m, _ := sanitizePlainData(child).(map[string]any)
			return m
		})
	case []string:
		return xslices.Map(v, SanitizeTerminalBlock)
	default:
		return value
	}
}

func defaultPlainConfig() plainConfig {
	return plainConfig{
		termWidth: 100,
		// config.DefaultTheme honors the JIRA_THEME process override and
		// falls back to the dark built-in, matching help and the TUI.
		theme: config.DefaultTheme(),
	}
}

// completionEvent opens the event for a command's completion line. A Jira
// mutation that really happened — not a dry-run preview, with no failed
// keys — logs at LevelSuccess so scrollback separates writes that landed
// from informational reads; everything else stays at Info. Keyed child rows
// (resultKey set) stay Info too: one batch earns one success line, on the
// summary.
func completionEvent(logger *clog.Logger, cfg plainConfig, data any, failed int) *clog.Event {
	if OpMutating(cfg.command) && cfg.resultKey == "" && !dataDryRun(data) && failed == 0 {
		return logger.Log(LevelSuccess)
	}
	return logger.Info()
}

func writeGenericPlain(logger *clog.Logger, cfg plainConfig, message string, data any) error {
	fields := plainFields(data)
	if len(fields) == 0 {
		return nil
	}
	event := completionEvent(logger, cfg, data, 0)
	// A multi-key entry must be identifiable from its own line. When the
	// data already carries the key's value under any field (issue=KEY,
	// resource names, ...), that field identifies it; otherwise the key
	// is emitted up front — linked like any other issue-key value.
	if cfg.resultKey != "" && !plainFieldsCarryValue(fields, cfg.resultKey) {
		if url, key := issueBrowseURL(cfg, cfg.resultKey); url != "" {
			event = event.Link("key", url, key)
		} else {
			event = event.Str("key", cfg.resultKey)
		}
	}
	for _, field := range fields {
		// An issue-key value becomes a clickable link to its browse URL.
		// clog owns the fallback: off a TTY (or with hyperlinks disabled)
		// the field renders as plain text.
		if url, key := issueBrowseURL(cfg, field.value); url != "" {
			event = event.Link(field.key, url, key)
			continue
		}
		event = event.Any(field.key, field.value)
	}
	if cfg.elapsed >= time.Second && cfg.resultKey == "" {
		// The command's blocking time trails the completion line — a
		// multi-key child row (resultKey set) already reports per-key timing
		// in the debug lifecycle, and the keyed summary line carries the
		// whole fan-out's elapsed. The 1s threshold hides it for
		// fast calls, so only genuinely slow round-trips grow the field.
		event = event.Duration("elapsed", cfg.elapsed)
	}
	// An empty message means the command's data fields already carry the
	// result; emitting a message line that just echoes the command name
	// adds no information, so the renderer drops it.
	if message == "" {
		event.Send()
		return nil
	}
	event.Msg(message)
	return nil
}

// completionMessageCommands lists the commands whose human/plain output ends
// with a past-tense confirmation line. The phrase itself comes from the verb
// registry (VerbFor.Pastf), so the spinner, the debug lifecycle, and this
// completion message all read one source of truth and can never disagree.
//
// Commands absent from this set emit no message line: a fallback derived from
// the command name just restates what the user typed (and auth.token /
// auth.whoami carry their result in data fields), so the renderer drops it and
// lets the data speak.
var completionMessageCommands = map[string]bool{
	"issue.list":       true,
	"issue.view":       true,
	"issue.create":     true,
	"issue.edit":       true,
	"issue.clone":      true,
	"issue.move":       true,
	"issue.delete":     true,
	"issue.transition": true,
	"issue.comment":    true,
	"epic.list":        true,
	"epic.board":       true,
	"search.jql":       true,
	"search.saved":     true,
	"worklog.add":      true,
	"worklog.list":     true,
	"auth.login":       true,
	"auth.status":      true,
	"auth.logout":      true,
	"auth.switch":      true,
	"schema":           true,
}

func messageForCommand(command string, data any) string {
	if !completionMessageCommands[command] {
		return ""
	}
	// The completion line is user-facing, so it is Sentence-cased; the verb
	// registry stays lower case for the structured debug lifecycle. A
	// dry-run preview takes the conditional form — past tense would claim a
	// mutation that never left the machine.
	if dataDryRun(data) {
		return SentenceCase(VerbFor(command).Conditionalf())
	}
	return SentenceCase(VerbFor(command).Pastf())
}

// dataDryRun reports whether the rendered payload is a dry-run preview: a
// top-level dry_run=true, or — for the shared multi-key envelope — one on
// a per-key result's data. Dry-run is all-or-nothing for an invocation, so
// the first entry that carries the field speaks for the batch.
func dataDryRun(data any) bool {
	// mapFromAny is the same conversion boundary keyedResultRows uses: the
	// production multi-key envelope arrives as a typed struct, and a bare
	// map assertion would miss it, leaving the batch header past-tense
	// above conditional children.
	m := mapFromAny(data)
	if m == nil {
		return false
	}
	if v, ok := m["dry_run"].(bool); ok {
		return v
	}
	for _, result := range keyedResultRows(m) {
		child, ok := result["data"].(map[string]any)
		if !ok {
			continue
		}
		if v, ok := child["dry_run"].(bool); ok {
			return v
		}
	}
	return false
}

type plainField struct {
	key   string
	value any
}

// plainFieldsCarryValue reports whether any rendered field's string value
// equals want — i.e. the data already self-identifies its multi-key entry.
func plainFieldsCarryValue(fields []plainField, want string) bool {
	for _, field := range fields {
		if s, ok := field.value.(string); ok && s == want {
			return true
		}
	}
	return false
}

func plainFields(data any) []plainField {
	fields := []plainField{}
	switch v := data.(type) {
	case map[string]any:
		for key, value := range xmaps.Sorted(v) {
			writePlainField(&fields, key, value)
		}
	default:
		// Any other string-keyed map (map[string]string, map[string]int, ...)
		// renders one field per key like map[string]any above, instead of
		// collapsing the whole map into a single value={...} line.
		if m, ok := stringKeyedMap(data); ok {
			return plainFields(m)
		}
		// A typed Output struct (internal/envelope) renders one field per
		// marshaled key, via a JSON round-trip so tags, omitempty, and
		// embedding behave exactly as the wire envelope does.
		rv := reflect.ValueOf(data)
		for rv.IsValid() && rv.Kind() == reflect.Pointer && !rv.IsNil() {
			rv = rv.Elem()
		}
		if rv.IsValid() && rv.Kind() == reflect.Struct {
			if m := mapFromAny(data); m != nil {
				return plainFields(m)
			}
		}
		writePlainField(&fields, "value", v)
	}
	return fields
}

// stringKeyedMap converts any map with string-kinded keys to map[string]any so
// the plain renderers can walk it per key. Non-map values and maps with
// non-string keys report false.
func stringKeyedMap(value any) (map[string]any, bool) {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	out := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		out[iter.Key().String()] = iter.Value().Interface()
	}
	return out, true
}

func writePlainField(fields *[]plainField, key string, value any) {
	writePlainValue(fields, key, value)
}

func writePlainValue(fields *[]plainField, key string, value any) {
	switch v := value.(type) {
	case adf.Document:
		writePlainLine(fields, key, normalizePlain(adf.ToPlain(v)))
	case *adf.Document:
		if v == nil {
			writePlainLine(fields, key, "")
			return
		}
		writePlainLine(fields, key, normalizePlain(adf.ToPlain(*v)))
	case []any:
		writePlainLine(fields, key, v)
	case map[string]any:
		if doc, ok := adfDocumentFromMap(v); ok {
			writePlainLine(fields, key, normalizePlain(adf.ToPlain(doc)))
			return
		}
		for childKey, child := range xmaps.Sorted(v) {
			writePlainValue(fields, key+"."+childKey, child)
		}
	default:
		// Nested string-keyed maps of any value type take the map[string]any
		// path above rather than collapsing to {...}.
		if m, ok := stringKeyedMap(value); ok {
			writePlainValue(fields, key, m)
			return
		}
		writePlainLine(fields, key, value)
	}
}

// writePlainLine is the human-mode render boundary for generic plain
// fields. Keys and string values can carry Jira-controlled text
// (summaries, descriptions, custom field names), so both are sanitized
// here — one choke point for every field the generic renderer emits —
// before clog writes them to the terminal. Machine modes are protected
// by the JSON encoder; this is the human-mode counterpart.
func writePlainLine(fields *[]plainField, key string, value any) {
	rendered := plainFieldValue(value)
	if s, ok := rendered.(string); ok {
		rendered = SanitizeTerminalText(s)
	}
	*fields = append(*fields, plainField{key: SanitizeTerminalText(key), value: rendered})
}

func writeIssueListPlain(logger *clog.Logger, data any, cfg plainConfig) error {
	msg := messageForCommand("issue.list", data)
	m, ok := data.(map[string]any)
	if !ok {
		return writeGenericPlain(logger, cfg, msg, data)
	}
	rawIssues, detail, jql, ok := issueListPlainData(m)
	if !ok {
		return writeGenericPlain(logger, cfg, msg, data)
	}
	issues, ok := normalizeIssueRows(rawIssues)
	if !ok {
		return writeGenericPlain(logger, cfg, msg, data)
	}
	if cfg.tsv {
		return writeIssueTSV(logger, issues, cfg)
	}
	event := logger.Info().Int("count", len(issues))
	// detail=false only restates the default list mode, so the field appears
	// only when true — gated like threads/jql below rather than via a
	// per-line OmitZero, which is entry-scoped and would drop the meaningful
	// count=0 from the same line.
	if detail {
		event = event.Bool("detail", true)
	}
	if cfg.threads > 0 {
		event = event.Int("threads", cfg.threads)
	}
	if jql != "" && cfg.debug {
		event = event.Str("jql", jql)
	}
	if cfg.elapsed >= time.Second && cfg.resultKey == "" {
		// Trailing blocking time; the 1s threshold hides fast lists.
		event = event.Duration("elapsed", cfg.elapsed)
	}
	event.Msg(msg)
	if len(issues) == 0 {
		return nil
	}
	rows, err := issueRows(issues, cfg)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row != "" {
			logger.Info().Parts(clog.PartMessage).Msg(row)
		}
	}
	return nil
}

// writeJQLPreviewPlain renders the `--as-jql` / `jql build` preview. The
// preview's whole job is to hand back the JQL the command WOULD run, so its
// human output is the bare query — copy/paste- and pipe-safe — not the
// envelope's diagnostic fields. On a TTY the query is wrapped in an OSC 8
// hyperlink to the Jira search URL (degrading to plain text off a TTY), and
// --debug restores the operational diagnostics for troubleshooting.
func writeJQLPreviewPlain(logger *clog.Logger, data any, cfg plainConfig) error {
	// mapFromAny accepts both the typed JQLBuildOutput struct (the default,
	// single-command path) and a raw map (a keyed child, or a legacy caller):
	// a map passes through untouched, a struct round-trips to the same
	// diagnostic fields this preview reads.
	m := mapFromAny(data)
	if m == nil {
		return writeGenericPlain(logger, cfg, "", data)
	}
	query, _ := m["jql"].(string)
	if xstrings.IsBlank(query) {
		return writeGenericPlain(logger, cfg, "", data)
	}
	if cfg.debug {
		// Flatten through the shared plain-field helper so the diagnostic uses
		// the same dotted form (board_scope.applied=…) as every other command's
		// human output, rather than a raw Go map blob.
		diag := map[string]any{"jql": query}
		for _, key := range []string{"board_scope", "detail", "precedence"} {
			if v, present := m[key]; present {
				diag[key] = v
			}
		}
		event := logger.Info()
		for _, field := range plainFields(diag) {
			event = event.Any(field.key, field.value)
		}
		event.Send()
	}
	url, _ := m["url"].(string)
	logger.Info().Parts(clog.PartMessage).Msg(hyperlink(cfg, url, query))
	return nil
}

// writeCountPlain renders `--count` output: the bare estimate and nothing
// else, so it pipes cleanly into a shell. The query that was counted is a
// diagnostic, restored under --debug like the JQL preview.
func writeCountPlain(logger *clog.Logger, data any, cfg plainConfig) error {
	// mapFromAny accepts the typed SearchCountOutput struct and a raw map
	// alike (see writeJQLPreviewPlain).
	m := mapFromAny(data)
	if m == nil {
		return writeGenericPlain(logger, cfg, "", data)
	}
	if cfg.debug {
		event := logger.Info()
		for _, field := range plainFields(map[string]any{"jql": m["jql"]}) {
			event = event.Any(field.key, field.value)
		}
		event.Send()
	}
	logger.Info().Parts(clog.PartMessage).Msg(fmt.Sprintf("%v", m["count"]))
	return nil
}

// writeValidatePlain renders `jql validate` output: one line per query stating
// whether it is valid, with the parse errors (or warnings) appended. The query
// text is included so a multi-query run is legible.
func writeValidatePlain(logger *clog.Logger, data any, cfg plainConfig) error {
	// mapFromAny accepts the typed JQLValidateOutput struct and a raw map
	// alike (see writeJQLPreviewPlain); normalizeMapList then reads the
	// per-query entries whichever way they arrived.
	m := mapFromAny(data)
	if m == nil {
		return writeGenericPlain(logger, cfg, "", data)
	}
	queries := normalizeMapList(m["queries"])
	if len(queries) == 0 {
		return writeGenericPlain(logger, cfg, "", data)
	}
	for _, q := range queries {
		query, _ := q["query"].(string)
		valid, _ := q["valid"].(bool)
		switch {
		case !valid:
			logger.Info().Parts(clog.PartMessage).Msg("INVALID  " + query + " — " + strings.Join(coerceStringSlice(q["errors"]), "; "))
		case len(coerceStringSlice(q["warnings"])) > 0:
			logger.Info().Parts(clog.PartMessage).Msg("OK (warnings)  " + query + " — " + strings.Join(coerceStringSlice(q["warnings"]), "; "))
		default:
			logger.Info().Parts(clog.PartMessage).Msg("OK  " + query)
		}
	}
	return nil
}

// writeUserSearchPlain renders `user search`: one line per match —
// "display name — email account_id=…" — so a human scans candidates the
// same way an agent scans data.users[]. The display name links to the
// person's profile on a supporting terminal. Zero matches states so
// plainly instead of printing an empty count line.
func writeUserSearchPlain(logger *clog.Logger, data any, cfg plainConfig) error {
	// mapFromAny accepts the typed UserSearchOutput struct and a raw map
	// alike (see writeJQLPreviewPlain).
	m := mapFromAny(data)
	if m == nil {
		return writeGenericPlain(logger, cfg, "", data)
	}
	users := normalizeMapList(m["users"])
	if len(users) == 0 {
		query, _ := m["query"].(string)
		logger.Info().Str("query", query).Msg("No users matched")
		return nil
	}
	for _, u := range users {
		name, _ := u["display_name"].(string)
		email, _ := u["email_address"].(string)
		id, _ := u["account_id"].(string)
		display := name
		if xstrings.AllNonEmpty(cfg.baseURL, id) {
			display = hyperlink(cfg, cfg.baseURL+"/jira/people/"+id, name)
		}
		line := display
		if email != "" {
			line += " — " + email
		}
		logger.Info().Str("account_id", id).Msg(line)
	}
	return nil
}

// writeReferencePlain renders `jql reference`: one line per queryable field,
// "value — displayName", so the field set (including custom fields) is legible
// and greppable. Functions and reserved words ride along in --output=json.
func writeReferencePlain(logger *clog.Logger, data any, cfg plainConfig) error {
	// mapFromAny accepts the typed JQLReferenceOutput struct and a raw map
	// alike (see writeJQLPreviewPlain).
	m := mapFromAny(data)
	if m == nil {
		return writeGenericPlain(logger, cfg, "", data)
	}
	fields := normalizeMapList(m["fields"])
	if len(fields) == 0 {
		return writeGenericPlain(logger, cfg, "", data)
	}
	for _, f := range fields {
		value, _ := f["value"].(string)
		display, _ := f["display_name"].(string)
		line := value
		if display != "" {
			line += " — " + display
		}
		logger.Info().Parts(clog.PartMessage).Msg(line)
	}
	return nil
}

// coerceStringSlice normalises a value that may be []string (the in-process
// envelope path) or []any of strings (a JSON round-trip) into []string. Shared
// by the validate and board renderers.
func coerceStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

// hyperlink renders text as an OSC 8 terminal link to url and is the single
// source of truth for how every terminal link in the CLI is styled. On a TTY
// the display text is underlined so the link reads as a link; off a TTY (or
// when url is empty) the bare text is returned unchanged, keeping output
// copy/paste- and pipe-safe. The link itself comes from clog's hyperlink
// primitive — one implementation across ADF rendering and plain output.
func hyperlink(cfg plainConfig, url, text string) string {
	if url == "" || !cfg.tty {
		return text
	}
	styled := lipgloss.NewStyle().Underline(true).Render(SanitizeTerminalText(text))
	return HyperlinkPreStyled(SanitizeTerminalText(url), styled)
}

// issueBrowseURL returns the browse URL and the key when v is an issue-key
// string and the profile's base URL is known; empty strings otherwise.
func issueBrowseURL(cfg plainConfig, v any) (string, string) {
	s, ok := v.(string)
	if !ok || cfg.baseURL == "" || !plainIssueKeyPattern.MatchString(s) {
		return "", ""
	}
	return browser.IssueURL(cfg.baseURL, s), s
}

// plainIssueKeyPattern matches a bare Jira issue key ("PROJ-123").
var plainIssueKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

// writeIssueTSV emits the issue list as tab-separated values: one header line
// of column names plus one line per issue, with no status line, ANSI, or box.
// The header is always written so a script can detect the columns even when
// the result set is empty.
func writeIssueTSV(logger *clog.Logger, issues []map[string]any, cfg plainConfig) error {
	lines, err := issueTSVLines(issues, cfg)
	if err != nil {
		return err
	}
	for _, line := range lines {
		logger.Info().Parts(clog.PartMessage).Msg(line)
	}
	return nil
}

func issueListPlainData(data map[string]any) (rawIssues any, detail bool, jql string, ok bool) {
	rawIssues, ok = data["issues"]
	if !ok {
		return nil, false, "", false
	}
	if v, ok := data["detail"].(bool); ok {
		detail = v
	}
	if v, ok := data["jql"].(string); ok {
		jql = v
	}
	return rawIssues, detail, jql, true
}

type issueTableRow struct {
	Key         string
	Summary     string
	Status      string
	StatusCat   string
	StatusColor string
	Assignee    string
	Priority    string
	Created     string
	Updated     string
}

// issueColumn defines one selectable issue-list column: its flag name, table
// header, and the two ways it renders — a plain string for TSV and a styled
// table.Cell for the human table. issueColumnDefs is the single source of
// column truth shared by both renderers and the --columns validator.
type issueColumn struct {
	name   string
	header string
	flex   bool
	text   func(issueTableRow) string
	cell   func(issueTableRow, plainConfig, *table.RenderContext) table.Cell
}

var issueColumnDefs = []issueColumn{
	{
		name:   "key",
		header: "KEY",
		text:   func(r issueTableRow) string { return r.Key },
		cell: func(r issueTableRow, cfg plainConfig, _ *table.RenderContext) table.Cell {
			text := hyperlink(cfg, browser.IssueURL(cfg.baseURL, r.Key), r.Key)
			return table.StyledCell(text, r.Key)
		},
	},
	{
		name:   "summary",
		header: "SUMMARY",
		flex:   true,
		text:   func(r issueTableRow) string { return r.Summary },
		cell: func(r issueTableRow, _ plainConfig, _ *table.RenderContext) table.Cell {
			return table.TextCell(r.Summary)
		},
	},
	{
		name:   "status",
		header: "STATUS",
		text:   func(r issueTableRow) string { return r.Status },
		cell: func(r issueTableRow, cfg plainConfig, _ *table.RenderContext) table.Cell {
			return statusPillCell(cfg, r.Status, r.StatusCat, r.StatusColor)
		},
	},
	{
		name:   "assignee",
		header: "ASSIGNEE",
		flex:   true,
		text:   func(r issueTableRow) string { return r.Assignee },
		cell: func(r issueTableRow, cfg plainConfig, _ *table.RenderContext) table.Cell {
			return styledCell(assigneeStyle(cfg, r.Assignee), firstNonEmpty(r.Assignee, "unassigned"))
		},
	},
	{
		name:   "priority",
		header: "PRIORITY",
		text:   func(r issueTableRow) string { return r.Priority },
		cell: func(r issueTableRow, cfg plainConfig, _ *table.RenderContext) table.Cell {
			return styledCell(priorityStyle(cfg, r.Priority), r.Priority)
		},
	},
	{
		name:   "created",
		header: "CREATED",
		text:   func(r issueTableRow) string { return r.Created },
		cell: func(r issueTableRow, _ plainConfig, _ *table.RenderContext) table.Cell {
			return table.TextCell(r.Created)
		},
	},
	{
		name:   "updated",
		header: "UPDATED",
		text:   func(r issueTableRow) string { return r.Updated },
		cell: func(r issueTableRow, _ plainConfig, _ *table.RenderContext) table.Cell {
			return table.TextCell(r.Updated)
		},
	},
}

var defaultIssueColumns = []string{"key", "summary", "status", "assignee", "priority"}

func issueColumnNames() []string {
	return xslices.Map(issueColumnDefs, func(c issueColumn) string { return c.name })
}

// resolveIssueColumns maps the requested column names (or the default set when
// empty) to their definitions, preserving the caller's order. Names are
// case-insensitive and space-trimmed; an unknown name is an error.
func resolveIssueColumns(selected []string) ([]issueColumn, error) {
	names := defaultIssueColumns
	if len(selected) > 0 {
		names = selected
	}
	cols := make([]issueColumn, 0, len(names))
	for _, raw := range names {
		want := strings.ToLower(strings.TrimSpace(raw))
		idx := -1
		for i := range issueColumnDefs {
			if issueColumnDefs[i].name == want {
				idx = i
				break
			}
		}
		if idx == -1 {
			return nil, fmt.Errorf("unknown column %q: valid columns are %s", raw, strings.Join(issueColumnNames(), ", "))
		}
		cols = append(cols, issueColumnDefs[idx])
	}
	return cols, nil
}

// ValidateIssueColumns reports whether every requested column name is known,
// so a command can fail fast before calling Jira.
func ValidateIssueColumns(selected []string) error {
	_, err := resolveIssueColumns(selected)
	return err
}

func buildIssueRows(issues []map[string]any) []issueTableRow {
	rows := make([]issueTableRow, 0, len(issues))
	for _, issue := range issues {
		rows = append(rows, issueTableRow{
			Key:         formatHumanField(issue["key"]),
			Summary:     formatHumanField(issue["summary"]),
			Status:      formatHumanField(issue["status"]),
			StatusCat:   formatHumanField(issue["status_category"]),
			StatusColor: formatHumanField(issue["status_color"]),
			Assignee:    SanitizeTerminalBlock(formatAssignee(issue["assignee"])),
			Priority:    formatHumanField(issue["priority"]),
			Created:     formatHumanField(issue["created"]),
			Updated:     formatHumanField(issue["updated"]),
		})
	}
	return rows
}

func issueRows(issues []map[string]any, cfg plainConfig) ([]string, error) {
	cols, err := resolveIssueColumns(cfg.columns)
	if err != nil {
		return nil, err
	}
	rows := buildIssueRows(issues)
	cfg.statusPillWidth = widestStatusLabel(rows)
	renderer := issueTableRenderer(cfg, cols)
	rendered := renderer.Render(rows)
	if rendered.String() == "" {
		return nil, nil
	}
	if !cfg.tty {
		return strings.Split(rendered.String(), "\n"), nil
	}
	method := ansi.WcWidth
	if cfg.graphemeWidth {
		method = ansi.GraphemeWidth
	}
	th := primerTheme{theme: cfg.theme, styled: true}
	header := make([]string, len(cols))
	for i, col := range cols {
		header[i] = th.RenderBold(col.header)
	}
	lines := []string{terminalTableRow(header, rendered.ColWidths, method)}
	for _, row := range rendered.Rows {
		cells := make([]string, len(row.Cells))
		for i, cell := range row.Cells {
			cells[i] = cell.Text
		}
		lines = append(lines, terminalTableRow(cells, rendered.ColWidths, method))
	}
	return lines, nil
}

// issueTSVLines renders the selected columns as tab-separated lines: a header
// line followed by one line per issue. Tabs and newlines inside a value are
// collapsed to spaces so they never break the column layout.
func issueTSVLines(issues []map[string]any, cfg plainConfig) ([]string, error) {
	cols, err := resolveIssueColumns(cfg.columns)
	if err != nil {
		return nil, err
	}
	headers := xslices.Map(cols, func(c issueColumn) string { return c.header })
	lines := []string{strings.Join(headers, "\t")}
	for _, row := range buildIssueRows(issues) {
		fields := xslices.Map(cols, func(c issueColumn) string { return sanitizeTSVField(c.text(row)) })
		lines = append(lines, strings.Join(fields, "\t"))
	}
	return lines, nil
}

func sanitizeTSVField(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}

func issueTableRenderer(cfg plainConfig, cols []issueColumn) *table.Renderer[issueTableRow] {
	th := primerTheme{theme: cfg.theme, styled: cfg.tty}
	a := termansi.New(
		termansi.WithTerminal(cfg.tty),
		termansi.WithHyperlinkFallback(termansi.HyperlinkFallbackText),
	)
	ctx := table.NewRenderContext(th, a)
	columns := make([]table.Column[issueTableRow], 0, len(cols))
	for _, c := range cols {
		columns = append(columns, table.Column[issueTableRow]{
			Name:   c.name,
			Header: c.header,
			Flex:   c.flex,
			Render: func(row issueTableRow, rctx *table.RenderContext) table.Cell {
				return c.cell(row, cfg, rctx)
			},
		})
	}
	opts := []table.Option{table.WithTermWidth(cfg.termWidth)}
	if cfg.graphemeWidth {
		opts = append(opts, table.WithGridOptions(table.WithWidthMethod(ansi.GraphemeWidth)))
	}
	return table.NewRenderer(columns, ctx, opts...)
}

type primerTheme struct {
	theme  *clibtheme.Theme
	styled bool
}

func (t primerTheme) RenderBold(s string) string {
	if !t.styled || t.theme == nil || t.theme.Bold == nil {
		return s
	}
	return t.theme.Bold.Render(s)
}

func (t primerTheme) RenderDim(s string) string {
	if !t.styled || t.theme == nil || t.theme.Dim == nil {
		return s
	}
	return t.theme.Dim.Render(s)
}

func (t primerTheme) EntityColors() []color.Color {
	if !t.styled || t.theme == nil {
		return nil
	}
	return t.theme.EntityColors
}

func styledCell(style lipgloss.Style, text string) table.Cell {
	if text == "" {
		return table.TextCell("")
	}
	return table.StyledCell(style.Render(text), text)
}

// statusPillCell renders a status as a filled badge ("pill") colored by its
// workflow category, mirroring Jira's own status colors. Jira defines four
// global status categories — new, indeterminate, done, and an undefined
// fallback — and every status a tenant creates, however it is named, inherits
// one of them, so the color space stays fixed regardless of the workflow. Off
// a TTY or without a color theme the status renders as bare text.
func statusPillCell(cfg plainConfig, status, category, colorName string) table.Cell {
	if status == "" {
		return table.TextCell("")
	}
	if !cfg.tty || cfg.theme == nil {
		return table.TextCell(status)
	}
	// The label carries its own surrounding spaces: StyledCell sizes the column
	// from its second argument, not the styled string, so the padding has to be
	// in the measured text or the column would be one space too narrow on each
	// side and the fill would overflow.
	label := statusPillLabel(status)
	// Pad inside the label — not via the grid — so the filled background
	// extends to the widest pill in the table and the badges form a uniform
	// block instead of ragged per-status widths.
	if pad := cfg.statusPillWidth - ansi.WcWidth.StringWidth(label); pad > 0 {
		label += strings.Repeat(" ", pad)
	}
	return table.StyledCell(statusPill(cfg, status, category, colorName).Render(label), label)
}

// statusPillLabel is the pill's visible text: the status wrapped in one space
// of breathing room on each side, which the fill color covers.
func statusPillLabel(status string) string {
	return " " + status + " "
}

// widestStatusLabel measures the widest pill label across the rows about to
// be rendered, so every pill in the table can pad to it. Empty statuses render
// no pill and are skipped.
func widestStatusLabel(rows []issueTableRow) int {
	widest := 0
	for _, row := range rows {
		if row.Status == "" {
			continue
		}
		if w := ansi.WcWidth.StringWidth(statusPillLabel(row.Status)); w > widest {
			widest = w
		}
	}
	return widest
}

// statusPill builds the badge style for a status: a category-derived color as
// the fill, a contrasting foreground, bold. The pill sets both background and
// foreground, so it reads on any terminal background.
func statusPill(cfg plainConfig, status, category, colorName string) lipgloss.Style {
	// The theme no longer influences the fill (fixed truecolor — see
	// statusFill), but a nil theme still means "unstyled output requested",
	// so the gate stays.
	if !cfg.tty || cfg.theme == nil {
		return lipgloss.NewStyle()
	}
	return pill.Style(status, category, colorName)
}

// priorityStyle colors a priority on Jira's scale: red for high and highest,
// orange for medium, blue for low and lowest. It uses the theme's semantic
// colors so it tracks the active theme, and bolds the most urgent level to keep
// it distinct from high. An unrecognized priority falls back to a per-name hash
// color.
func priorityStyle(cfg plainConfig, priority string) lipgloss.Style {
	if !cfg.tty || cfg.theme == nil {
		return lipgloss.NewStyle()
	}
	switch normalizeStyleKey(priority) {
	case "blocker", "critical", "highest", "p0", "p1":
		return foregroundStyle(cfg.theme.Red).Bold(true)
	case "high", "p2":
		return foregroundStyle(cfg.theme.Red)
	case "medium", "normal", "p3":
		return foregroundStyle(cfg.theme.Orange)
	case "low", "p4", "lowest", "trivial", "p5":
		return foregroundStyle(cfg.theme.Blue)
	default:
		return hashStyle(cfg.theme, "priority:"+priority)
	}
}

func assigneeStyle(cfg plainConfig, assignee string) lipgloss.Style {
	if xstrings.IsBlank(assignee) {
		return dimStyle(cfg)
	}
	if !cfg.tty {
		return lipgloss.NewStyle()
	}
	return hashStyle(cfg.theme, "assignee:"+assignee)
}

func dimStyle(cfg plainConfig) lipgloss.Style {
	if !cfg.tty || cfg.theme == nil || cfg.theme.Dim == nil {
		return lipgloss.NewStyle()
	}
	return *cfg.theme.Dim
}

// entityHues are the fixed mid-tone foregrounds hashStyle picks from,
// NOT the theme's EntityColors: entity colors are pure identity hints,
// and theme palettes are designed for one background — the default dark
// palette rendered assignee names near-invisible on white terminals.
// Mid-tone hues (the same luma band the pill fills sit in) stay legible
// on both black and white, remap-proof for the same reason the pills are.
var entityHues = []color.Color{
	lipgloss.Color("#8f7ee7"), // purple
	lipgloss.Color("#e56910"), // orange
	lipgloss.Color("#227d9b"), // teal
	lipgloss.Color("#da62ac"), // magenta
	lipgloss.Color("#82b536"), // lime
	lipgloss.Color("#1d7afc"), // blue
	lipgloss.Color("#1f845a"), // green
	lipgloss.Color("#b3822e"), // bronze
}

// hashStyle picks a stable per-name foreground from the fixed mid-tone
// entityHues. The theme no longer supplies the colors — see entityHues —
// but it still carries two signals: nil means "no styling requested", and
// an empty EntityColors slice is the monochrome/plain presets' deliberate
// opt-out of entity coloring, honored by rendering bare.
func hashStyle(theme *clibtheme.Theme, key string) lipgloss.Style {
	if theme == nil || len(theme.EntityColors) == 0 || xstrings.IsBlank(key) {
		return lipgloss.NewStyle()
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(key))))

	return lipgloss.NewStyle().Foreground(entityHues[h.Sum32()%uint32(len(entityHues))])
}

func foregroundStyle(style *lipgloss.Style) lipgloss.Style {
	if style == nil {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(style.GetForeground())
}

func normalizeStyleKey(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func formatAssignee(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case *jira.User:
		if v == nil {
			return ""
		}
		return jiraUserName(*v)
	case jira.User:
		return jiraUserName(v)
	case map[string]any:
		for _, key := range []string{"displayName", "display_name", "name", "emailAddress", "email", "accountId", "account_id"} {
			if s, ok := v[key].(string); ok && !xstrings.IsBlank(s) {
				return s
			}
		}
		// A user object with no recognizable identity is unassigned as
		// far as a human cell is concerned — never leak fmt's Go-map
		// rendering ("map[]") into the table.
		return ""
	}
	return formatHumanField(value)
}

func jiraUserName(user jira.User) string {
	if user.DisplayName != nil && !xstrings.IsBlank(*user.DisplayName) {
		return *user.DisplayName
	}
	if user.EmailAddress != nil && !xstrings.IsBlank(*user.EmailAddress) {
		return *user.EmailAddress
	}
	if user.AccountID != nil && !xstrings.IsBlank(*user.AccountID) {
		return *user.AccountID
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if !xstrings.IsBlank(value) {
			return value
		}
	}
	return ""
}

func normalizeIssueRows(raw any) ([]map[string]any, bool) {
	switch issues := raw.(type) {
	case []map[string]any:
		return issues, true
	case []*jira.Issue:
		return issueMapsFromPointers(issues), true
	case []jira.Issue:
		rows := make([]map[string]any, 0, len(issues))
		for i := range issues {
			rows = append(rows, issueMapFromJiraIssue(&issues[i]))
		}
		return rows, true
	case []any:
		rows := make([]map[string]any, 0, len(issues))
		for _, issue := range issues {
			switch v := issue.(type) {
			case map[string]any:
				rows = append(rows, v)
			case *jira.Issue:
				rows = append(rows, issueMapFromJiraIssue(v))
			case jira.Issue:
				rows = append(rows, issueMapFromJiraIssue(&v))
			default:
				return nil, false
			}
		}
		return rows, true
	default:
		return nil, false
	}
}

func issueMapsFromPointers(issues []*jira.Issue) []map[string]any {
	rows := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		rows = append(rows, issueMapFromJiraIssue(issue))
	}
	return rows
}

func issueMapFromJiraIssue(issue *jira.Issue) map[string]any {
	row := map[string]any{
		"key":      "",
		"summary":  "",
		"status":   "",
		"assignee": nil,
		"priority": "",
		"created":  "",
		"updated":  "",
	}
	if issue == nil {
		return row
	}
	if issue.Key != nil {
		row["key"] = *issue.Key
	}
	if issue.Fields == nil {
		return row
	}
	if issue.Fields.Summary != nil {
		row["summary"] = *issue.Fields.Summary
	}
	if status := issue.Fields.Status; status != nil {
		if status.Name != nil {
			row["status"] = *status.Name
		}
		// The status pill colors by category, not name: without these two
		// fields statusFill falls through to its per-name hash and the same
		// status renders a different hue than the compact list shape.
		if status.StatusCategory != nil && status.StatusCategory.Key != nil {
			row["status_category"] = *status.StatusCategory.Key
		}
		if status.StatusCategory != nil && status.StatusCategory.ColorName != nil {
			row["status_color"] = *status.StatusCategory.ColorName
		}
	}
	if issue.Fields.Assignee != nil {
		row["assignee"] = issue.Fields.Assignee
	}
	if issue.Fields.Priority != nil && issue.Fields.Priority.Name != nil {
		row["priority"] = *issue.Fields.Priority.Name
	}
	if issue.Fields.Created != nil {
		row["created"] = *issue.Fields.Created
	}
	if issue.Fields.Updated != nil {
		row["updated"] = *issue.Fields.Updated
	}
	return row
}

// formatHumanField extracts a display string from a (possibly typed)
// Jira value. It is a render boundary: typed issue rows bypass the
// sanitizePlainData map walk, so the extracted string is sanitized here
// before it can reach a table cell or message line.
func formatHumanField(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return SanitizeTerminalBlock(v)
	default:
		return SanitizeTerminalBlock(fmt.Sprint(v))
	}
}

func plainFieldValue(value any) any {
	if value == nil {
		return ""
	}
	rv := reflect.ValueOf(value)
	if rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) && rv.IsNil() {
		return ""
	}
	switch v := value.(type) {
	case string:
		return plainStringValue(v)
	case fmt.Stringer:
		return plainStringValue(v.String())
	}
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return ""
		}
		rv = rv.Elem()
	}
	if rv.IsValid() && rv.CanInterface() {
		value = rv.Interface()
	}
	switch {
	case rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array):
		if rv.Len() == 0 {
			return "[]"
		}
		return "[" + human.Pluralize(rv.Len(), "item", "items") + "]"
	case rv.IsValid() && rv.Kind() == reflect.Map:
		if rv.Len() == 0 {
			return "{}"
		}
		return "{...}"
	case rv.IsValid() && rv.Kind() == reflect.Struct:
		return "{...}"
	}
	return fmt.Sprint(value)
}

func plainStringValue(value string) string {
	if strings.ContainsAny(value, "\r\n\t") {
		return normalizePlain(value)
	}
	return value
}

func adfDocumentFromMap(value map[string]any) (adf.Document, bool) {
	if value["type"] != "doc" {
		return adf.Document{}, false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return adf.Document{}, false
	}
	var doc adf.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return adf.Document{}, false
	}
	return doc, true
}

func normalizePlain(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// normalizeMapList accepts either []any or []map[string]any (Go can't
// type-assert across these even when the contents match) and returns a
// uniform slice. Handy when the caller built the data with concrete
// element types instead of the more permissive []any.
func normalizeMapList(v any) []map[string]any {
	switch list := v.(type) {
	case []map[string]any:
		return list
	case []any:
		out := make([]map[string]any, 0, len(list))
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// writeAuthStatusPlain renders the auth.status envelope as a per-profile
// block: identity, credential, and live permission grid. Replaces the
// generic key=value dump that mashed everything onto one line.
func writeAuthStatusPlain(logger *clog.Logger, data any, cfg plainConfig) error {
	authMsg := messageForCommand("auth.status", data)
	m, ok := data.(map[string]any)
	if !ok {
		return writeGenericPlain(logger, cfg, authMsg, data)
	}
	active, _ := m["active_profile"].(string)
	profiles := normalizeMapList(m["profiles"])
	if len(profiles) == 0 {
		// nothing to render — fall back to the generic dump so we at least
		// surface the envelope shape.
		return writeGenericPlain(logger, cfg, authMsg, data)
	}

	th := cfg.theme
	style := authPlainStyle{tty: cfg.tty, theme: th}

	// User asked for the active profile only — auth status is the
	// "is THIS one working?" overview, not a multi-profile audit.
	rendered := 0
	for _, p := range profiles {
		name, _ := p["profile"].(string)
		if active != "" && name != active {
			continue
		}
		header := style.bold(name) + style.dim(" (active)")
		if rendered > 0 {
			logger.Info().Parts(clog.PartMessage).Msg("")
		}
		rendered++
		logger.Info().Parts(clog.PartMessage).Msg(style.label("Profile:    ") + header)

		valid, _ := p["valid"].(bool)
		source, _ := p["source"].(string)
		redacted, _ := p["redacted"].(string)
		credLine := style.dim(source) + "  " + style.dim(redacted)
		if valid {
			credLine = style.ok() + " " + credLine
		} else if errStr, _ := p["error"].(string); errStr != "" {
			credLine = style.no() + " " + style.dim(source) + "  " + style.warn(errStr)
		} else {
			credLine = style.no() + " " + credLine
		}
		logger.Info().Parts(clog.PartMessage).Msg(style.label("  Credential:  ") + credLine)

		remote, _ := p["remote"].(map[string]any)
		if remote == nil {
			logger.Info().Parts(clog.PartMessage).Msg(style.label("  Remote:      ") + style.dim("(probe skipped)"))
			continue
		}
		if site, _ := remote["site"].(string); site != "" {
			logger.Info().Parts(clog.PartMessage).Msg(style.label("  Site:        ") + style.url(site))
		}
		if myself, _ := remote["myself"].(map[string]any); myself != nil {
			logger.Info().Parts(clog.PartMessage).Msg(style.label("  Identity:    ") + formatAuthIdentity(myself, style))
			if hint, _ := myself["hint"].(string); hint != "" {
				logger.Info().Parts(clog.PartMessage).Msg("               " + style.warn(hint))
			}
		}
		if perms, _ := remote["permissions"].(map[string]any); perms != nil {
			logger.Info().Parts(clog.PartMessage).Msg(style.label("  Permissions: ") + formatAuthPerms(perms, style))
			if hint, _ := perms["hint"].(string); hint != "" {
				logger.Info().Parts(clog.PartMessage).Msg("               " + style.warn(hint))
			}
		}
	}
	return nil
}

// authPlainStyle centralizes theme decisions for the auth.status renderer
// so every line goes through the same tty/no-tty branching. Without TTY
// (test mode, no-color terminal) every method returns plain text.
type authPlainStyle struct {
	tty   bool
	theme *clibtheme.Theme
}

func (s authPlainStyle) bold(text string) string {
	if !s.tty || s.theme == nil || s.theme.Bold == nil {
		return text
	}
	return s.theme.Bold.Render(text)
}

func (s authPlainStyle) dim(text string) string {
	if !s.tty || s.theme == nil || s.theme.Dim == nil {
		return text
	}
	return s.theme.Dim.Render(text)
}

func (s authPlainStyle) label(text string) string { return s.dim(text) }

func (s authPlainStyle) url(text string) string {
	if !s.tty || s.theme == nil || s.theme.Blue == nil {
		return text
	}
	return s.theme.Blue.Render(text)
}

func (s authPlainStyle) warn(text string) string {
	if !s.tty || s.theme == nil || s.theme.Yellow == nil {
		return text
	}
	return s.theme.Yellow.Render(text)
}

func (s authPlainStyle) ok() string {
	if !s.tty {
		return "+"
	}
	if s.theme != nil && s.theme.Green != nil {
		return s.theme.Green.Render("✓")
	}
	return "✓"
}

func (s authPlainStyle) no() string {
	if !s.tty {
		return "-"
	}
	if s.theme != nil && s.theme.Red != nil {
		return s.theme.Red.Render("✗")
	}
	return "✗"
}

func (s authPlainStyle) emph(text string) string {
	if !s.tty || s.theme == nil || s.theme.Green == nil {
		return text
	}
	return s.theme.Green.Render(text)
}

func formatAuthIdentity(myself map[string]any, s authPlainStyle) string {
	if v, _ := myself["ok"].(bool); !v {
		status := ""
		if code, ok := myself["status"].(float64); ok {
			status = " HTTP " + strings.TrimSuffix(formatHumanField(code), ".0")
		}
		return s.no() + " /myself unreachable" + s.dim(status)
	}
	email, _ := myself["email"].(string)
	if email == "" {
		email = "(unknown)"
	}
	return s.ok() + " " + s.bold(email)
}

func formatAuthPerms(perms map[string]any, s authPlainStyle) string {
	if v, _ := perms["ok"].(bool); !v {
		return s.no() + " " + s.dim("/mypermissions failed")
	}
	grants := normalizeBoolMap(perms["grants"])
	if len(grants) == 0 {
		return s.dim("(none reported)")
	}
	parts := make([]string, 0, len(grants))
	granted := 0
	for name, has := range xmaps.Sorted(grants) {
		mark := s.no()
		if has {
			mark = s.ok()
			granted++
		}
		// Compact display: BROWSE_PROJECTS → browse
		short := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, "_PROJECTS"), "_ISSUES"), "S"))
		parts = append(parts, mark+" "+short)
	}
	count := strings.TrimSuffix(formatHumanField(float64(granted)), ".0")
	total := strings.TrimSuffix(formatHumanField(float64(len(grants))), ".0")
	prefix := ""
	if granted == len(grants) {
		prefix = s.ok() + " " + s.emph("all "+count) + s.dim(" · ")
	} else {
		prefix = s.bold(count) + s.dim("/"+total) + s.dim(" · ")
	}
	return prefix + strings.Join(parts, s.dim("  "))
}

// normalizeBoolMap accepts a map keyed by string with boolean values that
// may have been built as map[string]bool (preferred for clarity) or
// map[string]any (after JSON round-trips). Returns a flat map[string]bool.
func normalizeBoolMap(v any) map[string]bool {
	switch m := v.(type) {
	case map[string]bool:
		return m
	case map[string]any:
		out := make(map[string]bool, len(m))
		for k, raw := range m {
			if b, ok := raw.(bool); ok {
				out[k] = b
			}
		}
		return out
	}
	return nil
}
