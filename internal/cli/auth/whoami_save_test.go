package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestWhoamiSaveIsRegisteredAsPreviewableLocalMutation(t *testing.T) {
	t.Parallel()
	c := newWhoami(Deps{})
	metas, ok := command.MetaFor(c)
	if !ok || len(metas) != 2 || metas[0].Op != "auth.whoami" || metas[0].Class() != command.ClassRead ||
		metas[1].Op != "auth.whoami.save" || metas[1].Variant != "--save" || metas[1].Class() != command.ClassLocalState {
		t.Fatalf("identity save lacks independent typed local mutation: %+v", metas)
	}
	if _, ok := envelope.OutputSample("auth.whoami.save"); !ok {
		t.Fatal("identity save output is unregistered")
	}
	for _, name := range []string{"save", "dry-run"} {
		flag := c.Flags().Lookup(name)
		if flag == nil || flag.Value.Type() != "bool" || flag.DefValue != "false" {
			t.Fatalf("identity save lacks safe --%s declaration", name)
		}
	}
}

type savingIdentityClient struct {
	*fakeClient
	user   core.UserRead
	err    error
	lookup func()
}

func (c savingIdentityClient) CurrentUser(context.Context) (core.UserRead, error) {
	if c.lookup != nil {
		c.lookup()
	}
	return c.user, c.err
}

