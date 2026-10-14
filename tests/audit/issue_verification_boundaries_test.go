//go:build outputaudit

package audit

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestVerificationObservedNativeNamesKeepNarrowBoundary(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"issue.create", "issue.edit"} {
		for _, mode := range []Mode{ModeJSON, ModeCompact} {
			for _, badOwned := range []bool{false, true} {
				name := op + "/" + string(mode) + "/native"
				if badOwned {
					name = op + "/" + string(mode) + "/bad-owned"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					fieldName := "field"
					if badOwned {
						fieldName = "fieldID"
					}
					data := `{"verified":{"fields":[{"` + fieldName + `":"priority","outcome":"matches","reason":"object-identity","observed":{"name":"High","avatarId":0,"futureValue":false}}]}}`
					if op == "issue.edit" {
						data = `{"results":[{"data":` + data + `}]}`
					}
					if mode == ModeJSON {
						data = `{"ok":true,"data":` + data + `}`
					}
					capture := Capture{Stdout: data, Mode: mode}
					ok, detail := checkSuccessSnakeCase(capture, command.Meta{Op: op, Passthrough: true})
					if ok == badOwned {
						t.Fatal("verification passthrough boundary lost native admission or owned-key refusal:", detail)
					}
				})
			}
		}
	}
}
