//go:build outputaudit

package audit

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestAliasLiteralMapBoundariesKeepCLIKeysStrict(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ op, field string }{{"alias.list", "aliases"}, {"alias.import", "skipped"}} {
		for _, mode := range []Mode{ModeJSON, ModeCompact} {
			body := `{"` + tc.field + `":{"User.Name With Spaces":"literal"}}`
			if mode == ModeJSON {
				body = `{"data":` + body + `}`
			}
			if ok, detail := checkSuccessSnakeCase(Capture{Mode: mode, Stdout: body}, command.Meta{Op: tc.op}); !ok {
				t.Fatalf("literal alias keys rejected: %s %s %s", tc.op, mode, detail)
			}
			for _, field := range []string{"Changed", tc.field + "_extra"} {
				body = `{"` + field + `":{"UserName":"literal"}}`
				if mode == ModeJSON {
					body = `{"data":` + body + `}`
				}
				if ok, _ := checkSuccessSnakeCase(Capture{Mode: mode, Stdout: body}, command.Meta{Op: tc.op}); ok {
					t.Fatalf("alias boundary accepted a CLI field or lookalike map: %s %s %s", tc.op, mode, field)
				}
			}
		}
	}
}
