//go:build !unix

package selfupdate

import "os"

func executableReadFlags() int { return os.O_RDONLY }
