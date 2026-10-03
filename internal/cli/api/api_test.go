package api

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestRawValidationPrecedesBodyAndClient(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, endpoint, code, field string
		in                          input
	}{
		{"absolute", "https://example.com/rest/api/3/issue", cli.CodePathInvalid, "", input{maxResponseBytes: 8}},
		{"encoded", "/rest/api/3/issue/%2e%2e", cli.CodePathInvalid, "", input{maxResponseBytes: 8}},
		{"embedded-query", "/rest/api/3/issue?secret=value", cli.CodePathInvalid, "", input{maxResponseBytes: 8}},
		{"negative-bound", "/rest/api/3/issue", cli.CodeFlagValueInvalid, flagMaxResponseBytes, input{maxResponseBytes: -1}},
		{"content-type", "/rest/api/3/issue", cli.CodeFlagValueInvalid, flagContentType, input{contentType: "bad\r\ntype"}},
		{"entity-tag", "/rest/api/3/issue", cli.CodeFlagValueInvalid, flagIfMatch, input{ifMatch: "unquoted"}},
		{"modification-date", "/rest/api/3/issue", cli.CodeFlagValueInvalid, flagIfUnmodifiedSince, input{ifUnmodifiedSince: "2026-10-02"}},
		{"query", "/rest/api/3/issue", cli.CodeFlagValueInvalid, flagQuery, input{query: []string{"missing-equals"}}},
		{"empty-query-name", "/rest/api/3/issue", cli.CodeFlagValueInvalid, flagQuery, input{query: []string{"=value"}}},
		{"force", "/rest/api/3/issue", cli.CodeFlagRequired, command.FlagForce, input{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deps := Deps{JiraClient: func() (core.JiraClient, error) {
				t.Error("invalid raw request constructed its client")
				return nil, errors.New("unexpected client")
			}}
			test.in.path = "-"
			out, err := run(t.Context(), deps, command.Invocation{Args: []string{test.endpoint}, Stdin: failReader{t}}, "POST", test.in)
			mapped := cli.MapError(err)
			if mapped.Code != test.code || mapped.Field != test.field || out.Response != nil {
				t.Fatalf("invalid raw input classified as %s field %q, want %s field %q", mapped.Code, mapped.Field, test.code, test.field)
			}
		})
	}
}

type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Error("invalid raw request read its body before validation")
	return 0, errors.New("unexpected read")
}

func TestRawPreviewReadsExactStdinWithoutClient(t *testing.T) {
	t.Parallel()
	deps := Deps{JiraClient: func() (core.JiraClient, error) {
		t.Error("preview constructed a client")
		return nil, errors.New("unexpected client")
	}}
	const body = "\x00raw\xff bytes\n"
	out, err := run(t.Context(), deps, command.Invocation{Args: []string{"/rest/api/3/issue"}, DryRun: true, Stdin: strings.NewReader(body)}, "POST", input{path: "-", contentType: "application/octet-stream", maxResponseBytes: 8, ifMatch: `"revision"`, ifUnmodifiedSince: "Thu, 01 Jan 1970 00:00:00 GMT"})
	if err != nil || out.Response != nil || out.Request.BodyBase64 == nil || *out.Request.BodyBase64 != base64.StdEncoding.EncodeToString([]byte(body)) {
		t.Fatalf("stdin preview changed bytes or reached its client: %v", err)
	}
	if out.Request.IfMatch != `"revision"` || out.Request.IfUnmodifiedSince != "Thu, 01 Jan 1970 00:00:00 GMT" {
		t.Fatal("local preview dropped its HTTP preconditions")
	}
}

func TestRawMultipleInvalidOptionsNameTheMatchingFault(t *testing.T) {
	t.Parallel()
	_, err := run(t.Context(), Deps{}, command.Invocation{Args: []string{"/rest/api/3/issue"}}, "POST", input{contentType: "invalid\r\ntype", maxResponseBytes: -1})
	fault := cli.MapError(err)
	if fault.Field != flagContentType || !strings.Contains(fault.Message, "content type") {
		t.Fatalf("raw flag failure named an unrelated cause: field=%q message=%q", fault.Field, fault.Message)
	}
}
