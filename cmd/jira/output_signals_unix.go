//go:build unix

package main

import (
	"os/signal"
	"syscall"
)

// configureOutputSignals lets failed stdout/stderr writes reach the output
// boundary. Go otherwise terminates on SIGPIPE for those descriptors before
// the CLI can return its documented output failure exit code.
func configureOutputSignals() {
	signal.Ignore(syscall.SIGPIPE)
}
