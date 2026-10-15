package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCommentVisibilityNativeWire(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "edit"} {
		for _, tc := range []struct {
			name  string
			flags []string
			want  json.RawMessage
		}{
			{"omitted", nil, nil},
			{"literal-role", []string{"--visibility-role= Administrators\t"}, json.RawMessage(`{"type":"role","value":" Administrators\t"}`)},
			{"role-with-clear-false", []string{"--visibility-role=Administrators", "--clear-visibility=false"}, json.RawMessage(`{"type":"role","value":"Administrators"}`)},
			{"literal-group", []string{"--visibility-group= café<&> "}, json.RawMessage(`{"type":"group","value":" café<&> "}`)},
			{"clear", []string{"--clear-visibility"}, json.RawMessage(`null`)},
		} {
			if verb == "add" && (tc.name == "clear" || tc.name == "role-with-clear-false") {
				continue
			}
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					method, path := http.MethodPost, "/rest/api/3/issue/KEY-1/comment"
					if verb == "edit" {
						method, path = http.MethodPut, path+"/10042"
					}
					body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
					var request map[string]json.RawMessage
					if err != nil || len(body) > 4096 || json.Unmarshal(body, &request) != nil || r.Method != method || r.URL.Path != path {
						t.Error("comment visibility fixture received an invalid native request")
					}
					got, present := request["visibility"]
					var gotValue, wantValue any
					if present != (tc.want != nil) || (present && (json.Unmarshal(got, &gotValue) != nil || json.Unmarshal(tc.want, &wantValue) != nil || !reflect.DeepEqual(gotValue, wantValue))) {
						t.Errorf("visibility changed presence or literal value: got=%s want=%s", got, tc.want)
					}
					if verb == "add" {
						w.WriteHeader(http.StatusCreated)
					}
					_, _ = w.Write([]byte(`{"id":"10042"}`))
				}))
				t.Cleanup(server.Close)
				args := []string{"issue", "comment", verb, "KEY-1"}
				if verb == "edit" {
					args = append(args, "10042")
				}
				args = append(args, "--markdown", "Authored body", "--output=json", "--no-input")
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), append(args, tc.flags...)...)
				var frame struct {
					OK bool `json:"ok"`
				}
				if json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || exit != 0 || calls.Load() != 1 {
					t.Fatalf("valid visibility failed or changed request count: exit=%d calls=%d stdout=%s stderr=%q", exit, calls.Load(), stdout, stderr)
				}
			})
		}
	}
}

func TestCommentVisibilityAuthoredEmptyRefused(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "edit"} {
		for _, preview := range []bool{false, true} {
			for _, tc := range []struct {
				name, code, field string
				flags             []string
			}{
				{"empty-role", cli.CodeFlagValueInvalid, "visibility-role", []string{"--visibility-role="}},
				{"blank-role", cli.CodeFlagValueInvalid, "visibility-role", []string{"--visibility-role= \t\u2003"}},
				{"empty-group", cli.CodeFlagValueInvalid, "visibility-group", []string{"--visibility-group="}},
				{"blank-group", cli.CodeFlagValueInvalid, "visibility-group", []string{"--visibility-group= \t\u2003"}},
				{"both-empty", cli.CodeFlagConflict, "visibility-group", []string{"--visibility-role=", "--visibility-group="}},
				{"empty-role-with-group", cli.CodeFlagConflict, "visibility-group", []string{"--visibility-role=", "--visibility-group=developers"}},
				{"empty-group-with-role", cli.CodeFlagConflict, "visibility-group", []string{"--visibility-role=Administrators", "--visibility-group="}},
				{"empty-role-with-clear", cli.CodeFlagConflict, "clear-visibility", []string{"--visibility-role=", "--clear-visibility"}},
				{"empty-group-with-clear", cli.CodeFlagConflict, "clear-visibility", []string{"--visibility-group=", "--clear-visibility"}},
			} {
				if verb == "add" && tc.field == "clear-visibility" {
					continue
				}
				mode := "live"
				if preview {
					mode = "preview"
				}
				t.Run(verb+"/"+mode+"/"+tc.name, func(t *testing.T) {
					t.Parallel()
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						calls.Add(1)
						w.WriteHeader(http.StatusBadRequest)
					}))
					t.Cleanup(server.Close)
					args := []string{"issue", "comment", verb, "KEY-1"}
					if verb == "edit" {
						args = append(args, "10042")
					}
					args = append(args, "--markdown", "Authored body", "--output=json", "--no-input")
					args = append(args, tc.flags...)
					if preview {
						args = append(args, "--dry-run")
					}
					stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
					var frame struct {
						OK     bool             `json:"ok"`
						Data   json.RawMessage  `json:"data"`
						Errors []envelope.Error `json:"errors"`
					}
					if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || exit != cli.ExitValidation || len(frame.Errors) != 1 || frame.Errors[0].Code != tc.code || frame.Errors[0].Field != tc.field || string(frame.Data) != "null" || calls.Load() != 0 {
						t.Fatalf("authored visibility was accepted, misclassified or reached HTTP: exit=%d calls=%d stdout=%s stderr=%q", exit, calls.Load(), stdout, stderr)
					}
				})
			}
		}
	}
}
