package issueedit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/core"
)

const draftNative = `{
 "type":"doc","version":1,"future":{"large":9007199254740993,"raw":"<>&"},
 "content":[{"type":"paragraph","content":[{"type":"text","text":"alpha"}]},{"type":"futureNode","attrs":{"nativeCase":"retained"}}]
}`

func draftSnapshot(native string) core.IssueDescriptionSnapshot {
	return core.IssueDescriptionSnapshot{Version: 1, Site: "https://jira.example.test", Account: "editor@example.test", APIBaseURL: "https://jira.example.test", IssueID: "123456", IssueKey: "KEY-1", Description: json.RawMessage(native), DescriptionBytes: []byte(native)}
}

func createDraft(t *testing.T, native string) (string, core.IssueEditDraftState) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "literal~$draft")
	target, err := PrepareTarget(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := target.Create(t.Context(), draftSnapshot(native))
	closeErr := target.Close()
	if err != nil || closeErr != nil {
		t.Fatal("create draft", err, closeErr)
	}
	return path, state
}

func replaceMember(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDraftRoundTripPreservesNativePreimageAndProtectedState(t *testing.T) {
	t.Parallel()
	for _, native := range []string{draftNative, "null", `{"type":"doc","version":1,"content":[]}`} {
		t.Run(native, func(t *testing.T) {
			t.Parallel()
			path, state := createDraft(t, native)
			if !state.DirectoryCreated || !state.SourcePublished || !state.MarkdownPublished || !state.Ready || filepath.Base(state.Directory) != "literal~$draft" {
				t.Fatal("incomplete or redirected creation receipt", state)
			}
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) != 3 {
				t.Fatal("publication left private stages or lost members", err, entries)
			}
			if xos.IsUnix() {
				for name, mode := range map[string]os.FileMode{path: 0o700, state.SourceFile: 0o400, state.ManifestFile: 0o400, state.MarkdownFile: 0o600} {
					info, err := os.Stat(name)
					if err != nil || info.Mode().Perm() != mode {
						t.Fatal("artifact permissions changed", name, err)
					}
				}
				if state.Durability != "synced" {
					t.Fatal("completed Unix directory sync was not reported")
				}
			} else if state.Durability != "platform_unconfirmed" {
				t.Fatal("unsupported directory durability was claimed")
			}
			draft, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := draft.Close(); err != nil {
					t.Error(err)
				}
			}()
			candidate, err := draft.Candidate(t.Context())
			if err != nil || string(candidate) != native || string(draft.Snapshot().DescriptionBytes) != native {
				t.Fatal("reopened untouched draft changed native bytes", err, string(candidate))
			}
			if native == draftNative {
				before, err := os.ReadFile(state.SourceFile)
				if err != nil {
					t.Fatal(err)
				}
				replaceMember(t, state.MarkdownFile, []byte(strings.Replace(draft.Markdown(), "alpha", "authored", 1)))
				candidate, err := draft.Candidate(t.Context())
				after, readErr := os.ReadFile(state.SourceFile)
				if err != nil || readErr != nil || !bytes.Equal(before, after) || !bytes.Contains(candidate, []byte(`"text":"authored"`)) || !bytes.Contains(candidate, []byte(`"nativeCase":"retained"`)) || !bytes.Contains(candidate, []byte(`9007199254740993`)) {
					t.Fatal("authored draft changed backup or protected native state", err, readErr)
				}
			}
		})
	}
}

