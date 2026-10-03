package jira

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

const rankReceiptDocument = `{"entries":[{"issueId":10001,"issueKey":"KEY-1","status":200}]}`

func TestIssueDeleteAdapterRequiresNative204WithoutReplay(t *testing.T) {
	t.Parallel()
	for _, cascade := range []bool{false, true} {
		for _, status := range []int{204, 200, 201, 202, 400, 404, 429, 500} {
			t.Run(strconv.FormatBool(cascade)+"/"+strconv.Itoa(status), func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls.Add(1)
					if req.Method != http.MethodDelete || req.URL.Path != "/rest/api/3/issue/KEY-1" || req.URL.Query().Get("deleteSubtasks") != strconv.FormatBool(cascade) {
						t.Error("native deletion changed its explicit target or cascade")
					}
					w.Header().Set("Retry-After", "7")
					w.WriteHeader(status)
				}))
				t.Cleanup(server.Close)
				client, err := New(Config{BaseURL: server.URL, Email: "u@example.com", APIToken: "tok"})
				if err != nil {
					t.Fatal(err)
				}
				write, err := client.DeleteIssue(t.Context(), "KEY-1", cascade)
				if calls.Load() != 1 {
					t.Fatal("issue deletion replayed a write")
				}
				switch {
				case status == http.StatusNoContent:
					if err != nil || write.StatusCode != status || write.Retry.Attempts != 1 {
						t.Fatal("issue deletion lost its native204 acknowledgement")
					}
				case status >= 200 && status < 300:
					if !errors.Is(err, core.ErrIssueDeleteResponseInvalid) || write.StatusCode != 0 {
						t.Fatal("issue deletion accepted an undocumented acknowledgement")
					}
				default:
					api, ok := errors.AsType[*APIError](err)
					if !ok || api.StatusCode != status || api.RetryAfterSeconds != 7 || api.Retry.Attempts != 1 || write.StatusCode != 0 {
						t.Fatal("issue deletion changed its native error facts")
					}
				}
			})
		}
	}
}

func TestRankReceiptPreservesBoundedObservation(t *testing.T) {
	t.Parallel()
	for _, size := range []int{maxErrorBodyBytes, maxErrorBodyBytes + 1} {
		body := rankReceiptDocument + strings.Repeat(" ", size-len(rankReceiptDocument))
		outcomes, err := decodeRankReceipt(strings.NewReader(body))
		if size > maxErrorBodyBytes {
			if outcomes != nil || !errors.Is(err, core.ErrRankResponseInvalid) {
				t.Fatal("oversized native rank receipt bypassed its existing byte ceiling")
			}
		} else if err != nil || len(outcomes) != 1 || outcomes[0].IssueKey != "KEY-1" || outcomes[0].Status != 200 {
			t.Fatal("receipt at the byte ceiling lost its native outcome")
		}
	}
}

func TestRankReceiptPreservesOriginalReaderFailure(t *testing.T) {
	t.Parallel()
	cause := errors.New("fixture rank response stream failed")
	outcomes, err := decodeRankReceipt(io.MultiReader(strings.NewReader(rankReceiptDocument), commentFailureReader{cause: cause}))
	if outcomes != nil || !errors.Is(err, cause) || !errors.Is(err, core.ErrRankResponseInvalid) {
		t.Fatal("rank receipt hid the original read failure or fabricated a complete observation")
	}
}

func TestRankAdapterRefusesInvalidRequestsBeforeTransport(t *testing.T) {
	t.Parallel()
	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "KEY-" + strconv.Itoa(i+1)
	}
	for name, req := range map[string]wire.RankRequest{
		"empty-batch":       {RankBeforeIssue: "KEY-9"},
		"over-ceiling":      {Issues: tooMany, RankBeforeIssue: "KEY-999"},
		"empty-identity":    {Issues: []string{""}, RankBeforeIssue: "KEY-9"},
		"repeated-identity": {Issues: []string{"KEY-1", "KEY-1"}, RankBeforeIssue: "KEY-9"},
		"self-before":       {Issues: []string{"KEY-1"}, RankBeforeIssue: "KEY-1"},
		"self-after":        {Issues: []string{"KEY-1"}, RankAfterIssue: "KEY-1"},
		"both-anchors":      {Issues: []string{"KEY-1"}, RankBeforeIssue: "KEY-9", RankAfterIssue: "KEY-2"},
		"no-anchor":         {Issues: []string{"KEY-1"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "u@example.com", APIToken: "tok"})
			if err != nil {
				t.Fatal(err)
			}
			write, err := client.RankIssues(t.Context(), req)
			input, ok := errors.AsType[*core.RankRequestError](err)
			if !ok || input == nil || calls.Load() != 0 || write.StatusCode != 0 || len(write.Outcomes) != 0 {
				t.Fatal("invalid direct ranking input escaped before transport")
			}
		})
	}
}
