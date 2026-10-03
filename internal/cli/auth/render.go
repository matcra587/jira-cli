package auth

import (
	"fmt"
	"net/url"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/output"
)

// The account block `auth login` and `auth status` both print. Shared
// because they answer the same question — who am I, and where — and a
// second layout would let one of them drift into reading like a
// different tool.

// tick marks the affirmative line. A plain rune, not a styled one: this
// writes to stdout, where the envelope lives, and color there would
// land in whatever captured the output.
const tick = "\u2713"

// writeIdentity prints the account block's header: the host, then a
// status line, then returns the block's own detail field (Account ID) for
// the caller to combine with whatever it has to say and render as ONE
// aligned output.Fields block.
//
// The shape follows `gh auth status` deliberately — a host heading, a
// tick, and detail rows — because it is the layout people already read
// for exactly this question, and matching it costs nothing.
//
// A caller with more to say appends its own fields to the returned slice
// before calling v.Fields; see renderStatusHuman, which appends the
// token, and renderLoginHuman, which appends what was replaced. Account
// ID rides through even when empty — v.Fields skips it — so this stays
// one unconditional field rather than a second place that re-decides
// emptiness.
func writeIdentity(v *output.View, headline, displayName, email, accountID, site string) []output.Field {
	host := site
	if u, err := url.Parse(site); err == nil && u.Host != "" {
		host = u.Host
	}

	// cli.Text is the H4 sanitization sink: both email and displayName
	// come from Jira, and an escape sequence or stray control byte in
	// either would be interpreted by the terminal rather than shown —
	// able to rewrite the line, hide text, or forge the rest of the
	// output. email is sanitized unconditionally, not only inside the
	// displayName branch below, since account stays exactly this value
	// when displayName is empty.
	account := cli.Text(email)
	if displayName != "" {
		account = fmt.Sprintf("%s (%s)", cli.Text(email), cli.Text(displayName))
	}
	v.Line(host)
	v.Line(fmt.Sprintf("  %s %s account %s", tick, headline, account))

	// The value JQL and the REST API actually take
	// (`assignee = <accountId>`). Worth a row precisely because it is not
	// guessable from anything else on screen.
	return []output.Field{{Label: "Account ID", Value: accountID}}
}

// tokenMask is the fixed-width stand-in for a token's hidden part. Fixed
// on purpose: a mask matching the real length would leak it, and a
// token's length identifies which kind of token it is.
const tokenMask = "****************"

// tokenTailLen is how much of the token stays visible — enough to tell
// two tokens apart when checking which one is stored, and no more. Four
// is the convention card numbers and cloud keys settled on for the same
// reason.
const tokenTailLen = 4

// maskToken hides all but the last few characters.
//
// A token shorter than twice the tail is masked completely: revealing
// half of a short secret to help identify it is a bad trade, and a token
// that short is not a real one anyway.
func maskToken(token string) string {
	if len(token) < tokenTailLen*2 {
		return tokenMask
	}
	return tokenMask + token[len(token)-tokenTailLen:]
}
