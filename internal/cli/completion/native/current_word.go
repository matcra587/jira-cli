package native

import "strings"

// WordArgument identifies a literal current-word value inside callback args,
// after clib's transport terminator and before forwarded context. A second
// terminator separates completed positionals from those context flags.
const WordArgument = "--@word="

// WordDirectoryHint marks a filesystem directory in PowerShell's native
// completion transport. A slash in an ordinary value is not a directory hint.
const WordDirectoryHint = "--@directory"

// CurrentWordPredictor is the explicit metadata convention for callbacks that
// need the current logical argument. The selected clib model provides predictor
// names but no current-word option. Ordinary predictors retain their protocol;
// word_ callbacks return complete arguments and perform their own matching.
func CurrentWordPredictor(kind string) bool { return strings.HasPrefix(kind, "word_") }

func hasCurrentWordPredictor(g *Generator) bool {
	for _, kind := range g.DynamicArgs {
		if CurrentWordPredictor(kind) {
			return true
		}
	}
	return anySpecInTree(g.Specs, g.Subs,
		func(spec Spec) bool { return CurrentWordPredictor(spec.Dynamic) },
		func(sub SubSpec) bool {
			for _, kind := range sub.DynamicArgs {
				if CurrentWordPredictor(kind) {
					return true
				}
			}
			return false
		})
}
