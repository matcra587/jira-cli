package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gechr/clib/theme"
	"github.com/gechr/clog"
	clogtheme "github.com/gechr/clog/theme"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// The theme is resolved HERE, at the lowest layer every command can
// reach, and not in the root command, because more than one place needs
// the answer. Anything that styles output without it calls clib's
// theme.Auto(), which probes the terminal over OSC 11 and waits — the
// 5108ms stall this resolution exists to avoid. A second call site is
// how that stall came back once already; tests/guardrails pins that it
// cannot come back again.

// ConfigureTheme resolves the theme exactly once per process.
//
// Once because the libraries re-read the environment whenever a prefix
// is set, and again at their own package init — so anything repeated
// here repeats the report of a bad value with it.
var ConfigureTheme = sync.OnceFunc(applyTheme)

// ourThemeEnv is our own theme variable, and also the environment layer
// of the `theme` config key — which is why it is the only one that can
// outlive resolution. See applyTheme.
const ourThemeEnv = "JIRA_THEME"

// themeEnv is searched in order and the first variable with a value
// wins: ours, then each library's own, then the generic one — so
// JIRA_THEME can override a THEME set for the whole shell.
var themeEnv = []string{ourThemeEnv, "CLIB_THEME", "CLOG_THEME", "THEME"}

// chosenTheme is the clib theme someone actually named, or nil while
// nobody has. applyTheme sets it from the environment at startup, and
// readThemeFromConfig from the config file on the first resolution that
// finds no environment answer.
var (
	// chosenMu guards chosenTheme. Resolution is startup wiring and was
	// written as if it were single-threaded, but the config layer
	// resolves LAZILY — the first dialog to ask can be on any goroutine,
	// while others are already reading. The race detector found exactly
	// that once parallel tests started drawing prompts.
	chosenMu    sync.RWMutex
	chosenTheme *theme.Theme
)

// setChosenTheme records the resolved theme.
func setChosenTheme(t *theme.Theme) {
	chosenMu.Lock()
	defer chosenMu.Unlock()
	chosenTheme = t
}

// currentTheme returns the resolved theme, or nil when nothing has
// named one yet.
func currentTheme() *theme.Theme {
	chosenMu.RLock()
	defer chosenMu.RUnlock()
	return chosenTheme
}

// applyTheme resolves the theme, and takes ownership of complaining
// about a bad one.
//
// A theme that does not exist is NOT fatal — the defaults stand and the
// command works — so it is a warning. But clog and clib each print
// their own complaint straight to stderr, which bypasses the contract:
// the text lands in no envelope, no warnings[], and no agent ever sees
// it. So an unusable name is validated here, recorded as a warning, and
// REMOVED from the environment before the libraries read it, leaving
// exactly one report through the one channel that carries it.
//
// Runs before conductor.New, which is what sets the prefix and triggers
// the libraries' own env pass.
func applyTheme() {
	// Every candidate is taken away, the winner included, save for the
	// one exception keep names — decided in the loop below.
	//
	// The libraries re-read the environment when conductor sets the
	// prefix, and a losing CLOG_THEME=bogus beside a winning
	// JIRA_THEME=nord had clog complaining on stderr, twice, about a
	// variable that had already lost — outside the envelope, with
	// warnings[] empty.
	//
	// One case none of this reaches: clog reads its own CLOG_THEME at
	// package init, before main runs, so an unusable value THERE still
	// draws one line from clog directly. Nothing in a process can unset
	// a variable earlier than the init that read it.
	keep := ""
	defer func() {
		for _, envVar := range themeEnv {
			if envVar == keep {
				continue
			}
			// Nothing to recover to: a failed unset just leaves that
			// library to report the name itself, which is the state
			// this whole function exists to avoid.
			_ = os.Unsetenv(envVar) //nolint:errcheck // see above
		}
	}()

	for _, envVar := range themeEnv {
		name := strings.TrimSpace(os.Getenv(envVar))
		if name == "" {
			continue
		}

		used, knownToBoth := applyThemeName(name)
		switch {
		case !used:
			// Neither knows it. The names offered are clib's, because
			// its list is the longer one — it covers every theme that
			// would have worked.
			AddWarning(envelope.Warning{
				Type:    "theme_unknown",
				Message: unknownThemeMessage(envVar, name),
			})
		case knownToBoth && envVar == ourThemeEnv:
			// The exception, and it is narrow. JIRA_THEME is also the
			// environment layer of the `theme` config key, so unsetting
			// it made `config list` report the theme as coming from the
			// file JIRA_THEME was overriding — the command
			// contradicting the behavior.
			//
			// Only when BOTH libraries know the name, though: they
			// re-read the survivor, and re-reading a name one of them
			// does not know is a complaint on stderr. `nord` is clib's
			// and not clog's, and keeping it for provenance had clog
			// reporting it as unknown — the leak this function exists
			// to prevent, traded for a nicety. Provenance loses, and a
			// clib-only theme reads as coming from the file or the
			// default.
			keep = envVar
		}
		return
	}
}

