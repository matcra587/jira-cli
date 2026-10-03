package auth

import (
	"errors"
	"strings"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/config"
)

// errAborted reports that the person closed the form rather than
// completing it. Canceling is a choice, not a failure, so the caller
// turns this into a quiet exit instead of an error banner.
var errAborted = errors.New("canceled")

// askForLogin collects whatever is still missing, validating each field
// where it is typed.
//
// Per-field validation is the point. Asking three questions and only
// then reporting that the first answer was malformed makes the person
// start over; catching it in place lets them fix it and move on.
//
// prefill is what is known already — from storage or the command line.
// pinned is the subset given on the command line, which is not asked
// for at all.
func askForLogin(prefill, pinned identity) (identity, string, error) {
	got := prefill
	var token string

	var fields []huh.Field
	// A stored value is PREFILLED rather than skipped. Skipping it makes
	// the value unchangeable from the form — the one place someone would
	// look to change it — and turns "log in again" into "log out, then
	// log in". A value pinned on the command line is a different matter:
	// that is an instruction, so it is not re-asked.
	if pinned.site == "" {
		fields = append(fields, huh.NewInput().
			Title("Jira site").
			Placeholder("https://your-site.atlassian.net").
			Value(&got.site).
			Validate(func(s string) error { return config.ValidateBaseURL(strings.TrimSpace(s)) }))
	}
	if pinned.email == "" {
		fields = append(fields, huh.NewInput().
			Title("Email").
			Placeholder("you@example.com").
			Value(&got.email).
			Validate(notBlank("email")))
	}
	fields = append(fields, huh.NewInput().
		Title("API token").
		// Never echoed: a token on screen is a token in the scrollback,
		// and in whatever recorded the session.
		EchoMode(huh.EchoModePassword).
		Value(&token).
		Validate(notBlank("API token")))

	if err := huh.NewForm(huh.NewGroup(fields...)).Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return identity{}, "", errAborted
		}
		return identity{}, "", err
	}

	return identity{
		site:  strings.TrimSpace(got.site),
		email: strings.TrimSpace(got.email),
	}, strings.TrimSpace(token), nil
}

// confirmLogout asks before removing a stored credential, naming what
// would go.
//
// The account and site are in the question on purpose: "are you sure?"
// is not a question anyone can answer well, and the one thing worth
// checking before logging out is that it is the account you meant.
func confirmLogout(ask func(prompt.Confirm) (bool, error), account, site string) (bool, error) {
	ok, err := ask(prompt.Confirm{
		Question:    prompt.Question("Remove the stored token for ", account, " on ", site, "?"),
		Caveat:      "The token is not revoked — this only stops this machine using it.",
		Affirmative: "Remove",
		Negative:    "Keep",
	})
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, errAborted
		}
		return false, err
	}
	return ok, nil
}

// notBlank rejects an empty answer, naming the field so the message
// reads as guidance rather than a complaint.
func notBlank(what string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New(what + " is required")
		}
		return nil
	}
}
