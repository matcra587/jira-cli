package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/gechr/clib/theme"
	"github.com/gechr/clog"
	clogtheme "github.com/gechr/clog/theme"
)

// clearEnv removes variables for the test's duration.
//
// t.Setenv(name, "") is NOT the same thing: NO_COLOR counts as set
// whatever its value, so blanking it leaves colors disabled — which
// silently made an earlier version of a case below pass for the wrong
// reason.
func clearEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if old, ok := os.LookupEnv(name); ok {
			t.Cleanup(func() {
				if err := os.Setenv(name, old); err != nil {
					t.Errorf("restore %s: %v", name, err)
				}
			})
		}
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

// resetTheme clears what applyTheme records, so each case starts from an
// unconfigured process.
func resetTheme(t *testing.T) {
	t.Helper()
	setChosenTheme(nil)

	// The config layer is stubbed OUT, not just reset. themeFromConfig
	// reads the real XDG config path, so without this a developer with
	// `theme = ...` in their own config file changes what these tests
	// resolve — and sync.OnceValue caches the first read for the whole
	// package run, so the leak would be order-dependent too. What the
	// file layer does is proven end-to-end against the built binary,
	// not here.
	previous := themeFromConfig
	themeFromConfig = func() *theme.Theme { return nil }

	// Startup warnings are a package-level slice, so a test that raises
	// one and does not read it leaves it for whoever runs next — which
	// made a warning-count assertion here pass alone and fail in the
	// full package run. Drained on the way in AND out.
	TakeWarnings()

	t.Cleanup(func() {
		setChosenTheme(nil)
		themeFromConfig = previous
		TakeWarnings()
	})
}

func themeEnvNames() []string {
	return append([]string{"COLORFGBG", "NO_COLOR"}, themeEnv...)
}

func TestAutomaticThemeUsesHintsAndPreservesEnvironmentSelection(t *testing.T) {
	for _, tc := range []struct {
		name, hint, want string
	}{
		{"light hint", "0;15", "light"},
		{"dark hint", "15;0", "dark"},
		{"missing hint", "", "dark"},
		{"invalid hint", "invalid", "dark"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t, themeEnvNames()...)
			resetTheme(t)
			t.Setenv("JIRA_THEME", " AUTO ")
			t.Setenv("COLORFGBG", tc.hint)
			t.Setenv("CLIB_THEME", "monokai")
			applyTheme()
			if got := ResolvedTheme(); got == nil || got.String() != tc.want {
				t.Fatal("automatic selection ignored its hint or used a losing preset")
			}
			if os.Getenv("JIRA_THEME") != " AUTO " || len(TakeWarnings()) != 0 {
				t.Fatal("automatic selection lost authored environment provenance or warned")
			}
		})
	}
}

func TestLibraryThemeEnvironmentRestoresAutomaticSelectionAfterPanic(t *testing.T) {
	t.Setenv("COLORFGBG", "")
	t.Setenv("JIRA_THEME", " AUTO ")
	t.Setenv("JIRA_EMAIL", "public@example.invalid")
	func() {
		defer func() {
			if recover() != "synthetic startup failure" {
				t.Fatal("startup failure was swallowed")
			}
		}()
		WithLibraryThemeEnvironment(func() {
			if os.Getenv("JIRA_THEME") != "dark" || os.Getenv("JIRA_EMAIL") != "public@example.invalid" {
				t.Fatal("library pass saw the application selector or altered another setting")
			}
			panic("synthetic startup failure")
		})
	}()
	if os.Getenv("JIRA_THEME") != " AUTO " {
		t.Fatal("library pass lost exact authored environment bytes")
	}
	t.Setenv("JIRA_THEME", "dark")
	WithLibraryThemeEnvironment(func() {
		if os.Getenv("JIRA_THEME") != "dark" {
			t.Fatal("concrete preset was hidden from the libraries")
		}
	})
}

