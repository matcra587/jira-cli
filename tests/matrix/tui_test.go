//go:build flagmatrix

package matrix

import "testing"

func TestDashboardJQLRejectsWhitespaceBeforeTerminalOrCredentialAdmission(t *testing.T) {
	dir := t.TempDir()
	invalid := mx3Run(t, dir, "http://127.0.0.1:1", "tui", "--jql=   ", "--output=json")
	mx3RequireRefusal(t, invalid, "flag_value_invalid", "jql")
	valid := mx3Run(t, dir, "http://127.0.0.1:1", "tui", "--jql=project = DEMO", "--output=json")
	mx3RequireRefusal(t, valid, "dashboard_terminal_required", "")
	Prove(t, "jira tui", "jql", "bound whitespace queries fail pure validation on jql; substantive queries reach terminal refusal without HTTP; native session acceptance separately binds the authored query to search requests")
}
