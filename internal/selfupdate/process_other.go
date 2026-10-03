//go:build !unix

package selfupdate

import "os/exec"

// Homebrew runs on Unix; other release targets retain CommandContext's own
// cancellation. A compiled target is not evidence of native Homebrew support.
func configureUpdateProcess(_ *exec.Cmd) {}
