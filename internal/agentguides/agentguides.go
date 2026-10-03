// Package agentguides owns the embedded, validated CLI workflow instructions.
package agentguides

import (
	"embed"
	"io/fs"

	"github.com/matcra587/docent"
)

//go:embed guides/*.md
var guidesFS embed.FS

// Files returns the guide-rooted filesystem used by loading and contract tests.
func Files() fs.FS {
	sub, err := fs.Sub(guidesFS, "guides")
	if err != nil {
		panic(err) // the embedded directory is a fixed construction invariant
	}
	return sub
}

// Load parses and validates the embedded guide set without external I/O.
func Load() (*docent.GuideSet, error) {
	return docent.LoadGuides(Files())
}
