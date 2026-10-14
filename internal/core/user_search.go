package core

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"

	"github.com/matcra587/jira-cli/internal/jira"
)

var (
	ErrUserSearchResponseInvalid = errors.New("user search response invalid")
	ErrUserNotFound              = errors.New("no visible active user matches")
	ErrUserAmbiguous             = errors.New("multiple visible active users match")
	ErrUserResolutionIncomplete  = errors.New("visible user search window is incomplete")
)

const UserSearchLimit = 1000

// UserSearchClient exposes one authenticated native search page.
type UserSearchClient interface {
	SearchUsers(context.Context, string, int, int) (UserSearchRead, error)
}

// ActiveUserSearchPage validates every row before publishing identities to seen.
// A hidden email does not discard a visible search match. Unknown activity does.
func ActiveUserSearchPage(users []*jira.User, maxResults int, seen map[string]struct{}) ([]*jira.User, error) {
	if users == nil || seen == nil || maxResults < 1 || maxResults > UserSearchLimit || len(users) > maxResults {
		return nil, ErrUserSearchResponseInvalid
	}
	pageIDs := make(map[string]struct{}, len(users))
	active := make([]*jira.User, 0, len(users))
	for _, user := range users {
		if user == nil || !user.AccountID.Set || user.AccountID.Null || strings.TrimSpace(user.AccountID.Value) == "" {
			return nil, ErrUserSearchResponseInvalid
		}
		id := user.AccountID.Value
		if _, duplicate := seen[id]; duplicate {
			return nil, ErrUserSearchResponseInvalid
		}
		if _, duplicate := pageIDs[id]; duplicate {
			return nil, ErrUserSearchResponseInvalid
		}
		pageIDs[id] = struct{}{}
		var deleted jira.Nullable[bool]
		if raw, present := user.Extra["deleted"]; present {
			if err := json.Unmarshal(raw, &deleted); err != nil {
				return nil, ErrUserSearchResponseInvalid
			}
		}
		if !user.Active.Set || user.Active.Null || !user.Active.Value || (deleted.Set && !deleted.Null && deleted.Value) {
			continue
		}
		active = append(active, user)
	}
	maps.Copy(seen, pageIDs)
	return active, nil
}

// ResolveVisibleUser preserves the legacy single visible active match policy.
// It does not verify exact email equality or assignability. Only an observed
// empty page establishes completion within the endpoint's visible search window.
func ResolveVisibleUser(ctx context.Context, client UserSearchClient, query string) (string, error) {
	seen := make(map[string]struct{})
	selected := ""
	for start := 0; start < UserSearchLimit; {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		limit := min(50, UserSearchLimit-start)
		page, err := client.SearchUsers(ctx, query, start, limit)
		if err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		active, err := ActiveUserSearchPage(page.Users, limit, seen)
		if err != nil {
			return "", err
		}
		for _, user := range active {
			if selected != "" {
				return "", ErrUserAmbiguous
			}
			selected = user.AccountID.Value
		}
		if len(page.Users) == 0 {
			if selected == "" {
				return "", ErrUserNotFound
			}
			return selected, ctx.Err()
		}
		start += len(page.Users)
	}
	return "", ErrUserResolutionIncomplete
}
