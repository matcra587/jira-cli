//go:build windows

package issueedit

import (
	"errors"

	"golang.org/x/sys/windows"
)

func directoryRenameLocked(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
