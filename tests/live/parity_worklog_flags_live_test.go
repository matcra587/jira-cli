//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type parityWorklogFlagRecipe struct {
	Key, Started, Source, Native, Markdown string
}

func parityWorklogFlagArgs(lane string, recipe parityWorklogFlagRecipe) ([]string, []string, error) {
	instant, err := time.Parse(time.RFC3339, recipe.Started)
	if err != nil || instant.UTC().Format(time.RFC3339) != recipe.Started || !liveKey.MatchString(recipe.Key) || (lane != "before" && lane != "after") || (recipe.Source != "inline" && recipe.Source != "file") {
		return nil, nil, fmt.Errorf("worklog flags require their fixed owned recipe and UTC start")
	}
	actual := []string{"worklog", "add", recipe.Key, "--time-spent", "2m", "--started", recipe.Started, "--parallelism=1"}
	if recipe.Source == "inline" {
		actual = append(actual, "--markdown", "**Owned café**")
	} else {
		actual = append(actual, "--markdown-file", recipe.Markdown)
	}
	canonical := []string{"worklog", "add", recipe.Key, "--json-input", recipe.Native, "--parallelism=1"}
	if lane == "after" {
		actual, canonical = append(actual, "--notify=false"), append(canonical, "--notify=false")
	}
	return actual, canonical, nil
}

func (r *liveRun) parityWorklogFlagInput(t *testing.T, key, started string) string {
	t.Helper()
	if !r.verified || r.ownedLiveIssue(key) == nil {
		t.Fatal("worklog flag input requires its verified active owned parent")
	}
	body := []byte(fmt.Sprintf(`{"time_spent":"2m","started":%q,"comment":%s}`, started, parityAuthoringDocument))
	file, err := os.CreateTemp(r.dir, "worklog-flags-*.json")
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(body)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("publish owned worklog flag input", writeErr, closeErr)
	}
	if r.worklogInputs == nil {
		r.worklogInputs = make(map[string]nativeWorklogInput)
	}
	r.worklogInputs[file.Name()] = nativeWorklogInput{keys: []string{key}, digest: sha256.Sum256(body)}
	return file.Name()
}

func (p *parityRun) validateWorklogFlagMutation(lane parityLane, recipe parityWorklogFlagRecipe, args []string) error {
	expected, canonical, err := parityWorklogFlagArgs(lane.Name, recipe)
	if err != nil || !slices.Equal(args, expected) {
		return fmt.Errorf("worklog flag argv differs from its exact owned recipe")
	}
	if err := p.validateWorklogMutation(lane, recipe.Key, canonical); err != nil {
		return err
	}
	native, err := os.ReadFile(recipe.Native)
	want := fmt.Sprintf(`{"time_spent":"2m","started":%q,"comment":%s}`, recipe.Started, parityAuthoringDocument)
	if err != nil || !parityAuthoringJSON(native, []byte(want)) {
		return fmt.Errorf("worklog flags differ from their fixed registered native input")
	}
	if recipe.Source == "inline" {
		if recipe.Markdown != "" {
			return fmt.Errorf("inline worklog flags cannot carry an unused file")
		}
		return nil
	}
	if recipe.Markdown != filepath.Join(p.owner.dir, filepath.Base(recipe.Markdown)) {
		return fmt.Errorf("worklog Markdown must be an exact direct child of its owned directory")
	}
	info, err := os.Lstat(recipe.Markdown)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("worklog Markdown must be an owned regular file")
	}
	body, err := os.ReadFile(recipe.Markdown)
	if err != nil || string(body) != "**Owned café**" {
		return fmt.Errorf("worklog Markdown differs from its exact authored bytes")
	}
	return nil
}

func parityWorklogFlagEffect(raw []byte, id, started string) bool {
	if !parityWorklogFlagID(id) || !jsontext.Value(raw).IsValid() || !parityCreateFlagsIdentity(raw, map[string]string{"id": id}) {
		return false
	}
	var fields map[string]json.RawMessage
	var stamp string
	if json.Unmarshal(raw, &fields) != nil || string(fields["timeSpentSeconds"]) != "120" || !parityAuthoringJSON(fields["comment"], []byte(parityAuthoringDocument)) || json.Unmarshal(fields["started"], &stamp) != nil {
		return false
	}
	observed, err := time.Parse("2006-01-02T15:04:05.999-0700", stamp)
	want, wantErr := time.Parse(time.RFC3339, started)
	return err == nil && wantErr == nil && observed.Equal(want)
}