func TestLibraryThemeEnvironmentKeepsAutomaticWinnerAbovePairs(t *testing.T) {
	for _, pair := range []string{"dracula", "invalid-losing-pair"} {
		t.Run(pair, func(t *testing.T) {
			t.Setenv("JIRA_THEME", "auto")
			t.Setenv("COLORFGBG", "0;15")
			t.Setenv("JIRA_THEME_LIGHT", pair)
			t.Setenv("JIRA_THEME_DARK", pair)
			t.Setenv("CLOG_THEME_LIGHT", "light")
			t.Setenv("CLOG_THEME_DARK", "dark")
			t.Cleanup(func() { clog.SetEnvPrefix("") })
			WithLibraryThemeEnvironment(func() {
				clog.SetEnvPrefix("JIRA")
				selected, err := clogtheme.FromEnv()
				if err != nil || selected == nil {
					t.Fatal("automatic winner exposed a losing theme pair", err)
				}
				for _, background := range []clogtheme.Background{clogtheme.BackgroundLight, clogtheme.BackgroundDark} {
					if selected.ForBackground(background).Name() != "light" {
						t.Fatal("syntax background differs from the passive help selection")
					}
				}
			})
			if os.Getenv("JIRA_THEME") != "auto" {
				t.Fatal("theme pair guard lost automatic provenance")
			}
		})
	}
}

func TestLibraryThemeEnvironmentSerializesConcurrentConstructors(t *testing.T) {
	t.Setenv("JIRA_THEME", "auto")
	t.Setenv("COLORFGBG", "0;15")
	start := make(chan struct{})
	var constructors sync.WaitGroup
	for range 100 {
		constructors.Go(func() {
			<-start
			WithLibraryThemeEnvironment(func() {
				if os.Getenv("JIRA_THEME") != "light" {
					t.Error("concurrent library pass lost the concrete winner on entry")
				}
				runtime.Gosched()
				if os.Getenv("JIRA_THEME") != "light" {
					t.Error("another constructor restored auto during the library pass")
				}
			})
		})
	}
	close(start)
	constructors.Wait()
	if os.Getenv("JIRA_THEME") != "auto" {
		t.Fatal("concurrent constructors lost exact authored provenance")
	}
}

// TestThemeResolutionAvoidsTheTerminalProbe pins a startup cost that is
// invisible until you meet a terminal that stays quiet.
//
// conductor calls theme.Auto() when App.Theme is nil, which asks the
// terminal for its background over OSC 11 and waits. A terminal that
// never answers costs termenv's whole timeout — measured at 5108ms per
// command, `version` included, against 104ms when the theme is already
// known. resolvedTheme must therefore never return nil.
func TestThemeResolutionAvoidsTheTerminalProbe(t *testing.T) {
	t.Run("never nil, so conductor never probes", func(t *testing.T) {
		clearEnv(t, themeEnvNames()...)
		resetTheme(t)

		got := ResolvedTheme()
		if got == nil {
			t.Fatal("nil with nothing configured; every command would pay the terminal probe")
		}
		// And it is the LAST-RESORT theme specifically. Asserting only
		// non-nil cannot tell the fallback apart from a theme that
		// leaked in from the developer's own config file — which is
		// what resetTheme stubs out, and this is what proves the stub
		// is doing something.
		if got.String() != theme.Dark().String() {
			t.Errorf("fallback resolved to %s, want dark — something outside the test supplied a theme", got.String())
		}
	})

	t.Run("COLORFGBG answers without asking", func(t *testing.T) {
		clearEnv(t, themeEnvNames()...)
		resetTheme(t)
		t.Setenv("COLORFGBG", "0;15")

		got := themeFromColorFGBG()
		if got == nil {
			t.Fatal("COLORFGBG was not read; a terminal that sets it would still be probed")
		}
		// The background is the LAST field, and 15 is light.
		if got.String() != theme.Light().String() {
			t.Errorf("COLORFGBG=0;15 read as %s, want light", got.String())
		}
	})
}

// TestThemeNamesAreAcceptedIfEitherLibraryKnowsThem pins the rule that
// stopped a working theme being called unknown.
//
// Two libraries carry theme sets and they are not the same size: clib
// knows nord, gruvbox-* and tokyo-night; clog does not. Validating
// against clog alone warned "unknown theme nord" while clib was busy
// applying it — the CLI contradicting itself in one line of output.
func TestThemeNamesAreAcceptedIfEitherLibraryKnowsThem(t *testing.T) {
	for _, name := range []string{"nord", "gruvbox-dark", "tokyo-night", "monokai", "dracula"} {
		t.Run(name, func(t *testing.T) {
			clearEnv(t, themeEnvNames()...)
			resetTheme(t)
			t.Setenv("JIRA_THEME", name)

			applyTheme()

			if currentTheme() == nil {
				t.Errorf("%q was not accepted, though clib knows it", name)
			}
		})
	}
}

