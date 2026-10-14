//go:build flagmatrix

package matrix

// Phase-2 cases for the `jira issue watchers` cluster. The harness (cw*
// helpers) lives in comment_test.go.

import (
	"net/http"
	"strings"
	"testing"
)

func TestWatchersAddUserRidesBody(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "watchers", "add", "KEY-123",
		"--user", "acc-42", "--output=json")
	cwRequireOK(t, res)
	posts := stub.matching(http.MethodPost, "/issue/KEY-123/watchers")
	if len(posts) != 1 || strings.TrimSpace(string(posts[0].Body)) != `"acc-42"` {
		t.Fatalf("watcher POST body is not the bare account id string: %d posts, body %s", len(posts), postsBody(posts))
	}
	Prove(t, "jira issue watchers add", "user",
		`stub saw POST /issue/KEY-123/watchers whose whole body is the --user account id ("acc-42")`)
}

func TestWatchersUserQueryResolvesIdentityBeforeWrites(t *testing.T) {
	const query = "Ada Lovelace"
	const identity = "resolved@opaque"
	for _, verb := range []string{"add", "remove"} {
		t.Run(verb, func(t *testing.T) {
			stub := newCWStub(t, cwStubConfig{userSearch: []string{`{"accountId":"resolved@opaque","active":true,"emailAddress":null}`}})
			res := cwRun(t, stub, t.TempDir(), "issue", "watchers", verb, "KEY-123", "KEY-124",
				"--user-query", query, "--no-readback", "--output=json")
			env := cwRequireOK(t, res)
			data := cwDataMap(t, env)
			if data["account_id"] != identity || data["user_query"] != query {
				t.Fatal("watcher query receipt lost native identity or authored intent", data)
			}
			requests := stub.requests()
			if len(requests) != 4 {
				t.Fatal("query should resolve once before two writes", requests)
			}
			for i, offset := range []string{"0", "1"} {
				r := requests[i]
				if r.Method != http.MethodGet || r.Path != "/rest/api/3/user/search" || r.Query.Get("query") != query || r.Query.Get("startAt") != offset || r.Query.Get("maxResults") != "50" {
					t.Fatal("watcher query did not complete its native walk before writes", requests)
				}
			}
			seen := map[string]bool{}
			for _, r := range requests[2:] {
				if seen[r.Path] || r.Path != "/rest/api/3/issue/KEY-123/watchers" && r.Path != "/rest/api/3/issue/KEY-124/watchers" {
					t.Fatal("watcher query wrote an unexpected or repeated target", requests)
				}
				seen[r.Path] = true
				if verb == "add" {
					if r.Method != http.MethodPost || strings.TrimSpace(string(r.Body)) != `"resolved@opaque"` {
						t.Fatal("watcher query add did not send the resolved native identity", r)
					}
				} else if r.Method != http.MethodDelete || r.Query.Get("accountId") != identity {
					t.Fatal("watcher query remove did not send the resolved native identity", r)
				}
			}
			Prove(t, "jira issue watchers "+verb, "user-query",
				"one complete native user search precedes both keyed writes; the resolved opaque account ID and authored query are preserved")
		})
	}
}

func TestWatchersAddNoReadbackSkipsSecondRequest(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "watchers", "add", "KEY-123",
		"--user", "acc-42", "--no-readback", "--output=json")
	env := cwRequireOK(t, res)
	if n := len(stub.matching(http.MethodPost, "/issue/KEY-123/watchers")); n != 1 {
		t.Fatalf("watcher POSTs = %d, want 1", n)
	}
	if n := len(stub.matching(http.MethodGet, "/issue/KEY-123/watchers")); n != 0 {
		t.Fatalf("--no-readback still read the watcher set back: %d GETs", n)
	}
	if strings.Contains(string(env.Data), `"readback"`) {
		t.Fatalf("envelope still carries a readback: %s", env.Data)
	}
	Prove(t, "jira issue watchers add", "no-readback",
		"the write POST landed but the stub saw ZERO readback GETs and the entry carries no readback (the default does both)")
}

func TestWatchersAddDryRunSendsNothing(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "watchers", "add", "KEY-123",
		"--user", "acc-42", "--dry-run", "--output=json")
	env := cwRequireOK(t, res)
	if data := cwDataMap(t, env); data["dry_run"] != true {
		t.Fatalf("dry_run = %v, want true", data["dry_run"])
	}
	if reqs := stub.requests(); len(reqs) != 0 {
		t.Fatalf("dry run contacted the stub %d times: %+v", len(reqs), reqs)
	}
	Prove(t, "jira issue watchers add", "dry-run",
		"with --dry-run the stub saw ZERO requests and the envelope reports dry_run=true; a live run POSTs and reads back")
}

