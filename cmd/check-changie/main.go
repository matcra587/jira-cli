package main

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"time"

	"github.com/gechr/clog"
	xstrings "github.com/gechr/x/strings"
)

// A feat/fix subject (optionally scoped, optionally breaking) is the only kind
// user-facing enough to require a fragment.
var featFix = regexp.MustCompile(`^(feat|fix)(\(.+\))?!?:`)

// An explicit escape hatch for a feat/fix that genuinely isn't user-facing.
var skipTrailer = regexp.MustCompile(`(?i)^Changelog:[ \t]*skip$`)

// A staged changie fragment. git emits forward-slash paths on every platform.
var fragment = regexp.MustCompile(`^\.changes/unreleased/.+\.ya?ml$`)

// fail prints msg on the clog stderr path and exits with code.
func fail(code int, msg string) {
	clog.Error().Parts(clog.PartMessage).Msg(msg)
	os.Exit(code)
}

func main() {
	if len(os.Args) < 2 {
		fail(2, "usage: check-changie <commit-msg-file>")
	}

	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(2, "check-changie: "+err.Error())
	}

	// SplitLines yields trimmed, non-empty lines, so the subject is lines[0]
	// and the skip trailer matches without further trimming.
	lines := xstrings.SplitLines(string(raw))
	if len(lines) == 0 || !featFix.MatchString(lines[0]) {
		return
	}
	if slices.ContainsFunc(lines, skipTrailer.MatchString) {
		return
	}

	// Bound the subprocess so a stalled git (index lock, wedged filesystem)
	// can never hang the commit indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "diff", "--cached", "--name-only").Output()
	if err != nil {
		fail(2, "check-changie: git diff failed: "+err.Error())
	}
	if slices.ContainsFunc(xstrings.SplitLines(string(out)), fragment.MatchString) {
		return
	}

	fail(1, "This feat/fix needs a changelog fragment: run 'changie new' and stage it, "+
		"or add a 'Changelog: skip' trailer if it isn't user-facing.")
}
