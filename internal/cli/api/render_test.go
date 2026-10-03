package api

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gechr/clog"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestHumanRawReceiptSanitizesEveryUntrustedField(t *testing.T) {
	t.Parallel()
	const hostile = "label\x1b[2J\a\u0085hidden"
	body := hostile + "\nnext"
	out := envelope.APIOutput{
		Request:   envelope.APIRequest{Method: "GET", Endpoint: "/rest/api/3/fixture", IfMatch: hostile, IfUnmodifiedSince: hostile, Query: []envelope.APIValues{{Name: hostile, Values: []string{hostile}}}},
		Attempted: true,
		Response:  &envelope.APIResponse{StatusCode: 200, Headers: []envelope.APIValues{{Name: hostile, Values: []string{hostile}}}, BodyText: &body},
	}
	var buf bytes.Buffer
	if err := render(output.NewView(&buf, clog.ColorNever), envelope.Envelope{Data: out}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(buf.String(), "\x1b\a\u0085") || !strings.Contains(buf.String(), "labelhidden\nnext") || !strings.Contains(buf.String(), "HTTP 200") {
		t.Fatalf("human raw receipt lost useful data or emitted terminal controls: %q", buf.String())
	}
	if *out.Response.BodyText != body || out.Response.Headers[0].Name != hostile {
		t.Fatal("human rendering changed its original machine receipt")
	}
}
