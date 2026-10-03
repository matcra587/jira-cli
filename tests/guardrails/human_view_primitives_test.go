package guardrails

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoHumanRendererWritesDirectly is H1 run against the live tree: see
// human_view_primitives.go for what it scopes, what it matches, and what
// it deliberately does not.
func TestNoHumanRendererWritesDirectly(t *testing.T) {
	t.Parallel()
	violations, err := ScanHumanRenderers(repoRoot(t))
	if err != nil {
		t.Fatalf("scan human renderers: %v", err)
	}
	for _, v := range violations {
		t.Error(v.String())
	}
}

// writeFixture writes files (relative-path -> source) under dir,
// creating parent directories as needed — the fixture builder every
// case in TestScanHumanRenderersDetectsDirectWrites shares.
func writeFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, src := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir for fixture %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", rel, err)
		}
	}
}

// commandSpecStub is the minimal command.Spec[T] type ScanHumanRenderers
// needs to see, laid down at commandSpecPkg's own import path inside
// each fixture's own temp module tree, entirely detached from the real
// repo — the real command.Spec, its generic type parameter, and its
// dozen other fields carry nothing this scan reads.
const commandSpecStub = `package command

type Spec[T any] struct {
	Op    string
	Human func()
}
`

// TestScanHumanRenderersDetectsDirectWrites is the fixture that keeps
// the real scan above honest, matching the pattern
// TestCallsRawEncoderBindsToTheImport and
// TestCallsTerminalProbeBindsToTheImport already set for this package:
// the live walk over internal/cli is expected to find NOTHING, so a
// detector that quietly stopped matching would pass exactly as loudly as
// a clean tree — these cases are what stands between the two.
func TestScanHumanRenderersDetectsDirectWrites(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		renderer   string // contents of internal/cli/widget/render.go
		wantDetail string // substring of the one violation's Detail, "" for none
		wantOp     string
	}{
		{
			name: "clean renderer using only View primitives",
			renderer: `package widget

import "github.com/matcra587/jira-cli/internal/cli/command"

func newWidget() {
	_ = command.Spec[int]{Op: "widget.view", Human: renderHuman}
}

func renderHuman() {
	v.Line("fine")
}
`,
			wantDetail: "",
		},
		{
			name: "direct fmt.Fprintf bypasses the View",
			renderer: `package widget

import (
	"fmt"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func newWidget() {
	_ = command.Spec[int]{Op: "widget.view", Human: renderHuman}
}

func renderHuman() {
	fmt.Fprintf(w, "bad")
}
`,
			wantOp:     "widget.view",
			wantDetail: "calls fmt.Fprintf directly",
		},
		{
			name: "aliased fmt import is still caught",
			renderer: `package widget

import (
	stdfmt "fmt"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func newWidget() {
	_ = command.Spec[int]{Op: "widget.view", Human: renderHuman}
}

func renderHuman() {
	stdfmt.Fprintln(w, "bad")
}
`,
			wantOp:     "widget.view",
			wantDetail: "calls stdfmt.Fprintln directly",
		},
		{
			name: "io.WriteString bypasses the View",
			renderer: `package widget

import (
	"io"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func newWidget() {
	_ = command.Spec[int]{Op: "widget.view", Human: renderHuman}
}

func renderHuman() {
	io.WriteString(w, "bad")
}
`,
			wantOp:     "widget.view",
			wantDetail: "calls io.WriteString directly",
		},
		{
			name: "os.Stdout reached through a same-package helper two calls deep",
			renderer: `package widget

import "github.com/matcra587/jira-cli/internal/cli/command"

func newWidget() {
	_ = command.Spec[int]{Op: "widget.view", Human: renderHuman}
}

func renderHuman() {
	drawDirectly()
}

func drawDirectly() {
	writeRaw()
}
`,
			wantOp:     "widget.view",
			wantDetail: "references os.Stdout directly",
		},
		{
			name: "the token in a comment is prose, not a call",
			renderer: `package widget

import "github.com/matcra587/jira-cli/internal/cli/command"

func newWidget() {
	_ = command.Spec[int]{Op: "widget.view", Human: renderHuman}
}

// fmt.Fprintf is what this function must never call.
func renderHuman() {
	v.Line("fmt.Fprintf(w, \"bad\") is also just data, not a call")
}
`,
			wantDetail: "",
		},
		{
			name: "a call to Sprintf is not a write and must not be flagged",
			renderer: `package widget

import (
	"fmt"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func newWidget() {
	_ = command.Spec[int]{Op: "widget.view", Human: renderHuman}
}

func renderHuman() {
	v.Line(fmt.Sprintf("fine: %d", 1))
}
`,
			wantDetail: "",
		},
		{
			name: "an inline closure entry point is scanned directly",
			renderer: `package widget

import (
	"fmt"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func newWidget() {
	_ = command.Spec[int]{Op: "widget.view", Human: func() {
		fmt.Fprintln(w, "bad")
	}}
}
`,
			wantOp:     "widget.view",
			wantDetail: "calls fmt.Fprintln directly",
		},
	}

	// writeRaw is the second hop drawDirectly reaches — its own file, to
	// prove the closure follows a call graph ACROSS files in one package
	// and not merely within one.
	const writeRawFile = `package widget

import "os"

func writeRaw() {
	_ = os.Stdout
}
`

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			files := map[string]string{
				"internal/cli/command/spec.go": commandSpecStub,
				"internal/cli/widget/view.go":  tt.renderer,
			}
			if tt.name == "os.Stdout reached through a same-package helper two calls deep" {
				files["internal/cli/widget/raw.go"] = writeRawFile
			}
			writeFixture(t, root, files)

			violations, err := ScanHumanRenderers(root)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if tt.wantDetail == "" {
				for _, v := range violations {
					t.Errorf("unexpected violation: %s", v.String())
				}
				return
			}
			if len(violations) != 1 {
				t.Fatalf("violations = %v, want exactly one", violations)
			}
			if violations[0].Op != tt.wantOp {
				t.Errorf("op = %q, want %q", violations[0].Op, tt.wantOp)
			}
			if got := violations[0].Detail; !strings.Contains(got, tt.wantDetail) {
				t.Errorf("detail = %q, want it to contain %q", got, tt.wantDetail)
			}
		})
	}
}

