package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gechr/clog"
	xstrings "github.com/gechr/x/strings"
	"github.com/matcra587/jira-cli/internal/adf"
)

// WriteIssueViewPlain renders the `issue.view` envelope data as a human issue
// card — header, the key summary fields, and the rendered description — and
// fans out to the multi-issue layout when the payload carries keyed results.
func WriteIssueViewPlain(w io.Writer, command string, data any, opts ...PlainOption) error {
	cfg := defaultPlainConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	logger := newPlainLogger(w)

	m := mapFromAny(data)
	if len(m) == 0 {
		return writeGenericPlain(logger, cfg, messageForCommand(command, data), data)
	}
	if rawResults, ok := m["results"]; ok {
		results := normalizeMapList(rawResults)
		if results == nil {
			return writeGenericPlain(logger, cfg, messageForCommand(command, data), data)
		}
		return writeIssueViewManyPlain(logger, command, results, cfg)
	}
	issue := mapFromAny(m["issue"])
	if len(issue) == 0 {
		return writeGenericPlain(logger, cfg, messageForCommand(command, data), data)
	}
	fields := mapFromAny(issue["fields"])
	style := authPlainStyle{tty: cfg.tty, theme: cfg.theme}

	key := stringFromMap(issue, "key")
	summary := stringFromMap(fields, "summary")
	header := strings.TrimSpace(strings.Join([]string{key, summary}, "  "))
	if header == "" {
		header = "Issue"
	}
	logger.Info().Parts(clog.PartMessage).Msg(style.bold(header))

	// The single-issue view renders status as plain text by design; category
	// coloring is scoped to the issue-list table (see statusPillCell), where
	// scanning many rows makes color worthwhile.
	status := nestedString(fields, "status", "name")
	priority := nestedString(fields, "priority", "name")
	assignee := nestedString(fields, "assignee", "displayName")
	if assignee == "" {
		assignee = nestedString(fields, "assignee", "accountId")
	}
	if assignee == "" {
		assignee = "unassigned"
	}
	logger.Info().Parts(clog.PartMessage).Msg(fmt.Sprintf(
		"  status: %s  priority: %s  assignee: %s",
		firstNonEmpty(status, "unknown"),
		firstNonEmpty(priority, "none"),
		assignee,
	))

	if transitions := issueTransitionsPlain(issue); transitions != "" {
		logger.Info().Parts(clog.PartMessage).Msg("  transitions: " + transitions)
	}

	if description := issueDescriptionPlain(fields); description != "" {
		logger.Info().Parts(clog.PartMessage).Msg("  " + description)
	}
	return nil
}

