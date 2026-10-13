package issueedit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

type manifest struct {
	Version            int    `json:"version"`
	SourceSHA256       string `json:"source_sha256"`
	SourceBytes        int    `json:"source_bytes"`
	InitialMarkdownSHA string `json:"initial_markdown_sha256"`
}

// Create publishes the original backup before editable text and readiness.
// Interrupted creation is retained, never deleted or silently retried.
func (t *Target) Create(ctx context.Context, snapshot core.IssueDescriptionSnapshot) (core.IssueEditDraftState, error) {
	return t.create(ctx, snapshot, defaultPublicationOps())
}

func (t *Target) create(ctx context.Context, snapshot core.IssueDescriptionSnapshot, ops publicationOps) (state core.IssueEditDraftState, err error) {
	state = t.state
	defer func() { t.state = state }()
	if err := ctx.Err(); err != nil {
		return state, err
	}
	session, err := core.OpenIssueDescriptionSession(snapshot)
	if err != nil {
		return state, fmt.Errorf("%w: source snapshot: %w", core.ErrIssueEditDraftInvalid, err)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(session.Snapshot()); err != nil {
		return state, fmt.Errorf("%w: encode source: %w", core.ErrIssueEditDraftInvalid, err)
	}
	source, markdown := buffer.Bytes(), []byte(session.Markdown())
	if len(source) > MaxMemberBytes || len(markdown) > MaxMemberBytes {
		return state, fmt.Errorf("%w: prepared artifact exceeds the draft member limit", core.ErrIssueEditDraftInvalid)
	}
	ready, err := json.Marshal(manifest{Version: 1, SourceSHA256: digest(source), SourceBytes: len(source), InitialMarkdownSHA: digest(markdown)})
	if err != nil {
		return state, fmt.Errorf("%w: encode manifest: %w", core.ErrIssueEditDraftInvalid, err)
	}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if err := t.parent.Mkdir(t.name, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return state, core.ErrIssueEditDraftChanged
		}
		return state, draftIO("create unused draft directory", err)
	}
	state.DirectoryCreated = true
	root, err := t.parent.OpenRoot(t.name)
	if err != nil {
		return state, draftIO("open created directory", err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	if err := checkDirectory(root, state.Directory); err != nil {
		return state, err
	}
	for _, member := range []struct {
		name string
		data []byte
		mode os.FileMode
		ack  *bool
	}{
		{SourceName, source, 0o400, &state.SourcePublished},
		{MarkdownName, markdown, 0o600, &state.MarkdownPublished},
		{ManifestName, ready, 0o400, &state.Ready},
	} {
		state.MemberAttempted = member.name
		published, err := publishMember(ctx, root, member.name, member.data, member.mode, ops)
		*member.ack = published
		if err != nil {
			return state, draftIO("publish "+member.name, err)
		}
	}
	if err := ops.sync(t.parent); err != nil {
		return state, draftIO("sync draft parent after publication", err)
	}
	if xos.IsUnix() {
		state.Durability = "synced"
	} else {
		state.Durability = "platform_unconfirmed"
	}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	return state, checkDirectory(root, state.Directory)
}

// Draft retains its rooted directory and immutable session during editing.
// A digest detects changed artifacts; it supplies no authenticity or Jira permission.
type Draft struct {
	root        *os.Root
	state       core.IssueEditDraftState
	session     *core.IssueDescriptionSession
	sourceSHA   string
	manifestSHA string
	markdown    string
}

// Open validates one ready artifact without credentials, editors or remote I/O.
// Invalid authored anchors remain available for a resumed editor to repair.
func Open(ctx context.Context, path string) (*Draft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, core.ErrIssueEditDraftInvalid
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, draftIO("resolve draft directory", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, draftIO("resolve draft parent", err)
	}
	path = filepath.Join(parent, filepath.Base(abs))
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, draftIO("open draft directory", err)
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = root.Close() //nolint:errcheck // preserve the actual failed admission; no state was changed
		}
	}()
	if err := checkDirectory(root, path); err != nil {
		return nil, err
	}
	ready, err := readMember(ctx, root, ManifestName, 4096)
	if err != nil {
		return nil, err
	}
	var record manifest
	if err := strictObject(ready, []string{"version", "source_sha256", "source_bytes", "initial_markdown_sha256"}, &record); err != nil || record.Version != 1 {
		return nil, fmt.Errorf("%w: readiness manifest", core.ErrIssueEditDraftInvalid)
	}
	source, err := readMember(ctx, root, SourceName, MaxMemberBytes)
	if err != nil {
		return nil, err
	}
	if len(source) != record.SourceBytes || digest(source) != record.SourceSHA256 {
		return nil, core.ErrIssueEditDraftChanged
	}
	var snapshot core.IssueDescriptionSnapshot
	keys := []string{"version", "site", "account", "api_base_url", "issue_id", "issue_key", "description", "description_bytes", "updated"}
	if err := strictObject(source, keys, &snapshot); err != nil {
		return nil, fmt.Errorf("%w: source backup: %w", core.ErrIssueEditDraftInvalid, err)
	}
	session, err := core.OpenIssueDescriptionSession(snapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: source backup: %w", core.ErrIssueEditDraftInvalid, err)
	}
	if digest([]byte(session.Markdown())) != record.InitialMarkdownSHA {
		return nil, core.ErrIssueEditDraftInvalid
	}
	markdown, err := readMember(ctx, root, MarkdownName, MaxMemberBytes)
	if err != nil {
		return nil, err
	}
	if err := checkDirectory(root, path); err != nil {
		return nil, err
	}
	state := draftState(path)
	state.Durability = "not_checked"
	state.Ready = true
	draft := &Draft{root: root, state: state, session: session, sourceSHA: record.SourceSHA256, manifestSHA: digest(ready), markdown: string(markdown)}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	accepted = true
	return draft, nil
}