func TestWatchersAddValidateRemoteResolvesMe(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "watchers", "add", "KEY-123",
		"--user", "@me", "--dry-run", "--validate-remote", "--output=json")
	env := cwRequireOK(t, res)
	if n := len(stub.matching(http.MethodGet, "/rest/api/3/myself")); n != 1 {
		t.Fatalf("validate-remote preview made %d /myself reads, want 1", n)
	}
	if data := cwDataMap(t, env); data["account_id"] != cwAccountID {
		t.Fatalf("account_id = %v, want the stub's resolved %q", data["account_id"], cwAccountID)
	}
	Prove(t, "jira issue watchers add", "validate-remote",
		"a --dry-run preview with --validate-remote read /myself from the stub and published the resolved account id in place of @me (a bare preview leaves the sentinel)")
}

func TestWatchersAddParallelismRange(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "watchers", "add", "KEY-123",
		"--user", "acc-42", "--parallelism", "99", "--output=json")
	cwRequireCode(t, res, "flag_value_invalid", "parallelism")
	if n := len(stub.requests()); n != 0 {
		t.Fatalf("refused run still contacted the stub %d times", n)
	}
	Prove(t, "jira issue watchers add", "parallelism",
		"--parallelism 99 is refused with flag_value_invalid (field parallelism) before any request; the range check reads the flag's value")
}

func TestWatchersRemoveUserRidesQuery(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "watchers", "remove", "KEY-123",
		"--user", "acc-42", "--output=json")
	cwRequireOK(t, res)
	dels := stub.matching(http.MethodDelete, "/issue/KEY-123/watchers")
	if len(dels) != 1 || dels[0].Query.Get("accountId") != "acc-42" {
		t.Fatalf("watcher DELETE does not carry ?accountId=acc-42: %d deletes, query %v", len(dels), putsQuery(dels))
	}
	Prove(t, "jira issue watchers remove", "user",
		"stub saw DELETE /issue/KEY-123/watchers carry ?accountId=acc-42 from --user")
}

func TestWatchersRemoveNoReadbackSkipsSecondRequest(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "watchers", "remove", "KEY-123",
		"--user", "acc-42", "--no-readback", "--output=json")
	env := cwRequireOK(t, res)
	if n := len(stub.matching(http.MethodDelete, "/issue/KEY-123/watchers")); n != 1 {
		t.Fatalf("watcher DELETEs = %d, want 1", n)
	}
	if n := len(stub.matching(http.MethodGet, "/issue/KEY-123/watchers")); n != 0 {
		t.Fatalf("--no-readback still read the watcher set back: %d GETs", n)
	}
	if strings.Contains(string(env.Data), `"readback"`) {
		t.Fatalf("envelope still carries a readback: %s", env.Data)
	}
	Prove(t, "jira issue watchers remove", "no-readback",
		"the write DELETE landed but the stub saw ZERO readback GETs and the entry carries no readback (the default does both)")
}

func TestWatchersRemoveDryRunSendsNothing(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "watchers", "remove", "KEY-123",
		"--user", "acc-42", "--dry-run", "--output=json")
	env := cwRequireOK(t, res)
	if data := cwDataMap(t, env); data["dry_run"] != true {
		t.Fatalf("dry_run = %v, want true", data["dry_run"])
	}
	if reqs := stub.requests(); len(reqs) != 0 {
		t.Fatalf("dry run contacted the stub %d times: %+v", len(reqs), reqs)
	}
	Prove(t, "jira issue watchers remove", "dry-run",
		"with --dry-run the stub saw ZERO requests and the envelope reports dry_run=true; a live run DELETEs and reads back")
}

func TestWatchersRemoveValidateRemoteResolvesMe(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "watchers", "remove", "KEY-123",
		"--user", "@me", "--dry-run", "--validate-remote", "--output=json")
	env := cwRequireOK(t, res)
	if n := len(stub.matching(http.MethodGet, "/rest/api/3/myself")); n != 1 {
		t.Fatalf("validate-remote preview made %d /myself reads, want 1", n)
	}
	if data := cwDataMap(t, env); data["account_id"] != cwAccountID {
		t.Fatalf("account_id = %v, want the stub's resolved %q", data["account_id"], cwAccountID)
	}
	Prove(t, "jira issue watchers remove", "validate-remote",
		"a --dry-run preview with --validate-remote read /myself from the stub and published the resolved account id in place of @me")
}

func TestWatchersRemoveParallelismRange(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "watchers", "remove", "KEY-123",
		"--user", "acc-42", "--parallelism", "0", "--output=json")
	cwRequireCode(t, res, "flag_value_invalid", "parallelism")
	if n := len(stub.requests()); n != 0 {
		t.Fatalf("refused run still contacted the stub %d times", n)
	}
	Prove(t, "jira issue watchers remove", "parallelism",
		"--parallelism 0 is refused with flag_value_invalid (field parallelism) before any request")
}

func TestWatchersListParallelismRange(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "watchers", "list", "KEY-123",
		"--parallelism", "99", "--output=json")
	cwRequireCode(t, res, "flag_value_invalid", "parallelism")
	if n := len(stub.requests()); n != 0 {
		t.Fatalf("refused run still contacted the stub %d times", n)
	}
	Prove(t, "jira issue watchers list", "parallelism",
		"--parallelism 99 is refused with flag_value_invalid (field parallelism) before any request")
}
