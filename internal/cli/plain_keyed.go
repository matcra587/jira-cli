package cli

import (
	"io"
	"strings"
	"time"

	"github.com/gechr/clog"
)

// WriteKeyedResultsPlain renders the shared multi-key envelope. It keeps stdout
// focused on the successful per-key payloads; failed keys are mirrored to
// stderr by WriteKeyedResultsFailureDiagnostics.
func WriteKeyedResultsPlain(w io.Writer, command string, data any, opts ...PlainOption) error {
	cfg := defaultPlainConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	cfg.command = command
	logger := newPlainLogger(w)
	results := keyedResultRows(data)
	if len(results) == 0 {
		return writeGenericPlain(logger, cfg, messageForCommand(command, data), data)
	}
	total, succeeded, failed := keyedResultCounts(data, len(keyedFailureKeys(data)))
	// succeeded renders as a gradient fraction — full batches read green at
	// a glance — and failed appears only when something failed, matching
	// the omit-noise policy on the issue-list line.
	event := completionEvent(logger, cfg, data, failed).
		Fraction("succeeded", succeeded, total)
	if failed > 0 {
		event = event.Int("failed", failed)
	}
	if cfg.threads > 0 {
		event = event.Int("threads", cfg.threads)
	}
	if cfg.elapsed >= time.Second {
		// The whole fan-out's wall time; the 1s threshold hides
		// fast batches. Child rows never repeat it (resultKey guard).
		event = event.Duration("elapsed", cfg.elapsed)
	}
	event.Msg(messageForCommand(command, data))

	// No per-entry heading: identification is the child renderer's job.
	// The entry key rides down as render context — block renderers fold
	// it into their own header ("Comments on KEY"), and the generic
	// renderer emits it only when the data doesn't already carry it, so
	// the key appears exactly once per entry.
	for _, result := range results {
		if ok, _ := result["ok"].(bool); !ok {
			continue
		}
		child, hasChild := result["data"]
		if !hasChild {
			continue
		}
		childOpts := opts
		if key := stringFromMap(result, "key"); key != "" {
			childOpts = append(append([]PlainOption{}, opts...), WithPlainResultKey(key))
		}
		if err := WriteCommandPlain(w, command, child, childOpts...); err != nil {
			return err
		}
	}
	return nil
}

// WriteKeyedResultsFailureDiagnostics writes the bounded failed-key summary for
// shared multi-key commands.
func WriteKeyedResultsFailureDiagnostics(w io.Writer, data any, errorsOut []Error) error {
	failures := keyedFailureKeys(data)
	if len(failures) == 0 || w == nil {
		return nil
	}
	return withTrackedWriter(w, func(out io.Writer) error {
		logger := newPlainLoggerAt(out, clog.LevelError)
		shown, omitted := shownFailureKeys(failures)
		total, succeeded, failed := keyedResultCounts(data, len(failures))
		event := logger.Error().
			Fraction("succeeded", succeeded, total).
			Int("failed", failed).
			Str("reason", plainFailureReason(errorsOut)).
			Str("keys", strings.Join(shown, ", ")).
			Int("shown", len(shown))
		if omitted > 0 {
			event = event.Int("omitted", omitted)
		}
		event.Str("hint", "use --output=json for full per-key errors").Msg(failedKeysSummary)
		return nil
	})
}

func keyedResultRows(data any) []map[string]any {
	m := mapFromAny(data)
	return normalizeMapList(m["results"])
}

func keyedFailureKeys(data any) []string {
	results := keyedResultRows(data)
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

func keyedResultCounts(data any, failureKeys int) (int, int, int) {
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

const failedKeysPlainLimit = 5

// failedKeysSummary labels the bounded failed-key diagnostics block shared by
// the keyed-results and multi-key issue-view renderers. It is a UI heading over
// a count of failed keys, not an operation verb, so it lives here rather than in
// the verb registry.
const failedKeysSummary = "Failed keys"

func shownFailureKeys(keys []string) ([]string, int) {
	limit := failedKeysPlainLimit
	if len(keys) < limit {
		limit = len(keys)
	}
	return keys[:limit], len(keys) - limit
}

func plainFailureReason(errorsOut []Error) string {
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
