//go:build live

package live

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// Only owned public settings are published. Credential storage and Jira remain
// untouched; the native identity and public routing scope are read independently.
func TestLiveWhoamiSavePublishesVerifiedBoundIdentity(t *testing.T) {
	r := newLiveRun(t)
	native := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var user jira.User
	if err := json.Unmarshal(native.Response.JSON, &user); err != nil || !user.AccountID.Set || user.AccountID.Null || strings.TrimSpace(user.AccountID.Value) == "" {
		t.Fatal("verified saving requires independent native identity")
	}
	frame, status, exit := r.invoke(t, t.Context(), nil, "auth", "status")
	var scope struct {
		Data envelope.AuthStatusOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(status, &scope) != nil || scope.Data.BaseURL != r.manifest.BaseURL || scope.Data.Email != r.account || scope.Data.AccountID != user.AccountID.Value {
		t.Fatal("independent public identity scope unavailable")
	}
	path := filepath.Join(r.dir, "verified-identity", "settings.toml")
	decode := func(data []byte) any {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var result any
		if err := decoder.Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, phase := range []string{"preview", "save", "unchanged"} {
		t.Run(phase, func(t *testing.T) {
			args := []string{"auth", "whoami", "--save", "--config", path}
			if phase == "preview" {
				args = append(args, "--dry-run")
			}
			frame, stdout, exit := r.invoke(t, t.Context(), nil, args...)
			var out struct {
				Data envelope.AuthWhoamiSaveOutput `json:"data"`
			}
			if exit != 0 || !frame.OK || json.Unmarshal(stdout, &out) != nil {
				t.Fatal("actual verified identity saving failed")
			}
			var raw struct {
				Data struct {
					User json.RawMessage `json:"user"`
				} `json:"data"`
			}
			if json.Unmarshal(stdout, &raw) != nil || !reflect.DeepEqual(decode(raw.Data.User), decode(native.Response.JSON)) || out.Data.Scope.BaseURL != scope.Data.BaseURL || out.Data.Scope.Email != scope.Data.Email || out.Data.Scope.CloudID != scope.Data.CloudID {
				t.Fatal("native identity or independently bound public scope changed")
			}
			if phase == "preview" {
				if !out.Data.DryRun || !out.Data.Settings.WouldChange || out.Data.Settings.Saved || out.Data.Settings.Changed || out.Data.Settings.Created {
					t.Fatal("preview fabricated publication")
				}
				if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("preview created owned directories")
				}
				return
			}
			if out.Data.DryRun || !out.Data.Settings.Saved || out.Data.Settings.Changed != (phase == "save") || out.Data.Settings.Created != (phase == "save") || out.Data.Settings.WouldChange != (phase == "save") {
				t.Fatal("actual publication receipt differs from observed phase")
			}
			file, err := config.ReadFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{"account_id": user.AccountID.Value, "account_id_site": scope.Data.BaseURL, "account_id_email": scope.Data.Email, "account_id_cloud_id": scope.Data.CloudID} {
				setting, err := file.Setting(key)
				if err != nil || !setting.Present || setting.Secret || setting.Value != want {
					t.Fatal("actual settings lack independently observed verified binding", key)
				}
			}
		})
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(before)
	offline := cachedAccountOfflineEnvironment(os.Environ(), scope.Data.BaseURL, scope.Data.Email, scope.Data.CloudID)
	frame, stdout, exit := r.invoke(t, t.Context(), offline, "issue", "create", "--project", r.manifest.Project, "--type", r.issueType, "--summary", "Local verified draft", "--assignee=@me", "--reporter=@me", "--config", path, "--dry-run")
	var preview struct {
		Data     any                `json:"data"`
		Warnings []envelope.Warning `json:"warnings"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &preview) != nil || !cachedAccountLeaf(preview.Data, user.AccountID.Value) || cachedAccountLeaf(preview.Data, "@me") {
		t.Fatal("saved verified ID was not consumed offline")
	}
	warnings := 0
	for _, warning := range preview.Warnings {
		if warning.Type == "account_id_cached" {
			warnings++
		}
	}
	if warnings != 1 {
		t.Fatal("saved cached ID lost unverified local-preview provenance")
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(after) != digest {
		t.Fatal("offline preview changed the owned verified settings")
	}
}
