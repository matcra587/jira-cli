package main

import (
	"os"

	"github.com/gechr/conductor"

	"github.com/matcra587/jira-cli/internal/cli/root"
)

func main() {
	configureOutputSignals()
	ctx, cancel := conductor.SignalContext()
	defer cancel()
	os.Exit(root.Run(ctx, os.Args[1:]))
}
