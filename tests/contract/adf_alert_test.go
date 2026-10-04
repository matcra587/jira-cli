package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const alertMarkdown = "> [!WARNING]\n> **Keep** this.\n"

const alertDocument = `{"version":1,"type":"doc","content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"Keep","marks":[{"type":"strong"}]},{"type":"text","text":" this."}]}]}]}`

func TestADFAlertBinaryConversionIsCredentialFree(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_BASE_URL=http://127.0.0.1:9", "JIRA_READ_ONLY=true", "AGENT=1"}, alertMarkdown, nil, "adf", "convert", "--output="+mode)
			var converted envelope.ADFConvertOutput
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &converted) != nil || converted.Lossy || len(converted.Diagnostics) != 0 {
				t.Fatalf("local alert conversion failed: exit %d, stderr %q", exit, stderr)
			}
			equal, err := adf.StructuralEqual(converted.Document, []byte(alertDocument))
			if err != nil || !equal {
				t.Fatal("binary did not author the independent expected panel")
			}
		})
	}
}

func TestADFAlertStrictConversionRefusesDroppedHTML(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			stdout, _, exit := runBinaryEnvInputStarted(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_READ_ONLY=true", "JIRA_BASE_URL=http://127.0.0.1:9"}, "> [!NOTE]\n> <span>kept\n", nil, "adf", "convert", "--output="+mode)
			var refusal frame
			var failure envelope.Error
			var candidate envelope.ADFConvertOutput
			if exit != 3 || json.Unmarshal([]byte(stdout), &refusal) != nil || refusal.OK || len(refusal.Errors) != 1 || json.Unmarshal(refusal.Errors[0], &failure) != nil || failure.Code != "adf_content_loss" {
				t.Fatal("strict conversion did not refuse actual alert content loss")
			}
			if json.Unmarshal(refusal.Data, &candidate) != nil || !candidate.Lossy || len(candidate.Diagnostics) != 1 || !candidate.Diagnostics[0].Lossy || adf.Validate(candidate.Document) != nil {
				t.Fatal("strict refusal lost the valid candidate or the body's HTML diagnostic")
			}
		})
	}
}

func TestADFAlertBinaryBodyRoutesSendNativePanel(t *testing.T) {
	t.Parallel()
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body struct {
			Body   json.RawMessage `json:"body"`
			Fields struct {
				Description json.RawMessage `json:"description"`
			} `json:"fields"`
		}
		if json.Unmarshal(data, &body) != nil {
			t.Error("body route did not send a native JSON request")
		}
		var document json.RawMessage
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/rest/api/3/issue/KEY-123/comment":
			document = body.Body
		case req.Method == http.MethodPut && req.URL.Path == "/rest/api/3/issue/KEY-123":
			document = body.Fields.Description
		default:
			t.Error("alert body performed an unrelated request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		equal, err := adf.StructuralEqual(document, []byte(alertDocument))
		if err != nil || !equal {
			t.Error("body request lost its native panel or inline formatting")
		}
		writes.Add(1)
		if req.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"10042","body":`+alertDocument+`}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	for _, mode := range []string{"json", "compact"} {
		for _, route := range []string{"comment", "description"} {
			t.Run(mode+"/"+route, func(t *testing.T) {
				args := []string{"issue", "edit", "KEY-123", "--markdown", alertMarkdown, "--output=" + mode}
				if route == "comment" {
					args = []string{"issue", "comment", "add", "KEY-123", "--markdown", alertMarkdown, "--output=" + mode}
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				if exit != 0 || stdout == "" || stderr != "" {
					t.Fatalf("native alert body route failed: exit %d, stderr %q", exit, stderr)
				}
			})
		}
	}
	if writes.Load() != 4 {
		t.Fatal("body routes did not each submit exactly one native write")
	}
}
