package config

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// ErrAccountIdentityScope refuses saving verified identity into conflicting
// authored scope. Environment overlays cannot rebind another tenant's settings.
var ErrAccountIdentityScope = errors.New("configuration scope differs from verified identity")

type accountIdentityTarget struct {
	file       *File
	scope      core.AccountIdentityScope
	additional map[string]string
}

type accountIdentityUpdate struct {
	file     *File
	settings *SettingsUpdate
}

// OpenAccountIdentityTarget checks public scope and snapshots the selected file
// before remote lookup. It opens no providers and creates no files/directories.
// Missing scope declarations may inherit credentials; existing declarations
// must agree. Publication stores only the independently bound identity fields.
func OpenAccountIdentityTarget(ctx context.Context, path string, scope core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
	return OpenAccountIdentityTargetFor(ctx, Options{Path: path}, scope)
}

// OpenAccountIdentityTargetFor retains an invocation's selected profile while
// preparing the same checked publication target as OpenAccountIdentityTarget.
func OpenAccountIdentityTargetFor(ctx context.Context, opts Options, scope core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
	target, err := openAccountIdentityTarget(ctx, opts, scope)
	if err != nil {
		return nil, err
	}
	return target, nil
}

func openAccountIdentityTarget(ctx context.Context, opts Options, scope core.AccountIdentityScope) (*accountIdentityTarget, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateBaseURL(scope.BaseURL); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFileInvalid, err)
	}
	if strings.TrimSpace(scope.Email) == "" {
		return nil, fmt.Errorf("%w: identity email must be nonblank", ErrFileInvalid)
	}
	if err := core.ValidateCloudID(scope.CloudID); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFileInvalid, err)
	}
	file, err := OpenFile(ctx, opts.Path)
	if err != nil {
		return nil, err
	}
	if opts.Snapshot != nil {
		file = opts.Snapshot
	}
	if opts.Profile != "" {
		file, err = file.SelectProfile(opts.Profile)
		if err != nil {
			return nil, err
		}
	}
	_, fields, decoded, err := file.selectedFields()
	if err != nil {
		return nil, err
	}
	for _, binding := range []struct {
		key   string
		match bool
	}{
		{"base_url", credentials.NormalizeSite(decoded.BaseURL) == credentials.NormalizeSite(scope.BaseURL)},
		{"email", strings.EqualFold(strings.TrimSpace(decoded.Email), strings.TrimSpace(scope.Email))},
		{"cloud_id", strings.TrimSpace(decoded.CloudID) == strings.TrimSpace(scope.CloudID)},
	} {
		if _, declared := fields[binding.key]; declared && !binding.match {
			return nil, fmt.Errorf("%w: authored %s conflicts; repair the setting before saving identity", ErrAccountIdentityScope, binding.key)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &accountIdentityTarget{file: file, scope: scope}, nil
}

func (t *accountIdentityTarget) Prepare(accountID string) (core.AccountIdentityUpdate, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, fmt.Errorf("%w: verified account_id must be nonblank", ErrFileInvalid)
	}
	values := map[string]string{
		"account_id": accountID, "account_id_site": t.scope.BaseURL,
		"account_id_email": t.scope.Email, "account_id_cloud_id": t.scope.CloudID,
	}
	for key, value := range t.additional {
		values[key] = value
	}
	settings, err := t.file.PrepareSettings(values)
	if err != nil {
		return nil, err
	}
	return &accountIdentityUpdate{file: t.file, settings: settings}, nil
}

func (u *accountIdentityUpdate) Receipt() core.AccountIdentityPublication {
	return core.AccountIdentityPublication{Path: u.file.Path(), WouldChange: u.settings.WouldChange(), Status: "prepared"}
}

func (u *accountIdentityUpdate) Save(ctx context.Context) (core.AccountIdentityPublication, error) {
	out := u.Receipt()
	if err := ctx.Err(); err != nil {
		out.Status = "not_saved"
		return out, err
	}
	// An unchanged candidate still promises that this verified identity is
	// present. File.Save's byte-equal fast path does not inspect the target.
	if err := u.file.checkSnapshot(ctx); err != nil {
		out.Status = "not_saved"
		return out, err
	}
	var err error
	if u.file.Exists() {
		out.Changed, err = u.settings.Save(ctx)
	} else {
		out.Changed, err = u.settings.Create(ctx)
		out.Created = out.Changed
	}
	switch {
	case err == nil:
		out.Saved = true
		out.Status = "unchanged"
		if out.Changed {
			out.Status = "saved"
		}
	case out.Changed:
		out.Status = "written"
	case errors.Is(err, ErrFileChanged), errors.Is(err, ErrFileInvalid), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		out.Status = "not_saved"
	default:
		out.Status = "unknown"
	}
	return out, err
}
