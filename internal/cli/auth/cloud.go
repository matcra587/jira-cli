package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/matcra587/jira-cli/internal/core"
)

// cloudDiscoverer is the one public-metadata read login needs after a genuine
// site authentication refusal. Ordinary account reads do not infer token type.
type cloudDiscoverer interface {
	DiscoverCloudID(context.Context) (string, error)
}

func verifyLogin(ctx context.Context, deps Deps, ident identity, token string) (core.UserRead, string, error) {
	if err := ctx.Err(); err != nil {
		return core.UserRead{}, "", err
	}
	cloudID := ""
	if ident.cloudSet {
		cloudID = ident.cloudID
	}
	client, err := deps.ClientFor(ident.site, ident.email, token, cloudID)
	if err != nil {
		return core.UserRead{}, "", err
	}
	user, siteErr := verify(ctx, client)
	if err := ctx.Err(); err != nil {
		return core.UserRead{}, "", err
	}
	if siteErr == nil {
		return user, cloudID, nil
	}
	if errors.Is(siteErr, context.Canceled) || errors.Is(siteErr, context.DeadlineExceeded) {
		return core.UserRead{}, "", siteErr
	}
	var refusal interface{ HTTPStatusCode() int }
	if ident.cloudSet || !errors.As(siteErr, &refusal) || refusal.HTTPStatusCode() != http.StatusUnauthorized && refusal.HTTPStatusCode() != http.StatusForbidden {
		return core.UserRead{}, "", siteErr
	}
	discovery, available := client.(cloudDiscoverer)
	if !available {
		return core.UserRead{}, "", siteErr
	}
	cloudID, err = discovery.DiscoverCloudID(ctx)
	if contextErr := ctx.Err(); contextErr != nil {
		return core.UserRead{}, "", contextErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return core.UserRead{}, "", err
	}
	if err != nil || cloudID == "" || core.ValidateCloudID(cloudID) != nil {
		return core.UserRead{}, "", siteErr
	}
	gateway, err := deps.ClientFor(ident.site, ident.email, token, cloudID)
	if err != nil {
		return core.UserRead{}, "", err
	}
	user, err = verify(ctx, gateway)
	if contextErr := ctx.Err(); contextErr != nil {
		return core.UserRead{}, "", contextErr
	}
	if err != nil {
		return core.UserRead{}, "", err
	}
	return user, cloudID, nil
}
