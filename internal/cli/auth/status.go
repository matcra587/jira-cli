package auth

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// newStatus returns `jira auth status`.
func newStatus(deps Deps) *cobra.Command {
	var showToken bool

	return command.MustNew(command.Spec[envelope.AuthStatusOutput]{
		Op:    "auth.status",
		Use:   "status",
		Short: "Report who you are authenticated as",
		Long: "Report the account `jira` is authenticated as, by asking Jira.\n\n" +
			"The credential is TESTED, not merely looked up. A stored token " +
			"that has been revoked is indistinguishable from a working one " +
			"until something calls the API, and reporting \"logged in\" for a " +
			"dead token is worse than reporting nothing.\n\n" +
			"An unreadable selected credential store without a usable environment token fails " +
			"with exit 1 and auth_store_unavailable. That failure does not establish " +
			"whether stored credentials exist; restore store access before replacing them.\n\n" +
			"A successful HTTP reply must contain one complete account object " +
			"with an account ID and exact native member names. Duplicate members " +
			"or case variants refuse before verification. A malformed reply fails with exit 5 and " +
			"current_user_response_invalid; it does not confirm authentication.\n\n" +
			"Scoped tokens use their bound cloud ID through the fixed Atlassian " +
			"gateway. The reported site remains the browser identity; cloud_id " +
			"is present only for a verified scoped account.\n\n" +
			"Not being authenticated is a failure here — exit 1 — in every " +
			"output mode. Some CLIs return success in machine mode regardless, " +
			"which leaves the caller least able to parse prose relying on the " +
			"least reliable signal.\n\n" +
			"The token is masked unless `--show-token` is passed. The mask is " +
			"a fixed width, so it does not leak the token's length either.",
		Flags: func(c *cobra.Command) {
			command.AddBoolVar(c, &showToken, "show-token", false, "Show the API token instead of a mask",
				command.FlagOpts{Terse: "Reveal token", Shorthand: "t"})
		},
		Human: renderStatusHuman,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.AuthStatusOutput, error) {
			return status(ctx, deps, showToken)
		},
	})
}

// status reports the account Jira says these credentials belong to.
func status(ctx context.Context, deps Deps, showToken bool) (envelope.AuthStatusOutput, error) {
	var zero envelope.AuthStatusOutput

	site, user, err := currentIdentity(ctx, deps)
	if err != nil {
		return zero, err
	}

	shown := ""
	if showToken {
		shown = site.Token
	}
	return envelope.AuthStatusOutput{
		Token:       shown,
		TokenTail:   site.Token,
		BaseURL:     site.BaseURL,
		CloudID:     site.CloudID,
		Email:       site.Email,
		AccountID:   user.User.AccountID.Value,
		DisplayName: user.User.DisplayName.Value,
		TokenSource: site.TokenSource,
	}, nil
}

// renderStatusHuman prints the answer to "who am I, where, and why that
// credential" in as few lines as carry those three facts.
//
// The token source is the line people do not know they need: an
// environment variable quietly beating the keyring is the single most
// confusing state this command exists to expose.
func renderStatusHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.AuthStatusOutput)
	if !ok {
		return fmt.Errorf("auth.status: unexpected payload %T", env.Data)
	}
	fields := writeIdentity(v, "Logged in to", out.DisplayName, out.Email, out.AccountID, out.BaseURL)
	fields = append(fields, output.Field{Label: "Cloud ID", Value: out.CloudID})
	if out.TokenSource == "" {
		// No layer claimed the token, so there is nothing truthful to say
		// about where it came from — and the row exists to say that.
		v.Fields(fields...)
		return nil
	}

	// Token itself only when --show-token was passed; otherwise the mask.
	shown := maskToken(out.TokenTail)
	if out.Token != "" {
		shown = out.Token
	}
	// The source is named only when it is NOT the keyring. Storage is
	// where a token is meant to live, so saying so every time is noise;
	// an environment variable or a config file quietly winning is the
	// surprising state, and that is worth a word.
	if out.TokenSource != string(envelope.TokenSourceKeyring) {
		shown += " (from " + out.TokenSource + ")"
	}
	fields = append(fields, output.Field{Label: "Token", Value: shown})
	v.Fields(fields...)
	return nil
}
