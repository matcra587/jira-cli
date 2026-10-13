package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/jira"
)

type cloudFlowClient struct {
	core.JiraClient
	read     func(context.Context) (core.UserRead, error)
	discover func(context.Context) (string, error)
}

func (client cloudFlowClient) CurrentUser(ctx context.Context) (core.UserRead, error) {
	return client.read(ctx)
}

func (client cloudFlowClient) DiscoverCloudID(ctx context.Context) (string, error) {
	return client.discover(ctx)
}

func cloudFlowIdentity() core.UserRead {
	return core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("fixture-account"), DisplayName: jira.NullableOf("Scoped Fixture")}}
}

func TestCloudLoginVerifiesExplicitOrDiscoveredScopeBeforeSaving(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"classic", "discovered", "explicit scoped", "explicit classic", "preview"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			store := newFakeStore()
			opened, discoveries, reads := 0, 0, 0
			scopes := []string{}
			ident := identity{site: "https://example.invalid", email: "person@example.invalid"}
			if mode == "explicit scoped" {
				ident.cloudSet = true
				ident.cloudID = "fixture-cloud"
			}
			if mode == "explicit classic" {
				ident.cloudSet = true
			}
			deps := Deps{Credentials: func() (core.CredentialStore, error) { opened++; return store, nil }, ClientFor: func(site, email, token, cloudID string) (core.JiraClient, error) {
				if site != ident.site || email != ident.email || token != "synthetic-flow-token" {
					t.Fatal("verification lost explicit credential identity")
				}
				scopes = append(scopes, cloudID)
				return cloudFlowClient{read: func(context.Context) (core.UserRead, error) {
					reads++
					if (mode == "discovered" || mode == "preview") && cloudID == "" {
						return core.UserRead{}, &adapterjira.APIError{StatusCode: 401}
					}
					return cloudFlowIdentity(), nil
				}, discover: func(context.Context) (string, error) { discoveries++; return "fixture-cloud", nil }}, nil
			}}
			deps.IdentityTarget = loginTestTarget(t)
			out, err := login(t.Context(), deps, ident, "synthetic-flow-token", mode == "preview")
			if err != nil {
				t.Fatal(err)
			}
			wantCloud := ""
			wantReads := 1
			wantDiscoveries := 0
			if mode == "discovered" || mode == "preview" {
				wantCloud = "fixture-cloud"
				wantReads = 2
				wantDiscoveries = 1
			}
			if mode == "explicit scoped" {
				wantCloud = "fixture-cloud"
			}
			if out.BaseURL != ident.site || out.CloudID != wantCloud || out.AccountID != "fixture-account" || reads != wantReads || discoveries != wantDiscoveries || scopes[len(scopes)-1] != wantCloud {
				t.Fatal("verified scope/identity or discovery policy was lost")
			}
			if mode == "preview" {
				if opened != 0 || store.setCall != 0 {
					t.Fatal("scoped preview opened persistent credentials")
				}
				return
			}
			record, err := credentials.Load(t.Context(), store)
			if err != nil || record.Version != 2 || record.CloudID != wantCloud || record.Site != ident.site || record.Token != "synthetic-flow-token" || opened != 1 || store.setCall != 1 {
				t.Fatal("saved record did not bind verified cloud scope", err)
			}
		})
	}
}

