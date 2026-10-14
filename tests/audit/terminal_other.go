//go:build outputaudit && !linux && !darwin && !(windows && amd64)

package audit

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

func runAuditTerminal(context.Context, *exec.Cmd, *bytes.Buffer) (int, error) {
	return 0, errors.New("native audit terminal allocation is not implemented on this platform")
}
