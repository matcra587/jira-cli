package config

import (
	"fmt"
	"strings"

	"github.com/knadh/koanf/v2"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// CachedAccountID returns public identity only for its recorded matching scope.
// It never supplies authenticated identity or permission for a live write.
func (c Config) CachedAccountID() string {
	if validateAccountIdentity(c) != nil || ValidateBaseURL(c.BaseURL) != nil || strings.TrimSpace(c.AccountID) == "" || c.AccountIDSite == "" || strings.TrimSpace(c.AccountIDEmail) == "" ||
		credentials.NormalizeSite(c.BaseURL) != credentials.NormalizeSite(c.AccountIDSite) ||
		!strings.EqualFold(strings.TrimSpace(c.Email), strings.TrimSpace(c.AccountIDEmail)) ||
		strings.TrimSpace(c.CloudID) != strings.TrimSpace(c.AccountIDCloudID) {
		return ""
	}
	return c.AccountID
}

func validateAccountIdentity(c Config) error {
	for _, entry := range []struct{ key, value string }{{"account_id", c.AccountID}, {"account_id_email", c.AccountIDEmail}} {
		if entry.value != "" && strings.TrimSpace(entry.value) == "" {
			return fmt.Errorf("%s must be empty or nonblank", entry.key)
		}
	}
	if c.AccountIDSite != "" {
		if err := ValidateBaseURL(c.AccountIDSite); err != nil {
			return fmt.Errorf("account_id_site: %w", err)
		}
	}
	if err := core.ValidateCloudID(c.AccountIDCloudID); err != nil {
		return fmt.Errorf("account_id_cloud_id: %w", err)
	}
	return nil
}

// Environment fields are checked before provider callbacks, independently of
// file declarations. Empty bindings retain inactive public metadata.
func prepareAccountIdentityEnvironment(layer *koanf.Koanf) error {
	return validateAccountIdentity(Config{
		AccountID: layer.String("account_id"), AccountIDSite: layer.String("account_id_site"),
		AccountIDEmail: layer.String("account_id_email"), AccountIDCloudID: layer.String("account_id_cloud_id"),
	})
}
