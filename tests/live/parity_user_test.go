//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

const parityUsersFixture = `[{"accountId":"owned-visible","displayName":"Owned <&> café","active":true,"emailAddress":null,"deleted":false},{"accountId":"owned-hidden","displayName":"Other visible","active":true},{"accountId":"owned-inactive","displayName":"Inactive","active":false},{"accountId":"owned-deleted","displayName":"Deleted","active":true,"deleted":true},{"accountId":"owned-unknown","displayName":"Unknown activity","active":null}]`

// These literals bind the two synthetic samples independently of the human
// renderer. Legacy populated output has no heading: its exact row count is the
// count receipt. Current output adds explicit query, paging and privacy facts.
func parityUserSearchHuman(stdout []byte, lane string, empty bool) bool {
	text := string(stdout)
	if lane == "before" {
		if empty {
			return text == "INF ℹ️ No users matched query=\"--owned <&> café\"\n"
		}
		return text == "INF ℹ️ Owned <&> café account_id=owned-visible\nINF ℹ️ Other visible account_id=owned-hidden\n"
	}
	if lane != "after" {
		return false
	}
	const privacy = "Visibility depends on permissions; email depends on privacy settings. Empty results do not prove an account is absent."
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if empty {
		return len(lines) == 2 && lines[0] == "0 visible active matches for \"--owned <&> café\"" && lines[1] == privacy
	}
	return len(lines) == 7 && lines[0] == "2 visible active matches for \"--owned <&> café\"" &&
		reflect.DeepEqual(strings.Fields(lines[1]), []string{"ACCOUNT", "ID", "NAME", "EMAIL"}) &&
		regexp.MustCompile(`^owned-visible[\t ]+Owned <&> café[\t ]+-$`).MatchString(lines[2]) &&
		regexp.MustCompile(`^owned-hidden[\t ]+Other visible[\t ]+-$`).MatchString(lines[3]) &&
		lines[4] == "3 inactive, deleted or unknown-active rows excluded." &&
		lines[5] == "Next probe: jira user search '--owned <&> café' --start 5" && lines[6] == privacy
}

// This projection uses independent native JSON, not the production identity
// filter or generated user model. Only the command's documented summary fields
// are compared; it does not promise passthrough of the entire native directory.
func parityUserSearchProjection(data, native []byte, lane, query string) bool {
	if (lane != "before" && lane != "after") || !jsontext.Value(data).IsValid() || !jsontext.Value(native).IsValid() {
		return false
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(native, &rows) != nil || rows == nil || len(rows) > 50 {
		return false
	}
	selected := make([]map[string]any, 0, len(rows))
	seen := make(map[string]bool)
	for _, row := range rows {
		var id, name, email string
		var active, deleted *bool
		if row == nil || json.Unmarshal(row["accountId"], &id) != nil || strings.TrimSpace(id) == "" || seen[id] {
			return false
		}
		seen[id] = true
		if raw, present := row["active"]; present && json.Unmarshal(raw, &active) != nil {
			return false
		}
		if raw, present := row["deleted"]; present && json.Unmarshal(raw, &deleted) != nil {
			return false
		}
		if active == nil || !*active || (deleted != nil && *deleted) {
			continue
		}
		if raw, present := row["displayName"]; present && json.Unmarshal(raw, &name) != nil {
			return false
		}
		rawEmail, hasEmail := row["emailAddress"]
		if hasEmail && json.Unmarshal(rawEmail, &email) != nil {
			return false
		}
		match := map[string]any{"account_id": id, "display_name": name}
		if lane == "before" {
			match["email_address"] = email
		} else if hasEmail {
			match["email_address"] = rawEmail
		}
		selected = append(selected, match)
	}
	want := map[string]any{"query": query, "users": selected, "count": len(selected)}
	if lane == "after" {
		page := map[string]any{"start_at": 0, "max_results": 50, "fetched": len(rows), "complete": len(rows) == 0, "search_limit": 1000}
		if len(rows) > 0 {
			page["next_start"] = len(rows)
		}
		want["skipped"] = len(rows) - len(selected)
		want["pagination"] = page
		want["truncated"] = false
	}
	encoded, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, encoded)
}

