// Package onepassword stores the CLI-owned credential field through the official
// 1Password SDK. Record encoding and Jira site/account binding belong to the
// credentials and configuration owners.
package onepassword

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"

	sdk "github.com/1password/onepassword-sdk-go"
	xslices "github.com/gechr/x/slices"
	xstrings "github.com/gechr/x/strings"
	"golang.org/x/sync/semaphore"

	"github.com/matcra587/jira-cli/internal/core"
)

// CredentialFieldID identifies ownership independently of item titles or tags.
const CredentialFieldID = "jira-cli-credential"

// Client exposes only the SDK operations needed by this adapter. Whole-item
// deletion is deliberately absent: logout removes only the owned field.
type Client interface {
	ListVaults(context.Context) ([]sdk.VaultOverview, error)
	ListItems(context.Context, string) ([]sdk.ItemOverview, error)
	GetItem(context.Context, string, string) (sdk.Item, error)
	CreateItem(context.Context, sdk.ItemCreateParams) (sdk.Item, error)
	PutItem(context.Context, sdk.Item) (sdk.Item, error)
}

// ClientOptions carries authentication only during lazy SDK construction. The
// service token comes exclusively from OP_SERVICE_ACCOUNT_TOKEN, never Config.
type ClientOptions struct {
	Account             string
	ServiceAccountToken string
	IntegrationVersion  string
}

// Config selects an exact vault/item ID or unique exact name. Optional seams
// allow hermetic tests without initializing the SDK or reading native secrets.
type Config struct {
	Account            string
	Vault              string
	Item               string
	IntegrationVersion string
	ClientFactory      func(context.Context, ClientOptions) (Client, error)
	LookupEnv          func(string) (string, bool)
}

// Store serializes its operations because the SDK's desktop session renewal
// mutates client state. Queued operations can cancel without a worker goroutine.
type Store struct {
	config Config
	gate   *semaphore.Weighted
	client Client
}

var (
	_ core.CredentialStore           = (*Store)(nil)
	_ core.CredentialBackendProvider = (*Store)(nil)
	// v0.4.1 initializes both core globals without synchronization. Never race
	// SDK constructors, including constructors of different Store instances.
	sdkInitialization = semaphore.NewWeighted(1)
	// The desktop core pins its first account even if client initialization
	// subsequently fails. Refuse an account switch rather than use that core
	// against a different selected account. Protected by sdkInitialization.
	desktopAccount string
)

// New constructs no SDK client and does not resolve authentication.
func New(config Config) *Store {
	return &Store{config: config, gate: semaphore.NewWeighted(1)}
}

func (*Store) CredentialBackend() core.CredentialBackend { return core.CredentialBackendOnePassword }

// Get reads only the field with the stable ownership ID. The absence of a
// successfully enumerated item or owned field is ordinary credential absence.
func (s *Store) Get(ctx context.Context, account string) (string, error) {
	if err := s.admit(ctx, account, "read"); err != nil {
		return "", err
	}
	defer s.gate.Release(1)
	_, item, found, err := s.selectedItem(ctx, "read")
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", failure(ctx, "read", err, false)
	}
	if !found {
		return "", core.ErrNoCredential
	}
	index, err := ownedField(item)
	if err != nil {
		return "", failure(ctx, "read", err, false)
	}
	if index < 0 {
		return "", core.ErrNoCredential
	}
	if err := ctx.Err(); err != nil {
		return "", failure(ctx, "read", err, false)
	}
	return item.Fields[index].Value, nil
}

