//go:build outputaudit

package audit

import (
	"flag"
	"fmt"
	"os"
	"testing"
)

// TestMain finalizes the matrix after every test has recorded its
// cells: the Cartesian-product verification and the baseline ratchet
// are the run's real verdict, over and above individual test failures.
// A -run filter skips finalization — a partial run records a partial
// cell set, and holding that against the full expected product would
// report gaps that never existed.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--audit-terminal-fixture" {
		if err := runAuditTerminalFixture(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "--audit-terminal" {
		if err := runAuditTerminalProcess(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "--audit-editor" {
		if err := runAuditEditor(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	flag.Parse()
	code := m.Run()
	cleanupBinary()
	// A partial run (-run or -skip) yields a partial cell set; finalizing
	// the matrix against it would report phantom coverage gaps.
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			os.Exit(code)
		}
	}
	if problem := finalizeMatrix(); problem != "" {
		fmt.Fprintln(os.Stderr, "outputaudit matrix:\n  "+problem)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
