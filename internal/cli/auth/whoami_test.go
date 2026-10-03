package auth

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestWhoamiHelpDoesNotResolveCredentials(t *testing.T) {
	t.Parallel()
	deps := Deps{
		Site: func() (SiteInfo, error) { t.Fatal("help resolved credentials"); return SiteInfo{}, nil },
		ClientFor: func(string, string, string) (core.JiraClient, error) {
			t.Fatal("help constructed a client")
			return nil, nil
		},
		Credentials: func() (core.CredentialStore, error) { t.Fatal("help opened a credential store"); return nil, nil },
	}
	c := New(deps)
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"whoami", "--help"})
	if err := c.Execute(); err != nil || out.Len() == 0 {
		t.Fatalf("identity help failed: %v output=%q", err, out.String())
	}
}

type identityContextClient struct{ *fakeClient }

func (c identityContextClient) CurrentUser(ctx context.Context) (core.UserRead, error) {
	return core.UserRead{}, ctx.Err()
}

func TestCurrentIdentityHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	deps := Deps{
		Site: func() (SiteInfo, error) {
			return SiteInfo{BaseURL: "https://example.invalid", Email: "person@example.invalid", Token: "fixture-token"}, nil
		},
		ClientFor: func(string, string, string) (core.JiraClient, error) { return identityContextClient{}, nil },
	}
	_, _, err := currentIdentity(ctx, deps)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled identity context was lost: %v", err)
	}
}
