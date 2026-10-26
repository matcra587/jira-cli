package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const (
	literalMarkdown = `\*literal\* \[label\] &amp; &amp;lt; &#00065; 雪`
	literalDocument = `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"*literal* [label] & &lt; A 雪"}]}]}`
)

func TestADFTextBinaryConversionAndProjection(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_READ_ONLY=true", "AGENT=1"}, literalMarkdown, nil, "adf", "convert", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("literal conversion failed: exit %d, stderr %q", exit, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, `*literal* [label] & &lt; A 雪`) {
					t.Fatal("human candidate lost decoded literal text")
				}
				return
			}
			var out envelope.ADFConvertOutput
			if err := json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &out); err != nil {
				t.Fatal(err)
			}
			equal, err := adf.StructuralEqual([]byte(literalDocument), out.Document)
			if err != nil || !equal || out.Lossy || len(out.Diagnostics) != 0 {
				t.Fatal("binary conversion did not preserve independent literal expectation")
			}
			input := filepath.Join(t.TempDir(), "native.json")
			if err := os.WriteFile(input, []byte(literalDocument), 0o600); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, exit = runBinary(t, "adf", "render", "--input", input, "--output="+mode)
			var projection envelope.ADFRenderOutput
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.render", true), &projection) != nil || projection.Lossy {
				t.Fatal("native literal projection failed")
			}
			if !strings.Contains(projection.Markdown, "&amp;lt;") {
				t.Fatal("projection exposes native entity spelling to another decode")
			}
			stdout, stderr, exit = runBinaryEnvInputStarted(t, nil, projection.Markdown, nil, "adf", "convert", "--output="+mode)
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &out) != nil {
				t.Fatal("projected literals did not convert")
			}
			equal, err = adf.StructuralEqual([]byte(literalDocument), out.Document)
			if err != nil || !equal || out.Lossy {
				t.Fatal("binary round trip changed native literal content")
			}
		})
	}
}

func TestADFTextBinaryBodyRoutesPreserveLiteral(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		for _, route := range []string{"comment", "description"} {
			for _, action := range []string{"write", "preview", "read-only", "failure"} {
				t.Run(mode+"/"+route+"/"+action, func(t *testing.T) {
					var writes atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						writes.Add(1)
						raw, err := io.ReadAll(req.Body)
						if err != nil {
							t.Error(err)
							w.WriteHeader(400)
							return
						}
						var request struct {
							Body   json.RawMessage `json:"body"`
							Fields struct {
								Description json.RawMessage `json:"description"`
							} `json:"fields"`
						}
						if err := json.Unmarshal(raw, &request); err != nil {
							t.Error(err)
							w.WriteHeader(400)
							return
						}
						document := request.Fields.Description
						if route == "comment" {
							document = request.Body
							if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue/KEY-123/comment" {
								t.Error("unexpected comment request")
							}
						} else if req.Method != http.MethodPut || req.URL.Path != "/rest/api/3/issue/KEY-123" {
							t.Error("unexpected description request")
						}
						equal, err := adf.StructuralEqual([]byte(literalDocument), document)
						if err != nil || !equal {
							t.Error("request changed authored literal text")
						}
						if action == "failure" {
							w.WriteHeader(400)
							_, _ = io.WriteString(w, `{"errorMessages":["fixture write refused"]}`)
							return
						}
						if route == "comment" {
							w.WriteHeader(http.StatusCreated)
							_, _ = io.WriteString(w, `{"id":"10042","body":`+literalDocument+`}`)
							return
						}
						w.WriteHeader(http.StatusNoContent)
					}))
					t.Cleanup(server.Close)
					args := []string{"issue", "edit", "KEY-123", "--markdown", literalMarkdown, "--output=" + mode}
					op := "issue.edit"
					if route == "comment" {
						args = []string{"issue", "comment", "add", "KEY-123", "--markdown", literalMarkdown, "--output=" + mode}
						op = "issue.comment.add"
					}
					env := editReceiptEnv(server.URL)
					if action == "preview" {
						args = append(args, "--dry-run")
					}
					if action == "read-only" {
						env = append(env, "JIRA_READ_ONLY=true")
					}
					stdout, stderr, exit := runBinaryEnv(t, env, args...)
					if action == "read-only" {
						var f frame
						if exit != 3 || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || len(f.Errors) != 1 || writes.Load() != 0 {
							t.Fatal("read-only text write was not refused before requests")
						}
						return
					}
					if stderr != "" || action == "failure" && exit != 3 || action != "failure" && exit != 0 {
						t.Fatalf("body route status: exit %d, stderr %q", exit, stderr)
					}
					if action == "failure" {
						var failure apiFrame
						if json.Unmarshal([]byte(stdout), &failure) != nil || len(failure.Errors) != 1 || !strings.Contains(failure.Errors[0].Message, "fixture write refused") {
							t.Fatal("write failure lost structured diagnostic", stdout)
						}
					}
					data := adfData(t, stdout, mode, op, action != "failure")
					var document json.RawMessage
					var dryRun bool
					if route == "comment" {
						var out envelope.IssueCommentAddOutput
						if err := json.Unmarshal(data, &out); err != nil {
							t.Fatal(err)
						}
						document, dryRun = out.Body, out.DryRun
						if out.BodyRoute != "markdown" {
							t.Fatal("comment lost Markdown route provenance")
						}
					} else {
						var out envelope.IssueEditOutput
						if err := json.Unmarshal(data, &out); err != nil {
							t.Fatal(err)
						}
						var payload struct {
							Fields struct {
								Description json.RawMessage `json:"description"`
							} `json:"fields"`
						}
						if err := json.Unmarshal(out.Payload, &payload); err != nil {
							t.Fatal(err)
						}
						document, dryRun = payload.Fields.Description, out.DryRun
						if out.DescriptionRoute != "markdown" {
							t.Fatal("edit lost Markdown route provenance")
						}
					}
					equal, err := adf.StructuralEqual([]byte(literalDocument), document)
					if err != nil || !equal || dryRun != (action == "preview") {
						t.Fatal("body receipt lost literal text or preview state")
					}
					wantWrites := int32(1)
					if action == "preview" {
						wantWrites = 0
					}
					if writes.Load() != wantWrites {
						t.Fatalf("writes = %d, want %d", writes.Load(), wantWrites)
					}
				})
			}
		}
	}
}
