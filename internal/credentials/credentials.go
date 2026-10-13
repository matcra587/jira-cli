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
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
)

// account is the provider-local record name; Select binds it to a profile locator.
const account = "default"

// Version is the current record shape.
//
// Present from the first release on purpose: a stored blob outlives the
// code that wrote it, and adding a version after the fact means guessing
// at the shape of records already on people's machines.
const Version = 1

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

// Record is what lives in the credential store.
type Record struct {
	// Version identifies the record shape.
	Version int `json:"version"`
	// Site is the Jira base URL these credentials authenticate against.
	Site string `json:"site"`
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
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		// A record that does not parse is not "no credentials" — saying
		// so would send someone to log in again while the broken entry
		// stayed, and they would do it forever.
		return Record{}, fmt.Errorf("%w: not readable (%w); run `jira auth logout` then log in again", ErrUnreadable, err)
	}
	// Normalize on the way OUT as well as in, so a record written before
	// this rule existed still reads cleanly instead of needing a fresh
	// login to tidy up a trailing slash.
	rec.Site = normalizeSite(rec.Site)
	if rec.Version != Version {
		return Record{}, fmt.Errorf("%w: stored by a newer version (%d, this build understands %d); run `jira auth logout` then log in again", ErrUnreadable, rec.Version, Version)
	}
	return rec, nil
}

// Save writes the record, replacing anything stored.
func Save(ctx context.Context, store core.CredentialStore, rec Record) error {
	rec.Version = Version
	rec.Site = normalizeSite(rec.Site)
	if strings.TrimSpace(rec.Token) == "" {
		return errors.New("refusing to store an empty token")
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	return store.Set(ctx, account, string(raw))
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
