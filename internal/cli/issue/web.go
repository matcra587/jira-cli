package issue

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	xos "github.com/gechr/x/os"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// flagWeb is --web's own flag name, registered ONCE as a PERSISTENT flag
// on the `issue` group (issue.go) so every subcommand under it means the
// same "open the thing I'm looking at in a browser". isWeb below reads it
// back off each subcommand's OWN *cobra.Command rather than a shared
// package variable, because cobra merges a parent's persistent flags into
// every child's own flag set before that child's RunE runs (upstream
// cobra's mergePersistentFlags, called from ParseFlags) — the same "read
// from the flag the path itself registered" posture command.Invocation's
// own DryRun field documents for --dry-run.
const flagWeb = "web"

// isWeb reports whether --web was given for this invocation of cmd — the
// same "an undefined flag is a wiring question, not a runtime one, so a
// GetBool error just means false" posture command.go's own dryRunFlag
// documents for --dry-run.
func isWeb(cmd *cobra.Command) bool {
	web, err := cmd.Flags().GetBool(flagWeb)
	return err == nil && web
}

// browserOpener launches name (with args) to show a URL — injected so a
// test can assert what WOULD have run without a real browser ever
// starting. execOpener below is the only production implementation;
// every runXWeb function in this package takes one as a parameter rather
// than calling execOpener directly, for exactly that substitution.
type browserOpener func(name string, args ...string) error

// execOpener is the production browserOpener: exec.Command with name and
// every argument as SEPARATE argv entries, never a shell string — so a
// hostile issue key or JQL fragment that somehow reached this call (both
// are already validated or escaped well before targetURL is built — see
// cli.IssueBrowseURL and cli.SearchBrowseURL) could not execute anything even
// then, because there is no shell here for it to reach.
//
// Start, not Run: a browser is a GUI program this command hands off to,
// not a child whose exit code or output it has any use for — waiting on
// it would block the CLI for however long a person leaves the tab open.
// Start's own error already reports the two failures --web's degrade
// path (openForHuman below) cares about — the program does not exist (an
// absent xdg-open/wslview/explorer.exe, or a $BROWSER shim not on PATH)
// and a bad exec — without this command ever waiting on what it started.
func execOpener(name string, args ...string) error {
	return exec.Command(name, args...).Start() //nolint:gosec // name/args are a resolved opener and a validated URL, never a shell string
}

// openInBrowser opens targetURL for a human, trying $BROWSER FIRST — the
// fact that drove this function's whole shape. On the machine this was
// built against, xdg-open, wslview and explorer.exe are ALL absent, but
// $BROWSER is set to a user shim that hands off to the host browser: an
// opener that reaches straight for a platform default fails there,
// silently or loudly, exactly by skipping this check.
//
// $BROWSER, when set, is authoritative: its own success or failure is
// the whole answer, with no second attempt at a platform default a
// caller who deliberately set it almost certainly does not want tried
// instead. Only an UNSET $BROWSER falls through to the platform default
// — xdg-open (linux), open (darwin), or rundll32 url.dll,
// FileProtocolHandler (windows), chosen from runtime.GOOS via
// gechr/x/os's Platform constants rather than a raw string literal. A
// GOOS with no mapped default is reported as an error the same way a
// failing command is — never a panic, never silently swallowed — for
// openForHuman to turn into a warning rather than a command failure.
func openInBrowser(open browserOpener, targetURL string) error {
	if browser := strings.TrimSpace(os.Getenv("BROWSER")); browser != "" {
		return open(browser, targetURL)
	}
	switch runtime.GOOS {
	case xos.PlatformLinux:
		return open("xdg-open", targetURL)
	case xos.PlatformDarwin:
		return open("open", targetURL)
	case xos.PlatformWindows:
		return open("rundll32", "url.dll,FileProtocolHandler", targetURL)
	default:
		return fmt.Errorf("issue: no browser opener known for GOOS %q", runtime.GOOS)
	}
}

