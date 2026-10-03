//go:build outputaudit

package audit

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
)

func init() {
	stubServers["issue-details"] = func() http.Handler {
		issueHandler := stubServers["issue"]()
		searchHandler := stubServers["search"]()
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handler := issueHandler
			search := r.URL.Path == "/rest/api/3/search/jql"
			if search || r.URL.Path == "/rest/api/3/search/approximate-count" {
				handler = searchHandler
			}
			if search {
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				var request struct {
					Expand string   `json:"expand"`
					Fields []string `json:"fields"`
				}
				if err != nil || json.Unmarshal(body, &request) != nil || request.Expand != "renderedFields,names,schema,transitions,operations,changelog" || len(request.Fields) != 1 || request.Fields[0] != "*all" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
			} else if r.Method == http.MethodGet && r.URL.Path != "/rest/api/3/search/approximate-count" && r.URL.Query().Get("expand") != "names,schema" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			// Keep the existing authentication, routing, pagination, and field
			// stimuli. Enrich only successful issue/search response metadata.
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			body := recorder.Body.Bytes()
			if recorder.Code == http.StatusOK && (search || r.URL.Path != "/rest/api/3/search/approximate-count") {
				var native map[string]json.RawMessage
				if json.Unmarshal(body, &native) != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if search {
					var issues []map[string]json.RawMessage
					if json.Unmarshal(native["issues"], &issues) != nil {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					for _, issue := range issues {
						addIssueDetailsMetadata(issue)
					}
					native["issues"] = mustMarshal(issues)
					native["names"] = json.RawMessage(`{"summary":"Summary"}`)
					native["schema"] = json.RawMessage(`null`)
					native["futureCamelPage"] = json.RawMessage(`false`)
				} else {
					addIssueDetailsMetadata(native)
				}
				body = mustMarshal(native)
			}
			for name, values := range recorder.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(recorder.Code)
			if _, err := w.Write(body); err != nil {
				return
			}
		})
	}
	for index := range scenarios {
		scenario := &scenarios[index]
		if scenario.Category != CategorySuccess || scenario.Variant != "" {
			continue
		}
		switch scenario.Op {
		case "issue.view":
			scenario.Args = append(scenario.Args, "--expand=names,schema", "--comments=0")
		case "issue.list", "issue.mine":
			scenario.Args = append(scenario.Args, "--detail")
		default:
			continue
		}
		scenario.Stub = "issue-details"
	}
}

func addIssueDetailsMetadata(issue map[string]json.RawMessage) {
	issue["renderedFields"] = json.RawMessage(`{"summary":"<b>native HTML</b>"}`)
	issue["names"] = json.RawMessage(`{"summary":"Summary"}`)
	issue["schema"] = json.RawMessage(`null`)
	issue["futureCamelMeta"] = json.RawMessage(`{"largeValue":9007199254740993,"rawText":"\u2028\u003c\/"}`)
}
