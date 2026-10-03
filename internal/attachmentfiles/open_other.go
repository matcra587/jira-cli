//go:build !unix

package attachmentfiles

import (
	"fmt"
	"os"
)

func openFile(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file", path)
	}
	return os.Open(path) //nolint:gosec // explicit caller-named upload input; Open rechecks the descriptor
}
