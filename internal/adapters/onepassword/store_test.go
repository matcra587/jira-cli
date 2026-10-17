package onepassword

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/1password/onepassword-sdk-go"

	"github.com/matcra587/jira-cli/internal/core"
)

type fakeClient struct {
	mu      sync.Mutex
	vaults  []sdk.VaultOverview
	items   []sdk.ItemOverview
	item    sdk.Item
	calls   []string
	created []sdk.ItemCreateParams
	puts    []sdk.Item
	hook    func(context.Context, string) error
}

func fixtureClient() *fakeClient {
	section := "custom-section"
	return &fakeClient{
		vaults: []sdk.VaultOverview{{ID: "vault-id", Title: "selected vault"}},
		items:  []sdk.ItemOverview{{ID: "item-id", VaultID: "vault-id", Title: "selected item"}},
		item: sdk.Item{
			ID: "item-id", VaultID: "vault-id", Title: "selected item", Category: sdk.ItemCategoryLogin,
			Fields: []sdk.ItemField{
				{ID: "unmanaged", Title: CredentialFieldID, FieldType: sdk.ItemFieldTypeText, Value: "retained-unmanaged", SectionID: &section},
				{ID: CredentialFieldID, Title: "preserve owned title", FieldType: sdk.ItemFieldTypeConcealed, Value: "synthetic-original", SectionID: &section},
				{ID: "last-unmanaged", Title: "keep", FieldType: sdk.ItemFieldTypeConcealed, Value: "retained-other-secret"},
			},
			Sections: []sdk.ItemSection{{ID: section, Title: "retain section"}}, Notes: "retain notes", Tags: []string{"retain tag"},
			Websites: []sdk.Website{{URL: "https://example.invalid", Label: "retain label", AutofillBehavior: sdk.AutofillBehaviorNever}},
			Version:  19, Files: []sdk.ItemFile{{Attributes: sdk.FileAttributes{ID: "file-id", Name: "retained.txt", Size: 29}, SectionID: section, FieldID: "file-field"}},
			Document:  &sdk.FileAttributes{ID: "document-id", Name: "document.txt", Size: 37},
			CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(200, 0).UTC(),
		},
	}
}

func (f *fakeClient) call(ctx context.Context, name string) error {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
	if f.hook != nil {
		return f.hook(ctx, name)
	}
	return nil
}

func (f *fakeClient) ListVaults(ctx context.Context) ([]sdk.VaultOverview, error) {
	return f.vaults, f.call(ctx, "list vaults")
}

func (f *fakeClient) ListItems(ctx context.Context, vault string) ([]sdk.ItemOverview, error) {
	if vault != "vault-id" {
		return nil, errors.New("wrong selected vault")
	}
	return f.items, f.call(ctx, "list items")
}

func (f *fakeClient) GetItem(ctx context.Context, vault, item string) (sdk.Item, error) {
	if vault != "vault-id" || item != "item-id" {
		return sdk.Item{}, errors.New("wrong selected item")
	}
	return f.item, f.call(ctx, "get item")
}

func (f *fakeClient) CreateItem(ctx context.Context, params sdk.ItemCreateParams) (sdk.Item, error) {
	f.created = append(f.created, params)
	return sdk.Item{ID: "created-id", VaultID: params.VaultID, Title: params.Title, Fields: params.Fields}, f.call(ctx, "create item")
}

func (f *fakeClient) PutItem(ctx context.Context, item sdk.Item) (sdk.Item, error) {
	f.puts = append(f.puts, item)
	return item, f.call(ctx, "put item")
}

func fixtureConfig(client Client) Config {
	return Config{
		Account: "synthetic desktop account", Vault: "selected vault", Item: "selected item", IntegrationVersion: "test-version",
		LookupEnv:     func(string) (string, bool) { return "", false },
		ClientFactory: func(context.Context, ClientOptions) (Client, error) { return client, nil },
	}
}

func assertBackendFailure(t *testing.T, err error, attempted bool) {
	t.Helper()
	var classified *core.CredentialStoreError
	if !errors.As(err, &classified) || classified.Backend != core.CredentialBackendOnePassword || classified.Attempted != attempted {
		t.Fatalf("incorrect backend outcome: error=%v typed=%#v attempted=%t", err, classified, attempted)
	}
}

