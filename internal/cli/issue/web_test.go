package issue

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// recordedOpen is one call a fake browserOpener recorded — the shape
// every test below asserts against instead of a real subprocess ever
// starting (the whole point of browserOpener being injected: --web's own
// tests must never spawn a browser).
type recordedOpen struct {
	name string
	args []string
}

// fakeOpener returns a browserOpener that records every call it receives
// and answers err (nil for "succeeded") — shared by every test in this
// file so none of them execs a real program.
func fakeOpener(calls *[]recordedOpen, err error) browserOpener {
	return func(name string, args ...string) error {
		*calls = append(*calls, recordedOpen{name: name, args: args})
		return err
	}
}

// TestOpenInBrowserPrefersBrowserEnvOverPlatformDefault pins the ONE
// fact --web was built against (see openInBrowser's own doc comment):
// $BROWSER, when set, is used — and used ALONE, never alongside a
// platform-default attempt — regardless of what platform this test
// itself runs on.
func TestOpenInBrowserPrefersBrowserEnvOverPlatformDefault(t *testing.T) {
	t.Setenv("BROWSER", "/home/user/.local/bin/wsl-browser")

	var calls []recordedOpen
	if err := openInBrowser(fakeOpener(&calls, nil), "https://example.atlassian.net/browse/ABC-123"); err != nil {
		t.Fatalf("openInBrowser: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("opener called %d times, want 1: %+v", len(calls), calls)
	}
	if calls[0].name != "/home/user/.local/bin/wsl-browser" {
		t.Errorf("opener name = %q, want the $BROWSER shim, not a platform default", calls[0].name)
	}
	if len(calls[0].args) != 1 || calls[0].args[0] != "https://example.atlassian.net/browse/ABC-123" {
		t.Errorf("opener args = %v, want [targetURL]", calls[0].args)
	}
}

// TestOpenInBrowserBrowserEnvFailureDoesNotFallBackToPlatformDefault pins
// that $BROWSER is AUTHORITATIVE when set: a caller who set it
// deliberately does not want a different browser tried behind their
// back just because their own choice failed to launch.
func TestOpenInBrowserBrowserEnvFailureDoesNotFallBackToPlatformDefault(t *testing.T) {
	t.Setenv("BROWSER", "/does/not/exist")

	var calls []recordedOpen
	wantErr := errors.New("exec: no such file or directory")
	err := openInBrowser(fakeOpener(&calls, wantErr), "https://example.atlassian.net/browse/ABC-123")

	if !errors.Is(err, wantErr) {
		t.Errorf("openInBrowser error = %v, want %v", err, wantErr)
	}
	if len(calls) != 1 {
		t.Fatalf("opener called %d times, want exactly 1 (no platform-default retry): %+v", len(calls), calls)
	}
}

// TestOpenInBrowserFallsBackToPlatformDefaultWhenUnset pins the SECOND
// half of the ordering: an unset (or blank) $BROWSER falls through to
// the platform default this test's own runtime.GOOS maps to — asserted
// per-GOOS so the test is honest on whichever of linux/darwin/windows
// actually runs it, per this repo's own supported set (macOS/Linux/WSL).
func TestOpenInBrowserFallsBackToPlatformDefaultWhenUnset(t *testing.T) {
	t.Setenv("BROWSER", "")

	var calls []recordedOpen
	if err := openInBrowser(fakeOpener(&calls, nil), "https://example.atlassian.net/browse/ABC-123"); err != nil {
		t.Fatalf("openInBrowser: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("opener called %d times, want 1: %+v", len(calls), calls)
	}

	var wantName string
	switch runtime.GOOS {
	case "linux":
		wantName = "xdg-open"
	case "darwin":
		wantName = "open"
	case "windows":
		wantName = "rundll32"
	default:
		t.Skipf("no platform-default expectation pinned for GOOS %q", runtime.GOOS)
	}
	if calls[0].name != wantName {
		t.Errorf("opener name = %q, want %q for GOOS %q", calls[0].name, wantName, runtime.GOOS)
	}
}

// TestSearchWebURL pins issue list --web's own target shape: Jira
// Cloud's issue navigator at "<site>/issues/?jql=<escaped query>" (see
// cli.SearchBrowseURL's own doc comment for what was checked to confirm this
// shape).
func TestSearchWebURL(t *testing.T) {
	tests := []struct {
		name    string
		site    string
		jql     string
		wantURL string
		wantOK  bool
	}{
		{
			name:    "simple JQL",
			site:    "https://example.atlassian.net",
			jql:     `project = ABC`,
			wantURL: "https://example.atlassian.net/issues/?jql=project+%3D+ABC",
			wantOK:  true,
		},
		{
			name:    "JQL with structural URL bytes stays inside the jql value",
			site:    "https://example.atlassian.net",
			jql:     `project = "ABC" AND status = "In Progress"`,
			wantURL: "https://example.atlassian.net/issues/?jql=project+%3D+%22ABC%22+AND+status+%3D+%22In+Progress%22",
			wantOK:  true,
		},
		{
			name:   "site with no scheme is rejected",
			site:   "example.atlassian.net",
			jql:    "project = ABC",
			wantOK: false,
		},
		{
			name:   "empty site is rejected",
			site:   "",
			jql:    "project = ABC",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := cli.SearchBrowseURL(tc.site, tc.jql)
			if ok != tc.wantOK {
				t.Fatalf("cli.SearchBrowseURL(%q, %q) ok = %v, want %v (url = %q)", tc.site, tc.jql, ok, tc.wantOK, got)
			}
			if ok && got != tc.wantURL {
				t.Errorf("cli.SearchBrowseURL(%q, %q) = %q, want %q", tc.site, tc.jql, got, tc.wantURL)
			}
		})
	}
}

// TestOpenForHumanOnlyActsForHumanFormat pins the machine/human split:
// a resolved format that is not human (json/compact, or no format
// resolved at all) must never call the opener — see openForHuman's own
// doc comment for why launching a browser from a non-interactive caller
// is a side effect nobody asked for.
func TestOpenForHumanOnlyActsForHumanFormat(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() context.Context
	}{
		{"json", func() context.Context { return cli.WithFormat(t.Context(), output.FormatJSON) }},
		{"compact", func() context.Context { return cli.WithFormat(t.Context(), output.FormatCompact) }},
		{"no format resolved", func() context.Context { return t.Context() }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls []recordedOpen
			openForHuman(tc.ctx(), fakeOpener(&calls, nil), "https://example.atlassian.net/browse/ABC-123")
			if len(calls) != 0 {
				t.Errorf("opener called %d times for %s output, want 0: %+v", len(calls), tc.name, calls)
			}
		})
	}
}