// applyThemeName hands a theme name to BOTH libraries, reporting
// whether either one knew it.
//
// Both, every time, from every source. clib themes the help renderer
// and the prompts; clog themes everything a command actually says. A
// theme set in the config file used to reach only clib, so `config
// list` reported it faithfully while the output it described stayed the
// default — the setting appeared to do nothing.
//
// Usable if EITHER accepts it: the two libraries carry different theme
// sets, and clib knows nord, gruvbox-* and tokyo-night where clog does
// not. Checking one alone called a working theme unknown while the
// other was visibly applying it.
func applyThemeName(name string) (used, knownToBoth bool) {
	var clogT clogtheme.Theme
	clogKnows := clogT.UnmarshalText([]byte(name)) == nil
	if clogKnows {
		clog.SetTheme(clogtheme.Single(&clogT))
	}
	var clibT theme.Theme
	clibKnows := clibT.UnmarshalText([]byte(name)) == nil
	if clibKnows {
		setChosenTheme(&clibT)
	}
	return clogKnows || clibKnows, clogKnows && clibKnows
}

// unknownThemeMessage says a theme name is not usable, and — when it is
// close to one that is — which. source names where the name came from,
// and leads the message, because "JIRA_THEME" and "config theme" want
// different fixes.
//
// A near miss is worth far more than the full list: "monokaias" is one
// edit from "monokai", and saying so answers the question, where twenty
// names leaves the reader to diff them by eye. The list is the fallback
// for a name close to nothing.
//
// SuggestValues rather than Suggest: theme names are a CLOSED set, so
// the minimum-length floor that keeps an open pool of identifiers quiet
// would only suppress real answers here.
func unknownThemeMessage(source, value string) string {
	if near := SuggestValues(value, theme.Names()); len(near) > 0 {
		return fmt.Sprintf("%s: unknown theme %q — did you mean %s? Using the default theme.",
			source, value, humanList(near))
	}
	return fmt.Sprintf("%s: unknown theme %q — using the default theme. Valid: %s.",
		source, value, strings.Join(theme.Names(), ", "))
}

// humanList joins names the way a sentence would: "a", "a or b".
func humanList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

