//go:build windows

package issue

import (
	"errors"

	"golang.org/x/sys/windows"
)

func downloadDirectoryRenameLocked(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