func TestStoreConstructionAndAuthenticationAreLazy(t *testing.T) {
	for _, tc := range []struct {
		name, token, account string
	}{
		{name: "service account wins", token: "synthetic-service-token", account: "ignored desktop"},
		{name: "desktop account", account: "selected desktop"},
		{name: "missing authentication"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fixtureClient()
			config := fixtureConfig(client)
			config.Account = tc.account
			lookups, initializations := 0, 0
			config.LookupEnv = func(key string) (string, bool) {
				lookups++
				if key != "OP_SERVICE_ACCOUNT_TOKEN" {
					t.Errorf("unexpected environment lookup: %s", key)
				}
				return tc.token, tc.token != ""
			}
			config.ClientFactory = func(ctx context.Context, options ClientOptions) (Client, error) {
				initializations++
				if ctx != t.Context() || options.ServiceAccountToken != tc.token || options.IntegrationVersion != "test-version" {
					t.Error("SDK construction changed context, authentication or integration version")
				}
				if tc.token != "" && options.Account != "" || tc.token == "" && options.Account != tc.account {
					t.Error("service and desktop authentication were mixed")
				}
				return client, nil
			}
			store := New(config)
			if lookups != 0 || initializations != 0 || store.CredentialBackend() != core.CredentialBackendOnePassword {
				t.Fatal("construction eagerly resolved authentication or lost backend identity")
			}
			value, err := store.Get(t.Context(), "default")
			if tc.account == "" && tc.token == "" {
				assertBackendFailure(t, err, false)
				if initializations != 0 || len(client.calls) != 0 || value != "" {
					t.Fatal("missing authentication reached the SDK")
				}
				return
			}
			if err != nil || value != "synthetic-original" {
				t.Fatalf("owned field was not read: error=%v value_matches=%t", err, value == "synthetic-original")
			}
			if err := store.Delete(t.Context(), "default"); err != nil || initializations != 1 || lookups != 1 {
				t.Fatalf("client was not reused: error=%v initialization=%d lookups=%d", err, initializations, lookups)
			}
		})
	}
}

