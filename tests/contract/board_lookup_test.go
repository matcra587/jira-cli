package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBoardLookupBinaryRefusesMalformedPages(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`null`, `{}`, `{"isLast":true,"startAt":0,"maxResults":50,"values":null}`, `{"isLast":false,"startAt":0,"maxResults":50,"values":[]}`, `{"isLast":true,"startAt":0,"maxResults":50,"values":[{"id":0,"name":"Sprint"}]}`, `{"isLast":true,"startAt":0,"maxResults":50,"values":[]} {}`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			for _, mode := range []string{"json", "compact"} {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.URL.Path != "/rest/agile/1.0/board" {
						t.Errorf("malformed lookup still continued: %s", r.URL)
					}
					_, _ = fmt.Fprint(w, body)
				}))
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "list", "--board=Sprint", "--refresh", "--output="+mode)
				server.Close()
				if exit != 5 || requests.Load() != 1 {
					t.Fatalf("protocol failure lost boundary: exit=%d requests=%d stderr=%q stdout=%s", exit, requests.Load(), stderr, stdout)
				}
				var frame struct {
					OK     bool `json:"ok"`
					Errors []struct {
						Code string `json:"code"`
					} `json:"errors"`
				}
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "board_response_invalid" {
					t.Fatalf("malformed board became an input error or empty success: %s", stdout)
				}
			}
		})
	}
}

func TestBoardLookupBinaryRefusesExactNameOnPartialPage(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/rest/agile/1.0/board" {
			t.Errorf("ambiguous lookup continued: %s", r.URL)
		}
		_, _ = fmt.Fprint(w, `{"isLast":false,"startAt":0,"maxResults":50,"values":[{"id":42,"name":"Sprint","type":"scrum"}]}`)
	}))
	defer server.Close()
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "list", "--board=Sprint", "--refresh", "--output=json")
	if exit != 3 || requests.Load() != 1 || !strings.Contains(stdout, "flag_value_invalid") || !strings.Contains(stdout, "--board-id 42") {
		t.Fatalf("incomplete exact name resolved or lost actionable refusal: exit=%d requests=%d stderr=%q stdout=%s", exit, requests.Load(), stderr, stdout)
	}
}
