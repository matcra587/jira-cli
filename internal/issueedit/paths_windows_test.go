//go:build windows && nativefilesystem

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

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/testfixture"
)

func TestNativeWindowsDraftPaths(t *testing.T) {
	t.Run("junction ancestor", func(t *testing.T) {
		dir := t.TempDir()
		actual, link := filepath.Join(dir, "actual '古い"), filepath.Join(dir, "selected 'é")
		if err := os.MkdirAll(filepath.Join(actual, "nested"), 0o700); err != nil {
			t.Fatal(err)
		}
		before := testfixture.Junction(t, link, actual)
		selected := filepath.Join(link, "nested", "literal~$draft")
		want := filepath.Join(actual, "nested", "literal~$draft")
		target, err := PrepareTarget(t.Context(), selected)
		if err != nil {
			t.Fatal("junction parent could not prepare a draft", err)
		}
		defer func() {
			if err := target.Close(); err != nil {
				t.Error(err)
			}
		}()
		if target.State().Directory != want {
			t.Fatalf("draft target was not bound to its canonical parent: got=%q want=%q", target.State().Directory, want)
		}
		if _, err := os.Lstat(want); !os.IsNotExist(err) {
			t.Fatalf("draft preparation published a directory: %v", err)
		}
		state, err := target.Create(t.Context(), draftSnapshot(draftNative))
		if err != nil || !state.Ready || !state.DirectoryCreated || state.Directory != want {
			t.Fatalf("junction draft publication failed: state=%+v error=%v", state, err)
		}
		checkNativeWindowsDraft(t, selected, want)
		testfixture.SameJunction(t, link, actual, before)
	})
	for _, suffix := range []string{".", " "} {
		t.Run("literal suffix "+suffix, func(t *testing.T) {
			dir := t.TempDir()
			ordinary := filepath.Join(dir, "draft")
			if err := os.Mkdir(ordinary, 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(ordinary, "retain.txt")
			const untouched = "ordinary sibling must remain untouched\n"
			if err := os.WriteFile(marker, []byte(untouched), 0o600); err != nil {
				t.Fatal(err)
			}
			literal := `\\?\` + ordinary + suffix
			t.Cleanup(func() {
				if err := os.RemoveAll(literal); err != nil {
					t.Error(err)
				}
			})
			target, err := PrepareTarget(t.Context(), literal)
			if err != nil {
				t.Fatal("literal draft name selected the ordinary sibling", err)
			}
			defer func() {
				if err := target.Close(); err != nil {
					t.Error(err)
				}
			}()
			if target.State().Directory != literal {
				t.Fatalf("literal draft name changed: got=%q want=%q", target.State().Directory, literal)
			}
			state, err := target.Create(t.Context(), draftSnapshot(draftNative))
			if err != nil || !state.Ready || state.Directory != literal {
				t.Fatalf("literal draft publication failed: state=%+v error=%v", state, err)
			}
			checkNativeWindowsDraft(t, literal, literal)
			got, err := os.ReadFile(marker)
			entries, readErr := os.ReadDir(ordinary)
			if err != nil || string(got) != untouched || readErr != nil || len(entries) != 1 {
				t.Fatalf("literal draft changed ordinary sibling: bytes=%q read=%v entries=%v error=%v", got, err, entries, readErr)
			}
		})
		t.Run("literal creation race suffix "+suffix, func(t *testing.T) {
			literal := `\\?\` + filepath.Join(t.TempDir(), "draft") + suffix
			t.Cleanup(func() {
				if err := os.RemoveAll(literal); err != nil {
					t.Error(err)
				}
			})
			target, err := PrepareTarget(t.Context(), literal)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := target.Close(); err != nil {
					t.Error(err)
				}
			}()
			if err := os.Mkdir(literal, 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(literal, "retain.txt")
			const retained = "existing literal directory\n"
			if err := os.WriteFile(marker, []byte(retained), 0o600); err != nil {
				t.Fatal(err)
			}
			state, err := target.Create(t.Context(), draftSnapshot(draftNative))
			entries, readErr := os.ReadDir(literal)
			got, markerErr := os.ReadFile(marker)
			if !errors.Is(err, core.ErrIssueEditDraftChanged) || state.DirectoryCreated || state.SourcePublished || state.Ready || readErr != nil || len(entries) != 1 || markerErr != nil || string(got) != retained {
				t.Fatalf("literal race changed prior content or claimed publication: state=%+v error=%v entries=%v read=%v marker=%v", state, err, entries, readErr, markerErr)
			}
		})
		t.Run("literal reopened identity suffix "+suffix, func(t *testing.T) {
			dir := t.TempDir()
			literal := `\\?\` + filepath.Join(dir, "draft") + suffix
			other := filepath.Join(dir, "other")
			if err := os.Mkdir(other, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(literal); err != nil {
					t.Error(err)
				}
			})
			target, err := PrepareTarget(t.Context(), literal)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := target.Close(); err != nil {
					t.Error(err)
				}
			}()
			// Independently substitute the reopened path at this private owner
			// boundary. Native Windows locks prevent renaming the held child.
			target.state = draftState(other)
			state, err := target.Create(t.Context(), draftSnapshot(draftNative))
			if !errors.Is(err, core.ErrIssueEditDraftChanged) || !state.DirectoryCreated || state.SourcePublished || state.Ready {
				t.Fatalf("another reopened directory authorized member publication: state=%+v error=%v", state, err)
			}
			for _, path := range []string{literal, other} {
				entries, err := os.ReadDir(path)
				if err != nil || len(entries) != 0 {
					t.Fatalf("identity refusal published members: %q %v %v", path, entries, err)
				}
			}
		})
		t.Run("literal late cancellation suffix "+suffix, func(t *testing.T) {
			literal := `\\?\` + filepath.Join(t.TempDir(), "draft") + suffix
			t.Cleanup(func() {
				if err := os.RemoveAll(literal); err != nil {
					t.Error(err)
				}
			})
			target, err := PrepareTarget(t.Context(), literal)
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
			observed := cancelWhenDirectoryExists{Context: ctx, cancel: cancel, path: literal}
			state, err := target.Create(observed, draftSnapshot(draftNative))
			entries, readErr := os.ReadDir(literal)
			if !errors.Is(err, context.Canceled) || !state.DirectoryCreated || state.SourcePublished || state.Ready || readErr != nil || len(entries) != 0 {
				t.Fatalf("late cancellation lost acknowledged creation or published members: state=%+v error=%v entries=%v read=%v", state, err, entries, readErr)
			}
		})
	}
	t.Run("junction draft root refuses", func(t *testing.T) {
		path, state := createDraft(t, draftNative)
		link := filepath.Join(t.TempDir(), "selected")
		before := testfixture.Junction(t, link, path)
		source, err := os.ReadFile(state.SourceFile)
		if err != nil {
			t.Fatal(err)
		}
		draft, err := Open(t.Context(), link)
		if draft != nil || !errors.Is(err, core.ErrIssueEditDraftChanged) {
			if draft != nil {
				if closeErr := draft.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			}
			t.Fatalf("junction draft root supplied a candidate owner: %v", err)
		}
		after, err := os.ReadFile(state.SourceFile)
		if err != nil || !bytes.Equal(source, after) {
			t.Fatalf("root refusal changed native backup: %v", err)
		}
		testfixture.SameJunction(t, link, path, before)
	})
	for _, member := range []string{SourceName, MarkdownName, ManifestName} {
		t.Run("junction member "+member+" refuses", func(t *testing.T) {
			path, _ := createDraft(t, draftNative)
			outside := t.TempDir()
			marker := filepath.Join(outside, "retain.txt")
			const retained = "outside the draft root\n"
			if err := os.WriteFile(marker, []byte(retained), 0o600); err != nil {
				t.Fatal(err)
			}
			selected := filepath.Join(path, member)
			if err := os.Chmod(selected, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(selected); err != nil {
				t.Fatal(err)
			}
			before := testfixture.Junction(t, selected, outside)
			draft, err := Open(t.Context(), path)
			if draft != nil || !errors.Is(err, core.ErrIssueEditDraftInvalid) {
				if draft != nil {
					if closeErr := draft.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				}
				t.Fatalf("junction member supplied a candidate owner: %v", err)
			}
			got, err := os.ReadFile(marker)
			entries, readErr := os.ReadDir(outside)
			if err != nil || string(got) != retained || readErr != nil || len(entries) != 1 {
				t.Fatalf("member refusal changed outside content: bytes=%q read=%v entries=%v error=%v", got, err, entries, readErr)
			}
			testfixture.SameJunction(t, selected, outside, before)
		})
	}
}

// Cancel at the next context boundary after native directory creation. Checking
// the real fixture avoids depending on how many earlier Err calls the owner made.
type cancelWhenDirectoryExists struct {
	context.Context //nolint:containedctx // This test context overrides Err at the observed native creation boundary.
	cancel          context.CancelFunc
	path            string
}

func (c cancelWhenDirectoryExists) Err() error {
	if _, err := os.Lstat(c.path); err == nil {
		c.cancel()
	}
	return c.Context.Err()
}

func checkNativeWindowsDraft(t *testing.T, selected, canonical string) {
	t.Helper()
	draft, err := Open(t.Context(), selected)
	if err != nil {
		t.Fatal("published native draft could not reopen", err)
	}
	defer func() {
		if err := draft.Close(); err != nil {
			t.Error(err)
		}
	}()
	state := draft.State()
	if state.Directory != canonical {
		t.Fatalf("reopened draft lost canonical path: got=%q want=%q", state.Directory, canonical)
	}
	source, err := os.ReadFile(state.SourceFile)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot core.IssueDescriptionSnapshot
	if err := json.Unmarshal(source, &snapshot); err != nil || string(snapshot.DescriptionBytes) != draftNative {
		t.Fatalf("native backup lost exact preimage: %v", err)
	}
	candidate, err := draft.Candidate(t.Context())
	if err != nil || string(candidate) != draftNative {
		t.Fatalf("untouched native draft changed bytes: %q %v", candidate, err)
	}
	authored := strings.Replace(draft.Markdown(), "alpha", "authored", 1)
	if authored == draft.Markdown() {
		t.Fatal("fixture did not find editable Markdown")
	}
	if err := os.WriteFile(state.MarkdownFile, []byte(authored), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, err = draft.Candidate(t.Context())
	after, readErr := os.ReadFile(state.SourceFile)
	if err != nil || readErr != nil || !bytes.Equal(source, after) || !bytes.Contains(candidate, []byte(`"text":"authored"`)) || !bytes.Contains(candidate, []byte(`"nativeCase":"retained"`)) || !bytes.Contains(candidate, []byte(`9007199254740993`)) {
		t.Fatalf("native draft edit lost saved or protected state: candidate=%q error=%v backup=%v", candidate, err, readErr)
	}
}
