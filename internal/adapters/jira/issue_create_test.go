package jira

import (
	"bytes"
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

func TestIssueCreationAdapterRetainsIdentityWithFailedFollowup(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"transition", "watchers"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			body := `{"id":"9001","key":"NEW-1","` + operation + `":{"status":400,"errorCollection":{"errorMessages":["fixture followup rejected"]}},"future":{"nullable":null,"zero":0}}`
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue" {
					t.Error("creation changed its native dispatch")
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "u@example.com", APIToken: "tok"})
			if err != nil {
				t.Fatal(err)
			}
			write, err := client.CreateIssue(t.Context(), wire.IssueWriteRequest{})
			if !errors.Is(err, core.ErrIssueCreateFollowupFailed) || write.StatusCode != http.StatusCreated || write.Issue == nil || write.Issue.Key != "NEW-1" || !bytes.Equal(write.Receipt, []byte(body)) || write.Retry.Attempts != 1 || calls.Load() != 1 {
				t.Fatal("native creation discarded its followup failure, assigned identity, acknowledgement or complete receipt")
			}
		})
	}
}

func TestIssueCreationAdapterRequiresNative201WithoutReplay(t *testing.T) {
	t.Parallel()
	for _, status := range []int{201, 200, 202, 204, 400, 403, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue" || req.URL.RawQuery != "" {
					t.Error("creation changed its native method or endpoint")
				}
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"id":"9001","key":"NEW-1","self":"https://example.atlassian.net/rest/api/3/issue/9001"}`))
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "u@example.com", APIToken: "tok"})
			if err != nil {
				t.Fatal(err)
			}
			write, err := client.CreateIssue(t.Context(), wire.IssueWriteRequest{})
			if calls.Load() != 1 {
				t.Fatal("creation replayed a write")
			}
			switch {
			case status == http.StatusCreated:
				if err != nil || write.StatusCode != status || write.Issue == nil || write.Issue.Key != "NEW-1" || len(write.Receipt) == 0 || write.Retry.Attempts != 1 {
					t.Fatal("creation lost its native201 acknowledgement or receipt")
				}
			case status >= 200 && status < 300:
				if !errors.Is(err, core.ErrIssueCreateResponseInvalid) || write.StatusCode != 0 || write.Issue != nil || len(write.Receipt) != 0 {
					t.Fatal("creation accepted an undocumented acknowledgement")
				}
			default:
				api, ok := errors.AsType[*APIError](err)
				if !ok || api.StatusCode != status || api.RetryAfterSeconds != 7 || api.Retry.Attempts != 1 || write.StatusCode != 0 || write.Issue != nil {
					t.Fatal("creation changed its native error facts")
				}
			}
		})
	}
}

func TestIssueCreationReceiptPreservesOriginalReaderFailure(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"", `{"id":"9001","key":"NEW-1"}`} {
		t.Run(strconv.Itoa(len(prefix)), func(t *testing.T) {
			t.Parallel()
			cause := errors.New("fixture creation response stream failed")
			write, err := decodeIssueCreation(io.MultiReader(strings.NewReader(prefix), commentFailureReader{cause: cause}))
			if !errors.Is(err, cause) || !errors.Is(err, core.ErrIssueCreateResponseInvalid) || write.StatusCode != http.StatusCreated || write.Issue != nil || len(write.Receipt) != 0 {
				t.Fatal("creation lost its acknowledgement or original reader failure, or fabricated a complete receipt")
			}
		})
	}
}