// Set upserts a concealed owned field. Existing item metadata and unmanaged
// fields survive; a missing API Credentials item is created only after successful
// enumeration. Existing items retain their category, including old Secure Notes.
func (s *Store) Set(ctx context.Context, account, secret string) error {
	if err := s.admit(ctx, account, "save"); err != nil {
		return err
	}
	defer s.gate.Release(1)
	vault, item, found, err := s.selectedItem(ctx, "save")
	if err != nil {
		return err
	}
	field := sdk.ItemField{ID: CredentialFieldID, Title: CredentialFieldID, FieldType: sdk.ItemFieldTypeConcealed, Value: secret}
	if !found {
		if err := ctx.Err(); err != nil {
			return failure(ctx, "save", err, false)
		}
		_, err = s.client.CreateItem(ctx, sdk.ItemCreateParams{VaultID: vault, Title: s.config.Item, Category: sdk.ItemCategoryAPICredentials, Fields: []sdk.ItemField{field}})
		if err != nil {
			return failure(ctx, "save", err, true)
		}
		return nil // An admitted write's actual acknowledgment survives cancellation.
	}
	index, err := ownedField(item)
	if err != nil {
		return failure(ctx, "save", err, false)
	}
	item.Fields = slices.Clone(item.Fields)
	if index < 0 {
		item.Fields = append(item.Fields, field)
	} else {
		item.Fields[index].Value = secret
		item.Fields[index].FieldType = sdk.ItemFieldTypeConcealed
		item.Fields[index].Details = nil
	}
	return s.put(ctx, "save", item)
}

// Delete removes only the owned field, leaving the item and every other field.
func (s *Store) Delete(ctx context.Context, account string) error {
	if err := s.admit(ctx, account, "delete"); err != nil {
		return err
	}
	defer s.gate.Release(1)
	_, item, found, err := s.selectedItem(ctx, "delete")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return failure(ctx, "delete", err, false)
	}
	if !found {
		return nil
	}
	index, err := ownedField(item)
	if err != nil {
		return failure(ctx, "delete", err, false)
	}
	if index < 0 {
		return nil
	}
	item.Fields = slices.Delete(slices.Clone(item.Fields), index, index+1)
	return s.put(ctx, "delete", item)
}

func (s *Store) admit(ctx context.Context, account, operation string) error {
	if err := ctx.Err(); err != nil {
		return failure(ctx, operation, err, false)
	}
	if account != "default" {
		return failure(ctx, operation, errors.New("unsupported credential account"), false)
	}
	if err := s.gate.Acquire(ctx, 1); err != nil {
		return failure(ctx, operation, err, false)
	}
	if err := ctx.Err(); err != nil {
		s.gate.Release(1)
		return failure(ctx, operation, err, false)
	}
	return nil
}

func (s *Store) initialize(ctx context.Context, operation string) error {
	if err := ctx.Err(); err != nil {
		return failure(ctx, operation, err, false)
	}
	if s.client != nil {
		return nil
	}
	if xstrings.AnyEmpty(strings.TrimSpace(s.config.Vault), strings.TrimSpace(s.config.Item)) {
		return failure(ctx, operation, errors.New("explicit vault and item are required"), false)
	}
	lookup := s.config.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	token, _ := lookup("OP_SERVICE_ACCOUNT_TOKEN")
	options := ClientOptions{IntegrationVersion: s.config.IntegrationVersion}
	if token != "" {
		options.ServiceAccountToken = token
	} else if strings.TrimSpace(s.config.Account) != "" {
		options.Account = s.config.Account
	} else {
		return failure(ctx, operation, errors.New("service-account authentication or a selected desktop account is required"), false)
	}
	factory := s.config.ClientFactory
	if factory == nil {
		factory = newSDKClient
	}
	if err := sdkInitialization.Acquire(ctx, 1); err != nil {
		return failure(ctx, operation, err, false)
	}
	defer sdkInitialization.Release(1)
	if err := ctx.Err(); err != nil {
		return failure(ctx, operation, err, false)
	}
	client, err := factory(ctx, options)
	if err != nil {
		return failure(ctx, operation, err, false)
	}
	if err := ctx.Err(); err != nil {
		return failure(ctx, operation, err, false)
	}
	if client == nil {
		return failure(ctx, operation, errors.New("SDK returned no client"), false)
	}
	s.client = client
	return nil
}

func (s *Store) selectedItem(ctx context.Context, operation string) (string, sdk.Item, bool, error) {
	vault, err := s.selectedVault(ctx, operation)
	if err != nil {
		return "", sdk.Item{}, false, err
	}
	return s.itemInVault(ctx, operation, vault, s.config.Item)
}

