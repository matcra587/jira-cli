package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"

	usercmd "github.com/matcra587/jira-cli/internal/cli/user"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestUserSearchBinaryPagingPrivacyAndModes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var offsets []int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				start, err := strconv.Atoi(r.URL.Query().Get("startAt"))
				if err != nil || r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/user/search" || r.URL.Query().Get("query") != "sam &?+" || r.URL.Query().Get("maxResults") != "50" {
					t.Errorf("search query/route drift: %s", r.URL)
				}
				offsets = append(offsets, start)
				switch start {
				case 0:
					_, _ = fmt.Fprint(w, `[{"accountId":"visible","displayName":"Sam","active":true},{"accountId":"inactive","active":false},{"accountId":"deleted","active":true,"deleted":true}]`)
				case 3:
					_, _ = fmt.Fprint(w, `[{"accountId":"private","displayName":"Sam Private","active":true,"emailAddress":null},{"accountId":"empty-email","active":true,"emailAddress":""}]`)
				case 5:
					_, _ = fmt.Fprint(w, `[]`)
				default:
					t.Errorf("bad continuation %d", start)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), "user", "search", "sam &?+", "--all", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("search failed: %d %q %s", exit, stderr, stdout)
			}
			var out envelope.UserSearchOutput
			data := adfData(t, stdout, mode, "user.search", true)
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(offsets, []int{0, 3, 5}) || out.Count != 3 || out.Skipped != 2 || out.Pagination.Fetched != 5 || !out.Pagination.Complete || out.Pagination.NextStart != nil || out.Truncated || out.Query != "sam &?+" {
				t.Fatalf("wrong search contract: %+v offsets=%v", out, offsets)
			}
			if out.Users[0].EmailAddress.Set || !out.Users[1].EmailAddress.Null || !out.Users[2].EmailAddress.Set || out.Users[2].EmailAddress.Null {
				t.Fatal("email privacy states collapsed")
			}
		})
	}
}

func TestUserSearchDiscoveryAndPublishedExamples(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinary(t, "agent", "schema", "--path=user search", "--output=json")
	if exit != 0 || stderr != "" {
		t.Fatalf("user discovery failed: %d %q", exit, stderr)
	}
	tree, _ := readSchema(t, stdout, "json")
	if tree.Path != "jira user search" || !tree.ReadOnly || tree.OutputSchema == nil || !strings.Contains(tree.Extensions["long"].(string), "1,000") {
		t.Fatal("user search missing from runtime discovery or endpoint bound omitted")
	}
	props := tree.OutputSchema["properties"].(map[string]any)
	users := props["users"].(map[string]any)
	if !reflect.DeepEqual(users["type"], []any{"array", "null"}) || props["pagination"] == nil {
		t.Fatal("runtime search shape lost page/identity array")
	}
	item := users["items"].(map[string]any)["properties"].(map[string]any)
	if item["account_id"] == nil || item["email_address"] == nil {
		t.Fatal("runtime identity shape lost account/email state")
	}
	for _, required := range users["items"].(map[string]any)["required"].([]any) {
		if required == "email_address" {
			t.Fatal("runtime schema requires privacy-hidden email")
		}
	}
	flags := make(map[string]bool)
	for _, flag := range tree.Flags {
		flags[flag.Name] = true
	}
	for _, name := range []string{"start", "limit", "all"} {
		if !flags[name] {
			t.Fatalf("runtime omitted --%s", name)
		}
	}
	cmd, _, err := usercmd.New(usercmd.Deps{}).Find([]string{"search"})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/rest/api/3/user/search" || r.URL.Query().Get("query") == "" {
			t.Error("help recipe did not use account discovery")
		}
		if r.URL.Query().Get("startAt") != "0" {
			_, _ = fmt.Fprint(w, `[]`)
			return
		}
		_, _ = fmt.Fprint(w, `[{"accountId":"fixture-id","displayName":"Sam","active":true}]`)
	}))
	defer server.Close()
	count := 0
	for line := range strings.SplitSeq(cmd.Example, "\n") {
		if !strings.HasPrefix(line, "$ jira ") {
			continue
		}
		args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
		if exit != 0 || stderr != "" || stdout == "" {
			t.Fatalf("published search example failed: %q exit=%d stderr=%q", line, exit, stderr)
		}
		count++
	}
	if count != 4 || calls.Load() != 5 {
		t.Fatalf("user help recipes unproved: recipes=%d calls=%d", count, calls.Load())
	}
	stdout, stderr, exit = runBinaryEnv(t, append(editReceiptEnv(server.URL), "AGENT=1"), "user", "search", "sam")
	if exit != 0 || stderr != "" {
		t.Fatal("agent default search failed")
	}
	var compact envelope.UserSearchOutput
	if json.Unmarshal([]byte(stdout), &compact) != nil || compact.Count != 1 {
		t.Fatal("agent default did not return compact search data")
	}
	stdout, stderr, exit = runBinaryEnv(t, editReceiptEnv(server.URL), "user", "search", "sam", "--jq=.data.users[].account_id")
	if exit != 0 || stderr != "" || strings.TrimSpace(stdout) != "fixture-id" {
		t.Fatalf("user jq identity extraction failed: %d %q", exit, stdout)
	}
}