// issueTransitionsPlain flattens the expanded workflow transitions to a
// one-line "Name (id)" list so a human sees the valid moves in the same
// read. Empty when the payload carries no transitions.
func issueTransitionsPlain(issue map[string]any) string {
	transitions := normalizeMapList(issue["transitions"])
	parts := make([]string, 0, len(transitions))
	for _, transition := range transitions {
		name := stringFromMap(transition, "name")
		id := stringFromMap(transition, "id")
		switch {
		case xstrings.AllNonEmpty(name, id):
			parts = append(parts, name+" ("+id+")")
		case name != "":
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}

func writeIssueViewManyPlain(logger *clog.Logger, command string, results []map[string]any, cfg plainConfig) error {
	successes := make([]map[string]any, 0, len(results))
	failureCount := 0
	for _, result := range results {
		key := stringFromMap(result, "key")
		if ok, _ := result["ok"].(bool); !ok {
			failureCount++
			continue
		}
		issue := mapFromAny(result["issue"])
		row := issueViewSummaryRow(issue)
		if row["key"] == "" {
			row["key"] = key
		}
		successes = append(successes, row)
	}

	// succeeded renders as a gradient fraction; failed appears only when
	// something failed, matching the keyed summary line.
	event := logger.Info().
		Fraction("succeeded", len(successes), len(results))
	if failureCount > 0 {
		event = event.Int("failed", failureCount)
	}
	if cfg.threads > 0 {
		event = event.Int("threads", cfg.threads)
	}
	if cfg.elapsed >= time.Second {
		// The whole fan-out's wall time; the 1s threshold
		// hides fast batches.
		event = event.Duration("elapsed", cfg.elapsed)
	}
	event.Msg(SentenceCase(VerbFor(command).PastPlural()))
	rows, err := issueRows(successes, cfg)
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

const issueViewFailedKeysPlainLimit = 5

// WriteIssueViewFailureDiagnostics mirrors multi-key issue-view failures to
// stderr for human mode. stdout stays reserved for successful rows.
func WriteIssueViewFailureDiagnostics(w io.Writer, data any, errorsOut []Error) error {
	failures := issueViewFailureKeys(data)
	if len(failures) == 0 || w == nil {
		return nil
	}
	return withTrackedWriter(w, func(out io.Writer) error {
		logger := newPlainLoggerAt(out, clog.LevelError)
		shown, omitted := issueViewShownFailureKeys(failures)
		total, succeeded, failed := issueViewFailureCounts(data, len(failures))
		event := logger.Error().
			Fraction("succeeded", succeeded, total).
			Int("failed", failed).
			Str("reason", issueViewPlainFailureReason(errorsOut)).
			Str("keys", strings.Join(shown, ", ")).
			Int("shown", len(shown))
		if omitted > 0 {
			event = event.Int("omitted", omitted)
		}
		event = event.Str("hint", "use --output=json for full per-key errors")
		event.Msg(failedKeysSummary)
		return nil
	})
}

func issueViewFailureCounts(data any, failureKeys int) (int, int, int) {
	m := mapFromAny(data)
	results := normalizeMapList(m["results"])
	total := len(results)
	succeeded := intFromMap(m, "succeeded")
	failed := intFromMap(m, "failed")
	if failed == 0 {
		failed = failureKeys
	}
	if total == 0 {
		total = succeeded + failed
	}
	if succeeded == 0 && total > failed {
		succeeded = total - failed
	}
	return total, succeeded, failed
}

func issueViewPlainFailureReason(errorsOut []Error) string {
	if len(errorsOut) == 0 {
		return "error"
	}
	top := errorsOut[0]
	if top.Code != "" {
		return strings.ReplaceAll(top.Code, "_", " ")
	}
	if top.Type != "" {
		return top.Type
	}
	return "error"
}

func issueViewFailureKeys(data any) []string {
	m := mapFromAny(data)
	results := normalizeMapList(m["results"])
	if len(results) == 0 {
		return nil
	}
	failures := make([]string, 0)
	for _, result := range results {
		if ok, _ := result["ok"].(bool); ok {
			continue
		}
		if key := stringFromMap(result, "key"); key != "" {
			failures = append(failures, key)
		}
	}
	return failures
}

func issueViewShownFailureKeys(keys []string) ([]string, int) {
	limit := issueViewFailedKeysPlainLimit
	if len(keys) < limit {
		limit = len(keys)
	}
	return keys[:limit], len(keys) - limit
}

func issueViewSummaryRow(issue map[string]any) map[string]any {
	fields := mapFromAny(issue["fields"])
	// statusCategory rides along so the summary table's status pill colors
	// by category exactly like the issue-list shapes; without it the pill
	// falls back to statusFill's per-name hash and the hue drifts between
	// surfaces.
	statusCategory := mapFromAny(mapFromAny(fields["status"])["statusCategory"])
	return map[string]any{
		"key":             stringFromMap(issue, "key"),
		"summary":         stringFromMap(fields, "summary"),
		"status":          nestedString(fields, "status", "name"),
		"status_category": stringFromMap(statusCategory, "key"),
		"status_color":    stringFromMap(statusCategory, "colorName"),
		"assignee":        mapFromAny(fields["assignee"]),
		"priority":        nestedString(fields, "priority", "name"),
	}
}

// WriteIssueTransitionsPlain renders the `issue.transitions` envelope data as a
// header plus one "id  name" line per available transition, or an explicit "no
// transitions available" line when the workflow offers none.
func WriteIssueTransitionsPlain(w io.Writer, command string, data any, opts ...PlainOption) error {
	cfg := defaultPlainConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	logger := newPlainLogger(w)

	// mapFromAny accepts both the typed IssueTransitionsOutput struct (the
	// single-command path) and a raw map (a keyed child, or a legacy caller).
	// The []*jira.Transition slice flattens through normalizeMapList's JSON
	// path either way, so a struct needs no bespoke handling here.
	m := mapFromAny(data)
	if m == nil {
		return writeGenericPlain(logger, cfg, messageForCommand(command, data), data)
	}
	transitions := normalizeMapList(m["transitions"])
	style := authPlainStyle{tty: cfg.tty, theme: cfg.theme}

	issue := stringFromMap(mapFromAny(m["issue"]), "key")
	header := "Transitions"
	if issue != "" {
		header += " on " + issue
	}
	logger.Info().Parts(clog.PartMessage).Msg(style.bold(header))

	if len(transitions) == 0 {
		logger.Info().Parts(clog.PartMessage).Msg(style.dim("  (no transitions available)"))
		return nil
	}
	for _, transition := range transitions {
		id := stringFromMap(transition, "id")
		name := stringFromMap(transition, "name")
		logger.Info().Parts(clog.PartMessage).Msg("  " + padRight(id, 4) + "  " + name)
	}
	return nil
}

func mapFromAny(value any) map[string]any {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// stringFromMap extracts a display string from a plain-render map. It is
// the map-side render boundary for the bespoke renderers (issue view,
// transitions) that read a typed payload back through mapFromAny — that
// re-marshal bypasses the WriteCommandPlain data walk, so the extracted
// string is sanitized here, exactly as formatHumanField covers the typed
// table path. Keys the renderers pass on as identifiers (issue keys) are
// control-byte-free, so sanitizing them is a no-op.
func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch value := m[key].(type) {
	case string:
		return SanitizeTerminalBlock(value)
	case fmt.Stringer:
		return SanitizeTerminalBlock(value.String())
	default:
		return ""
	}
}

func intFromMap(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch value := m[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}

func nestedString(m map[string]any, key, child string) string {
	return stringFromMap(mapFromAny(m[key]), child)
}

func issueDescriptionPlain(fields map[string]any) string {
	docMap := mapFromAny(fields["description"])
	doc, ok := adfDocumentFromMap(docMap)
	if !ok {
		return ""
	}
	// The ADF is flattened straight from the (possibly typed, hence
	// un-walked) issue payload, so the rendered text is sanitized here at
	// the render boundary before it reaches the terminal.
	return SanitizeTerminalBlock(normalizePlain(adf.ToPlain(doc)))
}
