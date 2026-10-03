//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestIssueDetailAndExpansionFlagEffects(t *testing.T) {
	for _, verb := range []string{"view", "list", "mine"} {
		flags := []string{"expand"}
		if verb != "view" {
			flags = append(flags, "detail")
		}
		for _, flag := range flags {
			t.Run(verb+"/"+flag, func(t *testing.T) {
				rec := &mx3Recorder{}
				url := mx3Stub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					req := rec.record(r)
					w.Header().Set("Content-Type", "application/json")
					if verb == "view" {
						if req.Method != http.MethodGet || req.Path != "/rest/api/3/issue/KEY-1" || r.URL.Query().Get("expand") != "changelog" {
							t.Error("view expansion did not reach the native read")
						}
						_, _ = fmt.Fprint(w, `{"key":"KEY-1","fields":{"summary":"One"},"changelog":{"histories":[]}}`)
						return
					}
					var body struct {
						Expand string   `json:"expand"`
						Fields []string `json:"fields"`
					}
					if req.Method != http.MethodPost || req.Path != mx3SearchPath || json.Unmarshal(req.Body, &body) != nil {
						t.Error("expanded list did not use enhanced search")
					}
					want := "changelog"
					if flag == "detail" {
						want = "renderedFields,names,schema,transitions,operations,changelog"
						if len(body.Fields) != 1 || body.Fields[0] != "*all" {
							t.Error("detail did not replace the default field projection")
						}
					}
					if body.Expand != want {
						t.Errorf("native expansion=%q, want %q", body.Expand, want)
					}
					_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"KEY-1","fields":{"summary":"One"},"changelog":{"histories":[]}}]}`)
				}))
				args := []string{"issue", verb, "--output=json"}
				if verb == "view" {
					args = append(args, "KEY-1", "--comments=0")
				} else {
					args = append(args, "--project=ABC")
				}
				if flag == "detail" {
					args = append(args, "--detail")
				} else {
					args = append(args, "--expand=changelog")
				}
				res := mx3Run(t, t.TempDir(), url, args...)
				if res.Exit != 0 || len(rec.all()) != 1 {
					t.Fatalf("expanded read failed or performed extra requests: exit=%d stdout=%s", res.Exit, res.Stdout)
				}
				Prove(t, "jira issue "+verb, flag, "the real binary sent the exact native expansion and, for detail, replaced its field projection with *all")
			})
		}
	}
}
