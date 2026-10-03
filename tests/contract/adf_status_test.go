package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestADFStatusColorBody(t *testing.T) {
	t.Parallel()
	colors := []struct {
		name    string
		color   string
		invalid bool
	}{
		{name: "named", color: "neutral"},
		{name: "hex", color: "#a1B2c3"},
		{name: "shorthand", color: "#abc", invalid: true},
	}
	for _, color := range colors {
		for _, mode := range []string{"json", "compact"} {
			for _, preview := range []bool{false, true} {
				name := fmt.Sprintf("%s-%s-preview-%t", color.name, mode, preview)
				t.Run(name, func(t *testing.T) {
					document := []byte(fmt.Sprintf(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":"READY<&>`+"\u2028"+`end","color":%q}}]}]}`, color.color))
					path := filepath.Join(t.TempDir(), "comment.json")
					if err := os.WriteFile(path, document, 0o600); err != nil {
						t.Fatal(err)
					}
					var requests atomic.Int32
					bodies := make(chan []byte, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/issue/KEY-123/comment" {
							http.Error(w, "unexpected request", http.StatusNotFound)
							return
						}
						raw, err := io.ReadAll(r.Body)
						if err != nil {
							t.Errorf("read request: %v", err)
							http.Error(w, "unreadable request", http.StatusBadRequest)
							return
						}
						var request struct {
							Body json.RawMessage `json:"body"`
						}
						if err := json.Unmarshal(raw, &request); err != nil {
							t.Errorf("decode request: %v", err)
							http.Error(w, "invalid request", http.StatusBadRequest)
							return
						}
						select {
						case bodies <- request.Body:
						default:
							t.Error("comment mutation was replayed")
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusCreated)
						_, _ = fmt.Fprintf(w, `{"id":"10001","body":%s}`, request.Body)
					}))
					t.Cleanup(server.Close)
					env := []string{
						"JIRA_BASE_URL=" + server.URL,
						"JIRA_EMAIL=person@example.com",
						"JIRA_API_TOKEN=example-fixture-token",
						"JIRA_KEYRING=false",
						"NO_PROXY=127.0.0.1,localhost",
					}
					args := []string{"issue", "comment", "add", "KEY-123", "--json-input", path, "--output=" + mode, "--no-input"}
					if preview {
						args = append(args, "--dry-run")
					}
					stdout, _, exit := runBinaryEnv(t, env, args...)
					if color.invalid {
						var f frame
						if err := json.Unmarshal([]byte(stdout), &f); err != nil {
							t.Fatal(err)
						}
						if exit != 3 || f.OK || len(f.Errors) != 1 || requests.Load() != 0 {
							t.Fatalf("invalid status body: exit %d, ok %t, errors %d, requests %d", exit, f.OK, len(f.Errors), requests.Load())
						}
						var failure struct {
							Code  string `json:"code"`
							Field string `json:"field"`
						}
						if err := json.Unmarshal(f.Errors[0], &failure); err != nil {
							t.Fatal(err)
						}
						if failure.Code != "flag_value_invalid" || failure.Field != "json-input" {
							t.Fatalf("invalid status body taxonomy: %+v", failure)
						}
						return
					}
					if exit != 0 {
						t.Fatalf("valid status body failed with exit %d", exit)
					}
					data := []byte(stdout)
					if mode == "json" {
						var f frame
						if err := json.Unmarshal(data, &f); err != nil || !f.OK {
							t.Fatalf("valid status body frame: %v, ok %t", err, f.OK)
						}
						data = f.Data
					}
					var output envelope.IssueCommentAddOutput
					if err := json.Unmarshal(data, &output); err != nil {
						t.Fatal(err)
					}
					if output.DryRun != preview || output.BodyRoute != "adf" || !bytes.Equal(output.Body, document) {
						t.Fatal("native status body changed or lost its preview/route metadata")
					}
					if preview {
						if requests.Load() != 0 {
							t.Fatal("local preview contacted Jira")
						}
						return
					}
					if requests.Load() != 1 {
						t.Fatalf("comment requests %d, want exactly one", requests.Load())
					}
					if sent := <-bodies; !bytes.Equal(sent, document) {
						t.Fatal("native status body bytes changed on the wire")
					}
					if len(output.Results) != 1 || !output.Results[0].OK || output.Results[0].Data == nil || output.Results[0].Data.Comment == nil {
						t.Fatal("comment result lacks its created object")
					}
					comment := output.Results[0].Data.Comment
					if comment.ID != "10001" || !bytes.Equal(comment.Body, document) {
						t.Fatal("returned comment lost its identity or status body bytes")
					}
				})
			}
		}
	}
}