// HelpTheme returns the theme conductor should use. NEVER nil.
//
// nil is what makes conductor call theme.Auto(), which asks the
// terminal for its background over OSC 11 and waits — a terminal that
// never answers costs termenv's whole timeout, measured at 5108ms on
// every command, `version` included.
//
// This closes ONE of the two routes to that probe. clog takes the other
// and this cannot reach it: the first log line written to a terminal
// calls clog's own background detection, so any command that reports
// something still pays the five seconds. `version` and `config list`
// measure 21ms because they never log; `auth status` measures 5027ms.
//
// TODO: drop this note once clog no longer probes for a theme it has
// already been given — raised upstream. Setting the theme does not
// spare it today: SetTheme marks the print theme dirty, so even
// theme.Single, where light and dark are the SAME theme and the
// background cannot change the outcome, still queries. There is no
// local fix — termenv's timeout is a const, and the only lever short of
// disabling color is handing clog a non-file writer, which costs
// terminal width with it.
func HelpTheme() *theme.Theme {
	// Compose even before output/color flags are parsed. The renderer snapshots
	// this theme, while its writer decides whether to emit colors later. A
	// startup ColorsDisabled check discarded selected palettes when a later
	// --color=always enabled styling, including help redirected through a pipe.
	// Resolution also carries invalid-theme warnings when color is disabled.
	return ResolvedTheme()
}

// BackgroundIsDark reports whether th should be drawn for a dark
// background — the ONE place this question is asked, in any package.
// It was previously spelled out at six call sites, four as
// `== BackgroundDark` and two as the INVERSE `== BackgroundLight`; since
// clib's enum carries a third value (BackgroundUnspecified at iota 0),
// those two spellings disagree on a theme that never set one, and
// nothing made them agree.
//
// "Not light" rather than "is dark", so unifying them keeps the answer
// the inverse sites already gave: they defaulted to dark and stepped
// aside only for an explicitly light theme. That is also what
// ResolvedTheme below does when every source is silent, so every caller
// now shares the CLI's one stated default instead of splitting on an
// unset value.
//
// Lives here rather than beside the color sinks because it is a
// BACKGROUND-policy question, not a color one: sinks.go decides which
// hue, this file decides which background that hue is chosen for.
// Exported because internal/cli/prompt asks it too, for a dialog's base
// theme — a surface with no color table of its own.
//
// Unreachable today — every clib constructor sets Background explicitly
// and ResolvedTheme's last resort is theme.Dark() — so this closes a
// latent disagreement, not a live bug.
func BackgroundIsDark(th *theme.Theme) bool {
	return th.Background != theme.BackgroundLight
}

// ResolvedTheme answers which theme to use without asking the terminal.
func ResolvedTheme() *theme.Theme {
	if t := currentTheme(); t != nil {
		return t
	}
	if t := themeFromConfig(); t != nil {
		return t
	}
	return BootstrapTheme()
}

// BootstrapTheme supplies a nonnil theme without reading settings. Startup
// uses it to assemble the command grammar before aliases select the final
// config file. ConfigureTheme must run first to establish environment priority.
func BootstrapTheme() *theme.Theme {
	if t := currentTheme(); t != nil {
		return t
	}
	if t := themeFromColorFGBG(); t != nil {
		return t
	}
	// The last resort is a choice rather than a question. Dark, because
	// that is what a terminal is unless it says otherwise, and because
	// guessing wrong costs colors tuned for the wrong background —
	// visible, and fixed once with JIRA_THEME. Asking costs five
	// seconds of nothing, every time, with no clue why.
	return darkFallback()
}

// themeFromConfig reads the preset and custom palette selected in the config
// file, returning nil when neither supplies a usable clib theme.
//
// Below the environment, above the terminal's own hints — the same
// order every other setting resolves in.
//
// Read once per process: ResolvedTheme is called by every dialog as
// well as at startup, and re-reading would re-warn about the same bad
// name once per prompt.
//
// Credentials is deliberately nil: that skips the keyring layer
// entirely. The settings theme is resolved before output, and
// opening an OS credential store to pick a color would be slow on every
// command and, on macOS, could prompt for a password to answer a
// question nobody asked.
//
// Settings-load failures here are silent. A malformed config file is a real error,
// but it belongs to the command that loads config properly and can
// report it in the envelope — swallowing the theme is the correct
// response at this point, not a second complaint about a file the next
// few lines of output are about to complain about anyway.
var themeFromConfig = sync.OnceValue(readThemeFromConfig)

