package cli

import (
	"sync"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// warnings are non-fatal problems collected during a run, to be published
// on the envelope the run produces.
//
// **THIS IS THE ROUTE FOR A WARNING RAISED INSIDE A Spec.Run HANDLER**,
// and it is the only one, so it is worth saying here rather than in each
// caller. command.Spec's Run returns just (T, error) — a handler holds no
// writer and no warning sink, deliberately, because a handler that could
// reach a stream could bypass the envelope entirely (see command.Spec.Run
// and command.Invocation, which is documented as facts, never
// capabilities). A warning therefore has nowhere else to go that still
// reaches the SAME envelope the handler's return value feeds.
//
// They are collected rather than printed because a library writing to
// stderr bypasses the contract entirely: the text is not in warnings[],
// not in the JSON, and an agent never sees it. Anything worth telling a
// person is worth telling a program.
//
// **Process-wide state, which has two consequences a caller must plan
// for.** A warning rides the NEXT envelope written, whichever that is, so
// anything added before a command runs lands on that command's output.
// And because the slice is append-ordered, raising from inside a
// CONCURRENT fan-out orders the warnings by whichever goroutine finished
// first, which makes one invocation print them differently run to run. A
// keyed command must therefore collect its per-key warnings into its own
// indexed slice and raise them afterwards, walking in argv order, so they
// match results[] — `issue transition`'s collectOutcomes is the worked
// example.
//
// **The name was wrong for two releases and the cost was real.** These
// were AddStartupWarning/TakeStartupWarnings, living in theme.go, back
// when the only warning came from resolving a theme at startup. Three of
// the callers are now mid-command, per-key or per-invocation warnings
// that have nothing to do with startup or themes, and each had to write a
// paragraph saying "despite its name, this is the mechanism". Worse, a
// careful reader went looking for the warning route, did not recognize
// this as it, and concluded the framework needed a new one — a change to
// every handler signature in the CLI, to solve a problem already solved.
// The name is the fix; this comment is the record so it is not undone.
var (
	warningMu   sync.Mutex
	warningList []envelope.Warning
)

// AddWarning records a non-fatal problem for the next envelope. Safe
// before any command runs, and safe to call concurrently — though a
// concurrent caller should read the ordering note above first.
func AddWarning(w envelope.Warning) {
	warningMu.Lock()
	defer warningMu.Unlock()
	warningList = append(warningList, w)
}

// TakeWarnings returns and clears the collected warnings, so they ride the
// FIRST envelope written — success or failure — and are not repeated on a
// second.
func TakeWarnings() []envelope.Warning {
	warningMu.Lock()
	defer warningMu.Unlock()
	out := warningList
	warningList = nil
	return out
}
