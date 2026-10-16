//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func parityOpenProjection(data []byte, lane, key, site string) bool {
	if !jsontext.Value(data).IsValid() || key == "" {
		return false
	}
	field := "url"
	if lane == "after" {
		field = "web_url"
	} else if lane != "before" {
		return false
	}
	want := map[string]any{"issue": map[string]any{"key": key}, field: strings.TrimRight(site, "/") + "/browse/" + key, "opened": false}
	expected, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, expected)
}

// The URL-only producer must publish only its public scoped key history. This
// reader uses native wrappers and paths, never the command's acknowledgement.
func parityOpenHistory(t *testing.T, home, lane, site, key string, earliest time.Time) string {
	t.Helper()
	var path, identity string
	if lane == "before" {
		identity, path = parityMetadataLegacyLocation(home, site, "issuekeys")
	} else {
		scope := fmt.Sprintf("%d:%s%d:%s", len(site), site, len("fixture@example.invalid"), "fixture@example.invalid")
		namespace := fmt.Sprintf("%x", sha256.Sum256([]byte(scope)))
		identity = "scope-v2:" + namespace + ":v1:issuekeys"
		path = filepath.Join(home, "jira", "scope-v2-"+namespace, fmt.Sprintf("%x.json", sha256.Sum256([]byte(identity))))
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("open did not publish owned native scoped history")
	}
	raw, err := os.ReadFile(path)
	if err != nil || !jsontext.Value(raw).IsValid() {
		t.Fatal("open history is not a complete native JSON wrapper")
	}
	var wrapper struct {
		Profile, Resource, Key string
		Schema                 int
		FetchedAt              time.Time `json:"fetched_at"`
		Written                time.Time
		Data                   json.RawMessage
		Value                  []byte
	}
	if json.Unmarshal(raw, &wrapper) != nil {
		t.Fatal("open history wrapper is malformed")
	}
	payload, written := wrapper.Value, wrapper.Written
	if lane == "before" {
		if wrapper.Profile != identity || wrapper.Resource != "issuekeys" || wrapper.Schema != 1 {
			t.Fatal("open history changed legacy scope/schema")
		}
		payload, written = wrapper.Data, wrapper.FetchedAt
	} else if wrapper.Key != identity {
		t.Fatal("open history changed public site/account scope")
	}
	expected, err := json.Marshal([]string{key})
	if err != nil || !parityAuthoringJSON(payload, expected) || written.Before(earliest) || written.After(time.Now()) {
		t.Fatal("open history differs from its explicit normalized key or publication interval")
	}
	relative, err := filepath.Rel(home, path)
	if err != nil || !filepath.IsLocal(relative) {
		t.Fatal("open history escaped its owned home")
	}
	return relative
}

func parityOpenOnlyHistory(t *testing.T, home, path string, before map[string]parityLocalFile) {
	t.Helper()
	after := parityLocalTree(t, home)
	for name, original := range before {
		if observed, present := after[name]; !present || observed != original {
			t.Fatal("open altered an existing owned file or directory")
		}
	}
	allowed := map[string]bool{path: true}
	for parent := filepath.Dir(path); parent != "."; parent = filepath.Dir(parent) {
		allowed[parent] = true
	}
	for name := range after {
		if _, present := before[name]; !present && !allowed[name] {
			t.Fatal("open published files outside its one scoped history")
		}
	}
}

func TestParityActualBinaryOpen(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		for _, prefix := range []string{"", "/jira"} {
			for _, mode := range []string{"json", "compact"} {
				for _, seed := range []struct{ name, value string }{{"literal", "KEY-7"}, {"padded", "KEY-0007"}, {"duplicate", "KEY-7,KEY-7"}, {"one-key-range", "KEY-7:7"}} {
					t.Run(lane.name+"/prefix="+prefix+"/"+mode+"/"+seed.name, func(t *testing.T) {
						if lane.binary == "" {
							t.Skip("requires selected actual binary")
						}
						var calls atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
							calls.Add(1)
							http.Error(w, "URL-only command entered HTTP", 500)
						}))
						defer server.Close()
						site := server.URL + prefix
						home := t.TempDir()
						config := parityCachePublicConfig(t, home, lane.name, site)
						before := parityLocalTree(t, home)
						began := time.Now()
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						result := parityFixtureProcessAt(t, lane.name, lane.binary, site, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false", "BROWSER=" + filepath.Join(home, "unavailable-browser"), "JIRA_READ_ONLY=true"}, "open", seed.value)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != 0 {
							t.Fatalf("URL-only open failed or entered HTTP: exit %d; HTTP %d; stdout %s; stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
						}
						data := result.Stdout
						if mode == "json" {
							var err error
							data, err = json.Marshal(parityAuthoringCleanData(t, data, lane.name, "open"))
							if err != nil {
								t.Fatal(err)
							}
						}
						if !parityOpenProjection(data, lane.name, "KEY-7", site) {
							t.Fatalf("open changed native URL/key/handoff: %s", result.Stdout)
						}
						path := parityOpenHistory(t, home, lane.name, site, "KEY-7", began)
						parityOpenOnlyHistory(t, home, path, before)
						id := fmt.Sprintf("C061.open.prefix=%t.%s.%s", prefix != "", mode, seed.name)
						evidence.Checks[id] = append(evidence.Checks[id], lane.name)
					})
				}
			}
		}
		for _, mode := range []string{"json", "compact"} {
			t.Run(lane.name+"/refusal/"+mode, func(t *testing.T) {
				if lane.binary == "" {
					t.Skip("requires selected actual binary")
				}
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					http.Error(w, "refused URL entered HTTP", 500)
				}))
				defer server.Close()
				home := t.TempDir()
				config := parityCachePublicConfig(t, home, lane.name, server.URL)
				before := parityLocalTree(t, home)
				if err := parityFingerprint(lane.binary, lane.digest); err != nil {
					t.Fatal(err)
				}
				result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false"}, "open", "KEY-7:8")
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
				if calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) || !parityOpenRefusal(result.Stdout, lane.name, result.Exit) {
					t.Fatalf("ambiguous open changed local files/classification: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
				}
				id := "C061.open.refusal." + mode
				evidence.Checks[id] = append(evidence.Checks[id], lane.name)
			})
		}
	}
	saveParityLocal(t, "open", evidence)
}

