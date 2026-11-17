package jira

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestNativeJSONResponseLimits(t *testing.T) {
	for _, operation := range []struct {
		name string
		body string
		read func(context.Context, *Client) error
	}{
		{name: "issue", body: `{"id":"1","key":"KEY-1","fields":{}}`, read: func(ctx context.Context, c *Client) error {
			_, err := c.GetIssue(ctx, "KEY-1", []string{"summary"})
			return err
		}},
		{name: "count", body: `{"count":7}`, read: func(ctx context.Context, c *Client) error {
			_, err := c.CountIssues(ctx, "project = KEY")
			return err
		}},
		{name: "search", body: `{"issues":[],"isLast":true}`, read: func(ctx context.Context, c *Client) error {
			_, err := c.SearchIssues(
				ctx,
				"project = KEY",
				nil,
				"",
				1,
			)
			return err
		}},
		{
			name: "parse", body: `{"queries":[{"query":"project = KEY","errors":[]}]}`,
			read: func(ctx context.Context, c *Client) error {
				_, err := c.ParseJQL(ctx, []string{"project = KEY"}, core.JQLValidationStrict)
				return err
			},
		},
		{name: "reference", body: `{"visibleFieldNames":[]}`, read: func(ctx context.Context, c *Client) error {
			_, err := c.GetJQLReference(ctx)
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			for _, overflow := range []bool{false, true} {
				t.Run(fmt.Sprintf("overflow=%t", overflow), func(t *testing.T) {
					// A valid unknown member exercises actual JSON buffering, rather
					// than relying on Content-Length or invalid syntax to refuse it.
					prefix := strings.TrimSuffix(operation.body, "}") + `,"padding":"`
					size := int(core.DefaultMaxAPIResponseBytes)
					if overflow {
						size++
					}
					body := prefix + strings.Repeat("x", size-len(prefix)-2) + `"}`
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						_, _ = io.WriteString(w, body)
					}))
					t.Cleanup(server.Close)
					client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture"})
					if err != nil {
						t.Fatal(err)
					}
					err = operation.read(t.Context(), client)
					if overflow && !errors.Is(err, ErrAPIResponseTooLarge) {
						t.Fatalf("oversized native response was not refused: %v", err)
					}
					if !overflow && err != nil {
						t.Fatalf("exact-limit native response changed behavior: %v", err)
					}
				})
			}
		})
	}
}

type nativeResponseReadCounter struct {
	io.Reader
	bytes int
}

func (reader *nativeResponseReadCounter) Read(buffer []byte) (int, error) {
	count, err := reader.Reader.Read(buffer)
	reader.bytes += count
	return count, err
}

func TestNativeJSONReadStopsAtOverflowProbe(t *testing.T) {
	reader := &nativeResponseReadCounter{
		Reader: strings.NewReader(strings.Repeat("x", core.DefaultMaxAPIResponseBytes+1024)),
	}
	body, err := readNativeJSONResponse(reader)
	if body != nil || !errors.Is(err, ErrAPIResponseTooLarge) {
		t.Fatal("overflow returned usable native bytes", err)
	}
	if int64(reader.bytes) != core.DefaultMaxAPIResponseBytes+1 {
		t.Fatalf("read %d bytes, want only the bound plus one overflow probe", reader.bytes)
	}
}