func (d *Draft) State() core.IssueEditDraftState         { return d.state }
func (d *Draft) Snapshot() core.IssueDescriptionSnapshot { return d.session.Snapshot() }
func (d *Draft) Session() *core.IssueDescriptionSession  { return d.session }
func (d *Draft) Markdown() string                        { return d.markdown }
func (d *Draft) Close() error                            { return d.root.Close() }

// Reload reads newly saved Markdown only after the original artifacts and
// directory still match. It never replaces the native backup or saved work.
func (d *Draft) Reload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkDirectory(d.root, d.state.Directory); err != nil {
		return err
	}
	for _, member := range []struct{ name, sha string }{{SourceName, d.sourceSHA}, {ManifestName, d.manifestSHA}} {
		data, err := readMember(ctx, d.root, member.name, MaxMemberBytes)
		if err != nil || digest(data) != member.sha {
			return errors.Join(core.ErrIssueEditDraftChanged, err)
		}
	}
	markdown, err := readMember(ctx, d.root, MarkdownName, MaxMemberBytes)
	if err != nil {
		return err
	}
	if err := checkDirectory(d.root, d.state.Directory); err != nil {
		return err
	}
	d.markdown = string(markdown)
	return nil
}

// Candidate validates current saved text through the shared preservation owner.
func (d *Draft) Candidate(ctx context.Context) (json.RawMessage, error) {
	if err := d.Reload(ctx); err != nil {
		return nil, err
	}
	if !utf8.ValidString(d.markdown) {
		return nil, fmt.Errorf("%w: saved Markdown is not UTF-8", core.ErrIssueEditDraftInvalid)
	}
	candidate, err := d.session.Apply(d.markdown)
	if err != nil || ctx.Err() != nil {
		return nil, errors.Join(err, ctx.Err())
	}
	return candidate, err
}

func checkDirectory(root *os.Root, path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return errors.Join(core.ErrIssueEditDraftChanged, err)
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return errors.Join(core.ErrIssueEditDraftChanged, err)
	}
	return nil
}

func strictObject(data []byte, known []string, target any) error {
	if _, err := wire.DecodeNativeObject(data, known); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