// TestAnUnusableThemeNameIsReportedOnce pins that a name neither library
// knows is a WARNING carried by the envelope — not a library writing
// straight to stderr, where it reaches no warnings[], no JSON, and no
// agent.
func TestAnUnusableThemeNameIsReportedOnce(t *testing.T) {
	clearEnv(t, themeEnvNames()...)
	resetTheme(t)
	t.Setenv("JIRA_THEME", "no-such-theme")

	applyTheme()

	if currentTheme() != nil {
		t.Error("an unusable name was accepted as a theme")
	}
	// And the variable is taken away, so neither library re-reads it and
	// prints its own complaint outside the envelope.
	if _, stillSet := os.LookupEnv("JIRA_THEME"); stillSet {
		t.Error("JIRA_THEME survived; the libraries re-read it and report it again themselves")
	}
}

// TestUnknownThemeSuggestsTheNearOne pins that a typo gets an answer
// rather than the whole list.
//
// Twenty theme names leaves the reader to diff them by eye; "did you
// mean monokai?" is the answer to the question actually being asked.
// The full list stays as the fallback for a name close to nothing,
// where there is no better reply.
func TestUnknownThemeSuggestsTheNearOne(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ typo, want string }{
		{typo: "monokaias", want: "monokai"},
		{typo: "drakula", want: "dracula"},
		{typo: "nordd", want: "nord"},
		{typo: "catpuccin-mocha", want: "catppuccin-mocha"},
	} {
		t.Run(tt.typo, func(t *testing.T) {
			t.Parallel()
			got := unknownThemeMessage("JIRA_THEME", tt.typo)
			if !strings.Contains(got, "did you mean") {
				t.Errorf("no suggestion for a one-edit typo: %s", got)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("message does not name %q: %s", tt.want, got)
			}
			// The full list would drown the suggestion.
			if strings.Contains(got, "Valid:") {
				t.Errorf("the full list was printed alongside a suggestion: %s", got)
			}
		})
	}
}

// TestUnknownThemeFallsBackToTheList pins the other half: a name close
// to nothing has no better reply than the names that would work.
func TestUnknownThemeFallsBackToTheList(t *testing.T) {
	t.Parallel()
	got := unknownThemeMessage("JIRA_THEME", "zzzzzzzz")

	if strings.Contains(got, "did you mean") {
		t.Errorf("suggested something for a name close to nothing: %s", got)
	}
	if !strings.Contains(got, "monokai") || !strings.Contains(got, "nord") {
		t.Errorf("fallback does not list the valid themes: %s", got)
	}
}

// TestLosingThemeVariablesAreCleared pins that resolution takes away
// every variable that did not win.
//
// Precedence says the first variable with a value decides, so the rest
// are out of play — but the libraries re-read the environment when the
// prefix is set, and a leftover CLOG_THEME=bogus beside a winning
// JIRA_THEME=nord had clog complaining on stderr about a variable that
// had already lost, outside the envelope, while warnings[] stayed
// empty.
//
// A usable JIRA_THEME is the ONE exception and is asserted below: it is
// the environment layer of the `theme` config key, so unsetting it made
// `config list` report the theme as coming from the file it was
// overriding.
func TestLosingThemeVariablesAreCleared(t *testing.T) {
	clearEnv(t, themeEnvNames()...)
	resetTheme(t)
	// A name BOTH libraries know: that is what survival requires.
	t.Setenv("JIRA_THEME", "catppuccin-mocha")
	t.Setenv("CLOG_THEME", "bogus")
	t.Setenv("THEME", "also-bogus")

	applyTheme()

	if currentTheme() == nil {
		t.Error("JIRA_THEME=catppuccin-mocha was not applied")
	}
	if got, ok := os.LookupEnv("JIRA_THEME"); !ok || got != "catppuccin-mocha" {
		t.Errorf("a usable JIRA_THEME did not survive (%q, set=%v); config would report the wrong source", got, ok)
	}
	for _, name := range []string{"CLIB_THEME", "CLOG_THEME", "THEME"} {
		if value, stillSet := os.LookupEnv(name); stillSet {
			t.Errorf("%s survived as %q — the libraries re-read it and report it themselves", name, value)
		}
	}
	// A variable that lost is not warned about: it was never in play,
	// and saying so would be noise about a theme nothing would use.
	for _, w := range TakeWarnings() {
		if strings.Contains(w.Message, "bogus") {
			t.Errorf("warned about a variable that lost on precedence: %s", w.Message)
		}
	}
}

