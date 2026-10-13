//go:build outputaudit && !linux

package audit

import (
	"bytes"
	"errors"
	"os/exec"
)

func attachAuditTerminal(*exec.Cmd, *bytes.Buffer) (func() error, error) {
	return nil, errors.New("native audit terminal allocation is not implemented on this platform")
}
