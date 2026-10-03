package jira

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchRefusesMalformedPages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body string }{
		{"null", `null`},
		{"missing completion", `{"issues":[]}`},
		{"null completion", `{"isLast":null,"issues":[]}`},
		{"missing issues", `{"isLast":true}`},
		{"null issues", `{"isLast":true,"issues":null}`},
		{"object issues", `{"isLast":true,"issues":{}}`},
		{"null row", `{"isLast":true,"issues":[null]}`},
		{"missing key", `{"isLast":true,"issues":[{}]}`},
		{"null key", `{"isLast":true,"issues":[{"key":null}]}`},
		{"blank key", `{"isLast":true,"issues":[{"key":" "}]}`},
		{"duplicate key", `{"isLast":true,"issues":[{"key":"KEY-1"},{"key":"KEY-1"}]}`},
		{"oversized", `{"isLast":true,"issues":[{"key":"KEY-1"},{"key":"KEY-2"},{"key":"KEY-3"}]}`},
		{"trailing document", `{"isLast":true,"issues":[]} {}`},
		{"trailing garbage", `{"isLast":true,"issues":[]} garbage`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, tc.body) }))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture", ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			read, err := client.SearchIssues(t.Context(), "project = KEY", nil, "", 2)
			if !errors.Is(err, ErrSearchResponseInvalid) || read.Results != nil {
				t.Fatalf("malformed page accepted: %+v %v", read, err)
			}
		})
	}
}

func TestSearchSparseAndFinalPagesRemainInspectable(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"isLast":false,"issues":[],"nextPageToken":"advancing","future":9007199254740993}`,
		`{"isLast":true,"issues":[],"nextPageToken":null}`,
		`{"isLast":true,"issues":[]}`,
		`{"isLast":false,"issues":[{"key":"KEY-1"}],"nextPageToken":null}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture", ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			read, err := client.SearchIssues(t.Context(), "project = KEY", nil, "", 0)
			if err != nil || read.Results == nil || read.Retry.Attempts != 1 {
				t.Fatalf("valid sparse/final page discarded: %+v %v", read, err)
			}
			if raw, ok := read.Results.Extra["future"]; ok && string(raw) != "9007199254740993" {
				t.Fatal("extension value coerced")
			}
		})
	}
}
