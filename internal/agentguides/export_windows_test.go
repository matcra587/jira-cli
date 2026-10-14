package agentguides

import (
	"errors"

	"golang.org/x/sys/windows"
)

func exportSymlinkPrivilegeUnavailable(err error) bool {
	return errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD)
}