// darkFallback and lightFallback are the built-in themes every fallback
// below hands back, built ONCE.
//
// clib's theme.Dark()/theme.Light() are constructors, not accessors:
// each call allocates a fresh Theme of ~35 lipgloss.Style values plus
// its TimeAgoThresholds and EntityColors slices — measured at 8.4us and
// 25KB per call. Every fallback here used to call one directly, so a
// caller that never named a theme rebuilt an entire theme per call. That
// is the DEFAULT path (config.go defaults `theme` to ""), and it is
// reached from the value sinks: three times per row of an issue list,
// and once per inline ADF span in a description or comment. At `issue
// list --all`'s own row cap that was 384MB of allocation for one table.
//
// Memoized HERE, at the terminal constructors, rather than around
// ResolvedTheme as a whole: applyThemeName can call setChosenTheme
// later, from inside readThemeFromConfig, and theme_test.go's resetTheme
// reassigns themeFromConfig — so the ordered lookup has to keep running
// on every call. Only the object handed back at the very end is shared,
// which leaves resolution order and timing exactly as they were.
//
// sync.OnceValue rather than a package-init var so construction still
// happens lazily, after applyThemeName has had its chance to run.
//
// Sharing one *theme.Theme across callers is already the status quo, not
// something this introduces: the CONFIGURED path hands every caller the
// same pointer through chosenTheme. Every consumer only reads or
// nil-checks fields; nothing outside tests assigns to one.
var (
	darkFallback  = sync.OnceValue(theme.Dark)
	lightFallback = sync.OnceValue(theme.Light)
)

// readThemeFromConfig is themeFromConfig's one execution — see there.
func readThemeFromConfig() *theme.Theme {
	resolved, err := config.Load(config.Options{})
	if err != nil {
		return nil
	}
	name := strings.TrimSpace(resolved.Config.Theme)
	if name != "" {
		if used, _ := applyThemeName(name); !used {
			AddWarning(envelope.Warning{
				Type:    "theme_unknown",
				Message: unknownThemeMessage("config theme", name),
			})
		}
	}
	customPath := resolved.Config.ThemePath
	if customPath == "" {
		return currentTheme()
	}
	if !filepath.IsAbs(customPath) {
		customPath = filepath.Join(filepath.Dir(resolved.Path), customPath)
	}
	definition, err := config.LoadThemeFile(context.Background(), customPath)
	if err == nil {
		var selected *theme.Theme
		var syntax *clogtheme.Theme
		selected, syntax, err = customTheme(definition)
		if err == nil {
			setChosenTheme(selected)
			clog.SetTheme(clogtheme.Single(syntax))
			return selected
		}
	}
	AddWarning(envelope.Warning{Type: "theme_invalid", Message: "custom theme could not be loaded; retaining the preset fallback: " + err.Error()})
	// Whatever applyThemeName just set — which is nil when only clog
	// knew the name. clog is themed either way, and the clib half falls
	// through to the terminal's own hints.
	return currentTheme()
}

// themeFromColorFGBG reads the terminal's background from COLORFGBG,
// returning nil when it is unset or unreadable.
//
// The older convention for the same fact the OSC 11 query asks for, and
// several terminals that do NOT answer that query do set this. Reading
// it costs nothing, so it is tried before falling back to a guess.
//
// The value is "<fg>;<bg>" and the background is the LAST field.
// Indices 7 and 15 are light; everything else reads as dark.
func themeFromColorFGBG() *theme.Theme {
	raw := strings.TrimSpace(os.Getenv("COLORFGBG"))
	if raw == "" {
		return nil
	}
	fields := strings.Split(raw, ";")
	bg, err := strconv.Atoi(strings.TrimSpace(fields[len(fields)-1]))
	if err != nil {
		return nil
	}
	if bg == 7 || bg == 15 {
		return lightFallback()
	}
	return darkFallback()
}
