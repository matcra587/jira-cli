//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func parityNativeSearch(t *testing.T, p *parityRun, query string, ids []int64) []paritySearchRow {
	t.Helper()
	input := filepath.Join(p.owner.dir, fmt.Sprintf("search-native-%03d.json", p.owner.sequence+1))
	body, err := json.Marshal(map[string]any{"jql": query, "fields": []string{"summary", "labels", "assignee"}, "maxResults": 100, "reconcileIssues": ids})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, body, 0o600); err != nil {
		t.Fatal(err)
	}
	response := rawRead(t, p.owner, "api", "post", "/rest/api/3/search/jql", "--input", input)
	var page map[string]json.RawMessage
	var last *bool
	var cursor string
	rows, err := paritySearchRows(response.Response.JSON, "native")
	if err != nil || json.Unmarshal(response.Response.JSON, &page) != nil || json.Unmarshal(page["isLast"], &last) != nil || last == nil || !*last {
		t.Fatal("bounded native owned search did not establish a complete page")
	}
	if value, present := page["nextPageToken"]; present && (json.Unmarshal(value, &cursor) != nil || cursor != "") {
		t.Fatal("native terminal search retained a resume cursor")
	}
	return rows
}

func TestLiveParitySearch(t *testing.T) {
	p := newParityRun(t)
	r := p.owner
	self := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var account map[string]json.RawMessage
	var accountID string
	if !jsontext.Value(self.Response.JSON).IsValid() || json.Unmarshal(self.Response.JSON, &account) != nil || json.Unmarshal(account["accountId"], &accountID) != nil || accountID == "" || accountID != r.watcherAccountID {
		t.Fatal("independent native current identity does not match both selected CLI accounts")
	}
	keys := []string{p.create(t, p.evidence.Lanes[0]), p.create(t, p.evidence.Lanes[1]), p.create(t, p.evidence.Lanes[1])}
	var expected []paritySearchRow
	var ids []int64
	for i, key := range keys {
		assignment := "--assignee=" + r.watcherAccountID
		if i == 2 {
			assignment = "--unassign"
		}
		r.mutate(t, []string{key}, "issue", "edit", key, assignment)
		native := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=summary,labels,assignee")
		rows, err := paritySearchRows(append(append([]byte(`{"issues":[`), native.Response.JSON...), ']', '}'), "native")
		if err != nil || len(rows) != 1 {
			t.Fatal("native fixture read lacks complete field and identity facts")
		}
		want := paritySearchRow{ID: p.evidence.Creations[i].Identity.ID, Key: key, Summary: p.evidence.Creations[i].Summary, Labels: []string{r.manifest.RunID}}
		if i != 2 {
			want.Assignee = r.watcherAccountID
		}
		if !reflect.DeepEqual(rows[0], want) {
			t.Fatal("native fixture differs from the independently authored assignment and ownership")
		}
		id, err := strconv.ParseInt(want.ID, 10, 64)
		if err != nil || id <= 0 {
			t.Fatal("owned native issue lacks its positive reconciliation identity")
		}
		ids = append(ids, id)
		expected = append(expected, want)
	}
	query := "key in (" + strings.Join(keys, ",") + ") ORDER BY key ASC"
	var ordered []paritySearchRow
	stable := 0
	for range 30 {
		rows := parityNativeSearch(t, p, query, ids)
		if len(rows) == len(expected) {
			for _, row := range rows {
				index := slices.IndexFunc(expected, func(want paritySearchRow) bool { return want.Key == row.Key })
				if index < 0 || !reflect.DeepEqual(row, expected[index]) {
					t.Fatal("native search changed an authored owned fixture")
				}
			}
			if reflect.DeepEqual(rows, ordered) {
				stable++
			} else {
				ordered = rows
				stable = 1
			}
			if stable == 2 {
				break
			}
		} else {
			if !paritySearchMissingOnly(rows, expected) {
				t.Fatal("native index wait observed an invalid owned search result")
			}
			stable = 0
		}
		select {
		case <-t.Context().Done():
			t.Fatal("native index wait canceled")
		case <-time.After(500 * time.Millisecond):
		}
	}
	if stable != 2 {
		t.Fatal("native search never established two identical complete owned observations")
	}
	assigned := make([]paritySearchRow, 0, 2)
	for _, row := range ordered {
		if row.Assignee == r.watcherAccountID {
			assigned = append(assigned, row)
		}
	}
	if len(assigned) != 2 {
		t.Fatal("independent assigned fixture subset is incomplete")
	}
	queries := filepath.Join(r.dir, "queries")
	if err := os.Mkdir(queries, 0o700); err != nil {
		t.Fatal(err)
	}
	queryFiles := map[string][]byte{}
	for _, tc := range []struct{ name, prefix string }{{"plain", ""}, {"yaml", "---\nname: Owned query\ndescription: Independent search\nproject: metadata-only\n---\n"}, {"toml", "+++\nname = 'Owned query'\ndescription = 'Independent search'\nproject = 'metadata-only'\n+++\n"}} {
		queryFiles[tc.name] = []byte(tc.prefix + query)
		if err := os.WriteFile(filepath.Join(queries, tc.name+".jql"), queryFiles[tc.name], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// This is an owned public legacy configuration, never the user's file.
	public, err := os.ReadFile(p.evidence.Lanes[0].Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.evidence.Lanes[0].Config, append(fmt.Appendf(nil, "queries_path=%q\n", queries), public...), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, lane := range p.evidence.Lanes {
		for _, family := range []string{"list", "mine", "inline", "saved-plain", "saved-yaml", "saved-toml", "mine-or", "list-cursor", "mine-cursor"} {
			t.Run(lane.Name+"/"+family, func(t *testing.T) {
				want, id := ordered, "I018"
				verb := "list"
				if strings.HasPrefix(family, "mine") {
					want = assigned
					id = "I019"
					verb = "mine"
				}
				args := []string{"issue", verb, "--project=" + r.manifest.Project, "--label=" + r.manifest.RunID, "--order-by=key", "--detail", "--all"}
				if lane.Name == "before" {
					args = append(args, "--desc=false")
				}
				if family == "mine-or" {
					want = []paritySearchRow{assigned[0]}
					args = []string{"issue", "mine", "--jql=key = " + assigned[0].Key + " OR key = " + keys[2] + " ORDER BY key ASC", "--detail", "--all"}
				}
				if family == "inline" || strings.HasPrefix(family, "saved-") {
					id = "C064"
					args = []string{"issue", "list", "--jql=" + query, "--detail", "--expand=", "--all"}
					if lane.Name == "before" {
						args = []string{"search", "jql", query, "--full", "--all"}
					}
					if strings.HasPrefix(family, "saved-") {
						id = "C065"
						name := strings.TrimPrefix(family, "saved-")
						args = []string{"issue", "list", "--saved=" + name, "--detail", "--expand=", "--all"}
						if lane.Name == "before" {
							args = []string{"search", "saved", name, "--full"}
						}
					}
				}
				cursorWalk := strings.HasSuffix(family, "-cursor")
				if cursorWalk {
					args = slices.DeleteFunc(args, func(arg string) bool { return arg == "--all" })
					args = append(args, "--limit=1")
				}
				cursor := ""
				seen := map[string]bool{}
				var combined []paritySearchRow
				for page := 0; page < 4; page++ {
					if strings.HasPrefix(family, "saved-") {
						paritySearchFile(t, queries, strings.TrimPrefix(family, "saved-"), queryFiles[strings.TrimPrefix(family, "saved-")])
					}
					argv := slices.Clone(args)
					if cursor != "" {
						flag := "--next-page-token="
						if lane.Name == "before" {
							flag = "--cursor="
						}
						argv = append(argv, flag+cursor)
					}
					frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true", "JIRA_QUERIES_PATH=" + queries}, argv...)
					if exit != 0 || !frame.OK {
						t.Fatal("paired owned search failed; no mutation is replayed")
					}
					data := parityOriginalFrameData(stdout)
					rows, err := paritySearchRows(data, lane.Name)
					if err != nil {
						t.Fatal(err)
					}
					expectedPage := want
					if cursorWalk {
						if page >= len(want) {
							t.Fatal("cursor walk exceeded the independently known set")
						}
						expectedPage = want[page : page+1]
					}
					if !reflect.DeepEqual(rows, expectedPage) {
						t.Fatal("paired search differs from independently observed native order or fields")
					}
					combined = append(combined, rows...)
					last := !cursorWalk || page == len(want)-1
					var response struct {
						Meta struct {
							Pagination struct {
								Cursor string `json:"nextCursor"`
							} `json:"pagination"`
						}
						Data struct {
							Pagination struct {
								Cursor string `json:"next_page_token"`
							} `json:"pagination"`
						}
					}
					if json.Unmarshal(stdout, &response) != nil {
						t.Fatal("invalid retained search response")
					}
					next := response.Data.Pagination.Cursor
					if lane.Name == "before" {
						next = response.Meta.Pagination.Cursor
					}
					pageSize := 50
					if cursorWalk {
						pageSize = 1
					}
					drained := !cursorWalk && (lane.Name != "before" || !strings.HasPrefix(family, "saved-"))
					if !paritySearchPage(stdout, lane.Name, "json", len(rows), pageSize, last, next, drained) || (!last && (next == cursor || seen[next])) {
						t.Fatal("paired cursor receipt lost completion or progress")
					}
					if strings.HasPrefix(family, "saved-") {
						name := strings.TrimPrefix(family, "saved-")
						display, description, project := name, "", ""
						if name != "plain" {
							display, description, project = "Owned query", "Independent search", "metadata-only"
						}
						if !paritySearchSavedMetadata(data, lane.Name, name, display, query, description, project) {
							t.Fatal("saved search lost literal query metadata or changed its scope")
						}
					} else {
						metadataQuery := "project = " + r.manifest.Project
						if verb == "mine" {
							metadataQuery += " AND assignee = currentUser()"
						}
						metadataQuery += " AND labels = " + r.manifest.RunID + " ORDER BY key ASC"
						if family == "inline" {
							metadataQuery = query
						}
						if family == "mine-or" {
							metadataQuery = "assignee = currentUser() AND (key = " + assigned[0].Key + " OR key = " + keys[2] + ") ORDER BY key ASC"
						}
						if !paritySearchMetadata(data, lane.Name, family, metadataQuery) {
							t.Fatal("search metadata lost its declared query or scope")
						}
					}
					if last {
						break
					}
					seen[next] = true
					cursor = next
				}
				if !reflect.DeepEqual(combined, want) {
					t.Fatal("cursor walk did not return the complete independent owned set")
				}
				p.check(t, lane, id+".search."+family)
			})
			if t.Failed() {
				return
			}
		}
	}
	// Queries are read operations: native observations must retain every fixture.
	if !reflect.DeepEqual(parityNativeSearch(t, p, query, ids), ordered) {
		t.Fatal("search cohort changed native owned issue fields or order")
	}
	for name, body := range queryFiles {
		paritySearchFile(t, queries, name, body)
	}
}

func paritySearchFile(t *testing.T, directory, name string, expected []byte) {
	t.Helper()
	path := filepath.Join(directory, name+".jql")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("owned saved query is no longer a regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, expected) {
		t.Fatal("saved query changed its exact authored bytes")
	}
}
