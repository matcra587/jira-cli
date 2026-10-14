//go:build outputaudit && !(windows && amd64)

package audit

import "errors"

func runAuditTerminalProcess([]string) error {
	return errors.New("native audit terminal helper is unavailable on this platform")
}
