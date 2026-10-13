package config

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gechr/x/ansi"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// newList returns `jira config list`.
func newList(deps Deps) *cobra.Command {
	return command.MustNew(command.Spec[envelope.ConfigListOutput]{
		Op:    "config.list",
		Use:   "list",
		Short: "Show every setting and where its value came from",
		Long: "Show the EFFECTIVE value of every setting, and the layer that " +
			"supplied it.\n\n" +
			"This is deliberately not a dump of the config file. A value can " +
			"come from a `JIRA_*` environment variable, from the file, or from " +
			"a built-in default, and printing the file alone would contradict " +
			"the command that just ran. The source column is the answer to " +
			"*why* a setting is what it is.\n\n" +
			"Secrets are never printed. The config file path is shown so there " +
			"is no separate command for finding it. If the credential store cannot " +
			"be read, other layers remain visible and a warning identifies the unavailable layer. " +
			"Cancellation and timeout stop the read instead of falling back to another credential layer.",
		Example: "# every setting, with its source\n" +
			"$ jira config list\n\n" +
			"# just the effective values, for a script\n" +
			"$ jira config list --output=json --jq '.data.settings[] | \"\\(.key)=\\(.value)\"'\n\n" +
			"# which settings the environment is overriding\n" +
			"$ jira config list --output=json --jq '.data.settings[] | select(.source == \"env\") | .key'",
		Human: renderHuman,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.ConfigListOutput, error) {
			// Credentials wired in deliberately: this command publishes a
			// "keyring" layer, and a settings view blind to a layer it
			// names would report base_url unset while every other
			// command uses the stored one. The token stays hidden —
			// redaction is applied per key, not per layer.
			resolved, err := resolveList(ctx, deps)
			if err != nil {
				if errors.Is(err, cfg.ErrFileInvalid) {
					return envelope.ConfigListOutput{}, localIO("read config", err)
				}
				return envelope.ConfigListOutput{}, err
			}
			if resolved.CredentialError != nil {
				cli.AddWarning(envelope.Warning{
					Type:    "credential_store_unavailable",
					Message: "stored credentials are excluded because the credential store could not be read",
				})
			}
			return buildOutput(resolved), nil
		},
	})
}

func resolveList(ctx context.Context, deps Deps) (*cfg.Resolved, error) {
	if deps.Resolve != nil {
		return deps.Resolve(ctx)
	}
	store, err := deps.Credentials()
	if err != nil {
		return nil, err
	}
	return cfg.Resolve(ctx, cfg.Options{}, store)
}

// buildOutput turns a resolved configuration into the payload.
//
// Values render through the config package's redaction, so a secret
// shows a marker and never its content — including the case where a
// base URL happens to carry userinfo.
func buildOutput(r *cfg.Resolved) envelope.ConfigListOutput {
	values := r.Config.Values()
	settings := make([]envelope.ConfigSetting, 0, len(cfg.Keys()))
	for _, key := range cfg.Keys() {
		raw := values[key]
		rendered := cfg.Redacted(key, raw)
		settings = append(settings, envelope.ConfigSetting{
			Key:    key,
			Value:  rendered,
			Source: string(r.Source(key)),
			// Secret means "what you see is not what is configured", so
			// it is derived from whether redaction actually changed the
			// value — not from which key it is. A base_url carrying
			// userinfo is rewritten too, and keying off the secret-key
			// list would report that mangled URL as the literal value.
			Secret: rendered != raw,
		})
	}
	return envelope.ConfigListOutput{
		Profile: r.Profile, DefaultProfile: r.DefaultProfile,
		Path:       r.Path,
		FileExists: r.FileExists,
		Settings:   settings,
	}
}

// renderHuman prints one aligned row per setting.
//
// The source column is not decoration: it is the column people are
// reading for. There is no Table yet (design doc: primer/table lands
// with the first list command), so this is the one place a renderer
// still does its own width math — v.Line just writes the cells this
// builds, rather than owning column alignment itself.
func renderHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ConfigListOutput)
	if !ok {
		return fmt.Errorf("config.list: unexpected payload %T", env.Data)
	}

	// Display width, not byte length. `len` counts bytes, so an account
	// with an accented character or a site in a non-Latin script pushed
	// the source column out of line — "José@example.com" is 17 bytes and
	// 16 cells, and a CJK site is 18 bytes and 12 cells. Jira content is
	// user-authored text, so this is the normal case, not an edge one.
	keyWidth, valWidth := 0, 0
	for _, s := range out.Settings {
		keyWidth = max(keyWidth, ansi.StringWidth(s.Key))
		valWidth = max(valWidth, ansi.StringWidth(shownValue(s.Value)))
	}
	for _, s := range out.Settings {
		v.Line(pad(s.Key, keyWidth), pad(shownValue(s.Value), valWidth), s.Source)
	}
	// The path last, and always — it is the answer to "where do I change
	// this?", and it is worth stating even when the file is absent,
	// because that is the path to create.
	suffix := ""
	if !out.FileExists {
		suffix = " (not created yet)"
	}
	v.Line()
	v.Line("config file: " + out.Path + suffix)
	if out.Profile != "" {
		v.Line("selected profile: " + cli.Text(out.Profile))
	}
	return nil
}

// pad right-fills s to width display CELLS.
//
// Not `%-*s`: that pads to a byte count, which is the bug this exists to
// avoid. Anything wider than the column is left alone rather than
// truncated — a value the reader needs is worth a ragged column.
func pad(s string, width int) string {
	if gap := width - ansi.StringWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// shownValue is the human rendering of a setting's value: "-" when there
// is none.
//
// The empty cell was deliberate once — the argument being that blank
// reads differently from a short value — but in a column of real values
// a gap reads as a rendering fault, not as information. The dash says
// "nothing here" out loud.
//
// Human format only. The envelope keeps the empty string, because a
// machine reading "-" as a value would be wrong.
func shownValue(v string) string {
	if v == "" {
		return "-"
	}
	return cli.Text(v)
}