func parityOpenRefusal(stdout []byte, lane string, exit int) bool {
	code, message := "validation_failed", "validation: open takes exactly one issue key"
	if lane == "after" {
		code = "arg_value_invalid"
		message = "open takes exactly one distinct issue key; use issue view KEY --web for each wanted issue"
	}
	if !parityLocalRefusal(stdout, lane, "open", exit, code) {
		return false
	}
	var frame struct {
		Errors []struct{ Message, Field string }
	}
	if json.Unmarshal(stdout, &frame) != nil || frame.Errors[0].Message != message {
		return false
	}
	return lane == "before" || frame.Errors[0].Field == "ISSUE_KEY"
}

func TestParityOpenProjectionRejectsChangedMeaning(t *testing.T) {
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			field := "url"
			if lane == "after" {
				field = "web_url"
			}
			original := []byte(`{"issue":{"key":"KEY-7"},"` + field + `":"https://example.invalid/jira/browse/KEY-7","opened":false}`)
			if !parityOpenProjection(original, lane, "KEY-7", "https://example.invalid/jira") {
				t.Fatal("independent literal URL receipt refused")
			}
			for _, mutation := range []struct{ name, original, replacement string }{
				{"wrong-key", `"key":"KEY-7"`, `"key":"KEY-8"`},
				{"wrong-prefix", "/jira/browse/", "/browse/"},
				{"false-handoff", `"opened":false`, `"opened":true`},
				{"invented-id", `"key":"KEY-7"`, `"key":"KEY-7","id":"123"`},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					changed := strings.Replace(string(original), mutation.original, mutation.replacement, 1)
					if changed == string(original) || parityOpenProjection([]byte(changed), lane, "KEY-7", "https://example.invalid/jira") {
						t.Fatal("changed URL/identity/handoff earned local open acceptance")
					}
				})
			}
		})
	}
}

func TestLiveParityOpenURL(t *testing.T) {
	p := newParityRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !liveKey.MatchString(key) || !strings.HasPrefix(key, p.owner.manifest.Project+"-") {
		t.Fatal("paired open observation requires the explicit dedicated-project fixture")
	}
	reply := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=project")
	var native struct {
		ID, Key string
		Fields  struct{ Project struct{ ID, Key string } }
	}
	if json.Unmarshal(reply.Response.JSON, &native) != nil || native.ID == "" || native.Key != key || native.Fields.Project.ID == "" || native.Fields.Project.Key != p.owner.manifest.Project {
		t.Fatal("paired URL fixture lacks independent native issue/project identity")
	}
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			extra := []string{"JIRA_READ_ONLY=true", "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_EMAIL=" + p.owner.account, "BROWSER=/does/not/exist"}
			frame, stdout, exit := p.invoke(t, lane, extra, "open", key)
			if exit != 0 || !frame.OK {
				t.Fatal("paired URL-only invocation failed; inspect retained receipts")
			}
			data, err := json.Marshal(parityAuthoringCleanData(t, stdout, lane.Name, "open"))
			if err != nil || !parityOpenProjection(data, lane.Name, key, p.owner.manifest.BaseURL) {
				t.Fatal("paired open URL differs from independent native public identity")
			}
			parityLiveHistoryStored(t, p, lane, []string{key})
			p.check(t, lane, "C061.open.native-url")
		})
	}
}

func TestParityOpenOperationExcludesPositionalKey(t *testing.T) {
	for _, lane := range []string{"before", "after"} {
		for _, exit := range []int{0, 3} {
			if parityExpectedOperation(lane, []string{"open", "KEY-7"}, exit) != "open" {
				t.Fatal("top-level open operation consumed its positional issue key")
			}
		}
	}
	if parityOperation([]string{"open-other", "KEY-7"}) == "open" {
		t.Fatal("operation mapping guessed an open identity from a command prefix")
	}
}
