package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/jira"
)

var (
	// ErrIssueDescriptionUnavailable identifies a client without bound description reads.
	ErrIssueDescriptionUnavailable = errors.New("bound issue description client is not configured")
	// ErrIssueDescriptionInvalid refuses incomplete native editing snapshots.
	ErrIssueDescriptionInvalid = errors.New("invalid issue description snapshot")
	// ErrIssueDescriptionScopeChanged refuses a different client or issue identity.
	ErrIssueDescriptionScopeChanged = errors.New("issue description scope changed")
)

// IssueDescriptionClient exposes only the reads required by anchored editing.
// JiraScope identifies the constructed client's public account and actual route.
type IssueDescriptionClient interface {
	JiraScopedClient
	GetIssue(context.Context, string, []string, ...string) (IssueRead, error)
}

// IssueDescriptionSnapshot retains the original native description and its
// public binding. Updated is diagnostic metadata, never a remote precondition.
// Description is native ADF or explicit null; an absent field is not a snapshot.
type IssueDescriptionSnapshot struct {
	Version     int             `json:"version"`
	Site        string          `json:"site"`
	Account     string          `json:"account"`
	APIBaseURL  string          `json:"api_base_url"`
	IssueID     string          `json:"issue_id"`
	IssueKey    string          `json:"issue_key"`
	Description json.RawMessage `json:"description"`
	// DescriptionBytes is the exact native preimage. JSON encodes it as base64;
	// the adjacent native view may compact insignificant JSON whitespace.
	DescriptionBytes []byte                `json:"description_bytes"`
	Updated          jira.Nullable[string] `json:"updated,omitzero"`
}

// IssueDescriptionSession binds the shared preservation owner to one issue.
// Its immutable source is independent of supplied and returned snapshot buffers.
type IssueDescriptionSession struct {
	snapshot IssueDescriptionSnapshot
	edit     *adf.EditSession
}

// PrepareIssueDescription reads one native description and prepares its anchors.
// It never edits Jira or treats stored public identity as authenticated identity.
func PrepareIssueDescription(ctx context.Context, client IssueDescriptionClient, key string) (*IssueDescriptionSession, RetryInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, RetryInfo{}, err
	}
	if client == nil || strings.TrimSpace(key) == "" {
		return nil, RetryInfo{}, ErrIssueDescriptionInvalid
	}
	identity := client.JiraScope()
	scope := IssueDescriptionSnapshot{Site: identity.Site, Account: identity.Account, APIBaseURL: identity.APIBaseURL}
	if err := validateDescriptionScope(scope); err != nil {
		return nil, RetryInfo{}, err
	}
	read, err := client.GetIssue(ctx, key, []string{"description", "updated"})
	if err != nil {
		return nil, read.Retry, err
	}
	if err := ctx.Err(); err != nil {
		return nil, read.Retry, err
	}
	if err := checkDescriptionScope(client, scope); err != nil {
		return nil, read.Retry, err
	}
	snapshot, err := snapshotIssueDescription(read.Issue, scope)
	if err != nil {
		return nil, read.Retry, err
	}
	session, err := OpenIssueDescriptionSession(snapshot)
	if err == nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, read.Retry, contextErr
		}
	}
	return session, read.Retry, err
}

// OpenIssueDescriptionSession reconstructs anchors from the exact native source.
// Public snapshots are input, not authority to write or switch client scope.
func OpenIssueDescriptionSession(snapshot IssueDescriptionSnapshot) (*IssueDescriptionSession, error) {
	if snapshot.Version != 1 || strings.TrimSpace(snapshot.IssueID) == "" || strings.TrimSpace(snapshot.IssueKey) == "" {
		return nil, ErrIssueDescriptionInvalid
	}
	if err := validateDescriptionScope(snapshot); err != nil {
		return nil, err
	}
	equal, err := adf.StructuralEqual(snapshot.Description, snapshot.DescriptionBytes)
	if err != nil || !equal {
		return nil, ErrIssueDescriptionInvalid
	}
	doc, err := descriptionDocument(snapshot.DescriptionBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIssueDescriptionInvalid, err)
	}
	edit, err := adf.PrepareEdit(doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIssueDescriptionInvalid, err)
	}
	snapshot.Description = bytes.Clone(snapshot.DescriptionBytes)
	snapshot.DescriptionBytes = bytes.Clone(snapshot.DescriptionBytes)
	return &IssueDescriptionSession{snapshot: snapshot, edit: edit}, nil
}

// Snapshot returns an independent original snapshot, including native null.
func (s *IssueDescriptionSession) Snapshot() IssueDescriptionSnapshot {
	snapshot := s.snapshot
	snapshot.Description = bytes.Clone(snapshot.Description)
	snapshot.DescriptionBytes = bytes.Clone(snapshot.DescriptionBytes)
	return snapshot
}

// Markdown returns the anchored editor projection, never a native backup.
func (s *IssueDescriptionSession) Markdown() string { return s.edit.Markdown() }

// Regions returns independent immutable anchor/protection metadata.
func (s *IssueDescriptionSession) Regions() []adf.EditRegion { return s.edit.Regions() }

// Apply produces a local native candidate, preserving an unchanged null source.
func (s *IssueDescriptionSession) Apply(markdown string) (json.RawMessage, error) {
	if s == nil || s.edit == nil {
		return nil, ErrIssueDescriptionInvalid
	}
	doc, err := s.edit.Apply(markdown)
	if err != nil {
		return nil, err
	}
	if strings.ReplaceAll(markdown, "\r\n", "\n") == s.edit.Markdown() ||
		bytes.Equal(bytes.TrimSpace(s.snapshot.Description), []byte("null")) && len(doc.Content()) == 0 {
		return bytes.Clone(s.snapshot.Description), nil
	}
	return doc.Marshal()
}