func TestDraftTargetRefusesExistingNamesAndCreationRace(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"file", "directory", "missing parent", "regular parent", "race"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			path := filepath.Join(parent, "draft")
			switch name {
			case "file", "regular parent":
				if err := os.WriteFile(path, []byte("retain user content"), 0o600); err != nil {
					t.Fatal(err)
				}
				if name == "regular parent" {
					path = filepath.Join(path, "child")
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "missing parent":
				path = filepath.Join(parent, "absent", "child")
			}
			target, err := PrepareTarget(t.Context(), path)
			if name != "race" {
				if err == nil || target != nil {
					t.Fatal("existing or invalid target admitted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := target.Close(); err != nil {
					t.Error(err)
				}
			}()
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			state, err := target.Create(t.Context(), draftSnapshot(draftNative))
			entries, readErr := os.ReadDir(path)
			if !errors.Is(err, core.ErrIssueEditDraftChanged) || state.DirectoryCreated || state.SourcePublished || readErr != nil || len(entries) != 0 {
				t.Fatal("creation race replaced existing directory or claimed effects", err, readErr)
			}
		})
	}
}

func TestDraftPartialPublicationRetainsAcknowledgedEffects(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"source link", "Markdown link", "manifest sync", "parent sync", "late cancellation"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			target, err := PrepareTarget(t.Context(), filepath.Join(t.TempDir(), "draft"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := target.Close(); err != nil {
					t.Error(err)
				}
			}()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			marker := errors.New("injected publication failure")
			ops := defaultPublicationOps()
			ops.link = func(root *os.Root, old, name string) error {
				if failure == "source link" && name == SourceName || failure == "Markdown link" && name == MarkdownName {
					return marker
				}
				if err := root.Link(old, name); err != nil {
					return err
				}
				if failure == "late cancellation" && name == ManifestName {
					cancel()
				}
				return nil
			}
			syncs := 0
			ops.sync = func(root *os.Root) error {
				syncs++
				if failure == "manifest sync" && syncs == 3 || failure == "parent sync" && syncs == 4 {
					return marker
				}
				return syncRoot(root)
			}
			state, err := target.create(ctx, draftSnapshot(draftNative), ops)
			if err == nil || !state.DirectoryCreated {
				t.Fatal("failed publication lost its directory receipt", err)
			}
			wantReady := failure == "manifest sync" || failure == "parent sync" || failure == "late cancellation"
			if state.Ready != wantReady || state.SourcePublished != (failure != "source link") || state.MarkdownPublished != wantReady {
				t.Fatal("wrong acknowledged publication effects", state)
			}
			if failure == "late cancellation" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("late cancellation cause lost", err)
				}
			} else if !errors.Is(err, marker) || state.Durability != "unconfirmed" {
				t.Fatal("durability failure became a success", err, state)
			}
			draft, openErr := Open(t.Context(), state.Directory)
			if wantReady {
				if openErr != nil {
					t.Fatal("acknowledged ready draft not recoverable", openErr)
				}
				if err := draft.Close(); err != nil {
					t.Fatal(err)
				}
			} else if openErr == nil || draft != nil {
				t.Fatal("partial publication falsely became ready")
			}
		})
	}
}

func TestDraftRefusesChangedSourceAndRedirectedDirectory(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"source", "manifest", "directory"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			path, state := createDraft(t, draftNative)
			draft, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := draft.Close(); err != nil {
					t.Error(err)
				}
			}()
			switch change {
			case "source":
				replaceMember(t, state.SourceFile, []byte("{}"))
			case "manifest":
				replaceMember(t, state.ManifestFile, []byte("{}"))
			case "directory":
				if err := os.Rename(path, path+".moved"); err != nil {
					if directoryRenameLocked(err) {
						// Native Windows OpenRoot uses a handle without delete
						// sharing. Prove its refusal kept the original usable;
						// a failed fixture rename is not a redirected-directory test.
						candidate, candidateErr := draft.Candidate(t.Context())
						if candidateErr != nil || !bytes.Equal(candidate, []byte(draftNative)) {
							t.Fatal("native directory lock changed the original draft", candidateErr)
						}
						if _, err := os.Stat(path + ".moved"); !errors.Is(err, os.ErrNotExist) {
							t.Fatal("refused native move published another directory", err)
						}
						return
					}
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			candidate, err := draft.Candidate(t.Context())
			if candidate != nil || !errors.Is(err, core.ErrIssueEditDraftChanged) || string(draft.Snapshot().DescriptionBytes) != draftNative {
				t.Fatal("changed backup/directory produced a candidate or changed immutable memory", err)
			}
		})
	}
}

