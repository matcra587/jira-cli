package jira

import (
	"encoding/json"
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
		object, err := wire.MarshalNoEscape(map[string]string{"url": target, "title": "Runbook"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.AddRemoteLink(t.Context(), "KEY-1", wire.RemoteLinkRequest{"object": json.RawMessage(object)})
		if err == nil || calls.Load() != 0 {
			t.Fatal("alternate client path sent a non-web or non-absolute URL")
		}
	}
}

func TestWeblinkAdapterRefusesInvalidNativeRequests(t *testing.T) {
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
	for name, request := range map[string]wire.RemoteLinkRequest{
		"missing object":   {},
		"null object":      {"object": json.RawMessage(`null`)},
		"missing title":    {"object": json.RawMessage(`{"url":"https://example.com"}`)},
		"null title":       {"object": json.RawMessage(`{"url":"https://example.com","title":null}`)},
		"nonstring title":  {"object": json.RawMessage(`{"url":"https://example.com","title":false}`)},
		"status type":      {"object": json.RawMessage(`{"url":"https://example.com","title":"Example","status":{"resolved":0}}`)},
		"known casing":     {"object": json.RawMessage(`{"URL":"https://example.com","title":"Example"}`)},
		"nested duplicate": {"object": json.RawMessage(`{"url":"https://example.com","title":"Example","future":{"id":1,"id":2}}`)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := client.AddRemoteLink(t.Context(), "KEY-1", request); err == nil || calls.Load() != 0 {
				t.Fatal("alternate client path sent an invalid native request")
			}
		})
	}
}