// TestOpenForHumanOpensForHumanFormat is the positive half of the split
// above: a resolved human format DOES call the opener, with targetURL as
// its sole argument.
func TestOpenForHumanOpensForHumanFormat(t *testing.T) {
	defer cli.TakeWarnings() // drain whatever this test adds, so it cannot leak into another

	var calls []recordedOpen
	openForHuman(humanContext(t), fakeOpener(&calls, nil), "https://example.atlassian.net/browse/ABC-123")

	if len(calls) != 1 {
		t.Fatalf("opener called %d times, want 1: %+v", len(calls), calls)
	}
	if len(calls[0].args) != 1 || calls[0].args[0] != "https://example.atlassian.net/browse/ABC-123" {
		t.Errorf("opener args = %v, want [targetURL]", calls[0].args)
	}
}

// TestOpenForHumanFailureWarnsAndDoesNotError pins --web's own degrade
// path: a human render whose opener fails gets a warning through
// cli.AddWarning/TakeWarnings — never an error out of
// openForHuman itself (it has no return value to carry one), matching
// the contract that a missing/failing opener is a warning, not a command
// failure.
func TestOpenForHumanFailureWarnsAndDoesNotError(t *testing.T) {
	cli.TakeWarnings() // drain any warning a prior test left uncollected

	var calls []recordedOpen
	openForHuman(humanContext(t), fakeOpener(&calls, errors.New("exec: \"xdg-open\": executable file not found in $PATH")), "https://example.atlassian.net/browse/ABC-123")

	warnings := cli.TakeWarnings()
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %+v", len(warnings), warnings)
	}
	if warnings[0].Type != envelope.WarningBrowserOpenFailed {
		t.Errorf("warning type = %q, want %q", warnings[0].Type, envelope.WarningBrowserOpenFailed)
	}
	if warnings[0].Lossy {
		t.Errorf("warning Lossy = true, want false — a failed browser open drops no data, the URL still rides web_url")
	}
}

// TestOpenForHumanSuccessAddsNoWarning is the mirror of the failure
// case: a successful open leaves no warning behind for a later envelope
// to pick up.
func TestOpenForHumanSuccessAddsNoWarning(t *testing.T) {
	cli.TakeWarnings() // drain any warning a prior test left uncollected

	var calls []recordedOpen
	openForHuman(humanContext(t), fakeOpener(&calls, nil), "https://example.atlassian.net/browse/ABC-123")

	if warnings := cli.TakeWarnings(); len(warnings) != 0 {
		t.Errorf("got %d warnings after a successful open, want 0: %+v", len(warnings), warnings)
	}
}
