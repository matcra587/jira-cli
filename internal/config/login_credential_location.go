package config

import (
	"context"
	"fmt"

	"github.com/matcra587/jira-cli/internal/core"
)

// PrepareLoginCredentialLocation durably reserves a named profile's missing
// keyring slot before any token publication. The public reference survives
// cancellation and later failures so a fresh process can find that slot.
func PrepareLoginCredentialLocation(ctx context.Context, opts Options, selected core.CredentialLocation) (core.CredentialLocation, core.AccountIdentityPublication, error) {
	var receipt core.AccountIdentityPublication
	if selected.Backend != core.CredentialBackendKeyring || selected.Account != "" {
		return selected, receipt, ctx.Err()
	}
	if opts.Snapshot != nil {
		if err := opts.Snapshot.checkSnapshot(ctx); err != nil {
			return selected, receipt, err
		}
	}
	opts.Snapshot = nil
	file, err := OpenFile(ctx, opts.Path)
	if err != nil {
		return selected, receipt, err
	}
	if opts.Profile != "" {
		file, err = file.SelectProfile(opts.Profile)
		if err != nil {
			return selected, receipt, err
		}
	}
	opts.Snapshot = file
	resolved, err := Resolve(ctx, opts, nil)
	if err != nil {
		return selected, receipt, err
	}
	if resolved.Profile == "" || CredentialSelection(*resolved) != selected {
		return selected, receipt, fmt.Errorf("%w: selected credential location changed before login", ErrAccountIdentityScope)
	}
	settings, err := file.PrepareCredentialID()
	if err != nil {
		return selected, receipt, err
	}
	update := &accountIdentityUpdate{file: file, settings: settings}
	receipt, err = update.Save(ctx)
	if err != nil {
		return selected, receipt, err
	}
	// Read the committed public identifier through the normal strict owner;
	// only a durably successful publication permits the credential write.
	published := *file
	published.original, published.exists = settings.candidate, true
	if err := published.checkSnapshot(ctx); err != nil {
		return selected, receipt, err
	}
	opts.Snapshot = &published
	resolved, err = Resolve(ctx, opts, nil)
	if err != nil {
		return selected, receipt, err
	}
	location := CredentialSelection(*resolved)
	if location.Backend != selected.Backend || location.Account == "" {
		return selected, receipt, fmt.Errorf("%w: credential location changed after reservation", ErrAccountIdentityScope)
	}
	return location, receipt, nil
}