// TestAThemeOnlyOneLibraryKnowsIsCleared pins the trade this makes.
//
// `nord` is clib's and not clog's. Keeping it set for config provenance
// had clog reporting "unknown theme" on stderr — outside the envelope,
// about a theme that was working. Provenance loses: the variable goes,
// the theme still applies, and `config list` reports the layer below.
func TestAThemeOnlyOneLibraryKnowsIsCleared(t *testing.T) {
	clearEnv(t, themeEnvNames()...)
	resetTheme(t)
	t.Setenv("JIRA_THEME", "nord")

	applyTheme()

	if currentTheme() == nil {
		t.Error("nord was not applied; clib knows it")
	}
	if _, stillSet := os.LookupEnv("JIRA_THEME"); stillSet {
		t.Error("JIRA_THEME survived with a name clog does not know; clog reports it on stderr")
	}
}

// TestAnUnusableJiraThemeIsClearedToo pins the other half of that
// exception: the survival is for a name that WORKS. An unusable one is
// exactly what the libraries would complain about, so it still goes.
func TestAnUnusableJiraThemeIsClearedToo(t *testing.T) {
	clearEnv(t, themeEnvNames()...)
	resetTheme(t)
	t.Setenv("JIRA_THEME", "no-such-theme")

	applyTheme()

	if _, stillSet := os.LookupEnv("JIRA_THEME"); stillSet {
		t.Error("an unusable JIRA_THEME survived; the libraries report it themselves")
	}
}

// TestTheConfigFileThemeIsReadForReal exercises readThemeFromConfig
// itself.
//
// Every other test here stubs themeFromConfig out so a developer's own
// config file cannot change what they resolve — which leaves the real
// function, the whole point of the `theme` config key, untested. This
// one points XDG at a temp directory and calls it directly.
func TestTheConfigFileThemeIsReadForReal(t *testing.T) {
	write := func(t *testing.T, body string) {
		t.Helper()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "jira"), 0o750); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if err := os.WriteFile(filepath.Join(dir, "jira", "config.toml"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("XDG_CONFIG_HOME", dir)
	}

	t.Run("a name both libraries know is applied", func(t *testing.T) {
		clearEnv(t, themeEnvNames()...)
		resetTheme(t)
		write(t, "theme = \"catppuccin-mocha\"\n")

		got := readThemeFromConfig()
		if got == nil {
			t.Fatal("a valid theme in the config file was not applied")
		}
		if got.String() != "catppuccin-mocha" {
			t.Errorf("resolved %s, want catppuccin-mocha", got.String())
		}
	})

	t.Run("automatic fallback survives an unavailable custom palette", func(t *testing.T) {
		clearEnv(t, themeEnvNames()...)
		resetTheme(t)
		t.Setenv("COLORFGBG", "0;15")
		write(t, "theme='auto'\ntheme_path='missing.toml'\n")
		got := readThemeFromConfig()
		warnings := TakeWarnings()
		if got == nil || got.String() != "light" || len(warnings) != 1 || warnings[0].Type != "theme_invalid" {
			t.Fatal("custom palette failure lost automatic fallback or fabricated an unknown selector")
		}
	})

	t.Run("an unusable name warns and names its source", func(t *testing.T) {
		clearEnv(t, themeEnvNames()...)
		resetTheme(t)
		write(t, "theme = \"monokaias\"\n")

		if got := readThemeFromConfig(); got != nil {
			t.Errorf("an unusable name resolved to %s", got.String())
		}
		warnings := TakeWarnings()
		if len(warnings) != 1 {
			t.Fatalf("got %d warnings, want exactly 1", len(warnings))
		}
		// The source has to say it came from the FILE, or the reader
		// goes hunting through environment variables that are not set.
		if !strings.Contains(warnings[0].Message, "config theme") {
			t.Errorf("warning does not name the config file as the source: %s", warnings[0].Message)
		}
		if !strings.Contains(warnings[0].Message, "monokai") {
			t.Errorf("warning carries no suggestion: %s", warnings[0].Message)
		}
	})

	t.Run("no file, and no theme in it, both resolve to nothing", func(t *testing.T) {
		for _, body := range []string{"", "read_only = true\n"} {
			clearEnv(t, themeEnvNames()...)
			resetTheme(t)
			write(t, body)

			if got := readThemeFromConfig(); got != nil {
				t.Errorf("resolved %s with no theme configured", got.String())
			}
			if w := TakeWarnings(); len(w) != 0 {
				t.Errorf("warned with no theme configured: %v", w)
			}
		}
	})
}