func TestParityActualBinaryUserSearch(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			for _, sample := range []struct{ name, native string }{{"active-and-private", parityUsersFixture}, {"empty", "[]"}} {
				for _, mode := range []string{"json", "compact", "human"} {
					t.Run(sample.name+"/"+mode, func(t *testing.T) {
						query := "--owned <&> café"
						var calls atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls.Add(1)
							body, err := io.ReadAll(io.LimitReader(r.Body, 1))
							values, queryErr := url.ParseQuery(r.URL.RawQuery)
							want := url.Values{"query": {query}}
							if lane.name == "after" {
								want["startAt"] = []string{"0"}
								want["maxResults"] = []string{"50"}
							}
							user, password, ok := r.BasicAuth()
							if err != nil || len(body) != 0 || queryErr != nil || !reflect.DeepEqual(values, want) || r.Method != "GET" || r.URL.Path != "/rest/api/3/user/search" || !ok || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
								t.Error("user search changed its exact authenticated GET/query/body")
								http.Error(w, "wrong synthetic request", http.StatusBadRequest)
								return
							}
							w.Header().Set("Content-Type", "application/json")
							_, _ = w.Write([]byte(sample.native))
						}))
						defer server.Close()
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, t.TempDir(), parityFixtureInput{Output: mode}, []string{"JIRA_READ_ONLY=true"}, "user", "search", "--", query)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != 1 {
							t.Fatalf("user search failed: exit %d; HTTP %d; stdout %s; stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
						}
						if mode == "human" {
							if !parityUserSearchHuman(result.Stdout, lane.name, sample.name == "empty") {
								t.Fatal("human user search lost its filtered rows or meaningful receipt")
							}
						} else {
							data := result.Stdout
							if mode == "json" {
								var err error
								data, err = json.Marshal(parityAuthoringCleanData(t, data, lane.name, "user.search"))
								if err != nil {
									t.Fatal(err)
								}
							}
							if !parityUserSearchProjection(data, []byte(sample.native), lane.name, query) {
								t.Fatalf("user discovery differs from independent native filtering/privacy/page state: %s", result.Stdout)
							}
						}
						id := "C069.search." + sample.name + "." + mode
						evidence.Checks[id] = append(evidence.Checks[id], lane.name)
					})
				}
			}
		})
	}
	saveParityLocal(t, "user-search-fixture", evidence)
}

func TestLiveParityUserSearch(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			for _, sample := range []struct{ name, query string }{{"visible-account", p.owner.account}, {"empty", p.owner.manifest.RunID + "-unmatched-account"}} {
				t.Run(sample.name, func(t *testing.T) {
					args := []string{"api", "get", "/rest/api/3/user/search", "--query=query=" + sample.query}
					if lane.Name == "after" {
						args = append(args, "--query=startAt=0", "--query=maxResults=50")
					}
					native := rawRead(t, p.owner, args...)
					var rows []map[string]json.RawMessage
					if json.Unmarshal(native.Response.JSON, &rows) != nil || rows == nil || len(rows) > 50 {
						t.Fatal("independent user fixture returned an invalid or oversized page")
					}
					if sample.name == "visible-account" {
						found := false
						for _, row := range rows {
							var id string
							var active, deleted *bool
							if raw, present := row["deleted"]; present && json.Unmarshal(raw, &deleted) != nil {
								t.Fatal("independent user fixture returned malformed deletion metadata")
							}
							if json.Unmarshal(row["accountId"], &id) == nil && id == p.owner.watcherAccountID && json.Unmarshal(row["active"], &active) == nil && active != nil && *active && (deleted == nil || !*deleted) {
								found = true
							}
						}
						if !found {
							t.Fatal("selected public account is not visible in the independent search; positive fixture is unavailable")
						}
					} else if len(rows) != 0 {
						t.Fatal("independent empty-query fixture unexpectedly returned candidates")
					}
					frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "user", "search", "--", sample.query)
					if exit != 0 || !frame.OK {
						t.Fatal("paired live user discovery failed; inspect retained receipts")
					}
					data, err := json.Marshal(parityAuthoringCleanData(t, stdout, lane.Name, "user.search"))
					if err != nil || !parityUserSearchProjection(data, native.Response.JSON, lane.Name, sample.query) {
						t.Fatal("user discovery differs from independent visible-user/privacy/page observations")
					}
					p.check(t, lane, "C069.search."+sample.name+".native")
				})
			}
		})
	}
}

