package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const (
	authoredStatusMarkdown = `before :status[Ready \*literal\* &amp; 雪]{color=#12abEF localId="owned-id" style=""} after`
	authoredStatusDocument = `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"before "},{"type":"status","attrs":{"text":"Ready *literal* & 雪","color":"#12abEF","localId":"owned-id","style":""}},{"type":"text","text":" after"}]}]}`
)

func TestADFStatusBinaryConversion(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_READ_ONLY=true", "AGENT=1"}, authoredStatusMarkdown, nil, "adf", "convert", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("status conversion: exit %d, stderr %q", exit, stderr)
			}
			if mode == "human" {
				start := strings.IndexByte(stdout, '{')
				if start < 0 {
					t.Fatal("human conversion omitted its native candidate")
				}
				equal, err := adf.StructuralEqual([]byte(authoredStatusDocument), []byte(stdout[start:]))
				if err != nil || !equal {
					t.Fatal("human native candidate changed status attributes")
				}
				return
			}
			var out envelope.ADFConvertOutput
			if err := json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &out); err != nil {
				t.Fatal(err)
			}
			equal, err := adf.StructuralEqual([]byte(authoredStatusDocument), out.Document)
			if err != nil || !equal || out.Lossy || len(out.Diagnostics) != 0 {
				t.Fatal("conversion changed independently expected status attributes")
			}
		})
	}
}

func TestADFStatusBinaryURLLiterals(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		for _, source := range []string{
			`mailto:person@example.test?subject=:status[READY]{color=green}`,
			`urn:example?value=:status[READY]{color=green}`,
			`//example.test/?value=:status[READY]{color=green}`,
		} {
			t.Run(mode+"/"+source, func(t *testing.T) {
				stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_READ_ONLY=true", "AGENT=1"}, source, nil, "adf", "convert", "--output="+mode)
				if exit != 0 || stderr != "" {
					t.Fatalf("literal URL conversion: exit %d, stderr %q", exit, stderr)
				}
				var out envelope.ADFConvertOutput
				if err := json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &out); err != nil {
					t.Fatal(err)
				}
				doc, err := adf.ParseDocument(out.Document)
				if err != nil || doc.Validate() != nil || out.Lossy || len(out.Diagnostics) != 0 {
					t.Fatal("URL text became invalid or lossy native content")
				}
				blocks := doc.Content()
				if len(blocks) != 1 || blocks[0].Type() != "paragraph" {
					t.Fatal("literal URL changed its native block")
				}
				var literal strings.Builder
				for _, child := range blocks[0].Content() {
					if child.Type() != "text" || len(child.Marks()) != 0 || len(child.Content()) != 0 {
						t.Fatal("URL text became authored status or formatted content")
					}
					literal.WriteString(child.Text())
				}
				if literal.String() != source {
					t.Fatalf("literal URL = %q, want %q", literal.String(), source)
				}
			})
		}
	}
}

func TestADFStatusBinaryLiteralPanelRoundTrip(t *testing.T) {
	t.Parallel()
	const original = `{"version":1,"type":"doc","content":[{"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":":status[READY]{color=green}"}]}]}]}`
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_READ_ONLY=true", "AGENT=1"}, original, nil, "adf", "render", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("native literal projection: exit %d, stderr %q", exit, stderr)
			}
			var projection envelope.ADFRenderOutput
			if err := json.Unmarshal(adfData(t, stdout, mode, "adf.render", true), &projection); err != nil {
				t.Fatal(err)
			}
			if projection.Lossy || len(projection.LossyConstructs) != 0 {
				t.Fatal("literal native text lost panel representability")
			}
			stdout, stderr, exit = runBinaryEnvInputStarted(t, []string{"JIRA_READ_ONLY=true", "AGENT=1"}, projection.Markdown, nil, "adf", "convert", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("projected literal conversion: exit %d, stderr %q", exit, stderr)
			}
			var candidate envelope.ADFConvertOutput
			if err := json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &candidate); err != nil {
				t.Fatal(err)
			}
			equal, err := adf.StructuralEqual([]byte(original), candidate.Document)
			if err != nil || !equal || candidate.Lossy || len(candidate.Diagnostics) != 0 {
				t.Fatal("actual binary changed literal native panel text")
			}
		})
	}
}