// openForHuman shares the machine/human split for open and --web: it opens targetURL
// ONLY when this invocation's resolved output format is human — the same
// resolved-format signal issue view's own isHuman (view.go's runView)
// and issue list's own fetchCountEstimate gate already read
// (cli.FormatFromContext), reused here rather than a second notion of
// "is this human". An agent or script asking for --output=json/compact
// gets targetURL in the envelope and nothing else launched on its
// behalf — launching a browser from a non-interactive caller is a side
// effect nobody asked for.
//
// A failed or missing opener is reported as a WARNING, not a command
// error, and the invocation still exits 0: targetURL is already real,
// already about to be printed by the human renderer (see renderWebURL
// below), and a browser that will not launch takes nothing away from
// that — the user asked to see a thing, and can still see its URL.
//
// cli.AddWarning is the mechanism, and the reason it is the only one is
// recorded once at its own declaration (internal/cli/warnings.go) rather
// than restated here.
func openForHuman(ctx context.Context, open browserOpener, targetURL string) bool {
	format, ok := cli.FormatFromContext(ctx)
	if !ok || format != output.FormatHuman {
		return false
	}
	if err := openInBrowser(open, targetURL); err != nil {
		cli.AddWarning(envelope.Warning{
			Type:    envelope.WarningBrowserOpenFailed,
			Message: fmt.Sprintf("could not open a browser (%v) — the URL is printed below", err),
		})
		return false
	}
	return true
}

// openIssueBrowseTarget resolves key's browse URL and, on a human
// surface, hands it to open — the whole of what an issue-scoped `--web`
// path does before it fills in its own envelope shape.
//
// It exists because --web is registered ONCE as a persistent flag on the
// `issue` group (flagWeb above), so every subcommand under it accepts
// --web whether or not anyone wired a resolver for it. When runViewWeb
// and runCommentListWeb each carried their own copy, those copies were
// identical down to the error string, with nothing forcing them to stay
// that way; the next verb added under the group would have made a third.
//
// Production resolves the site from settings only. A URL-only command never
// constructs a Jira client or opens credentials, including for local history.
//
// A key that cannot become a browse URL (cli.IssueBrowseURL's own shape
// gate — see its doc comment) fails the command outright, rather than
// degrading to no link the way a passive RENDER would: producing this
// URL is the entire point of the invocation, so silence would answer
// nothing. cli.CodePathInvalid is deliberately reused, not a new code —
// this is the same "an identifier could not become a path segment"
// failure adapterjira.ErrUnsafePath already reports for GetIssue's own
// key, applied to a browse-URL path instead of a REST one.
//
// open is a parameter rather than a hard-coded execOpener so a test can
// supply a fake and prove nothing here spawns a real browser; the
// production execOpener is passed by each command's own Run closure.
func openIssueBrowseTarget(ctx context.Context, deps Deps, key string, open browserOpener) (string, error) {
	target, err := issueBrowseTarget(ctx, deps, key)
	if err != nil {
		return "", err
	}
	openForHuman(ctx, open, target)
	return target, nil
}

// issueBrowseTarget resolves a URL without fetching an issue. Production uses
// settings only; embedded callers may provide an existing client's site.
func issueBrowseTarget(ctx context.Context, deps Deps, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var site string
	if deps.BrowseSite != nil {
		var err error
		site, err = deps.BrowseSite()
		if err != nil {
			return "", err
		}
	} else if deps.JiraClient != nil {
		client, err := deps.JiraClient(ctx)
		if err != nil {
			return "", err
		}
		site = client.BaseURL()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if site == "" {
		e := cli.NewInputError(cli.CodeConfigInvalid, "opening an issue requires a configured base_url")
		e.Field = "base_url"
		return "", e
	}
	target, ok := cli.IssueBrowseURL(site, key)
	if !ok {
		e := cli.NewInputError(cli.CodePathInvalid,
			fmt.Sprintf("%q is not a shape a Jira browse URL can be built from", key))
		e.Field = "ISSUE_KEY"
		return "", e
	}
	return target, nil
}

// renderWebURL shares the human render for open and --web: one line naming
// the URL this invocation resolved, so the wording cannot drift between
// callers. Printed unconditionally whenever a caller
// reaches it, whether or not openForHuman's own browser launch actually
// succeeded (see its own doc comment): a copy-pasteable URL is useful
// either way. webURL is CLI-built from a validated site plus an already-
// validated key or an already-escaped JQL string, never Jira-authored
// text, so — like nextPageHint's and renderCommentList's own banners —
// there is no cli.Text sanitizing sink to run over it here.
func renderWebURL(v *output.View, webURL string) error {
	v.Line("Open in browser: " + webURL)
	return nil
}

// ScreenOpen uses the ordinary browser policy for an explicit dashboard action.
// The URL remains available on failure, and success only confirms process handoff.
func ScreenOpen(ctx context.Context, site, key string) (string, error) {
	return screenOpen(ctx, site, key, execOpener)
}

func screenOpen(ctx context.Context, site, key string, open browserOpener) (string, error) {
	target, ok := cli.IssueBrowseURL(site, key)
	if !ok {
		return "", fmt.Errorf("issue has no valid browser URL")
	}
	if err := ctx.Err(); err != nil {
		return target, err
	}
	return target, openInBrowser(open, target)
}
