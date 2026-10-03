//go:build flagmatrix

package matrix

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRawAPIFlagEffects(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"get", "head", "options", "post", "put", "patch", "delete"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			mutation := method == "post" || method == "put" || method == "patch" || method == "delete"
			const body = "native\x00body"
			const match = `"tag,with,commas", W/"weak"`
			const date = "Thu, 01 Jan 1970 00:00:00 GMT"
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				got, err := io.ReadAll(req.Body)
				if err != nil || string(got) != body || req.Method != strings.ToUpper(method) || req.Header.Get("Content-Type") != "application/octet-stream" || !slices.Equal(req.URL.Query()["field"], []string{"a,b", "c=d"}) {
					t.Error("raw flag did not affect its actual HTTP request")
				}
				if req.Header.Get("If-Match") != match || req.Header.Get("If-Unmodified-Since") != date {
					t.Error("raw precondition flags did not affect their HTTP headers")
				}
				_, _ = io.WriteString(w, "abcdef")
			}))
			t.Cleanup(server.Close)
			args := []string{"api", method, "/rest/api/3/fixture", "--input", "input", "--content-type=application/octet-stream", "--query=field=a,b", "--query=field=c=d", "--max-response-bytes=3", "--if-match=" + match, "--if-unmodified-since=" + date}
			if mutation {
				args = append(args, "--force")
			}
			r := fwExec(t, server.URL, map[string]string{"input": body}, "", args...)
			out := fwParse(t, r)
			wantExit := 8
			if method == "head" {
				wantExit = 0
			}
			if r.Exit != wantExit || calls.Load() != 1 || fwAt(t, out.Data, "request", "body_base64") != base64.StdEncoding.EncodeToString([]byte(body)) || fwAt(t, out.Data, "request", "max_response_bytes") != float64(3) {
				t.Fatalf("raw flags lost request facts: exit=%d stderr=%q", r.Exit, r.Stderr)
			}
			if method != "head" && (fwAt(t, out.Data, "response", "body_base64") != "YWJj" || fwAt(t, out.Data, "response", "truncated") != true) {
				t.Fatal("response byte cap did not bound its actual capture")
			}
			if fwAt(t, out.Data, "request", "if_match") != match || fwAt(t, out.Data, "request", "if_unmodified_since") != date {
				t.Fatal("request receipt dropped exact HTTP preconditions")
			}
			path := "jira api " + method
			for _, flag := range []string{"input", "content-type", "query", "max-response-bytes", "if-match", "if-unmodified-since"} {
				Prove(t, path, flag, "exact native bytes, content type, repeated query values, precondition headers and bounded response verified against recording server")
			}
			if !mutation {
				return
			}
			for _, flag := range []string{"force", "yes"} {
				initialCalls := calls.Load()
				missing := fwExec(t, server.URL, nil, "", "api", method, "/rest/api/3/fixture")
				fwRefusal(t, missing, "flag_required", "force")
				if calls.Load() != initialCalls {
					t.Fatal("unacknowledged mutation reached the server")
				}
				preview := fwExec(t, server.URL, map[string]string{"input": body}, "", "api", method, "/rest/api/3/fixture", "--input=input", "--dry-run")
				p := fwParse(t, preview)
				if preview.Exit != 0 || fwAt(t, p.Data, "dry_run") != true || fwAt(t, p.Data, "request", "body_base64") != base64.StdEncoding.EncodeToString([]byte(body)) || calls.Load() != initialCalls {
					t.Fatal("preview contacted Jira or lost request bytes")
				}
				Prove(t, path, "dry-run", "prepared exact native bytes with no HTTP request and dry_run=true")
				aliasArgs := slices.Clone(args[:len(args)-1])
				aliasArgs = append(aliasArgs, "--"+flag)
				before := calls.Load()
				allowed := fwExec(t, server.URL, map[string]string{"input": body}, "", aliasArgs...)
				if allowed.Exit != 8 || calls.Load() != before+1 {
					t.Fatal("explicit raw acknowledgement did not allow exactly one request")
				}
				Prove(t, path, flag, "refusal without acknowledgement and exactly one HTTP write when supplied")
			}
		})
	}
}
