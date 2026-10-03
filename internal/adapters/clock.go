package adapters

import (
	"time"

	"github.com/matcra587/jira-cli/internal/core"
)

// SystemClock is the production core.Clock: the wall clock, nothing else.
// A struct rather than a bare func value so the seam assertion below keeps
// the interface honest at compile time.
type SystemClock struct{}

var _ core.Clock = SystemClock{}

// Now reports the current wall-clock time.
func (SystemClock) Now() time.Time { return time.Now() }
