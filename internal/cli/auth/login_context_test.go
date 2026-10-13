package auth

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type loginContextStore struct {
	core.CredentialStore
	get func(context.Context) (string, error)
	set func(context.Context) error
}

func (s loginContextStore) Get(ctx context.Context, _ string) (string, error) { return s.get(ctx) }
func (s loginContextStore) Set(ctx context.Context, _, _ string) error        { return s.set(ctx) }

type loginContextUpdate struct {
	receipt func() core.AccountIdentityPublication
	save    func(context.Context) (core.AccountIdentityPublication, error)
}

func (u loginContextUpdate) Receipt() core.AccountIdentityPublication { return u.receipt() }
func (u loginContextUpdate) Save(ctx context.Context) (core.AccountIdentityPublication, error) {
	return u.save(ctx)
}

func TestLoginCancellationStopsEveryUnstartedPublicationStage(t *testing.T) {
	t.Parallel()
	stages := []string{"scope", "target", "prepare", "receipt", "locator", "provider", "read", "write", "settings"}
	for index, stage := range stages {
		for _, acknowledged := range []bool{false, true} {
			if stage != "write" && stage != "settings" && acknowledged {
				continue
			}
			name := stage
			if acknowledged {
				name += "/acknowledged"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var entered []string
				enter := func(actual context.Context, name string) {
					if actual != ctx || actual.Err() != nil {
						t.Fatalf("unstarted %s received canceled or substituted context", name)
					}
					entered = append(entered, name)
					if name == stage {
						cancel()
					}
				}
				store := loginContextStore{
					get: func(actual context.Context) (string, error) {
						enter(actual, "read")
						return "", core.ErrNoCredential
					},
					set: func(actual context.Context) error {
						enter(actual, "write")
						if stage == "write" && !acknowledged {
							return context.Canceled
						}
						return nil
					},
				}
				deps := Deps{
					ValidateLoginScope: func(actual context.Context, _ string) error { enter(actual, "scope"); return nil },
					PrepareLoginLocation: func(actual context.Context, location core.CredentialLocation) (core.CredentialLocation, core.AccountIdentityPublication, error) {
						enter(actual, "locator")
						return location, core.AccountIdentityPublication{Status: "saved", Changed: true, Saved: true}, nil
					},
					LoginTarget: func(actual context.Context, _ core.LoginIdentityBinding) (core.AccountIdentityTarget, error) {
						enter(actual, "target")
						return identityTargetProbe{prepare: func(string) (core.AccountIdentityUpdate, error) {
							enter(ctx, "prepare")
							return loginContextUpdate{
								receipt: func() core.AccountIdentityPublication {
									enter(ctx, "receipt")
									return core.AccountIdentityPublication{Status: "prepared", WouldChange: true}
								},
								save: func(actual context.Context) (core.AccountIdentityPublication, error) {
									enter(actual, "settings")
									if !acknowledged {
										return core.AccountIdentityPublication{Status: "unknown"}, context.Canceled
									}
									return core.AccountIdentityPublication{Status: "saved", Saved: true, Changed: true}, nil
								},
							}, nil
						}}, nil
					},
					CredentialsForLogin: func(actual context.Context, _ core.CredentialLocation) (core.CredentialStore, error) {
						enter(actual, "provider")
						return store, nil
					},
				}
				user := core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("native-id")}}
				out, err := publishLogin(ctx, deps, identity{site: "https://example.invalid", email: "person@example.invalid"}, "synthetic-login-token", false, "", user)
				if !slices.Equal(entered, stages[:index+1]) || !out.Verified || out.AccountID != "native-id" {
					t.Fatalf("cancellation entered later work or lost verified identity: stages=%v out=%+v", entered, out)
				}
				if index >= 4 && (out.LocatorPublication == nil || !out.LocatorPublication.Saved || !out.LocatorPublication.Changed) {
					t.Fatal("later cancellation lost durable locator publication")
				}
				if stage == "settings" && acknowledged {
					if err != nil || !out.Settings.Saved || !out.Credential.Saved {
						t.Fatal("late cancellation erased acknowledged settings or credentials", err)
					}
				} else if !errors.Is(err, context.Canceled) || out.Settings.Saved {
					t.Fatal("publication lost cancellation or invented settings acknowledgment", err)
				}
				wantSaved := stage == "settings" || stage == "write" && acknowledged
				if out.Credential.Saved != wantSaved || stage == "write" && !acknowledged && out.Credential.Status != "unknown" {
					t.Fatalf("publication lost actual credential outcome: %+v", out.Credential)
				}
			})
		}
	}
}

func TestLoginPreviewCancellationAfterReceiptCannotReturnSuccess(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	deps := Deps{LoginTarget: func(context.Context, core.LoginIdentityBinding) (core.AccountIdentityTarget, error) {
		return identityTargetProbe{prepare: func(string) (core.AccountIdentityUpdate, error) {
			return loginContextUpdate{receipt: func() core.AccountIdentityPublication {
				cancel()
				return core.AccountIdentityPublication{Status: "prepared", WouldChange: true}
			}, save: func(context.Context) (core.AccountIdentityPublication, error) {
				t.Fatal("preview entered settings publication")
				return core.AccountIdentityPublication{}, nil
			}}, nil
		}}, nil
	}}
	deps.PrepareLoginLocation = func(context.Context, core.CredentialLocation) (core.CredentialLocation, core.AccountIdentityPublication, error) {
		t.Fatal("preview reserved a credential locator")
		return core.CredentialLocation{}, core.AccountIdentityPublication{}, nil
	}
	user := core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("native-id")}}
	out, err := publishLogin(ctx, deps, identity{site: "https://example.invalid", email: "public@example.invalid"}, "synthetic-token", true, "", user)
	if !errors.Is(err, context.Canceled) || !out.Verified || out.Settings.Status != "not_saved" || out.Settings.Saved || out.Credential.Attempted {
		t.Fatal("canceled preview reported success or publication", err)
	}
}
