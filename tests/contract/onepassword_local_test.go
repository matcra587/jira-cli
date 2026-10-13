package contract

import (
	"strings"
	"testing"
)

func TestOnePasswordSelectionLeavesLocalCommandsCredentialFree(t *testing.T) {
	t.Parallel()
	// The selected SDK store cannot authorize: touching it would produce a
	// failure. Each command must nevertheless return its actual local result.
	env := []string{"JIRA_SECRET_BACKEND=1password", "JIRA_ONEPASSWORD_ACCOUNT=", "JIRA_ONEPASSWORD_VAULT=Fixture Vault", "JIRA_ONEPASSWORD_ITEM=Fixture Item", "OP_SERVICE_ACCOUNT_TOKEN=", "JIRA_API_TOKEN="}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"help", []string{"auth", "login", "--help"}, "selected credential store"},
		{"schema", []string{"agent", "schema", "--path=auth status", "--output=json"}, "token_source"},
		{"settings", []string{"config", "get", "secret_backend", "--output=json"}, `"key":"secret_backend"`},
		{"guide", []string{"agent", "guide", "configure-credentials", "--output=json"}, "jira-cli-credential"},
		{"setting completion", []string{"--@complete=configvalue", "--@shell=fish", "--", "secret_backend"}, "1password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exit := runBinaryEnv(t, env, tc.args...)
			if exit != 0 || stderr != "" || !strings.Contains(stdout, tc.want) {
				t.Fatalf("local command opened credentials or lost its result: exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
			}
		})
	}
}
