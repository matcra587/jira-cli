package native

import (
	"strings"

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
	OrderKeep    = complete.OrderKeep
	OrderShell   = complete.OrderShell
)

// Configure installs the backports on one generator before it is published.
// Other generators retain their selected shell clients; no global registry changes.
// Fish and Elvish delegate ordinary models to their built-in generators.
func Configure(generator *Generator) {
	complete.WithShellGenerator("bash", GenerateBash)(generator)
	complete.WithShellGenerator("zsh", GenerateZsh)(generator)
	complete.WithShellGenerator("pwsh", GeneratePwsh)(generator)
	complete.WithShellGenerator("fish", func(g *Generator) (string, error) {
		if hasCurrentWordPredictor(g) {
			return GenerateFish(g)
		}
		return complete.GenerateFish(g)
	})(generator)
	complete.WithShellGenerator("elvish", func(g *Generator) (string, error) {
		if hasCurrentWordPredictor(g) {
			return GenerateElvish(g)
		}
		return complete.GenerateElvish(g)
	})(generator)
}

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