func parityWorklogFlagID(id string) bool {
	n, err := strconv.ParseUint(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == id
}

func parityWorklogFlagReceipt(data []byte, lane, key, id, started string, native []byte) bool {
	if !parityWorklogFlagEffect(native, id, started) {
		return false
	}
	stamp := strings.TrimSuffix(started, "Z") + ".000+0000"
	payload := fmt.Sprintf(`{"timeSpentSeconds":120,"started":%q,"comment":%s}`, stamp, parityAuthoringDocument)
	var expected string
	switch lane {
	case "before":
		var fields map[string]json.RawMessage
		if json.Unmarshal(native, &fields) != nil {
			return false
		}
		expected = fmt.Sprintf(`{"issue":{"key":%q},"dry_run":false,"worklog":{"id":%q,"timeSpentSeconds":120,"started":%s,"comment":%s}}`, key, id, fields["started"], parityAuthoringDocument)
	case "after":
		expected = fmt.Sprintf(`{"dry_run":false,"payload":%s,"notify":false,"body_route":"markdown","workday_seconds":28800,"results":[{"issue":{"key":%q},"ok":true,"data":{"applied":true,"worklog":{"id":%q,"fields":%s}}}]}`, payload, key, id, native)
	default:
		return false
	}
	return parityAuthoringJSON(data, []byte(expected))
}

func TestParityWorklogFlagMutationAdmission(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "admitted-fixture")
	bytes := []byte("independent worklog flag admission bytes")
	if err := os.WriteFile(binary, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	lane := parityLane{Name: "after", Binary: binary, Digest: fmt.Sprintf("%x", sha256.Sum256(bytes))}
	r := &liveRun{verified: true, dir: dir, manifest: liveManifest{Issues: []ownedIssue{{Key: "PAIR-1"}}}}
	p := &parityRun{owner: r, evidence: parityEvidence{Lanes: []parityLane{lane}}}
	for _, source := range []string{"inline", "file"} {
		t.Run(source, func(t *testing.T) {
			recipe := parityWorklogFlagRecipe{Key: "PAIR-1", Started: "2026-01-02T12:30:00Z", Source: source, Native: r.parityWorklogFlagInput(t, "PAIR-1", "2026-01-02T12:30:00Z")}
			if source == "file" {
				recipe.Markdown = filepath.Join(dir, "worklog.md")
				if err := os.WriteFile(recipe.Markdown, []byte("**Owned café**"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args, _, err := parityWorklogFlagArgs(lane.Name, recipe)
			if err != nil || p.validateWorklogFlagMutation(lane, recipe, args) != nil {
				t.Fatal("complete fixed worklog flag recipe was refused")
			}
			for _, tc := range []struct {
				name   string
				change func(*parityWorklogFlagRecipe)
			}{
				{"foreign-parent", func(v *parityWorklogFlagRecipe) { v.Key = "PAIR-2" }},
				{"different-start", func(v *parityWorklogFlagRecipe) { v.Started = "2026-01-03T12:30:00Z" }},
				{"unregistered-native", func(v *parityWorklogFlagRecipe) { v.Native = filepath.Join(dir, "unknown.json") }},
				{"unsupported-source", func(v *parityWorklogFlagRecipe) { v.Source = "unregistered" }},
				{"outside-markdown", func(v *parityWorklogFlagRecipe) { v.Markdown = filepath.Join(filepath.Dir(dir), "other.md") }},
				{"non-UTC-start", func(v *parityWorklogFlagRecipe) { v.Started = "2026-01-02T12:30:00+01:00" }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					changed := recipe
					tc.change(&changed)
					actual, _, err := parityWorklogFlagArgs(lane.Name, changed)
					if err == nil && p.validateWorklogFlagMutation(lane, changed, actual) == nil {
						t.Fatal("different or unowned worklog recipe entered mutation")
					}
				})
			}
			for _, option := range []string{"PAIR-2", "--time-spent=3m", "--started=now", "--json-input=foreign", "--notify=true", "--dry-run"} {
				t.Run("extra/"+option, func(t *testing.T) {
					if p.validateWorklogFlagMutation(lane, recipe, append(slices.Clone(args), option)) == nil {
						t.Fatal("unauthored worklog argv entered mutation")
					}
				})
			}
			changed := lane
			changed.Digest = "unadmitted"
			if p.validateWorklogFlagMutation(changed, recipe, args) == nil {
				t.Fatal("unadmitted binary entered worklog flag mutation")
			}
			native, err := os.ReadFile(recipe.Native)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(recipe.Native, []byte(`{"time_spent":"3m"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if p.validateWorklogFlagMutation(lane, recipe, args) == nil {
				t.Fatal("changed registered worklog input entered mutation")
			}
			if err := os.WriteFile(recipe.Native, native, 0o600); err != nil {
				t.Fatal(err)
			}
			if source == "file" {
				for _, ancestor := range []bool{false, true} {
					t.Run(fmt.Sprintf("symlink/ancestor=%v", ancestor), func(t *testing.T) {
						outside := t.TempDir()
						leaf := filepath.Join(outside, "worklog.md")
						if err := os.WriteFile(leaf, []byte("**Owned café**"), 0o600); err != nil {
							t.Fatal(err)
						}
						link := filepath.Join(dir, fmt.Sprintf("worklog-link-%v", ancestor))
						target := leaf
						if ancestor {
							target = outside
						}
						if err := os.Symlink(target, link); err != nil {
							if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
								t.Skip("native Windows symlink privilege unavailable: ", err)
							}
							t.Fatal(err)
						}
						changed := recipe
						changed.Markdown = link
						if ancestor {
							changed.Markdown = filepath.Join(link, "worklog.md")
						}
						actual, _, err := parityWorklogFlagArgs(lane.Name, changed)
						if err != nil || p.validateWorklogFlagMutation(lane, changed, actual) == nil {
							t.Fatal("worklog Markdown symlink escaped owned input admission")
						}
					})
				}
				if err := os.WriteFile(recipe.Markdown, []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
				if p.validateWorklogFlagMutation(lane, recipe, args) == nil {
					t.Fatal("changed Markdown bytes entered worklog mutation")
				}
			}
		})
	}
}

func TestParityWorklogFlagNativeAndReceiptGuards(t *testing.T) {
	const started = "2026-01-02T12:30:00Z"
	const native = `{"id":"501","timeSpentSeconds":120,"started":"2026-01-02T12:30:00.000+0000","comment":` + parityAuthoringDocument + `}`
	if !parityWorklogFlagEffect([]byte(native), "501", started) {
		t.Fatal("independent native worklog flag control was refused")
	}
	for _, tc := range []struct{ name, old, value string }{
		{"wrong-id", `"id":"501"`, `"id":"502"`},
		{"wrong-case-id", `"id":`, `"ID":`},
		{"wrong-effort", `"timeSpentSeconds":120`, `"timeSpentSeconds":60`},
		{"wrong-start", "12:30:00.000", "13:30:00.000"},
		{"wrong-body", "Owned café", "foreign body"},
		{"duplicate-id", `"id":"501"`, `"id":"502","id":"501"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(native, tc.old, tc.value, 1)
			if changed == native || parityWorklogFlagEffect([]byte(changed), "501", started) {
				t.Fatal("different native effect earned worklog flag acceptance")
			}
		})
	}
	for _, id := range []string{"", "0", "0501", "../501", "-1", "opaque"} {
		t.Run("id/"+id, func(t *testing.T) {
			if parityWorklogFlagID(id) {
				t.Fatal("unbound resource ID admitted a native path")
			}
		})
	}
	const before = `{"issue":{"key":"PAIR-1"},"dry_run":false,"worklog":` + native + `}`
	const after = `{"dry_run":false,"payload":{"timeSpentSeconds":120,"started":"2026-01-02T12:30:00.000+0000","comment":` + parityAuthoringDocument + `},"notify":false,"body_route":"markdown","workday_seconds":28800,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":true,"worklog":{"id":"501","fields":` + native + `}}}]}`
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			raw := before
			if lane == "after" {
				raw = after
			}
			if !parityWorklogFlagReceipt([]byte(raw), lane, "PAIR-1", "501", started, []byte(native)) {
				t.Fatal("independent worklog flag receipt was refused")
			}
			for _, tc := range []struct{ name, old, value string }{
				{"wrong-parent", "PAIR-1", "PAIR-2"},
				{"wrong-effort", `"timeSpentSeconds":120`, `"timeSpentSeconds":60`},
				{"wrong-start", "12:30:00.000", "13:30:00.000"},
				{"wrong-body", "Owned café", "changed"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					changed := strings.Replace(raw, tc.old, tc.value, 1)
					if changed == raw || parityWorklogFlagReceipt([]byte(changed), lane, "PAIR-1", "501", started, []byte(native)) {
						t.Fatal("different receipt earned worklog flag acceptance")
					}
				})
			}
		})
	}
}

func TestLiveParityWorklogAuthoringFlags(t *testing.T) {
	p := newParityRun(t)
	r := p.owner
	started := time.Now().UTC().Add(-time.Hour).Truncate(time.Second).Format(time.RFC3339)
	for _, lane := range p.evidence.Lanes {
		if !t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			original := p.native(t, key)
			page, _ := p.nativeWorklogPage(t, key)
			if len(page.Native) != 0 {
				t.Fatal("fresh worklog flag parent has unexpected worklogs")
			}
			ids := []string{}
			for _, source := range []string{"inline", "file"} {
				if !t.Run(source, func(t *testing.T) {
					recipe := parityWorklogFlagRecipe{Key: key, Started: started, Source: source, Native: r.parityWorklogFlagInput(t, key, started)}
					if source == "file" {
						recipe.Markdown = filepath.Join(r.dir, "worklog-"+lane.Name+" spaces 古い '$;&.md")
						if err := os.WriteFile(recipe.Markdown, []byte("**Owned café**"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					args, _, err := parityWorklogFlagArgs(lane.Name, recipe)
					if err != nil || p.validateWorklogFlagMutation(lane, recipe, args) != nil {
						t.Fatal("fixed owned worklog flag recipe failed initial admission")
					}
					index := len(p.evidence.Worklogs)
					p.evidence.Worklogs = append(p.evidence.Worklogs, parityWorklogAttempt{Lane: lane.Name, Key: key, Digest: fmt.Sprintf("%x", r.worklogInputs[recipe.Native].digest)})
					p.save(t)
					if err := p.validateWorklogFlagMutation(lane, recipe, args); err != nil {
						t.Fatal("worklog flag recipe changed before entry")
					}
					frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_WORKDAY_SECONDS=28800"}, args...)
					id, acknowledged := parityWorklogAcknowledgement(stdout, lane.Name, key, exit == 0 && frame.OK)
					p.evidence.Worklogs[index].ID, p.evidence.Worklogs[index].Acknowledged = id, acknowledged
					p.save(t)
					if !parityWorklogFlagID(id) || slices.Contains(ids, id) {
						t.Fatal("worklog flags returned no fresh target-bound resource; retain intent and never replay")
					}
					read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/worklog/"+id)
					if !parityCreateFlagsIdentity(read.Response.JSON, map[string]string{"id": id}) {
						t.Fatal("worklog resource is not independently bound to its owned parent")
					}
					p.evidence.Worklogs[index].NativeID, p.evidence.Worklogs[index].Bound = id, true
					p.save(t)
					if !parityWorklogFlagEffect(read.Response.JSON, id, started) {
						t.Fatal("native worklog flags changed effort, start or Markdown body")
					}
					ids = append(ids, id)
					stored, _ := p.nativeWorklogPage(t, key)
					seen := make(map[string]bool)
					for _, record := range stored.Native {
						var fields map[string]json.RawMessage
						var nativeID string
						if json.Unmarshal(record, &fields) != nil || json.Unmarshal(fields["id"], &nativeID) != nil || seen[nativeID] || !slices.Contains(ids, nativeID) || !parityWorklogFlagEffect(record, nativeID, started) {
							t.Fatal("complete native worklog inventory gained an unexpected or changed record")
						}
						seen[nativeID] = true
					}
					if len(seen) != len(ids) || !acknowledged || exit != 0 || !frame.OK {
						t.Fatal("worklog flag effect lost complete single-creation acknowledgement; never replay")
					}
					parityAuthoringCleanData(t, stdout, lane.Name, "worklog.add")
					native, ok := parityWorklogReceiptRecord(stdout, lane.Name, key)
					if !ok || !parityWorklogFlagReceipt(parityOriginalFrameData(stdout), lane.Name, key, id, started, native) {
						t.Fatal("worklog flags lost their requested fields or understood native POST record")
					}
					if err := p.validateWorklogFlagMutation(lane, recipe, args); err != nil {
						t.Fatal("worklog flags changed frozen authoring inputs")
					}
					p.check(t, lane, "C072.worklog-flags."+source+".native")
				}) {
					return
				}
			}
			current := p.native(t, key)
			if current.ID != original.ID || !parityAuthoringJSON(current.Fields, original.Fields) {
				t.Fatal("worklog flags changed unrelated owned issue fields")
			}
		}) {
			return
		}
	}
}
