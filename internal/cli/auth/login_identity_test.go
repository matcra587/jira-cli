package auth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/jira"
)

func loginTestTarget(t *testing.T) func(context.Context, core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.toml")
	return func(ctx context.Context, scope core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
		return config.OpenAccountIdentityTarget(ctx, path, scope)
	}
}

type loginEffectStore struct {
	core.CredentialStore
	set           func(context.Context, string) error
	reads, writes int
}

func (s *loginEffectStore) Get(context.Context, string) (string, error) {
	s.reads++
	return "", core.ErrNoCredential
}

func (s *loginEffectStore) Set(ctx context.Context, _, value string) error {
	s.writes++
	return s.set(ctx, value)
}

func TestLoginPublicationRetainsActualEffectsAndStopsCanceledSettingsEntry(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"target", "prepare", "provider refusal", "provider unknown", "provider acknowledged cancellation", "settings refusal", "settings unknown", "settings written", "success"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("synthetic login publication failure")
			writes := 0
			store := &loginEffectStore{set: func(ctx context.Context, raw string) error {
				var record credentials.Record
				if json.Unmarshal([]byte(raw), &record) != nil || record.Token != "synthetic-login-token" || record.Site != "https://example.invalid" || record.Email != "credential@example.invalid" || record.CloudID != "" {
					t.Fatal("credential publication changed the verified tuple")
				}
				switch phase {
				case "provider refusal":
					return &core.CredentialStoreError{Backend: core.CredentialBackendKeyring, Operation: "save", Cause: cause}
				case "provider unknown":
					return cause
				case "provider acknowledged cancellation":
					cancel()
				}
				return nil
			}}
			deps := Deps{
				ClientFor: func(string, string, string, string) (core.JiraClient, error) {
					return savingIdentityClient{user: core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("native-id"), EmailAddress: jira.NullableNull[string]()}}}, nil
				},
				Credentials: func() (core.CredentialStore, error) {
					if phase == "target" || phase == "prepare" {
						t.Fatal("settings refusal opened publication provider")
					}
					return store, nil
				},
				IdentityTarget: func(context.Context, core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
					if phase == "target" {
						return nil, cause
					}
					return identityTargetProbe{prepare: func(id string) (core.AccountIdentityUpdate, error) {
						if id != "native-id" {
							t.Fatal("native account replaced by credential email")
						}
						if phase == "prepare" {
							return nil, cause
						}
						return identityUpdateProbe{receipt: core.AccountIdentityPublication{Path: "owned-settings.toml", WouldChange: true, Status: "prepared"}, save: func(actual context.Context) (core.AccountIdentityPublication, error) {
							if actual != ctx || actual.Err() != nil {
								t.Fatal("canceled login entered unstarted settings publisher")
							}
							writes++
							out := core.AccountIdentityPublication{Path: "owned-settings.toml", WouldChange: true, Status: "not_saved"}
							switch phase {
							case "settings refusal":
								return out, cause
							case "settings unknown":
								out.Status = "unknown"
								return out, cause
							case "settings written":
								out.Status = "written"
								out.Changed = true
								out.Created = true
								return out, cause
							}
							out.Status = "saved"
							out.Saved = true
							out.Changed = true
							return out, nil
						}}, nil
					}}, nil
				},
			}
			out, err := login(ctx, deps, identity{site: "https://example.invalid", email: "credential@example.invalid"}, "synthetic-login-token", false)
			if !out.Verified || out.AccountID != "native-id" || out.Email != "credential@example.invalid" {
				t.Fatal("publication failure discarded verified public identity")
			}
			wantCredentialSaved := phase == "provider acknowledged cancellation" || phase == "settings refusal" || phase == "settings unknown" || phase == "settings written" || phase == "success"
			if out.Credential.Saved != wantCredentialSaved || out.Settings.Saved != (phase == "success") {
				t.Fatalf("false publication effects: %+v", out)
			}
			if phase == "provider unknown" && (out.Credential.Status != "unknown" || !out.Credential.Attempted) {
				t.Fatal("unknown credential write denied")
			}
			if phase == "provider refusal" && (out.Credential.Status != "not_saved" || out.Credential.Attempted) {
				t.Fatal("known provider refusal became attempted")
			}
			if phase == "provider acknowledged cancellation" {
				if !errors.Is(err, context.Canceled) || writes != 0 || out.Credential.Status != "saved" || out.Settings.Status != "not_saved" {
					t.Fatalf("late cancellation lost actual credential save: %+v %v", out, err)
				}
			} else if phase == "success" {
				if err != nil || writes != 1 || store.writes != 1 {
					t.Fatal("successful publication was skipped or replayed", err)
				}
			} else if !errors.Is(err, cause) {
				t.Fatal("publication lost original cause", err)
			}
			if phase == "settings unknown" && out.Settings.Status != "unknown" || phase == "settings written" && (out.Settings.Status != "written" || !out.Settings.Changed || !out.Settings.Created) {
				t.Fatal("settings failure discarded actual effects")
			}
		})
	}
}

func TestLoginPublishesVerifiedBoundIdentityAndActualCredentialReceipt(t *testing.T) {
	t.Parallel()
	h := newHarness(t, configured())
	path := filepath.Join(t.TempDir(), "settings.toml")
	targets := 0
	h.deps.IdentityTarget = func(ctx context.Context, scope core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
		targets++
		if scope.BaseURL != h.site.BaseURL || scope.Email != h.site.Email || scope.CloudID != "" {
			t.Fatal("login changed the verified credential binding")
		}
		return config.OpenAccountIdentityTarget(ctx, path, scope)
	}
	out, err := login(t.Context(), h.deps, identOf(h.site), "synthetic-login-token", false)
	if err != nil {
		t.Fatal(err)
	}
	if targets != 1 {
		t.Fatalf("verified login did not prepare its bound settings target: targets=%d", targets)
	}
	file, err := config.ReadFile(t.Context(), path)
	if err != nil {
		t.Fatalf("verified login published no bound identity: %v", err)
	}
	for key, want := range map[string]string{"account_id": out.AccountID, "account_id_site": h.site.BaseURL, "account_id_email": h.site.Email, "account_id_cloud_id": ""} {
		setting, err := file.Setting(key)
		if err != nil || !setting.Present || setting.Secret || setting.Value != want {
			t.Fatalf("login lost stored binding %s: %+v %v", key, setting, err)
		}
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Credential struct {
			Saved     bool   `json:"saved"`
			Attempted bool   `json:"attempted"`
			Status    string `json:"status"`
		} `json:"credential"`
		Settings struct {
			Saved   bool   `json:"saved"`
			Changed bool   `json:"changed"`
			Created bool   `json:"created"`
			Status  string `json:"status"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Credential.Saved || !receipt.Credential.Attempted || receipt.Credential.Status != "saved" || !receipt.Settings.Saved || !receipt.Settings.Changed || !receipt.Settings.Created || receipt.Settings.Status != "saved" || h.store.setCall != 1 {
		t.Fatalf("login claimed no actual credential/settings publication: %s", data)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("publication receipt has no actual settings file", err)
	}
}
