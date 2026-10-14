//go:build outputaudit

package audit

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// runAuditCommand keeps the actual CLI exit separate from capture failures. A
// native terminal helper's incidental exit must never stand in for the CLI's.
// The caller prepares and fingerprints all fixtures before entering this owner.
func runAuditCommand(ctx context.Context, cmd *exec.Cmd, terminal bool, stderr *bytes.Buffer) (int, error) {
	if err := context.Cause(ctx); err != nil {
		return 0, err
	}
	if terminal {
		return runAuditTerminal(ctx, cmd, stderr)
	}
	code, err := auditCommandExit(cmd.Run())
	return code, errors.Join(err, context.Cause(ctx))
}

func auditCommandExit(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	return 0, err
}
