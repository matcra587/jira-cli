//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEpicReadFlagEffects(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "board"} {
		for _, tc := range []struct {
			flag string
			args []string
		}{
			{"limit", []string{"--limit=1"}},
			{"all", []string{"--all"}},
			{"unbounded", []string{"--all", "--unbounded"}},
			{"next-page-token", []string{"--next-page-token=anchor", "--limit=1"}},
			{"project", []string{"--project=KEY"}},
		} {
			t.Run(verb+" "+tc.flag, func(t *testing.T) {
				t.Parallel()
				epics := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request struct {
						JQL    string `json:"jql"`
						Cursor string `json:"nextPageToken"`
						Size   int    `json:"maxResults"`
					}
					if json.NewDecoder(r.Body).Decode(&request) != nil {
						t.Error("invalid search body")
						w.WriteHeader(400)
						return
					}
					if strings.HasPrefix(request.JQL, "parent = ") {
						_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[]}`)
						return
					}
					epics++
					if tc.flag == "project" && request.JQL != `issuetype = Epic AND project in ("KEY")` {
						t.Errorf("project ignored: %q", request.JQL)
					}
					if tc.flag == "next-page-token" && request.Cursor != "anchor" {
						t.Error("resume ignored")
					}
					if (tc.flag == "limit" || tc.flag == "next-page-token") && request.Size != 1 {
						t.Error("total limit ignored")
					}
					last := true
					if tc.flag == "all" {
						last = epics == 2
					}
					if tc.flag == "unbounded" {
						last = epics == 51
					}
					_, _ = fmt.Fprintf(w, `{"isLast":%t,"nextPageToken":"page-%d","issues":[{"key":"KEY-%d"}]}`, last, epics, epics)
				}))
				defer server.Close()
				r := fwExec(t, server.URL, nil, "", append([]string{"epic", verb}, tc.args...)...)
				out := fwParse(t, r)
				want := 1
				if tc.flag == "all" {
					want = 2
				}
				if tc.flag == "unbounded" {
					want = 51
				}
				if r.Exit != 0 || epics != want || fwAt(t, out.Data, "pagination", "returned") != float64(want) {
					t.Fatalf("flag ignored: %s exit=%d requests=%d", tc.flag, r.Exit, epics)
				}
				Prove(t, "jira epic "+verb, tc.flag, "search request scope, size, cursor and completed page count verified through the binary")
			})
		}
	}
}
