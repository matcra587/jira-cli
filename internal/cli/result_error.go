package cli

import (
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// ResultError carries a command's typed receipt alongside its original failure.
// The shared construction path validates Data's registered type before running
// the handler. The root error boundary owns rendering and classification;
// handlers retain no output stream and the original error remains unwrap-able.
type ResultError struct {
	Err      error
	Op       string
	Data     any
	Human    output.HumanRenderFunc
	Warnings []envelope.Warning
	// Mutation reports the declared operation class, not whether a write took
	// effect. The root adds its shared inspection instruction before suggesting
	// a retry, while retaining the original taxonomy and transport facts.
	Mutation bool
}

func (e *ResultError) Error() string { return e.Err.Error() }

func (e *ResultError) Unwrap() error { return e.Err }