// TestScanHumanRenderersFailsLoudOnAnUnresolvableHumanField pins the
// "never silently skip" rule findHumanEntries documents: a Human field
// this scan cannot resolve into either a package-level function or an
// inline closure must fail the scan, not quietly pass it as clean.
func TestScanHumanRenderersFailsLoudOnAnUnresolvableHumanField(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"internal/cli/command/spec.go": commandSpecStub,
		"internal/cli/widget/view.go": `package widget

import (
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/widget/other"
)

func newWidget() {
	_ = command.Spec[int]{Op: "widget.view", Human: other.RenderHuman}
}
`,
		"internal/cli/widget/other/other.go": `package other

func RenderHuman() {}
`,
	})

	if _, err := ScanHumanRenderers(root); err == nil {
		t.Error("want an error for a Human field this scan cannot resolve, got nil")
	}
}

// TestScanHumanRenderersFailsLoudOnAMissingOp pins the same rule for the
// other required field: a Spec literal that declares Human but no Op
// leaves H1 with no operation to attribute the verdict to.
func TestScanHumanRenderersFailsLoudOnAMissingOp(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"internal/cli/command/spec.go": commandSpecStub,
		"internal/cli/widget/view.go": `package widget

import "github.com/matcra587/jira-cli/internal/cli/command"

func newWidget() {
	_ = command.Spec[int]{Human: renderHuman}
}

func renderHuman() {}
`,
	})

	if _, err := ScanHumanRenderers(root); err == nil {
		t.Error("want an error for a Human field with no Op, got nil")
	}
}
