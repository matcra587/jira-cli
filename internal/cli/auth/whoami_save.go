package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/matcra587/jira-cli/internal/cli"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

const identitySaveDescription = "With --save, verify Jira and save account_id with its independent public site, credential account and cloud binding. " +
	"The selected profile or flat settings are checked before lookup; conflicting authored base_url, email or cloud_id is refused. " +
	"Missing declarations may inherit the resolved credential scope. Native privacy-hidden email remains unchanged. " +
	"--save --dry-run performs verification and prepares the same candidate without creating files or directories. " +
	"Without --save, identity inspection remains read-only, including when --dry-run is present. " +
	"Unchanged settings retain exact bytes; changed settings preserve other declarations and aliases. " +
	"A missing target is created without replacing another file. Snapshot conflicts refuse publication; " +
	"settings.status=unknown requires inspecting the selected file before retrying. " +
	"Saved metadata serves only matching local previews and never authenticates a live operation."

func saveIdentity(ctx context.Context, deps Deps, dryRun bool) (envelope.AuthWhoamiSaveOutput, error) {
	var zero envelope.AuthWhoamiSaveOutput
	if deps.SiteForSave != nil {
		deps.Site = deps.SiteForSave
	}
	if deps.ClientForSave != nil {
		deps.ClientFor = deps.ClientForSave
	}
	site, err := identitySite(ctx, deps)
	if err != nil {
		return zero, err
	}
	if deps.IdentityTarget == nil {
		return zero, cli.NewCodedError(cli.CodeConfigInvalid, "verified identity saving has no configured settings target")
	}
	scope := core.AccountIdentityScope{BaseURL: site.BaseURL, Email: site.Email, CloudID: site.CloudID}
	target, err := deps.IdentityTarget(ctx, scope)
	if contextErr := ctx.Err(); contextErr != nil {
		return zero, contextErr
	}
	if err != nil {
		return zero, identitySettingsError(err)
	}
	user, err := identityAt(ctx, deps, site)
	if err != nil {
		return zero, err
	}
	out := envelope.AuthWhoamiSaveOutput{
		MutationState:    envelope.MutationState{DryRun: dryRun},
		AuthWhoamiOutput: envelope.AuthWhoamiOutput{BaseURL: site.BaseURL, User: *user.User},
		Scope:            scope,
		Settings:         core.AccountIdentityPublication{Status: "not_saved"},
	}
	update, err := target.Prepare(user.User.AccountID.Value)
	if contextErr := ctx.Err(); contextErr != nil {
		return out, contextErr
	}
	if err != nil {
		return out, identitySettingsError(err)
	}
	out.Settings = update.Receipt()
	if err := ctx.Err(); err != nil {
		out.Settings.Status = "not_saved"
		return out, err
	}
	if !dryRun {
		out.Settings, err = update.Save(ctx)
		if err != nil {
			return out, identitySettingsError(err)
		}
	}
	return out, nil
}

func identitySettingsError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	code := cli.CodeLocalIO
	switch {
	case errors.Is(err, cfg.ErrFileChanged):
		code = cli.CodeConfigChanged
	case errors.Is(err, cfg.ErrFileInvalid), errors.Is(err, cfg.ErrAccountIdentityScope):
		code = cli.CodeConfigInvalid
	}
	input := cli.NewInputError(code, "cannot save verified identity; inspect the selected configuration before retrying")
	input.Field = "config"
	return fmt.Errorf("%w: %w", input, err)
}

func renderWhoamiSaveHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.AuthWhoamiSaveOutput)
	if !ok {
		return fmt.Errorf("auth.whoami.save: unexpected payload %T", env.Data)
	}
	if err := renderIdentityHuman(v, out.AuthWhoamiOutput); err != nil {
		return err
	}
	if out.DryRun && out.Settings.Status == "prepared" {
		if out.Settings.WouldChange {
			v.Line("would save verified cached identity")
		} else {
			v.Line("would keep verified cached identity unchanged")
		}
	} else {
		v.Fields(output.Field{Label: "Settings publication", Value: cli.Text(out.Settings.Status)})
	}
	v.Fields(output.Field{Label: "Configuration", Value: cli.Text(out.Settings.Path)})
	return nil
}
