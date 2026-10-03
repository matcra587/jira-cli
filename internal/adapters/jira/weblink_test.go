package jira

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

func TestWeblinkDecodersPreserveTrailingReadFailure(t *testing.T) {
	t.Parallel()
	cause := errors.New("fixture response stream failed")
	link, err := decodeRemoteLinkReceipt(io.MultiReader(strings.NewReader(`{"id":10042}`), commentFailureReader{cause: cause}))
	if link != nil || !errors.Is(err, cause) || !errors.Is(err, core.ErrWeblinkResponseInvalid) {
		t.Fatal("remote-link receipt hid the original read failure or published an incomplete document")
	}
	links, err := decodeRemoteLinks(io.MultiReader(strings.NewReader(`[{"id":10042}]`), commentFailureReader{cause: cause}))
	if links != nil || !errors.Is(err, cause) || !errors.Is(err, core.ErrWeblinkResponseInvalid) {
		t.Fatal("remote-link list hid the original read failure or published an incomplete observation")
	}
}

func TestWeblinkAdapterRefusesNonWebSchemes(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":10042}`))
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture-token"})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"ftp://example.com/runbook", "file://example.com/runbook", "javascript://example.com/runbook", "https://", "example.com/runbook", "https://:443/path"} {
		_, err := client.AddRemoteLink(t.Context(), "KEY-1", wire.RemoteLinkRequest{Object: wire.RemoteLinkObject{URL: target, Title: "Runbook"}})
		if err == nil || calls.Load() != 0 {
			t.Fatal("alternate client path sent a non-web or non-absolute URL")
		}
	}
}