// IssueDescriptionMerge retains the current observation and merged candidate.
// Preventing lost updates requires a demonstrated server-enforced condition;
// neither this fresh read nor a successful merge prevents a later writer.
type IssueDescriptionMerge struct {
	Current     IssueDescriptionSnapshot
	Description json.RawMessage
	Changed     bool
	Retry       RetryInfo
}

// CheckScope validates this session against the constructed client without I/O.
// Public snapshot identity is not authorization to use another account or site.
func (s *IssueDescriptionSession) CheckScope(client IssueDescriptionClient) error {
	if s == nil || s.edit == nil {
		return ErrIssueDescriptionInvalid
	}
	return checkDescriptionScope(client, s.snapshot)
}

// Merge validates the local edit before rereading the same immutable issue ID.
// Scope is checked on both sides of the read; issue-key renames remain usable.
// This consumer has no write method and cannot publish its candidate to Jira.
func (s *IssueDescriptionSession) Merge(ctx context.Context, client IssueDescriptionClient, markdown string) (IssueDescriptionMerge, error) {
	var zero IssueDescriptionMerge
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	local, err := s.Apply(markdown)
	if err != nil {
		return zero, err
	}
	if err := checkDescriptionScope(client, s.snapshot); err != nil {
		return zero, err
	}
	read, err := client.GetIssue(ctx, s.snapshot.IssueID, []string{"description", "updated"})
	if err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := checkDescriptionScope(client, s.snapshot); err != nil {
		return zero, err
	}
	current, err := snapshotIssueDescription(read.Issue, s.snapshot)
	if err != nil {
		return zero, err
	}
	if current.IssueID != s.snapshot.IssueID {
		return zero, ErrIssueDescriptionScopeChanged
	}
	var candidate json.RawMessage
	if bytes.Equal(bytes.TrimSpace(current.Description), []byte("null")) {
		if bytes.Equal(local, s.snapshot.Description) {
			candidate = bytes.Clone(current.Description)
		} else if bytes.Equal(bytes.TrimSpace(s.snapshot.Description), []byte("null")) {
			candidate = local
		} else {
			return zero, &adf.EditError{Kind: adf.EditMergeConflict, Reason: "description was edited locally but cleared remotely"}
		}
	} else {
		remote, parseErr := descriptionDocument(current.Description)
		if parseErr != nil {
			return zero, fmt.Errorf("%w: %w", ErrIssueDescriptionInvalid, parseErr)
		}
		merged, mergeErr := s.edit.Merge(markdown, remote)
		if mergeErr != nil {
			return zero, mergeErr
		}
		candidate, err = merged.Marshal()
		if err != nil {
			return zero, err
		}
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return IssueDescriptionMerge{Current: current, Description: candidate, Changed: !bytes.Equal(candidate, current.Description), Retry: read.Retry}, nil
}

func snapshotIssueDescription(issue *jira.IssueBean, scope IssueDescriptionSnapshot) (IssueDescriptionSnapshot, error) {
	if issue == nil || !issue.ID.Set || issue.ID.Null || strings.TrimSpace(issue.ID.Value) == "" ||
		!issue.Key.Set || issue.Key.Null || strings.TrimSpace(issue.Key.Value) == "" || !issue.Fields.Set || issue.Fields.Null {
		return IssueDescriptionSnapshot{}, ErrIssueDescriptionInvalid
	}
	fields := issue.Fields.Value
	if !fields.Description.Set {
		return IssueDescriptionSnapshot{}, ErrIssueDescriptionInvalid
	}
	if !fields.Description.Null && bytes.Equal(bytes.TrimSpace(fields.Description.Value), []byte("null")) {
		return IssueDescriptionSnapshot{}, ErrIssueDescriptionInvalid
	}
	description := bytes.Clone(fields.Description.Value)
	if fields.Description.Null {
		description = json.RawMessage("null")
	}
	return IssueDescriptionSnapshot{
		Version: 1, Site: scope.Site, Account: scope.Account, APIBaseURL: scope.APIBaseURL,
		IssueID: issue.ID.Value, IssueKey: issue.Key.Value,
		Description: description, DescriptionBytes: bytes.Clone(description), Updated: fields.Updated,
	}, nil
}

func validateDescriptionScope(snapshot IssueDescriptionSnapshot) error {
	if strings.TrimSpace(snapshot.Account) == "" {
		return ErrIssueDescriptionInvalid
	}
	for _, address := range []string{snapshot.Site, snapshot.APIBaseURL} {
		site, err := url.Parse(address)
		if err != nil || (site.Scheme != "https" && site.Scheme != "http") || site.Host == "" || site.User != nil || site.RawQuery != "" || site.Fragment != "" {
			return ErrIssueDescriptionInvalid
		}
	}
	return nil
}

func checkDescriptionScope(client IssueDescriptionClient, snapshot IssueDescriptionSnapshot) error {
	if client == nil {
		return ErrIssueDescriptionScopeChanged
	}
	if client.JiraScope() != (JiraScope{Site: snapshot.Site, Account: snapshot.Account, APIBaseURL: snapshot.APIBaseURL}) {
		return ErrIssueDescriptionScopeChanged
	}
	return nil
}

func descriptionDocument(data json.RawMessage) (*adf.Document, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return adf.NewDocument(), nil
	}
	if _, err := adf.StructuralEqual(data, data); err != nil {
		return nil, err
	}
	return adf.ParseDocument(data)
}
