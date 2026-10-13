package core

import (
	"errors"
	"strings"

	"github.com/matcra587/jira-cli/internal/jira"
)

// ErrCurrentUserResponseInvalid refuses an unusable authenticated identity.
var ErrCurrentUserResponseInvalid = errors.New("current user response invalid")

// RequireCurrentUserIdentity is shared by transport decoding and authentication
// publication. HTTP success alone never establishes a verified account.
func RequireCurrentUserIdentity(user *jira.UserDetails) error {
	if user == nil || !user.AccountID.Set || user.AccountID.Null || strings.TrimSpace(user.AccountID.Value) == "" {
		return ErrCurrentUserResponseInvalid
	}
	return nil
}