func TestStoreChangesOnlyOwnedField(t *testing.T) {
	for _, operation := range []string{"update", "conceal", "add", "delete", "failed update"} {
		t.Run(operation, func(t *testing.T) {
			client := fixtureClient()
			if operation == "add" {
				client.item.Fields = slices.Delete(slices.Clone(client.item.Fields), 1, 2)
			}
			if operation == "conceal" {
				client.item.Fields[1].FieldType = sdk.ItemFieldTypeText
			}
			original := client.item
			original.Fields = slices.Clone(original.Fields)
			want := original
			want.Fields = slices.Clone(original.Fields)
			var cause error
			switch operation {
			case "update", "conceal", "failed update":
				want.Fields[1].Value = "synthetic-replacement"
				want.Fields[1].FieldType = sdk.ItemFieldTypeConcealed
			case "add":
				want.Fields = append(want.Fields, sdk.ItemField{ID: CredentialFieldID, Title: CredentialFieldID, Value: "synthetic-replacement", FieldType: sdk.ItemFieldTypeConcealed})
			case "delete":
				want.Fields = []sdk.ItemField{original.Fields[0], original.Fields[2]}
			}
			if operation == "failed update" {
				cause = errors.New("synthetic SDK write failure")
				client.hook = func(_ context.Context, operation string) error {
					if operation == "put item" {
						return cause
					}
					return nil
				}
			}
			store := New(fixtureConfig(client))
			var err error
			if operation == "delete" {
				err = store.Delete(t.Context(), "default")
			} else {
				err = store.Set(t.Context(), "default", "synthetic-replacement")
			}
			if cause != nil {
				assertBackendFailure(t, err, true)
				if !errors.Is(err, cause) {
					t.Fatal("SDK cause lost")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(client.puts) != 1 || len(client.created) != 0 || !reflect.DeepEqual(client.puts[0], want) {
				t.Fatalf("whole-item preimage was not preserved around owned edit: puts=%d creates=%d", len(client.puts), len(client.created))
			}
			if !reflect.DeepEqual(client.item, original) {
				t.Fatal("editing SDK value mutated the original item's backing fields")
			}
		})
	}
}

func TestStoreMissingItemAndFieldAreExplicitAndIdempotent(t *testing.T) {
	for _, missingItem := range []bool{false, true} {
		name := "missing owned field"
		if missingItem {
			name = "missing item"
		}
		t.Run(name, func(t *testing.T) {
			client := fixtureClient()
			if missingItem {
				client.items = nil
			} else {
				client.item.Fields = client.item.Fields[:1]
			}
			store := New(fixtureConfig(client))
			if value, err := store.Get(t.Context(), "default"); !errors.Is(err, core.ErrNoCredential) || value != "" {
				t.Fatalf("explicit absence changed: secret_present=%t error=%v", value != "", err)
			}
			if err := store.Delete(t.Context(), "default"); err != nil || len(client.puts) != 0 || len(client.created) != 0 {
				t.Fatal("idempotent removal mutated an absent owned field")
			}
			if err := store.Set(t.Context(), "default", "synthetic-created-record"); err != nil {
				t.Fatal(err)
			}
			if missingItem {
				sectionID := "jira-cli"
				want := sdk.ItemCreateParams{
					VaultID: "vault-id", Title: "selected item", Category: sdk.ItemCategoryAPICredentials,
					Fields:   []sdk.ItemField{{ID: CredentialFieldID, Title: CredentialFieldID, FieldType: sdk.ItemFieldTypeConcealed, Value: "synthetic-created-record", SectionID: &sectionID}},
					Sections: []sdk.ItemSection{{ID: "jira-cli", Title: "Jira CLI"}}, Tags: []string{"jira-cli"},
				}
				if len(client.created) != 1 || len(client.puts) != 0 || !reflect.DeepEqual(client.created[0], want) {
					t.Fatal("missing selected item lost its API Credentials category, Jira CLI section, jira-cli tag or owned concealed field")
				}
			} else if len(client.puts) != 1 || len(client.created) != 0 {
				t.Fatal("missing owned field created a second item")
			}
		})
	}
}

func TestStorePreservesExistingItemCategory(t *testing.T) {
	for _, category := range []sdk.ItemCategory{sdk.ItemCategorySecureNote, sdk.ItemCategoryAPICredentials, sdk.ItemCategoryLogin} {
		t.Run(strings.ToLower(string(category)), func(t *testing.T) {
			client := fixtureClient()
			client.item.Category = category
			store := New(fixtureConfig(client))
			if value, err := store.Get(t.Context(), "default"); err != nil || value != "synthetic-original" {
				t.Fatal("existing item category prevented reading the owned credential")
			}
			if err := store.Set(t.Context(), "default", "synthetic-replacement"); err != nil {
				t.Fatal(err)
			}
			if err := store.Delete(t.Context(), "default"); err != nil {
				t.Fatal(err)
			}
			if len(client.created) != 0 || len(client.puts) != 2 {
				t.Fatal("existing item was recreated instead of updating its owned field")
			}
			for _, item := range client.puts {
				if item.Category != category {
					t.Fatal("changing the owned field replaced the existing item category")
				}
			}
		})
	}
}

func TestStoreExactIDsPrecedeNamesAndAmbiguityRefusesMutation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		alter   func(*fakeClient, *Config)
		success bool
	}{
		{name: "exact IDs", success: true, alter: func(c *fakeClient, config *Config) {
			config.Vault, config.Item = "vault-id", "item-id"
			c.vaults = append(c.vaults, sdk.VaultOverview{ID: "other-vault", Title: "vault-id"})
			c.items = append(c.items, sdk.ItemOverview{ID: "other-item", Title: "item-id"})
		}},
		{name: "ambiguous vault", alter: func(c *fakeClient, _ *Config) {
			c.vaults = append(c.vaults, sdk.VaultOverview{ID: "other-vault", Title: "selected vault"})
		}},
		{name: "ambiguous item", alter: func(c *fakeClient, _ *Config) {
			c.items = append(c.items, sdk.ItemOverview{ID: "other-item", Title: "selected item"})
		}},
		{name: "missing vault", alter: func(c *fakeClient, _ *Config) { c.vaults = nil }},
		{name: "duplicate owned fields", alter: func(c *fakeClient, _ *Config) { c.item.Fields = append(c.item.Fields, c.item.Fields[1]) }},
		{name: "wrong returned scope", alter: func(c *fakeClient, _ *Config) { c.item.VaultID = "other-vault" }},
		{name: "coordinate without ID", alter: func(c *fakeClient, _ *Config) { c.items[0].ID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, operation := range []string{"read", "save", "delete"} {
				client := fixtureClient()
				config := fixtureConfig(client)
				tc.alter(client, &config)
				store := New(config)
				var err error
				switch operation {
				case "read":
					_, err = store.Get(t.Context(), "default")
				case "save":
					err = store.Set(t.Context(), "default", "synthetic-record")
				case "delete":
					err = store.Delete(t.Context(), "default")
				}
				if tc.success {
					if err != nil {
						t.Fatal(err)
					}
					continue
				}
				assertBackendFailure(t, err, false)
				if errors.Is(err, core.ErrNoCredential) || len(client.puts) != 0 || len(client.created) != 0 {
					t.Fatalf("ambiguous/invalid coordinate became absence or mutation: operation=%s", operation)
				}
			}
		})
	}
}

