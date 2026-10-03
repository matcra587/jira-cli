package jira

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

const transitionReadFixture = `{"transitions":[{"id":"31","name":"Finish","to":{"id":"42","name":"Done"},"hasScreen":true,"fields":{"resolution":{"required":true,"schema":{"type":"resolution"}}}}]}`

func TestGetTransitionsRejectsUnexpectedNativeSuccess(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusCreated, http.StatusAccepted, http.StatusNoContent, http.StatusPartialContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				if status != http.StatusNoContent {
					_, _ = w.Write([]byte(transitionReadFixture))
				}
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			read, err := client.GetTransitions(t.Context(), "KEY-1")
			if read.Transitions != nil || !errors.Is(err, core.ErrTransitionReadInvalid) {
				t.Fatal("unexpected native workflow status lost its protocol classification or published data")
			}
		})
	}
}

func TestGetTransitionsRequestsNativeScreenMetadata(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/issue/KEY-1/transitions" || req.URL.Query().Get("expand") != "transitions.fields" || len(req.URL.Query()) != 1 {
			t.Error("transition read omitted native screen metadata or changed its scope")
		}
		_, _ = w.Write([]byte(transitionReadFixture))
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	read, err := client.GetTransitions(t.Context(), "KEY-1")
	if err != nil || read.Transitions == nil || len(read.Transitions.Transitions.Value) != 1 {
		t.Fatal("read-only native transition metadata was not returned")
	}
}

func TestGetTransitionsRejectsUnusableNativeLists(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"root-null": "null", "missing-list": `{}`, "null-list": `{"transitions":null}`,
		"blank-id":          `{"transitions":[{"id":" ","hasScreen":false}]}`,
		"missing-screen":    `{"transitions":[{"id":"31"}]}`,
		"duplicate-id":      `{"transitions":[{"id":"31","hasScreen":false},{"id":"31","hasScreen":true}]}`,
		"null-field":        `{"transitions":[{"id":"31","hasScreen":true,"fields":{"summary":null}}]}`,
		"required-type":     `{"transitions":[{"id":"31","hasScreen":true,"fields":{"summary":{"required":"yes"}}}]}`,
		"required-null":     `{"transitions":[{"id":"31","hasScreen":true,"fields":{"summary":{"required": null }} }]}`,
		"fields-null":       `{"transitions":[{"id":"31","hasScreen":false,"fields":null}]}`,
		"screen-null":       `{"transitions":[{"id":"31","hasScreen":null}]}`,
		"screen-type":       `{"transitions":[{"id":"31","hasScreen":"false"}]}`,
		"id-whitespace":     `{"transitions":[{"id":" 31","hasScreen":false}]}`,
		"trailing-document": transitionReadFixture + `{}`, "trailing-garbage": transitionReadFixture + `broken`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.GetTransitions(t.Context(), "KEY-1")
			if err == nil || !strings.Contains(err.Error(), "invalid transition response") {
				t.Fatal("unusable transition metadata was accepted or lost its protocol classification")
			}
		})
	}
}

type transitionErrorReader struct{ err error }

func (r transitionErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestDecodeTransitionsRetainsReaderCausesWithoutPartialData(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, io.ErrUnexpectedEOF} {
		for _, prefix := range []string{"", transitionReadFixture, `{"transitions":[`} {
			list, err := decodeTransitions(io.MultiReader(strings.NewReader(prefix), transitionErrorReader{cause}))
			if list != nil || !errors.Is(err, cause) || !errors.Is(err, core.ErrTransitionReadInvalid) {
				t.Fatal("workflow decoding published incomplete data or lost the original reader cause")
			}
		}
	}
}

func TestDecodeTransitionsPreservesNativeValuesAndExplicitEmptyList(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"transitions":[],"future":null}`,
		`{"transitions":[{"id":"31","hasScreen":false,"fields":{"summary":{"required":false,"schema":{"future":null},"allowedValues":[],"defaultValue":null}},"future":900719925474099312345}]}`,
	} {
		list, err := decodeTransitions(strings.NewReader(" \n" + body + " \t\n"))
		if err != nil || list == nil || list.Transitions.Value == nil {
			t.Fatal("explicit native workflow was refused")
		}
		actual, err := json.Marshal(list)
		decode := func(raw string) any {
			decoder := json.NewDecoder(strings.NewReader(raw))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		if err != nil || !reflect.DeepEqual(decode(string(actual)), decode(body)) {
			t.Fatal("native workflow lost array presence, precision or nullable metadata")
		}
	}
}
