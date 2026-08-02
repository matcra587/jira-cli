package tui

import (
	"strings"
	"testing"
)

func TestDashboardRefusesWithoutRuntime(t *testing.T) {
	cmd := NewCommand()
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "dashboard runtime is deferred") {
		t.Fatalf("dashboard refusal = %v", err)
	}
}