func TestStoreSDKReadFailuresNeverCreateAndRemainSanitized(t *testing.T) {
	for _, stage := range []string{"initialize", "list vaults", "list items", "get item"} {
		t.Run(stage, func(t *testing.T) {
			client := fixtureClient()
			cause := errors.New("synthetic-secret-reference and synthetic-service-token")
			client.hook = func(_ context.Context, operation string) error {
				if operation == stage {
					return cause
				}
				return nil
			}
			config := fixtureConfig(client)
			if stage == "initialize" {
				config.ClientFactory = func(context.Context, ClientOptions) (Client, error) { return nil, cause }
			}
			err := New(config).Set(t.Context(), "default", "synthetic-record")
			assertBackendFailure(t, err, false)
			if !errors.Is(err, cause) || strings.Contains(err.Error(), "synthetic-") || errors.Is(err, core.ErrNoCredential) || len(client.created) != 0 || len(client.puts) != 0 {
				t.Fatal("SDK failure leaked, lost its cause, or fabricated missing item")
			}
		})
	}
}

func TestStoreCancellationStopsReadsAndUnstartedWrites(t *testing.T) {
	for _, operation := range []string{"read", "save", "delete"} {
		for _, stage := range []string{"before", "initialize", "list vaults", "list items", "get item"} {
			t.Run(operation+" "+stage, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if stage == "before" {
					cancel()
				}
				client := fixtureClient()
				cause := errors.New("SDK lost cancellation cause")
				client.hook = func(observed context.Context, name string) error {
					if observed != ctx {
						t.Error("SDK operation lost caller context")
					}
					if name == stage {
						cancel()
						return cause
					}
					return nil
				}
				config := fixtureConfig(client)
				initializations := 0
				config.ClientFactory = func(observed context.Context, _ ClientOptions) (Client, error) {
					initializations++
					if observed != ctx {
						t.Error("SDK initialization lost caller context")
					}
					if stage == "initialize" {
						cancel()
						return nil, cause
					}
					return client, nil
				}
				store := New(config)
				var err error
				var secret string
				switch operation {
				case "read":
					secret, err = store.Get(ctx, "default")
				case "save":
					err = store.Set(ctx, "default", "synthetic-replacement")
				case "delete":
					err = store.Delete(ctx, "default")
				}
				assertBackendFailure(t, err, false)
				if !errors.Is(err, context.Canceled) || secret != "" || len(client.created) != 0 || len(client.puts) != 0 {
					t.Fatal("cancellation disclosed a credential or reached mutation")
				}
				if stage == "before" && (initializations != 0 || len(client.calls) != 0) {
					t.Fatal("canceled operation entered SDK")
				}
				if stage != "before" && !errors.Is(err, cause) {
					t.Fatal("normalized cancellation lost inspectable SDK cause")
				}
			})
		}
	}
}

