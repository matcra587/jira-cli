package contract

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestMachineFailuresRemainJSONWhenStreamsAreCombined(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		for _, tc := range []struct {
			name, code string
			args       []string
		}{
			{name: "schema", args: []string{"agent", "schema", "--path=missing command"}, code: "flag_value_invalid"},
			{name: "unknown command", args: []string{"verson"}, code: "command_unknown"},
			{name: "flag parse", args: []string{"version", "--no-such-flag"}, code: "flag_unknown"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				args := append([]string{"--output=" + mode}, tc.args...)
				stdout, stderr, exit := runBinary(t, args...)
				var frame struct {
					OK     bool             `json:"ok"`
					Errors []envelope.Error `json:"errors"`
				}
				if exit != 3 || stderr != "" || json.Unmarshal([]byte(stdout+stderr), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != tc.code || frame.Errors[0].Hint == "" {
					t.Fatalf("machine error stream changed: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
				}
			})
		}
	}
}