func TestCloudLoginPreservesFailureAndCancellationWithoutPublication(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"explicit classic refusal", "explicit scoped refusal", "server failure", "malformed site", "invalid discovery", "discovery canceled", "discovery deadline", "canceled after discovery", "gateway malformed", "gateway canceled", "gateway deadline", "before factory", "joined site cancellation"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			opened, factories, discovered := 0, 0, 0
			ident := identity{site: "https://example.invalid", email: "person@example.invalid"}
			if mode == "explicit classic refusal" {
				ident.cloudSet = true
			}
			if mode == "explicit scoped refusal" {
				ident.cloudSet = true
				ident.cloudID = "fixture-cloud"
			}
			if mode == "before factory" {
				cancel()
			}
			deps := Deps{Credentials: func() (core.CredentialStore, error) {
				opened++
				t.Fatal("failed verification opened credentials")
				return nil, nil
			}, ClientFor: func(_, _, _, cloudID string) (core.JiraClient, error) {
				factories++
				return cloudFlowClient{read: func(context.Context) (core.UserRead, error) {
					if cloudID != "" {
						switch mode {
						case "explicit scoped refusal":
							return core.UserRead{}, &adapterjira.APIError{StatusCode: 401}
						case "gateway canceled":
							return core.UserRead{}, fmt.Errorf("gateway: %w", context.Canceled)
						case "gateway deadline":
							return core.UserRead{}, fmt.Errorf("gateway: %w", context.DeadlineExceeded)
						default:
							return core.UserRead{}, nil
						}
					}
					switch mode {
					case "server failure":
						return core.UserRead{}, &adapterjira.APIError{StatusCode: 500}
					case "malformed site":
						return core.UserRead{}, nil
					case "joined site cancellation":
						return core.UserRead{}, errors.Join(&adapterjira.APIError{StatusCode: 401}, context.Canceled)
					}
					return core.UserRead{}, &adapterjira.APIError{StatusCode: 401}
				}, discover: func(context.Context) (string, error) {
					discovered++
					switch mode {
					case "invalid discovery":
						return "..", nil
					case "discovery canceled":
						return "", fmt.Errorf("public read: %w", context.Canceled)
					case "discovery deadline":
						return "", fmt.Errorf("public read: %w", context.DeadlineExceeded)
					case "canceled after discovery":
						cancel()
					}
					return "fixture-cloud", nil
				}}, nil
			}}
			out, err := login(ctx, deps, ident, "synthetic-failed-token", false)
			if err == nil || opened != 0 || out.AccountID != "" || out.CloudID != "" {
				t.Fatal("failure became successful authentication")
			}
			if mode == "before factory" && factories != 0 {
				t.Fatal("canceled verification entered factory")
			}
			switch mode {
			case "gateway canceled", "discovery canceled", "canceled after discovery", "before factory", "joined site cancellation":
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation became unauthorized", err)
				}
			case "gateway deadline", "discovery deadline":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("deadline became unauthorized", err)
				}
			case "gateway malformed", "malformed site":
				if !errors.Is(err, core.ErrCurrentUserResponseInvalid) {
					t.Fatal("invalid account lost its protocol error", err)
				}
			}
			if (mode == "explicit classic refusal" || mode == "explicit scoped refusal" || mode == "server failure" || mode == "malformed site" || mode == "joined site cancellation") && discovered != 0 {
				t.Fatal("explicit route or non-auth failure triggered discovery")
			}
		})
	}
}

func TestInvalidExplicitCloudFlagRefusesBeforeIdentityOrStdin(t *testing.T) {
	t.Parallel()
	deps := Deps{Site: func(context.Context) (SiteInfo, error) {
		t.Fatal("invalid cloud flag read configured credentials")
		return SiteInfo{}, nil
	}}
	if _, _, err := gather(t.Context(), command.Invocation{}, deps, loginFlags{cloudID: "..", cloudSet: true}); err == nil {
		t.Fatal("invalid explicit cloud flag accepted")
	}
}

func TestLiveCloudScopeRefusalAndCancellationPrecedeCredentialPublication(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"refusal", "cancellation", "preview"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			opened, checks := 0, 0
			failure := errors.New("authored scope differs")
			deps := Deps{Credentials: func() (core.CredentialStore, error) {
				opened++
				t.Fatal("scope refusal opened publication store")
				return nil, nil
			}, ClientFor: func(string, string, string, string) (core.JiraClient, error) {
				return cloudFlowClient{read: func(context.Context) (core.UserRead, error) { return cloudFlowIdentity(), nil }}, nil
			}, ValidateLoginScope: func(context.Context, string) error {
				checks++
				if mode == "cancellation" {
					cancel()
					return nil
				}
				return failure
			}}
			deps.IdentityTarget = loginTestTarget(t)
			out, err := login(ctx, deps, identity{site: "https://example.invalid", email: "person@example.invalid", cloudSet: true, cloudID: "fixture-cloud"}, "synthetic-flow-token", mode == "preview")
			if opened != 0 {
				t.Fatal("credential publication preceded scope check")
			}
			switch mode {
			case "preview":
				if err != nil || checks != 0 || out.CloudID != "fixture-cloud" {
					t.Fatal("preview claimed persistent scope activation", err)
				}
			case "refusal":
				if !errors.Is(err, failure) || checks != 1 {
					t.Fatal("scope refusal lost before publication", err)
				}
			case "cancellation":
				if !errors.Is(err, context.Canceled) || checks != 1 {
					t.Fatal("scope cancellation opened provider or disappeared", err)
				}
			}
		})
	}
}
