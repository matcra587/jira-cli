//go:build flagmatrix

package matrix

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWeblinkNativeInputFlagEffects(t *testing.T) {
	t.Parallel()
	const input = `{"object":{"url":"https://example.com/runbook","title":"Runbook","icon":{"url16x16":"https://example.com/icon"},"status":{"resolved":false},"future":{"id":900719925474099312345,"text":"\u003c\/"}},"application":{"name":"Example","type":"com.example"},"globalId":"native-fixture"}`
	for _, flag := range []string{"json-input", "from-json", "body-adf"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				body, err := io.ReadAll(req.Body)
				if err != nil || req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/remotelink") || !strings.Contains(string(body), `"future":{"id":900719925474099312345,"text":"\u003c\/"}`) || !strings.Contains(string(body), `"application":{"name":"Example","type":"com.example"}`) {
					t.Error("native request changed authored metadata or transport")
				}
				calls.Add(1)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":10042}`))
			}))
			t.Cleanup(server.Close)
			res := fwExec(t, server.URL, nil, input, "issue", "weblink", "add", "KEY-1", "--"+flag+"=-", "--no-input", "--output=json")
			out := fwParse(t, res)
			if res.Exit != 0 || !out.OK || calls.Load() != 1 || fwAt(t, out.Data, "payload", "application", "name") != "Example" || fwAt(t, out.Data, "payload", "object", "status", "resolved") != false || fwAt(t, out.Data, "results", 0, "issue", "key") != "KEY-1" {
				t.Fatalf("native input flag had no verified effect: exit=%d calls=%d stdout=%s", res.Exit, calls.Load(), res.Stdout)
			}
			Prove(t, "jira issue weblink add", flag, "native application, icon, false status and raw extension input reached the one POST and complete typed request receipt")
		})
	}
}
