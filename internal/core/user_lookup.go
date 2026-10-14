package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/matcra587/jira-cli/internal/jira"
)

// ErrUserLookupResponseInvalid refuses an unusable or mismatched exact-account reply.
var ErrUserLookupResponseInvalid = errors.New("user lookup response invalid")

// UserLookupClient reads one exact native account without searching or choosing a match.
type UserLookupClient interface {
	GetUser(context.Context, string) (UserRead, error)
}

// RequireActiveAccountIdentity binds an active native reply to the requested account.
// Email visibility is irrelevant; unknown members remain native data.
func RequireActiveAccountIdentity(user *jira.UserDetails, accountID string) error {
	if accountID == "" || accountID != strings.TrimSpace(accountID) || user == nil || !user.AccountID.Set || user.AccountID.Null || user.AccountID.Value != accountID || !user.Active.Set || user.Active.Null {
		return ErrUserLookupResponseInvalid
	}
	var deleted jira.Nullable[bool]
	if raw, present := user.Extra["deleted"]; present {
		if err := json.Unmarshal(raw, &deleted); err != nil {
			return ErrUserLookupResponseInvalid
		}
	}
	if !user.Active.Value || deleted.Set && !deleted.Null && deleted.Value {
		return ErrUserNotFound
	}
	return nil
}

// ResolveActiveAccount validates injected clients as well as transport replies.
// A late cancellation cannot publish an otherwise successful lookup.
func ResolveActiveAccount(ctx context.Context, client UserLookupClient, accountID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	read, err := client.GetUser(ctx, accountID)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := RequireActiveAccountIdentity(read.User, accountID); err != nil {
		return "", err
	}
	return read.User.AccountID.Value, nil
}
