// Package native backports reviewed clib Bash, PowerShell and Zsh generator repairs while
// retaining clib's public model, validation and installation owners.
package native

import (
	"strings"
	"sync"

	"github.com/gechr/clib/complete"
)

type (
	Generator = complete.Generator
	Spec      = complete.Spec
	SubSpec   = complete.SubSpec
	ValueDesc = complete.ValueDesc
)

const (
	FlagComplete = complete.FlagComplete
	HintFile     = complete.HintFile
	HintDir      = complete.HintDir
	HintCommand  = complete.HintCommand
	HintUser     = complete.HintUser
	HintHost     = complete.HintHost
	HintURL      = complete.HintURL
	HintEmail    = complete.HintEmail
)

// Register installs the backports before the composition root publishes its
// first generator. All application generator access follows that barrier, so
// concurrent program construction cannot race on clib's registration map.
// Other shells retain their built-in generators.
var Register = sync.OnceFunc(func() {
	complete.RegisterShell("bash", GenerateBash)
	complete.RegisterShell("zsh", GenerateZsh)
	complete.RegisterShell("pwsh", GeneratePwsh)
})

func ValidateGenerator(generator *Generator) error {
	return complete.ValidateGenerator(generator)
}

func SortVisibleSpecs(specs []Spec) []Spec {
	return complete.SortVisibleSpecs(specs)
}

func SortSubSpecs(subcommands []SubSpec) []SubSpec {
	return complete.SortSubSpecs(subcommands)
}

func WriteIndented(builder *strings.Builder, indent, block string) {
	complete.WriteIndented(builder, indent, block)
}