func TestADFStatusBinaryStrictCandidates(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		for _, input := range []struct{ name, source string }{
			{name: "invalid color", source: `:status[READY]{color=orange}`},
			{name: "unknown attribute", source: `:status[READY]{title=secret}`},
			{name: "formatted label", source: `:status[**READY**]{color=green}`},
		} {
			for _, bestEffort := range []bool{false, true} {
				name := input.name + "/strict"
				if bestEffort {
					name = input.name + "/best-effort"
				}
				t.Run(mode+"/"+name, func(t *testing.T) {
					args := []string{"adf", "convert", "--output=" + mode}
					if bestEffort {
						args = append(args, "--adf-best-effort")
					}
					stdout, _, exit := runBinaryEnvInputStarted(t, []string{"AGENT=1"}, input.source, nil, args...)
					wantExit := 3
					if bestEffort {
						wantExit = 0
					}
					if exit != wantExit {
						t.Fatalf("exit = %d, want %d", exit, wantExit)
					}
					var out envelope.ADFConvertOutput
					if err := json.Unmarshal(adfData(t, stdout, mode, "adf.convert", bestEffort), &out); err != nil {
						t.Fatal(err)
					}
					if !out.Lossy || len(out.Diagnostics) != 1 || !out.Diagnostics[0].Lossy || !strings.Contains(out.Diagnostics[0].Message, "line 1, col 1") {
						t.Fatal("unsupported status lost explicit candidate diagnostics")
					}
					want := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":` + statusJSONLiteral(t, input.source) + `}]}]}`)
					equal, err := adf.StructuralEqual(want, out.Document)
					if err != nil || !equal || adf.Validate(out.Document) != nil {
						t.Fatal("unsupported status candidate did not retain the complete literal spelling")
					}
					if !bestEffort && !strings.Contains(stdout, "adf_content_loss") {
						t.Fatal("strict refusal lost error taxonomy")
					}
				})
			}
		}
	}
}

func TestADFStatusBinaryBodyRoutes(t *testing.T) {
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
						equal, err := adf.StructuralEqual([]byte(authoredStatusDocument), document)
						if err != nil || !equal {
							t.Error("request changed authored status attributes")
						}
						if action == "failure" {
							w.WriteHeader(400)
							_, _ = io.WriteString(w, `{"errorMessages":["fixture write refused"]}`)
							return
						}
						if route == "comment" {
							w.WriteHeader(http.StatusCreated)
							_, _ = io.WriteString(w, `{"id":"10042","body":`+authoredStatusDocument+`}`)
							return
						}
						w.WriteHeader(http.StatusNoContent)
					}))
					t.Cleanup(server.Close)
					args := []string{"issue", "edit", "KEY-123", "--markdown", authoredStatusMarkdown, "--output=" + mode}
					op := "issue.edit"
					if route == "comment" {
						args = []string{"issue", "comment", "add", "KEY-123", "--markdown", authoredStatusMarkdown, "--output=" + mode}
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
					if action == "failure" && (exit != 3 || !strings.Contains(stderr, "fixture write refused")) || action != "failure" && (exit != 0 || stderr != "") {
						t.Fatalf("body route status: exit %d, stderr %q", exit, stderr)
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
					equal, err := adf.StructuralEqual([]byte(authoredStatusDocument), document)
					if err != nil || !equal || dryRun != (action == "preview") {
						t.Fatal("body receipt lost status attributes or preview state")
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

func statusJSONLiteral(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestADFStatusBodyLossKeepsWarningPolicy(t *testing.T) {
	t.Parallel()
	const source = `:status[READY]{title=green}`
	for _, route := range []string{"comment", "description"} {
		t.Run(route, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(server.Close)
			args := []string{"issue", "edit", "KEY-123", "--markdown", source, "--dry-run", "--output=json"}
			if route == "comment" {
				args = []string{"issue", "comment", "add", "KEY-123", "--markdown", source, "--dry-run", "--output=json"}
			}
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			var f frame
			if exit != 0 || json.Unmarshal([]byte(stdout), &f) != nil || !f.OK || requests.Load() != 0 || len(f.Warnings) != 1 {
				t.Fatal("lossy status preview changed existing body policy or made a request")
			}
			var warning lossyWarning
			if err := json.Unmarshal(f.Warnings[0], &warning); err != nil {
				t.Fatal(err)
			}
			if warning.Type != markdownLossyConversion || warning.Lossy == nil || !*warning.Lossy || !strings.Contains(warning.Message, "status directive") || !strings.Contains(warning.Message, "line 1, col 1") {
				t.Fatal("body preview lost explicit source-mapped status loss")
			}
			var document json.RawMessage
			if route == "comment" {
				var out envelope.IssueCommentAddOutput
				if err := json.Unmarshal(f.Data, &out); err != nil {
					t.Fatal(err)
				}
				document = out.Body
			} else {
				var out envelope.IssueEditOutput
				if err := json.Unmarshal(f.Data, &out); err != nil {
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
				document = payload.Fields.Description
			}
			want := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":":status[READY]{title=green}"}]}]}`)
			equal, err := adf.StructuralEqual(want, document)
			if err != nil || !equal {
				t.Fatal("lossy body preview did not retain the complete literal candidate")
			}
		})
	}
}