func (s *Store) selectedVault(ctx context.Context, operation string) (string, error) {
	if err := s.initialize(ctx, operation); err != nil {
		return "", err
	}
	vaults, err := s.client.ListVaults(ctx)
	if err = readFailure(ctx, operation, err); err != nil {
		return "", err
	}
	entries := xslices.Map(vaults, func(vault sdk.VaultOverview) coordinate { return coordinate{id: vault.ID, name: vault.Title} })
	vault, found, err := selectCoordinate(s.config.Vault, entries)
	if err != nil || !found {
		if err == nil {
			err = errors.New("selected vault was not found")
		}
		return "", failure(ctx, operation, err, false)
	}
	return vault, nil
}

func (s *Store) itemInVault(ctx context.Context, operation, vault, selector string) (string, sdk.Item, bool, error) {
	if err := readFailure(ctx, operation, nil); err != nil {
		return "", sdk.Item{}, false, err
	}
	items, err := s.client.ListItems(ctx, vault)
	if err = readFailure(ctx, operation, err); err != nil {
		return "", sdk.Item{}, false, err
	}
	entries := xslices.Map(items, func(item sdk.ItemOverview) coordinate { return coordinate{id: item.ID, name: item.Title} })
	itemID, found, err := selectCoordinate(selector, entries)
	if err != nil {
		return "", sdk.Item{}, false, failure(ctx, operation, err, false)
	}
	if !found {
		return vault, sdk.Item{}, false, nil
	}
	if err := readFailure(ctx, operation, nil); err != nil {
		return "", sdk.Item{}, false, err
	}
	item, err := s.client.GetItem(ctx, vault, itemID)
	if err = readFailure(ctx, operation, err); err != nil {
		return "", sdk.Item{}, false, err
	}
	if item.ID != itemID || item.VaultID != vault {
		return "", sdk.Item{}, false, failure(ctx, operation, errors.New("SDK item identity did not match the selected scope"), false)
	}
	return vault, item, true, nil
}

func (s *Store) put(ctx context.Context, operation string, item sdk.Item) error {
	_, err := s.putWithOutcome(ctx, operation, item)
	return err
}

func (s *Store) putWithOutcome(ctx context.Context, operation string, item sdk.Item) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, failure(ctx, operation, err, false)
	}
	_, err := s.client.PutItem(ctx, item)
	if err != nil {
		return true, failure(ctx, operation, err, true)
	}
	return true, nil
}

// Exact IDs take precedence over titles; ambiguous exact titles never choose an
// arbitrary resource. This is identity validation rather than fuzzy matching.
type coordinate struct{ id, name string }

func selectCoordinate(selector string, entries []coordinate) (string, bool, error) {
	for _, matchID := range []bool{true, false} {
		selected := ""
		count := 0
		for _, entry := range entries {
			if matchID && entry.id == selector || !matchID && entry.name == selector {
				count++
				selected = entry.id
			}
		}
		if count > 1 {
			return "", false, errors.New("selected coordinate is ambiguous; use an exact ID")
		}
		if count == 1 {
			if selected == "" {
				return "", false, errors.New("SDK coordinate has no ID")
			}
			return selected, true, nil
		}
	}
	return "", false, nil
}

func ownedField(item sdk.Item) (int, error) {
	index := -1
	for i, field := range item.Fields {
		if field.ID == CredentialFieldID {
			if index >= 0 {
				return -1, errors.New("duplicate owned credential field IDs")
			}
			index = i
		}
	}
	return index, nil
}

func readFailure(ctx context.Context, operation string, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return failure(ctx, operation, errors.Join(err, contextErr), false)
	}
	if err != nil {
		return failure(ctx, operation, err, false)
	}
	return nil
}

func failure(ctx context.Context, operation string, err error, attempted bool) error {
	if contextErr := ctx.Err(); contextErr != nil && !errors.Is(err, contextErr) {
		err = errors.Join(err, contextErr)
	}
	return &core.CredentialStoreError{Backend: core.CredentialBackendOnePassword, Operation: operation, Cause: err, Attempted: attempted}
}