func TestStoreStartedWritesRetainSDKOutcome(t *testing.T) {
	for _, operation := range []string{"create", "update", "delete"} {
		for _, successful := range []bool{false, true} {
			name := operation + " failure"
			if successful {
				name = operation + " success"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				client := fixtureClient()
				if operation == "create" {
					client.items = nil
				}
				cause := errors.New("SDK lost cancellation during admitted write")
				client.hook = func(_ context.Context, stage string) error {
					if stage == "put item" || stage == "create item" {
						cancel()
						if !successful {
							return cause
						}
					}
					return nil
				}
				store := New(fixtureConfig(client))
				var err error
				if operation == "delete" {
					err = store.Delete(ctx, "default")
				} else {
					err = store.Set(ctx, "default", "synthetic-record")
				}
				if len(client.puts)+len(client.created) != 1 {
					t.Fatal("write was not admitted exactly once")
				}
				if successful {
					if err != nil {
						t.Fatalf("actual SDK success was overwritten: %v", err)
					}
					return
				}
				assertBackendFailure(t, err, true)
				if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
					t.Fatal("uncertain admitted write lost cancellation or native cause")
				}
			})
		}
	}
}

func TestStoreConcurrentReadsInitializeOneClient(t *testing.T) {
	client := fixtureClient()
	config := fixtureConfig(client)
	var initializations atomic.Int64
	config.ClientFactory = func(context.Context, ClientOptions) (Client, error) { initializations.Add(1); return client, nil }
	store := New(config)
	var wait sync.WaitGroup
	for range 16 {
		wait.Go(func() {
			if value, err := store.Get(t.Context(), "default"); err != nil || value != "synthetic-original" {
				t.Errorf("concurrent read failed: %v", err)
			}
		})
	}
	wait.Wait()
	if initializations.Load() != 1 || len(client.calls) != 48 {
		t.Fatalf("lazy initialization/serialized operations changed: initializations=%d calls=%d", initializations.Load(), len(client.calls))
	}
}

func TestStoreInitializationIsSerializedAcrossStores(t *testing.T) {
	client := fixtureClient()
	var active, peak, initializations atomic.Int64
	config := fixtureConfig(client)
	config.ClientFactory = func(context.Context, ClientOptions) (Client, error) {
		initializations.Add(1)
		current := active.Add(1)
		for previous := peak.Load(); current > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, current) {
				break
			}
		}
		// Offer other callers a chance to enter the otherwise unsafe SDK core.
		runtime.Gosched()
		active.Add(-1)
		return client, nil
	}
	var wait sync.WaitGroup
	for range 16 {
		wait.Go(func() {
			if _, err := New(config).Get(t.Context(), "default"); err != nil {
				t.Error(err)
			}
		})
	}
	wait.Wait()
	if peak.Load() != 1 || initializations.Load() != 16 {
		t.Fatalf("unsafe cross-store construction: peak=%d initializations=%d", peak.Load(), initializations.Load())
	}
}

func TestStoreExpiredAndUnsupportedOperationsNeverInitialize(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "unsupported account"
		if expired {
			name = "expired deadline"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			account := "unsupported"
			if expired {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Unix(0, 0))
				defer cancel()
				account = "default"
			}
			config := fixtureConfig(nil)
			config.LookupEnv = func(string) (string, bool) { t.Error("rejected operation resolved authentication"); return "", false }
			config.ClientFactory = func(context.Context, ClientOptions) (Client, error) {
				t.Error("rejected operation initialized SDK")
				return nil, nil
			}
			store := New(config)
			operations := []func() error{func() error { _, err := store.Get(ctx, account); return err }, func() error { return store.Set(ctx, account, "synthetic-record") }, func() error { return store.Delete(ctx, account) }}
			for _, operation := range operations {
				err := operation()
				assertBackendFailure(t, err, false)
				if expired && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("expired operation lost deadline")
				}
			}
		})
	}
}

func TestStoreCanceledSuccessfulReadNeverReturnsCredential(t *testing.T) {
	for _, stage := range []string{"initialize", "list vaults", "list items", "get item"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := fixtureClient()
			client.hook = func(_ context.Context, name string) error {
				if name == stage {
					cancel()
				}
				return nil
			}
			config := fixtureConfig(client)
			config.ClientFactory = func(context.Context, ClientOptions) (Client, error) {
				if stage == "initialize" {
					cancel()
				}
				return client, nil
			}
			value, err := New(config).Get(ctx, "default")
			assertBackendFailure(t, err, false)
			if !errors.Is(err, context.Canceled) || value != "" {
				t.Fatal("successful SDK read escaped caller cancellation")
			}
			wantCalls := map[string]int{"initialize": 0, "list vaults": 1, "list items": 2, "get item": 3}[stage]
			if len(client.calls) != wantCalls {
				t.Fatalf("canceled successful SDK read admitted another API: calls=%v", client.calls)
			}
		})
	}
}

