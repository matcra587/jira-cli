//go:build live

package live

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/output"
)

func TestLiveIssueDetails(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "native issue details")
	r.mutate(t, []string{key}, "issue", "edit", key, "--assignee=@me")
	self := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var account struct {
		AccountID string `json:"accountId"`
	}
	if json.Unmarshal(self.Response.JSON, &account) != nil || account.AccountID == "" {
		t.Fatal("native detail search requires an independently read account")
	}
	waitAssignedSearch(t, r, account.AccountID, []string{key}, "mine", []string{"--key=" + key})
	for _, tc := range []struct {
		name, fields, expand string
		args                 []string
	}{
		{"full", "*all", "renderedFields,names,schema,transitions,operations,editmeta", []string{"issue", "view", key}},
		{"narrow", "summary", "renderedFields,names,schema", []string{"issue", "view", key, "--fields=summary"}},
	} {
		native := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields="+tc.fields, "--query=expand="+tc.expand)
		var observed map[string]json.RawMessage
		if json.Unmarshal(native.Response.JSON, &observed) != nil || len(observed["names"]) == 0 || len(observed["schema"]) == 0 || len(observed["renderedFields"]) == 0 {
			t.Fatal("independent native read did not establish expanded issue metadata")
		}
		got := r.success(t, tc.args...)
		if got.Data.Issue.Key != key || !nativeMetadataMatches(got.Data.Issue.Details, native.Response.JSON, []string{"id", "key", "self", "fields"}) {
			t.Fatalf("%s view dropped or changed independently observed native metadata", tc.name)
		}
	}
	id, err := strconv.ParseInt(r.manifest.Issues[0].ID, 10, 64)
	if err != nil || id <= 0 {
		t.Fatal("owned detail fixture lacks a native numeric identity")
	}
	const expand = "renderedFields,names,schema,transitions,operations,changelog"
	body, err := json.Marshal(struct {
		JQL             string   `json:"jql"`
		Fields          []string `json:"fields"`
		Expand          string   `json:"expand"`
		MaxResults      int      `json:"maxResults"`
		ReconcileIssues []int64  `json:"reconcileIssues"`
	}{"key = " + key, []string{"*all"}, expand, 50, []int64{id}})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(r.dir, "native-detail-search.json")
	if err := os.WriteFile(input, body, 0o600); err != nil {
		t.Fatal(err)
	}
	native := rawRead(t, r, "api", "post", "/rest/api/3/search/jql", "--input", input)
	var page struct {
		Issues []json.RawMessage `json:"issues"`
		IsLast *bool             `json:"isLast"`
	}
	if json.Unmarshal(native.Response.JSON, &page) != nil || page.IsLast == nil || !*page.IsLast || len(page.Issues) != 1 {
		t.Fatal("independent reconciled native detail search is incomplete")
	}
	for _, verb := range []string{"list", "mine"} {
		got := r.success(t, "issue", verb, "--key="+key, "--detail")
		if len(got.Data.Issues) != 1 || got.Data.Issues[0].Key != key || !got.Data.Pagination.IsLast || len(got.Data.SearchPages) != 1 ||
			!nativeMetadataMatches(got.Data.Issues[0].Details, page.Issues[0], []string{"id", "key", "self", "fields"}) ||
			!nativeMetadataMatches(got.Data.SearchPages[0], native.Response.JSON, []string{"issues"}) {
			t.Fatal("domain detail search differs from independently observed issue/page metadata")
		}
	}
}

// Compare decoded native values with exact numbers, retaining null/absent and
// every unknown member. Only explicitly projected identity/field slots and
// Jira's per-request action tokens in operations links differ.
func nativeMetadataMatches(receipt, native json.RawMessage, omitted []string) bool {
	if !json.Valid(receipt) || !json.Valid(native) {
		return false
	}
	got, err := output.DecodeJSONNumbers(receipt)
	if err != nil {
		return false
	}
	want, err := output.DecodeJSONNumbers(native)
	if err != nil {
		return false
	}
	actual, ok := got.(map[string]any)
	if !ok {
		return false
	}
	expected, ok := want.(map[string]any)
	if !ok {
		return false
	}
	for _, name := range omitted {
		delete(expected, name)
	}
	if !normalizeOperationTokens(actual["operations"]) || !normalizeOperationTokens(expected["operations"]) {
		return false
	}
	return len(expected) > 0 && reflect.DeepEqual(actual, expected)
}

// Native operations links contain independently generated atl_token values.
// Normalize only that query value, preserving its presence and every other
// byte in the URL. Other metadata and query parameters remain exact.
func normalizeOperationTokens(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		for name, child := range value {
			if href, ok := child.(string); name == "href" && ok {
				base, fragment, hasFragment := strings.Cut(href, "#")
				path, query, hasQuery := strings.Cut(base, "?")
				if !hasQuery {
					continue
				}
				parts := strings.Split(query, "&")
				seen := false
				for i, part := range parts {
					key, token, hasValue := strings.Cut(part, "=")
					decodedKey, err := url.QueryUnescape(key)
					if err != nil {
						return false
					}
					if decodedKey != "atl_token" {
						continue
					}
					decodedToken, err := url.QueryUnescape(token)
					if seen || !hasValue || err != nil || decodedToken == "" {
						return false
					}
					seen = true
					parts[i] = key + "=<per-request-token>"
				}
				stable := path + "?" + strings.Join(parts, "&")
				if hasFragment {
					stable += "#" + fragment
				}
				value[name] = stable
			} else if !normalizeOperationTokens(child) {
				return false
			}
		}
	case []any:
		for _, child := range value {
			if !normalizeOperationTokens(child) {
				return false
			}
		}
	}
	return true
}
