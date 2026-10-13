package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// OpenLoginIdentityTarget prepares complete public login metadata through the
// same immutable settings owner as whoami saving. Effective declarations must
// match the verified tuple; saving a token cannot repair a process overlay.
func OpenLoginIdentityTarget(ctx context.Context, opts Options, binding core.LoginIdentityBinding) (core.AccountIdentityTarget, error) {
	scope := binding.AccountIdentityScope
	target, err := openAccountIdentityTarget(ctx, opts, scope)
	if err != nil {
		return nil, err
	}
	opts.PermitMissingTarget = true
	resolved, err := Resolve(ctx, opts, nil)
	if err != nil {
		return nil, err
	}
	if CredentialSelection(*resolved) != binding.CredentialLocation {
		return nil, fmt.Errorf("%w: selected credential provider or reference changed before login publication", ErrAccountIdentityScope)
	}
	setting, err := target.file.Setting("secret_backend")
	if err != nil {
		return nil, err
	}
	var authoredBackend core.CredentialBackend
	if setting.Present {
		if err := authoredBackend.UnmarshalText([]byte(setting.Value)); err != nil {
			return nil, err
		}
		if authoredBackend != binding.Backend {
			return nil, fmt.Errorf("%w: authored credential provider conflicts; repair secret_backend before login", ErrAccountIdentityScope)
		}
	}
	for _, selection := range []struct {
		key   string
		value string
	}{
		{"onepassword_account", binding.SDKAccount},
		{"onepassword_vault", binding.Vault},
		{"onepassword_item", binding.Item},
		{"api_token_env", resolved.Config.APITokenEnv},
	} {
		if binding.Backend != core.CredentialBackendOnePassword && selection.key != "api_token_env" || binding.Backend != core.CredentialBackendEnvironment && selection.key == "api_token_env" {
			continue
		}
		setting, err := target.file.Setting(selection.key)
		if err != nil {
			return nil, err
		}
		value := setting.Value
		if selection.key == "onepassword_account" {
			value = strings.TrimSpace(value)
		}
		if setting.Present && value != selection.value {
			return nil, fmt.Errorf("%w: authored provider reference conflicts; repair %s before login", ErrAccountIdentityScope, selection.key)
		}
	}
	for _, binding := range []struct {
		key   string
		match bool
	}{
		{"base_url", credentials.NormalizeSite(resolved.Config.BaseURL) == credentials.NormalizeSite(scope.BaseURL)},
		{"email", strings.EqualFold(strings.TrimSpace(resolved.Config.Email), strings.TrimSpace(scope.Email))},
		{"cloud_id", resolved.Config.CloudID == scope.CloudID},
	} {
		layer := resolved.Source(binding.key)
		if layer != LayerDefault && layer != LayerUnknown && !binding.match {
			return nil, fmt.Errorf("%w: effective %s conflicts; repair the selected configuration or environment before login", ErrAccountIdentityScope, binding.key)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target.additional = map[string]string{
		"base_url": scope.BaseURL, "email": scope.Email, "cloud_id": scope.CloudID,
		"secret_backend": string(resolved.Config.SecretBackend),
	}
	switch resolved.Config.SecretBackend {
	case core.CredentialBackendEnvironment:
		target.additional["api_token_env"] = resolved.Config.APITokenEnv
	case core.CredentialBackendOnePassword:
		target.additional["onepassword_account"] = binding.SDKAccount
		target.additional["onepassword_vault"] = binding.Vault
		target.additional["onepassword_item"] = binding.Item
	}
	return target, nil
}
