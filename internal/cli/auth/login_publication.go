package auth

import (
	"context"
	"errors"

	"github.com/matcra587/jira-cli/internal/cli"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func publishLogin(ctx context.Context, deps Deps, ident identity, token string, dryRun bool, cloudID string, user core.UserRead) (envelope.AuthLoginOutput, error) {
	selected := ident.location
	backend := selected.Backend
	if backend == "" {
		backend = core.CredentialBackendKeyring
		selected.Backend = backend
	}
	out := envelope.AuthLoginOutput{
		MutationState: envelope.MutationState{DryRun: dryRun},
		Verified:      true, BaseURL: ident.site, Email: ident.email, CloudID: cloudID,
		AccountID: user.User.AccountID.Value, DisplayName: user.User.DisplayName.Value,
		Credential: core.CredentialPublication{Backend: backend, Status: "not_saved"},
		Settings:   core.AccountIdentityPublication{Status: "not_saved"},
	}
	if ident.external {
		out.Credential.Status, out.Credential.Reference = "external", selected.EnvironmentKey
	}
	if !dryRun && deps.ValidateLoginScope != nil {
		if err := deps.ValidateLoginScope(ctx, cloudID); err != nil {
			return out, err
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if dryRun && backend == core.CredentialBackendEnvironment && !ident.external {
		out.Settings.Status = "not_prepared"
		return out, nil
	}
	if deps.LoginTarget == nil && deps.IdentityTarget == nil {
		return out, cli.NewCodedError(cli.CodeConfigInvalid, "login has no configured public settings target")
	}
	scope := core.AccountIdentityScope{BaseURL: ident.site, Email: ident.email, CloudID: cloudID}
	var target core.AccountIdentityTarget
	var err error
	if deps.LoginTarget != nil {
		target, err = deps.LoginTarget(ctx, core.LoginIdentityBinding{AccountIdentityScope: scope, CredentialLocation: selected})
	} else {
		target, err = deps.IdentityTarget(ctx, scope)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return out, contextErr
	}
	if err != nil {
		if dryRun && errors.Is(err, cfg.ErrAccountIdentityScope) {
			out.Settings.Status = "blocked"
			return out, nil
		}
		return out, identitySettingsError(err)
	}
	update, err := target.Prepare(out.AccountID)
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
	if !dryRun && !ident.external && deps.PrepareLoginLocation != nil {
		// Scope and the complete public candidate are checked before reserving
		// a locator. A changed locator requires a fresh target snapshot because
		// its durable publication intentionally replaced the original bytes.
		next, publication, err := deps.PrepareLoginLocation(ctx, selected)
		if publication.Changed || publication.Status != "" {
			out.LocatorPublication = &publication
		}
		if err != nil {
			return out, identitySettingsError(err)
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if next != selected {
			selected = next
			if deps.LoginTarget == nil {
				return out, cli.NewCodedError(cli.CodeConfigInvalid, "login cannot bind its reserved credential reference")
			}
			target, err = deps.LoginTarget(ctx, core.LoginIdentityBinding{AccountIdentityScope: scope, CredentialLocation: selected})
			if contextErr := ctx.Err(); contextErr != nil {
				return out, contextErr
			}
			if err != nil {
				return out, identitySettingsError(err)
			}
			update, err = target.Prepare(out.AccountID)
			if err != nil {
				return out, identitySettingsError(err)
			}
			out.Settings = update.Receipt()
			if err := ctx.Err(); err != nil {
				out.Settings.Status = "not_saved"
				return out, err
			}
		}
	}
	if ident.external {
		out.Credential.Status, out.Credential.Reference = "external", selected.EnvironmentKey
	} else if dryRun {
		out.Credential.Status = "prepared"
	}
	if dryRun {
		return out, nil
	}
	if !ident.external {
		ident.location = selected
		if err := saveLoginCredential(ctx, deps, ident, token, &out); err != nil {
			out.Settings.Status = "not_saved"
			return out, err
		}
	}
	if err := ctx.Err(); err != nil {
		out.Settings.Status = "not_saved"
		return out, err
	}
	out.Settings, err = update.Save(ctx)
	if err != nil {
		return out, identitySettingsError(err)
	}
	return out, nil
}

func saveLoginCredential(ctx context.Context, deps Deps, ident identity, token string, out *envelope.AuthLoginOutput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var store core.CredentialStore
	var err error
	if deps.CredentialsForLogin != nil {
		store, err = deps.CredentialsForLogin(ctx, ident.location)
	} else {
		store, err = deps.Credentials()
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if err != nil {
		return err
	}
	previous, previousErr := credentials.Load(ctx, store)
	if err := ctx.Err(); err != nil {
		return err
	}
	if errors.Is(previousErr, context.Canceled) || errors.Is(previousErr, context.DeadlineExceeded) {
		return previousErr
	}
	replaced := ""
	if previousErr == nil && previous.Complete() && (previous.Site != ident.site || previous.Email != ident.email) {
		replaced = previous.Email + " on " + previous.Site
	}
	out.Credential, err = credentials.SaveWithPublication(ctx, store, credentials.Record{Site: ident.site, Email: ident.email, CloudID: out.CloudID, Token: token})
	if out.Credential.Saved {
		out.Replaced = replaced
	}
	if err != nil {
		return &cli.CodedError{Code: cli.CodeAuthStoreUnavailable, Message: "credential publication failed; inspect the selected store before retrying", Cause: err}
	}
	return nil
}

func loginWarnings(out envelope.AuthLoginOutput) []envelope.Warning {
	if out.Settings.Status == "blocked" {
		return []envelope.Warning{{Type: "login_settings_scope_conflict", Message: "verification succeeded, but publication is blocked by conflicting public settings; repair the selected configuration or environment before live login"}}
	}
	return nil
}
