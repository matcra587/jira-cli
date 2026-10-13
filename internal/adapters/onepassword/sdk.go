package onepassword

import (
	"context"
	"errors"

	sdk "github.com/1password/onepassword-sdk-go"
)

// newSDKClient runs under sdkInitialization. The upstream cold WASM loader uses
// its own background context, and desktop IPC is synchronous: this adapter never
// invents a detached worker or claims those portions are interruptible.
func newSDKClient(ctx context.Context, options ClientOptions) (Client, error) {
	version := options.IntegrationVersion
	if version == "" {
		version = sdk.DefaultIntegrationVersion
	}
	opts := []sdk.ClientOption{sdk.WithIntegrationInfo("jira-cli", version)}
	if options.ServiceAccountToken != "" {
		opts = append(opts, sdk.WithServiceAccountToken(options.ServiceAccountToken))
	} else {
		if desktopAccount != "" && desktopAccount != options.Account {
			return nil, errors.New("the SDK desktop core is already pinned to a different account")
		}
		desktopAccount = options.Account
		opts = append(opts, sdk.WithDesktopAppIntegration(options.Account))
	}
	client, err := sdk.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return sdkClient{client: client}, nil
}

type sdkClient struct{ client *sdk.Client }

func (c sdkClient) ListVaults(ctx context.Context) ([]sdk.VaultOverview, error) {
	return c.client.Vaults().List(ctx)
}

func (c sdkClient) ListItems(ctx context.Context, vault string) ([]sdk.ItemOverview, error) {
	return c.client.Items().List(ctx, vault)
}

func (c sdkClient) GetItem(ctx context.Context, vault, item string) (sdk.Item, error) {
	return c.client.Items().Get(ctx, vault, item)
}

func (c sdkClient) CreateItem(ctx context.Context, params sdk.ItemCreateParams) (sdk.Item, error) {
	return c.client.Items().Create(ctx, params)
}

func (c sdkClient) PutItem(ctx context.Context, item sdk.Item) (sdk.Item, error) {
	return c.client.Items().Put(ctx, item)
}
