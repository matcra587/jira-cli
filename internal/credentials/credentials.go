// Package credentials encodes what `jira auth login` stores: the site,
// the account, and the API token, kept together in the selected credential store.
//
// Together, deliberately. The site and account are not secret, but they
// are useless apart from the token and the token is useless apart from
// them — so storing them as one record means a login is complete in one
// command, and `jira config` is left holding preferences only.
package credentials

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
)

// account is the provider-local record name. Select binds keyring operations
// to a durable profile locator while SDK stores retain their owned field.
const account = "default"

// Version is the current record shape.
//
// Present from the first release on purpose: a stored blob outlives the
// code that wrote it, and adding a version after the fact means guessing
// at the shape of records already on people's machines.
const Version = 2

// ErrNotStored reports that no credential record exists.
var ErrNotStored = errors.New("no stored credentials")

// ErrUnreadable reports a record that exists but cannot be used — bad
// JSON, or a version this build does not understand.
//
// Its own sentinel so config resolution can treat it as "no usable
// credential" and carry on. Failing the load instead would take down
// EVERY command, including the `jira auth logout` the message tells you
// to run, leaving an OS keyring tool as the only way out.
var ErrUnreadable = errors.New("stored credentials are not usable")

// unreadableRecord keeps decoder causes inspectable without reflecting private
// record bytes, unexpected member names or values through normal diagnostics.
type unreadableRecord struct{ cause error }

func (*unreadableRecord) Error() string {
	return "stored credentials are not usable; run `jira auth logout` then log in again"
}
func (e *unreadableRecord) GoString() string { return e.Error() }
func (e *unreadableRecord) Unwrap() error    { return errors.Join(ErrUnreadable, e.cause) }

// Record is what lives in the credential store.
type Record struct {
	// Version identifies the record shape.
	Version int `json:"version"`
	// Site is the Jira base URL these credentials authenticate against.
	Site string `json:"site"`
	// CloudID binds scoped gateway routing to this verified credential tuple.
	// Empty is classic site routing; version-1 records cannot claim this field.
	CloudID string `json:"cloud_id,omitempty"`
	// Email is the account the token belongs to.
	Email string `json:"email"`
	// Token is the API token. SECRET.
	Token string `json:"token"`
}

// Complete reports whether the record carries a usable tuple. A record
// missing any part cannot authenticate anything: the three values were
// verified together, and only together do they mean anything.
func (r Record) Complete() bool {
	return strings.TrimSpace(r.Site) != "" &&
		strings.TrimSpace(r.Email) != "" &&
		strings.TrimSpace(r.Token) != ""
}

// Load reads the stored record.
func Load(ctx context.Context, store core.CredentialStore) (Record, error) {
	raw, err := store.Get(ctx, account)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Record{}, err
	}
	if errors.Is(err, core.ErrNoCredential) {
		return Record{}, ErrNotStored
	}
	if err != nil {
		return Record{}, err
	}

	var rec Record
	if err := jsonv2.Unmarshal([]byte(raw), &rec, jsonv2.RejectUnknownMembers(true)); err != nil {
		// A record that does not parse is not "no credentials" — saying
		// so would send someone to log in again while the broken entry
		// stayed, and they would do it forever.
		return Record{}, &unreadableRecord{cause: err}
	}
	var fields map[string]json.RawMessage
	if err := jsonv2.Unmarshal([]byte(raw), &fields); err != nil {
		return Record{}, &unreadableRecord{cause: err}
	}
	if value, present := fields["cloud_id"]; present && strings.TrimSpace(string(value)) == "null" {
		return Record{}, fmt.Errorf("%w: cloud routing must be a string", ErrUnreadable)
	}
	// Normalize on the way OUT as well as in, so a record written before
	// this rule existed still reads cleanly instead of needing a fresh
	// login to tidy up a trailing slash.
	rec.Site = normalizeSite(rec.Site)
	if rec.Version < 1 || rec.Version > Version {
		return Record{}, fmt.Errorf("%w: stored by a newer version (%d, this build understands %d); run `jira auth logout` then log in again", ErrUnreadable, rec.Version, Version)
	}
	if _, present := fields["cloud_id"]; rec.Version == 1 && present {
		return Record{}, fmt.Errorf("%w: version-1 credentials cannot contain cloud routing", ErrUnreadable)
	}
	if err := core.ValidateCloudID(rec.CloudID); err != nil {
		return Record{}, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	rec.CloudID = strings.TrimSpace(rec.CloudID)
	return rec, nil
}

// Save writes the record, replacing anything stored.
func Save(ctx context.Context, store core.CredentialStore, rec Record) error {
	_, err := SaveWithPublication(ctx, store, rec)
	return err
}

// SaveWithPublication preserves acknowledgment even when cancellation arrives
// during a synchronous write. Untyped errors after Set entry remain unknown;
// provider-owned admission errors can establish that no write was attempted.
func SaveWithPublication(ctx context.Context, store core.CredentialStore, rec Record) (core.CredentialPublication, error) {
	out := core.CredentialPublication{Backend: core.CredentialBackendKeyring, Status: "not_saved"}
	if provider, ok := store.(core.CredentialBackendProvider); ok {
		out.Backend = provider.CredentialBackend()
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	rec.Version = Version
	rec.Site = normalizeSite(rec.Site)
	if err := core.ValidateCloudID(rec.CloudID); err != nil {
		return out, err
	}
	rec.CloudID = strings.TrimSpace(rec.CloudID)
	if strings.TrimSpace(rec.Token) == "" {
		return out, errors.New("refusing to store an empty token")
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return out, fmt.Errorf("encode credentials: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	err = store.Set(ctx, account, string(raw))
	out.Attempted = true
	if err == nil {
		out.Saved, out.Status = true, "saved"
	} else {
		out.Status = "unknown"
		var providerError *core.CredentialStoreError
		if errors.As(err, &providerError) && !providerError.Attempted {
			out.Attempted, out.Status = false, "not_saved"
		}
	}
	return out, err
}

// Clear removes the record. Removing one that is not there succeeds:
// logout's contract is the end state, not the transition.
func Clear(ctx context.Context, store core.CredentialStore) error {
	return store.Delete(ctx, account)
}

// NormalizeSite is normalizeSite, exported because every comparison of
// two site URLs has to agree on their spelling. It did not: the stored
// side was normalized and the resolved side was not, so a base_url
// differing only by a trailing slash read as a DIFFERENT host and threw
// away a credential that plainly still applied.
func NormalizeSite(raw string) string { return normalizeSite(raw) }

// normalizeSite trims a trailing slash. `https://site/` and
// `https://site` address the same Jira, but only one of them reads like
// a site when printed back to someone.
func normalizeSite(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	// Case-folded too: a host differs only in spelling, and comparing
	// "https://COMPANY..." against a lowercase stored site dropped a
	// valid credential while warning about two hosts that look
	// identical on screen. Only the scheme and host fold — a Jira path
	// is case-sensitive.
	if u, err := url.Parse(trimmed); err == nil && u.Host != "" {
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		return u.String()
	}
	return trimmed
}