func TestWhoamiSavePublishesBoundIdentityOnlyForSelectedLiveVariant(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name              string
		args              []string
		selected, preview bool
	}{
		{name: "ordinary read"},
		{name: "ordinary read with preview flag", args: []string{"--dry-run"}},
		{name: "save preview", args: []string{"--save", "--dry-run"}, selected: true, preview: true},
		{name: "save live", args: []string{"--save"}, selected: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "settings.toml")
			const original = "theme='dark'\n[aliases]\n'Mixed.Name'='version'\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			var user jira.UserDetails
			if err := json.Unmarshal([]byte(`{"accountId":"native-id","emailAddress":null,"future":{"large":9007199254740993}}`), &user); err != nil {
				t.Fatal(err)
			}
			scope := core.AccountIdentityScope{BaseURL: "https://example.invalid", Email: "credential@example.invalid", CloudID: "verified-cloud"}
			events := []string{}
			deps := Deps{
				Site: func(context.Context) (SiteInfo, error) {
					events = append(events, "site")
					return SiteInfo{BaseURL: scope.BaseURL, Email: scope.Email, CloudID: scope.CloudID, Token: "fixture-token"}, nil
				},
				IdentityTarget: func(ctx context.Context, actual core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
					events = append(events, "target")
					if actual != scope {
						t.Fatal("publication scope invented from native email")
					}
					return cfg.OpenAccountIdentityTarget(ctx, path, actual)
				},
				ClientFor: func(site, email, token, cloudID string) (core.JiraClient, error) {
					events = append(events, "client")
					if site != scope.BaseURL || email != scope.Email || token != "fixture-token" || cloudID != scope.CloudID {
						t.Fatal("lookup changed resolved credential scope")
					}
					return savingIdentityClient{user: core.UserRead{User: &user}, lookup: func() { events = append(events, "lookup") }}, nil
				},
				Credentials: func() (core.CredentialStore, error) {
					t.Fatal("identity opened a second credential store")
					return nil, nil
				},
			}
			c := newWhoami(deps)
			var stdout bytes.Buffer
			c.SetOut(&stdout)
			c.SetErr(io.Discard)
			c.SetArgs(tc.args)
			if err := c.ExecuteContext(cli.WithFormat(t.Context(), output.FormatJSON)); err != nil {
				t.Fatal(err)
			}
			wantEvents := []string{"site", "client", "lookup"}
			if tc.selected {
				wantEvents = []string{"site", "target", "client", "lookup"}
			}
			if !reflect.DeepEqual(events, wantEvents) {
				t.Fatalf("identity preparation/lookup repeated or reordered: %v", events)
			}
			var frame struct {
				Meta envelope.Meta   `json:"meta"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
				t.Fatal(err)
			}
			var actual envelope.AuthWhoamiOutput
			if err := json.Unmarshal(frame.Data, &actual); err != nil {
				t.Fatal(err)
			}
			if actual.User.AccountID.Value != "native-id" || !actual.User.EmailAddress.Set || !actual.User.EmailAddress.Null || string(actual.User.Extra["future"]) != `{"large":9007199254740993}` {
				t.Fatal("native identity replaced or discarded")
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.selected || tc.preview {
				if string(contents) != original {
					t.Fatal("ordinary read or preview published settings")
				}
			} else {
				loaded, err := cfg.Load(cfg.Options{Path: path, Environ: func() []string {
					return []string{"JIRA_BASE_URL=" + scope.BaseURL, "JIRA_EMAIL=" + scope.Email, "JIRA_CLOUD_ID=" + scope.CloudID}
				}})
				if err != nil || loaded.Config.CachedAccountID() != "native-id" || loaded.Config.Aliases["Mixed.Name"] != "version" {
					t.Fatalf("saved identity unbound or unrelated alias lost: %+v %v", loaded, err)
				}
			}
			if tc.selected {
				var saved envelope.AuthWhoamiSaveOutput
				if err := json.Unmarshal(frame.Data, &saved); err != nil {
					t.Fatal(err)
				}
				if frame.Meta.Command != "auth.whoami.save" || saved.DryRun != tc.preview || saved.Scope != scope || !saved.Settings.WouldChange || saved.Settings.Saved == tc.preview || saved.Settings.Changed == tc.preview {
					t.Fatalf("save receipt falsely claims publication: %+v", saved)
				}
			} else if frame.Meta.Command != "auth.whoami" || bytes.Contains(frame.Data, []byte(`"dry_run"`)) || bytes.Contains(frame.Data, []byte(`"settings"`)) {
				t.Fatal("ordinary read inherited mutation shape")
			}
		})
	}
}

func TestWhoamiSaveScopeRefusalPrecedesRemoteLookup(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	const original = "base_url='https://other.invalid'\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := Deps{
		Site: func(context.Context) (SiteInfo, error) {
			return SiteInfo{BaseURL: "https://example.invalid", Email: "credential@example.invalid", Token: "fixture-token"}, nil
		},
		IdentityTarget: func(ctx context.Context, scope core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
			return cfg.OpenAccountIdentityTarget(ctx, path, scope)
		},
		ClientFor: func(string, string, string, string) (core.JiraClient, error) {
			t.Fatal("conflicting scope constructed client")
			return nil, nil
		},
	}
	_, err := saveIdentity(t.Context(), deps, false)
	if !errors.Is(err, cfg.ErrAccountIdentityScope) || cli.MapError(err).Code != cli.CodeConfigInvalid {
		t.Fatal("scope refusal lost classification", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != original {
		t.Fatal("scope refusal changed original target")
	}
}

type identityUpdateProbe struct {
	receipt core.AccountIdentityPublication
	save    func(context.Context) (core.AccountIdentityPublication, error)
}

func (u identityUpdateProbe) Receipt() core.AccountIdentityPublication { return u.receipt }
func (u identityUpdateProbe) Save(ctx context.Context) (core.AccountIdentityPublication, error) {
	return u.save(ctx)
}

type identityTargetProbe struct {
	prepare func(string) (core.AccountIdentityUpdate, error)
}

func (p identityTargetProbe) Prepare(id string) (core.AccountIdentityUpdate, error) {
	return p.prepare(id)
}

func TestWhoamiSavePreservesAcknowledgedAndUnknownPublicationOnFailure(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"unknown", "written", "not_saved"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			cause := errors.New("synthetic publication failure")
			writes := 0
			deps := Deps{
				Site: func(context.Context) (SiteInfo, error) {
					return SiteInfo{BaseURL: "https://example.invalid", Email: "credential@example.invalid", Token: "fixture-token"}, nil
				},
				ClientFor: func(string, string, string, string) (core.JiraClient, error) {
					return &fakeClient{accountID: "native-id"}, nil
				},
				IdentityTarget: func(context.Context, core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
					return identityTargetProbe{prepare: func(id string) (core.AccountIdentityUpdate, error) {
						if id != "native-id" {
							t.Fatal("cached/configured ID replaced verified reply")
						}
						return identityUpdateProbe{receipt: core.AccountIdentityPublication{Path: "owned-settings.toml", WouldChange: true, Status: "prepared"}, save: func(ctx context.Context) (core.AccountIdentityPublication, error) {
							writes++
							if ctx.Err() != nil {
								t.Fatal("publisher entered canceled")
							}
							return core.AccountIdentityPublication{Path: "owned-settings.toml", WouldChange: true, Changed: status == "written", Created: status == "written", Status: status}, cause
						}}, nil
					}}, nil
				},
			}
			c := newWhoami(deps)
			c.SetContext(t.Context())
			if err := c.Flags().Set("save", "true"); err != nil {
				t.Fatal(err)
			}
			err := c.RunE(c, nil)
			var retained *cli.ResultError
			if !errors.Is(err, cause) || !errors.As(err, &retained) || !retained.Mutation || retained.Op != "auth.whoami.save" || writes != 1 {
				t.Fatalf("publication failure discarded or replayed: %+v %v writes=%d", retained, err, writes)
			}
			out, ok := retained.Data.(envelope.AuthWhoamiSaveOutput)
			if !ok || out.User.AccountID.Value != "native-id" || out.Settings.Status != status || out.Settings.Saved || out.Settings.Changed != (status == "written") {
				t.Fatalf("actual publication outcome replaced: %+v", out)
			}
		})
	}
}

func TestWhoamiSaveRefusesInvalidIdentityBeforePreparingCandidate(t *testing.T) {
	t.Parallel()
	for _, user := range []*jira.UserDetails{nil, {}, {AccountID: jira.NullableNull[string]()}, {AccountID: jira.NullableOf("")}, {AccountID: jira.NullableOf(" \t")}, {AccountID: jira.Nullable[string]{Value: "populated-but-absent"}}} {
		t.Run("native identity", func(t *testing.T) {
			t.Parallel()
			deps := Deps{
				Site: func(context.Context) (SiteInfo, error) {
					return SiteInfo{BaseURL: "https://example.invalid", Email: "credential@example.invalid", Token: "fixture-token"}, nil
				},
				ClientFor: func(string, string, string, string) (core.JiraClient, error) {
					return savingIdentityClient{user: core.UserRead{User: user}}, nil
				},
				IdentityTarget: func(context.Context, core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
					return identityTargetProbe{prepare: func(string) (core.AccountIdentityUpdate, error) {
						t.Fatal("invalid native identity prepared publication")
						return nil, nil
					}}, nil
				},
			}
			out, err := saveIdentity(t.Context(), deps, false)
			if !errors.Is(err, core.ErrCurrentUserResponseInvalid) || out.User.AccountID.Set || out.Settings.Saved {
				t.Fatalf("invalid native reply accepted: %+v %v", out, err)
			}
		})
	}
}

func TestWhoamiSaveCancellationStopsUnstartedWorkAndRetainsAcknowledgedSave(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"before", "site", "target", "client", "lookup", "prepare", "receipt", "save acknowledged", "save unknown"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			events := []string{}
			enter := func(name string) {
				events = append(events, name)
				if name == phase {
					cancel()
				}
			}
			if phase == "before" {
				cancel()
			}
			deps := Deps{
				Site: func(context.Context) (SiteInfo, error) {
					enter("site")
					return SiteInfo{BaseURL: "https://example.invalid", Email: "credential@example.invalid", Token: "fixture-token"}, nil
				},
				ClientFor: func(string, string, string, string) (core.JiraClient, error) {
					enter("client")
					return savingIdentityClient{user: core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("native-id")}}, lookup: func() { enter("lookup") }}, nil
				},
				IdentityTarget: func(context.Context, core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
					enter("target")
					return identityTargetProbe{prepare: func(string) (core.AccountIdentityUpdate, error) {
						enter("prepare")
						return cancelIdentityUpdate{receipt: func() core.AccountIdentityPublication {
							enter("receipt")
							return core.AccountIdentityPublication{Path: "owned-settings.toml", WouldChange: true, Status: "prepared"}
						}, save: func(actual context.Context) (core.AccountIdentityPublication, error) {
							if actual != ctx || actual.Err() != nil {
								t.Fatal("publication lost context or entered canceled")
							}
							enter("save")
							cancel()
							if phase == "save unknown" {
								return core.AccountIdentityPublication{Path: "owned-settings.toml", Status: "unknown"}, context.Canceled
							}
							return core.AccountIdentityPublication{Path: "owned-settings.toml", Changed: true, Saved: true, Status: "saved"}, nil
						}}, nil
					}}, nil
				},
			}
			out, err := saveIdentity(ctx, deps, false)
			if phase == "save acknowledged" {
				if err != nil || !out.Settings.Saved || !out.Settings.Changed || out.Settings.Status != "saved" {
					t.Fatalf("acknowledged save replaced by later cancellation: %+v %v", out, err)
				}
			} else if !errors.Is(err, context.Canceled) || out.Settings.Saved || out.Settings.Changed {
				t.Fatalf("canceled operation lost cause or fabricated effects: %+v %v", out, err)
			}
			if phase == "save unknown" && out.Settings.Status != "unknown" {
				t.Fatal("cancellation discarded unknown publisher outcome")
			}
			stages := []string{"site", "target", "client", "lookup", "prepare", "receipt", "save"}
			want := []string{}
			if phase != "before" {
				for _, stage := range stages {
					want = append(want, stage)
					if stage == phase {
						break
					}
				}
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("cancellation entered unstarted work: %v, want %v", events, want)
			}
		})
	}
}

type cancelIdentityUpdate struct {
	receipt func() core.AccountIdentityPublication
	save    func(context.Context) (core.AccountIdentityPublication, error)
}

func (u cancelIdentityUpdate) Receipt() core.AccountIdentityPublication { return u.receipt() }
func (u cancelIdentityUpdate) Save(ctx context.Context) (core.AccountIdentityPublication, error) {
	return u.save(ctx)
}