func TestUserSearchBinaryRetainsFailureReceipt(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		for _, tc := range []struct {
			name, body, code string
			status, exit     int
		}{
			{name: "repeat", body: `[{"accountId":"first","active":true}]`, status: 200, exit: 5, code: "user_search_response_invalid"},
			{name: "protocol", body: `null`, status: 200, exit: 5, code: "user_search_response_invalid"},
			{name: "permission", body: `{"errorMessages":["denied"]}`, status: 403, exit: 1, code: "jira_forbidden"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("startAt") == "0" {
						_, _ = fmt.Fprint(w, `[{"accountId":"first","active":true}]`)
						return
					}
					if r.URL.Query().Get("startAt") != "1" {
						t.Error("failure probe skipped raw offset")
					}
					w.WriteHeader(tc.status)
					_, _ = fmt.Fprint(w, tc.body)
				}))
				defer server.Close()
				stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "user", "search", "sam", "--all", "--output="+mode)
				var failed struct {
					OK   bool `json:"ok"`
					Meta struct {
						Command string `json:"command"`
					} `json:"meta"`
					Data   envelope.UserSearchOutput `json:"data"`
					Errors []struct {
						Code string `json:"code"`
					} `json:"errors"`
				}
				if exit != tc.exit || json.Unmarshal([]byte(stdout), &failed) != nil || failed.OK || failed.Meta.Command != "user.search" || len(failed.Errors) != 1 || failed.Errors[0].Code != tc.code || failed.Data.Count != 1 || failed.Data.Users[0].AccountID != "first" || failed.Data.Pagination.NextStart == nil || *failed.Data.Pagination.NextStart != 1 || failed.Data.Pagination.Complete {
					t.Fatalf("partial failure receipt lost: exit=%d %s", exit, stdout)
				}
			})
		}
	}
}

func TestUserSearchBinaryValidationBeforeHTTP(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	for _, tc := range []struct {
		name string
		args []string
		code string
	}{
		{name: "blank", args: []string{" \t"}, code: "arg_value_invalid"},
		{name: "missing", code: "arg_count_invalid"},
		{name: "excess", args: []string{"sam", "other"}, code: "arg_count_invalid"},
		{name: "negative start", args: []string{"sam", "--start=-1"}, code: "flag_value_invalid"},
		{name: "ceiling start", args: []string{"sam", "--start=1000"}, code: "flag_value_invalid"},
		{name: "zero limit", args: []string{"sam", "--limit=0"}, code: "flag_value_invalid"},
		{name: "excess limit", args: []string{"sam", "--limit=1001"}, code: "flag_value_invalid"},
		{name: "all start", args: []string{"sam", "--all", "--start=0"}, code: "flag_conflict"},
		{name: "all limit", args: []string{"sam", "--all", "--limit=50"}, code: "flag_conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"user", "search"}, tc.args...)
			args = append(args, "--output=json")
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			var failed struct {
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			if exit != 3 || json.Unmarshal([]byte(stdout), &failed) != nil || len(failed.Errors) != 1 || failed.Errors[0].Code != tc.code {
				t.Fatalf("validation misclassified: %d %s", exit, stdout)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("invalid search contacted HTTP")
	}
}

func TestUserSearchHumanSanitizationAndNoMatches(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "empty" {
			_, _ = fmt.Fprint(w, `[]`)
			return
		}
		_, _ = fmt.Fprint(w, `[{"accountId":"fixture-id\u001b[31m","displayName":"Sam\u001b]52;c;stolen\u0007","active":true,"emailAddress":"sam@example.com\u001b[2J"}]`)
	}))
	defer server.Close()
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "user", "search", "sam", "--output=human", "--color=never")
	if exit != 0 || stderr != "" || strings.ContainsAny(stdout, "\x1b\x07") || !strings.Contains(stdout, "ACCOUNT ID") || !strings.Contains(stdout, "Sam") || !strings.Contains(stdout, "Next probe:") || !strings.Contains(stdout, "permissions") {
		t.Fatalf("unsafe/incomplete human result: %d %q %q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runBinaryEnv(t, editReceiptEnv(server.URL), "user", "search", "empty", "--output=human")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "0 visible active matches") || !strings.Contains(stdout, "do not prove") || strings.Contains(stdout, "Next probe:") {
		t.Fatalf("empty visibility claim changed: %d %q", exit, stdout)
	}
}
