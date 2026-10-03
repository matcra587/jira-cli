//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestLiveCloneDateInputBoundToOwnedSource(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, dir: t.TempDir(), manifest: liveManifest{Issues: []ownedIssue{
		{Key: "KEY-123", ID: "10001"}, {Key: "KEY-124", ID: "10002"},
	}}}
	path := r.nativeCloneDateInput(t, "KEY-123", "customfield_10011")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("native clone date input must be a private file")
	}
	var got map[string]map[string]string
	want := map[string]map[string]string{"fields": {"customfield_10011": "2026-01-03"}}
	if json.Unmarshal(data, &got) != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("native clone date input changed the fixed scalar stimulus")
	}
	if registered := r.issueInputs[path]; registered.key != "KEY-123" || registered.parent != "" || registered.digest != sha256.Sum256(data) {
		t.Fatal("native clone date input lacks its owned-source byte binding")
	}
	args := []string{"issue", "edit", "KEY-123", "--json-input", path}
	if err := r.checkMutation([]string{"KEY-123"}, args); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string][]string{
		"retarget": {"issue", "edit", "KEY-124", "--json-input", path},
		"fanout":   {"issue", "edit", "KEY-123", "--json-input", path, "KEY-124"},
		"selector": {"issue", "edit", "KEY-123", "--json-input", path, "--jql", "project = KEY"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := r.checkMutation([]string{call[2]}, call); err == nil {
				t.Fatal("native clone date input accepted an expanded mutation route")
			}
		})
	}
	r.manifest.Issues[0].Deleted = true
	if err := r.checkMutation([]string{"KEY-123"}, args); err == nil {
		t.Fatal("native clone date input accepted a deleted source")
	}
	r.manifest.Issues[0].Deleted = false
	if err := os.WriteFile(path, []byte(`{"fields":{"customfield_10011":"2026-01-04"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.checkMutation([]string{"KEY-123"}, args); err == nil {
		t.Fatal("native clone date input accepted changed bytes")
	}
}

func TestLiveNativeIssueInputBoundToOwnedTarget(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, dir: t.TempDir(), manifest: liveManifest{Issues: []ownedIssue{
		{Key: "KEY-123", ID: "10001"},
		{Key: "KEY-124", ID: "10002"},
		{Key: "KEY-125", ID: "10003", Deleted: true},
	}}}
	_, path := r.nativePropertyInput(t, "KEY-123", false)
	args := []string{"issue", "edit", "KEY-123", "--json-input", path}
	for _, preview := range []bool{false, true} {
		call := append([]string(nil), args...)
		if preview {
			call = append(call, "--dry-run")
		}
		if err := r.checkMutation([]string{"KEY-123"}, call); err != nil {
			t.Fatalf("registered property edit preview=%t refused: %v", preview, err)
		}
	}
	for name, call := range map[string][]string{
		"different-owned-target": {"issue", "edit", "KEY-124", "--json-input", path},
		"deleted-target":         {"issue", "edit", "KEY-125", "--json-input", path},
		"clone-route":            {"issue", "clone", "KEY-123", "--json-input", path},
		"extra-option":           {"issue", "edit", "KEY-123", "--json-input", path, "--summary", "changed"},
		"extra-payload":          {"issue", "edit", "KEY-123", "--json-input", path, "--json-input", path},
		"extra-target":           {"issue", "edit", "KEY-123", "--json-input", path, "KEY-124"},
		"unregistered-input":     {"issue", "edit", "KEY-123", "--json-input", "unregistered.json"},
		"equal-form":             {"issue", "edit", "KEY-123", "--json-input=" + path},
		"selector":               {"issue", "edit", "KEY-123", "--json-input", path, "--jql", "project = KEY"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := r.checkMutation([]string{call[2]}, call); err == nil {
				t.Errorf("native issue guard accepted %s, want refusal", name)
			}
		})
	}
	if err := r.checkMutation([]string{"KEY-123", "KEY-124"}, args); err == nil {
		t.Error("native issue guard accepted multiple declared targets")
	}
	if err := os.WriteFile(path, []byte(`{"properties":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.checkMutation([]string{"KEY-123"}, args); err == nil {
		t.Error("native issue guard accepted input modified after registration")
	}
}

func TestLiveParentInputRequiresActiveOwnedParent(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, dir: t.TempDir(), manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-1", ID: "1"}, {Key: "KEY-2", ID: "2"}}}}
	path := r.parentInput(t, "KEY-1", "KEY-2")
	args := []string{"issue", "edit", "KEY-1", "--json-input", path}
	if err := r.checkMutation([]string{"KEY-1"}, args); err != nil {
		t.Fatal(err)
	}
	r.manifest.Issues[1].Deleted = true
	if err := r.checkMutation([]string{"KEY-1"}, args); err == nil {
		t.Fatal("deleted parent accepted")
	}
	r.manifest.Issues = r.manifest.Issues[:1]
	if err := r.checkMutation([]string{"KEY-1"}, args); err == nil {
		t.Fatal("foreign parent accepted")
	}
}
