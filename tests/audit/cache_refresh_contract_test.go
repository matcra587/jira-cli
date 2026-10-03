//go:build outputaudit

package audit

import (
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestResourceResultAuditAcceptsRetainedFailuresAndRejectsMalformedRows(t *testing.T) {
	t.Parallel()
	const valid = `{"results":[{"resource":"fields","ok":false,"data":{"resource":"fields","count":1},"error":{"code":"cache_io","message":"save failed","hint":"inspect storage"}},{"resource":"labels","ok":true,"data":{"resource":"labels","count":0}}]}`
	for _, tc := range []struct {
		name, data string
		want       bool
	}{
		{name: "retained partial resource failure", data: valid, want: true},
		{name: "missing failed data", data: strings.Replace(valid, `"data":{"resource":"fields","count":1},`, "", 1)},
		{name: "null failed data", data: strings.Replace(valid, `"data":{"resource":"fields","count":1}`, `"data":null`, 1)},
		{name: "scalar failed data", data: strings.Replace(valid, `"data":{"resource":"fields","count":1}`, `"data":1`, 1)},
		{name: "missing error", data: strings.Replace(valid, `,"error":{"code":"cache_io","message":"save failed","hint":"inspect storage"}`, "", 1)},
		{name: "missing hint", data: strings.Replace(valid, `"hint":"inspect storage"`, `"hint":""`, 1)},
		{name: "wrong retained resource", data: strings.Replace(valid, `"data":{"resource":"fields"`, `"data":{"resource":"labels"`, 1)},
		{name: "fabricated issue", data: strings.Replace(valid, `"resource":"fields"`, `"resource":"fields","issue":{"key":"fields"}`, 1)},
		{name: "wrong row order", data: strings.Replace(valid, `"resource":"fields"`, `"resource":"labels"`, 1)},
		{name: "missing verdict", data: strings.Replace(valid, `"ok":false,`, "", 1)},
		{name: "success carrying failure", data: strings.Replace(valid, `"ok":false`, `"ok":true`, 1)},
		{name: "total failure in success frame", data: strings.Replace(valid, `"resource":"labels","ok":true,"data":{"resource":"labels","count":0}`, `"resource":"labels","ok":false,"data":{"resource":"labels","count":0},"error":{"code":"cache_io","message":"failed","hint":"inspect storage"}`, 1)},
	} {
		for _, mode := range []Mode{ModeJSON, ModeCompact} {
			t.Run(tc.name+"/"+string(mode), func(t *testing.T) {
				t.Parallel()
				data := tc.data
				if mode == ModeJSON {
					data = `{"ok":true,"data":` + data + `}`
				}
				ok, detail := checkSuccessKeyedResults(Capture{Mode: mode, Stdout: data}, command.Meta{Op: "cache.refresh"})
				if ok != tc.want {
					t.Fatalf("resource M3(%s)=%v, %q, want %v", tc.name, ok, detail, tc.want)
				}
			})
		}
	}
}

func TestIssueResultAuditKeepsExistingFailedDataRefusal(t *testing.T) {
	t.Parallel()
	cap := Capture{Mode: ModeCompact, Stdout: `{"results":[{"issue":{"key":"TST-2"},"ok":false,"data":{"count":1},"error":{"code":"jira_not_found","message":"gone","hint":"inspect target"}},{"issue":{"key":"TST-3"},"ok":true,"data":{}}]}`}
	if ok, detail := checkSuccessKeyedResults(cap, command.Meta{Op: "issue.link.list"}); ok || !strings.Contains(detail, "failed yet carries data") {
		t.Fatalf("issue M3 accepted failed data or refused wrong boundary: ok=%v detail=%q", ok, detail)
	}
}