func TestStoreDesktopAccountSwitchRefusesBeforeSDKInitialization(t *testing.T) {
	if err := sdkInitialization.Acquire(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	previous := desktopAccount
	desktopAccount = "synthetic pinned account"
	sdkInitialization.Release(1)
	t.Cleanup(func() {
		if !sdkInitialization.TryAcquire(1) {
			t.Error("SDK admission gate remained held after test")
			return
		}
		desktopAccount = previous
		sdkInitialization.Release(1)
	})
	config := fixtureConfig(nil)
	config.Account = "synthetic other account"
	config.ClientFactory = nil
	value, err := New(config).Get(t.Context(), "default")
	assertBackendFailure(t, err, false)
	if value != "" || !strings.Contains(errors.Unwrap(err).Error(), "already pinned") {
		t.Fatal("SDK desktop core accepted an account switch")
	}
}

// admissionContext observes admission within one test operation; it is not a
// production adapter or retained request context.
//
//nolint:containedctx // A test-only Context decorator needs its parent Context.
type admissionContext struct {
	context.Context
	entered  chan struct{}
	once     sync.Once
	calls    atomic.Int64
	signalAt int64
}

func (c *admissionContext) Err() error {
	err := c.Context.Err()
	target := c.signalAt
	if target == 0 {
		target = 1
	}
	if c.calls.Add(1) == target {
		c.once.Do(func() { close(c.entered) })
	}
	return err
}

func TestStoreQueuedReadCanCancelWithoutWaitingForAdmittedRead(t *testing.T) {
	client := fixtureClient()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	client.hook = func(_ context.Context, stage string) error {
		if stage == "get item" {
			once.Do(func() { close(entered); <-release })
		}
		return nil
	}
	store := New(fixtureConfig(client))
	firstDone := make(chan error, 1)
	go func() { _, err := store.Get(t.Context(), "default"); firstDone <- err }()
	<-entered
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	queued := &admissionContext{Context: parent, entered: make(chan struct{})}
	secondDone := make(chan error, 1)
	go func() { _, err := store.Get(queued, "default"); secondDone <- err }()
	<-queued.entered
	cancel()
	select {
	case err := <-secondDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("queued cancellation lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("queued operation waited for an unrelated admitted SDK read")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 3 {
		t.Fatalf("queued canceled operation entered SDK: calls=%d", len(client.calls))
	}
}

func TestStoreQueuedInitializationCanCancelAcrossStores(t *testing.T) {
	client := fixtureClient()
	entered, release := make(chan struct{}), make(chan struct{})
	firstConfig := fixtureConfig(client)
	firstConfig.ClientFactory = func(context.Context, ClientOptions) (Client, error) {
		close(entered)
		<-release
		return client, nil
	}
	firstDone := make(chan error, 1)
	go func() { _, err := New(firstConfig).Get(t.Context(), "default"); firstDone <- err }()
	<-entered
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Signal after the second Store's admission and initialization preflight
	// have observed a live context, while the first Store still owns the core.
	queued := &admissionContext{Context: parent, entered: make(chan struct{}), signalAt: 3}
	secondConfig := fixtureConfig(client)
	secondConfig.ClientFactory = func(context.Context, ClientOptions) (Client, error) {
		t.Error("canceled queued initializer entered SDK")
		return client, nil
	}
	secondDone := make(chan error, 1)
	go func() { _, err := New(secondConfig).Get(queued, "default"); secondDone <- err }()
	<-queued.entered
	cancel()
	returned := false
	select {
	case err := <-secondDone:
		returned = true
		if !errors.Is(err, context.Canceled) {
			t.Errorf("queued initializer lost cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("queued initializer waited for another Store's admitted initialization")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if !returned {
		if err := <-secondDone; !errors.Is(err, context.Canceled) {
			t.Errorf("queued initializer lost cancellation: %v", err)
		}
	}
	if len(client.calls) != 3 {
		t.Fatalf("queued initializer entered SDK operations: calls=%d", len(client.calls))
	}
}
