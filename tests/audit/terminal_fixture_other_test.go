//go:build outputaudit && !linux && !darwin && !(windows && amd64)

package audit

import "errors"

func runAuditTerminalFixture([]string) error {
	return errors.New("native audit terminal fixture is unavailable on this platform")
}