func TestParityUserSearchProjectionRejectsChangedObservations(t *testing.T) {
	const old = `{"query":"independent","users":[{"account_id":"owned-visible","display_name":"Owned <&> café","email_address":""},{"account_id":"owned-hidden","display_name":"Other visible","email_address":""}],"count":2}`
	const current = `{"query":"independent","users":[{"account_id":"owned-visible","display_name":"Owned <&> café","email_address":null},{"account_id":"owned-hidden","display_name":"Other visible"}],"count":2,"skipped":3,"pagination":{"start_at":0,"max_results":50,"fetched":5,"next_start":5,"complete":false,"search_limit":1000},"truncated":false}`
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			valid := old
			if lane == "after" {
				valid = current
			}
			check := func(data string) bool {
				return parityUserSearchProjection([]byte(data), []byte(parityUsersFixture), lane, "independent")
			}
			if !check(valid) {
				t.Fatal("independent visible-user fixture refused")
			}
			type change struct{ name, original, replacement string }
			cases := []change{
				{"query", `"query":"independent"`, `"query":"changed"`},
				{"count", `"count":2`, `"count":3`},
				{"filtered-identity", `"account_id":"owned-hidden"`, `"account_id":"owned-inactive"`},
				{"duplicate-json", `"count":2`, `"count":1,"count":2`},
			}
			if lane == "after" {
				cases = append(cases,
					change{"missing-null-email", `,"email_address":null`, ``},
					change{"invented-email", `"display_name":"Other visible"`, `"display_name":"Other visible","email_address":null`},
					change{"false-completion", `"complete":false`, `"complete":true`},
					change{"wrong-offset", `"next_start":5`, `"next_start":2`},
					change{"wrong-fetched", `"fetched":5`, `"fetched":2`},
				)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if check(strings.Replace(valid, tc.original, tc.replacement, 1)) {
						t.Fatal("changed native observation earned user-search credit")
					}
				})
			}
		})
	}
}

func TestParityUserSearchHumanRejectsUnrelatedOrChangedOutput(t *testing.T) {
	const privacy = "Visibility depends on permissions; email depends on privacy settings. Empty results do not prove an account is absent.\n"
	for _, sample := range []struct {
		name, lane string
		empty      bool
		valid      string
	}{
		{"legacy-empty", "before", true, "INF ℹ️ No users matched query=\"--owned <&> café\"\n"},
		{"legacy-populated", "before", false, "INF ℹ️ Owned <&> café account_id=owned-visible\nINF ℹ️ Other visible account_id=owned-hidden\n"},
		{"current-empty", "after", true, "0 visible active matches for \"--owned <&> café\"\n" + privacy},
		{"current-populated", "after", false, "2 visible active matches for \"--owned <&> café\"\nACCOUNT ID  NAME EMAIL\nowned-visible  Owned <&> café  -\nowned-hidden  Other visible  -\n3 inactive, deleted or unknown-active rows excluded.\nNext probe: jira user search '--owned <&> café' --start 5\n" + privacy},
	} {
		t.Run(sample.name, func(t *testing.T) {
			if !parityUserSearchHuman([]byte(sample.valid), sample.lane, sample.empty) {
				t.Fatal("independent valid human receipt refused")
			}
			wrong := map[string]string{"unrelated": "Usage: jira user search QUERY\n", "extra-row": sample.valid + "unexpected-account  Somebody else\n"}
			if !sample.empty {
				wrong["wrong-name"] = strings.Replace(sample.valid, "Owned <&> café", "Wrong name", 1)
			}
			if sample.empty || sample.lane == "after" {
				wrong["wrong-query"] = strings.ReplaceAll(sample.valid, "--owned <&> café", "wrong query")
			}
			if sample.lane == "after" {
				wrong["wrong-summary"] = strings.Replace(sample.valid, "visible active matches", "unverified results", 1)
				wrong["missing-privacy"] = strings.Replace(sample.valid, privacy, "", 1)
			}
			if !sample.empty && sample.lane == "after" {
				wrong["wrong-next-probe"] = strings.Replace(sample.valid, "--start 5", "--start 2", 1)
			}
			if sample.empty && sample.lane == "before" {
				wrong["wrong-empty-summary"] = strings.Replace(sample.valid, "No users matched", "Some users matched", 1)
			}
			for name, output := range wrong {
				t.Run(name, func(t *testing.T) {
					if parityUserSearchHuman([]byte(output), sample.lane, sample.empty) {
						t.Fatal("unrelated or changed human receipt earned user-search credit")
					}
				})
			}
		})
	}
}