func TestDraftRefusesProtectedEditsAndCanceledWorkWithoutLosingSavedText(t *testing.T) {
	t.Parallel()
	path, state := createDraft(t, draftNative)
	draft, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := draft.Close(); err != nil {
			t.Error(err)
		}
	}()
	saved := []byte(strings.Replace(draft.Markdown(), "protected", "editable", 1))
	replaceMember(t, state.MarkdownFile, saved)
	if candidate, err := draft.Candidate(t.Context()); err == nil || candidate != nil {
		t.Fatal("protected source became editable")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if candidate, err := draft.Candidate(ctx); !errors.Is(err, context.Canceled) || candidate != nil {
		t.Fatal("canceled draft returned a candidate", err)
	}
	after, err := os.ReadFile(state.MarkdownFile)
	if err != nil || !bytes.Equal(after, saved) {
		t.Fatal("refusal deleted authored recovery text", err)
	}
	if target, err := PrepareTarget(ctx, filepath.Join(t.TempDir(), "unstarted")); target != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled preparation performed admission", err)
	}
}

func TestDraftStrictManifestAndSourceAdmission(t *testing.T) {
	t.Parallel()
	for _, invalid := range []string{"manifest duplicate", "manifest trailing", "manifest version", "source duplicate", "source trailing", "source preimage", "Markdown UTF-8", "missing Markdown"} {
		t.Run(invalid, func(t *testing.T) {
			t.Parallel()
			path, state := createDraft(t, draftNative)
			ready, err := os.ReadFile(state.ManifestFile)
			if err != nil {
				t.Fatal(err)
			}
			var record manifest
			if err := json.Unmarshal(ready, &record); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(invalid, "manifest") {
				switch invalid {
				case "manifest duplicate":
					ready = bytes.Replace(ready, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
				case "manifest trailing":
					ready = append(ready, []byte("{}")...)
				case "manifest version":
					ready = bytes.Replace(ready, []byte(`"version":1`), []byte(`"version":2`), 1)
				}
				replaceMember(t, state.ManifestFile, ready)
			} else if strings.HasPrefix(invalid, "source") {
				source, err := os.ReadFile(state.SourceFile)
				if err != nil {
					t.Fatal(err)
				}
				switch invalid {
				case "source duplicate":
					source = bytes.Replace(source, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
				case "source trailing":
					source = append(source, []byte("{}")...)
				case "source preimage":
					var snapshot core.IssueDescriptionSnapshot
					if err := json.Unmarshal(source, &snapshot); err != nil {
						t.Fatal(err)
					}
					snapshot.DescriptionBytes = []byte("null")
					source, err = json.Marshal(snapshot)
					if err != nil {
						t.Fatal(err)
					}
				}
				record.SourceBytes, record.SourceSHA256 = len(source), digest(source)
				ready, err = json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				replaceMember(t, state.SourceFile, source)
				replaceMember(t, state.ManifestFile, ready)
			} else if invalid == "Markdown UTF-8" {
				replaceMember(t, state.MarkdownFile, []byte{0xff})
			} else if err := os.Remove(state.MarkdownFile); err != nil {
				t.Fatal(err)
			}
			draft, err := Open(t.Context(), path)
			if invalid == "Markdown UTF-8" {
				if err != nil {
					t.Fatal("invalid authored text could not be reopened for repair", err)
				}
				candidate, err := draft.Candidate(t.Context())
				if candidate != nil || !errors.Is(err, core.ErrIssueEditDraftInvalid) {
					t.Fatal("invalid UTF-8 was silently converted", err)
				}
				if err := draft.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || draft != nil {
				t.Fatal("incomplete or ambiguous draft became ready")
			}
		})
	}
}
