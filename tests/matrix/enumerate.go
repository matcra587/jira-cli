//go:build flagmatrix

package matrix

import (
	"cmp"
	"fmt"
	"slices"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Class says what a (command path, flag) pair IS at that path, which
// decides what the gate demands of it.
type Class string

const (
	// ClassOwned: the flag is declared by this command (local or its
	// own persistent registration, hidden aliases included). Every
	// owned pair must be proved — this is the required set.
	ClassOwned Class = "owned"
	// ClassInherited: the flag is visible here but declared by an
	// ancestor's persistent registration. Inherited pairs are
	// enumerated so the surface is complete, but the proof obligation
	// sits on the owner: one proof at the declaring command covers the
	// declaration, and a matrix demanding it again on every descendant
	// would inflate the number without adding evidence.
	ClassInherited Class = "inherited"
	// ClassFramework: cobra's auto-registered --help, one per command.
	// It is the same framework surface everywhere, so — like an
	// inherited flag — it is required once, at the root.
	ClassFramework Class = "framework"
)

// Pair is one cell of the matrix: a flag as it appears at one command
// path.
type Pair struct {
	// Path is the command the flag appears on ("jira issue list").
	Path string
	// Flag is the long flag name, no dashes.
	Flag  string
	Class Class
	// Hidden mirrors pflag's bit: the compatibility aliases live here,
	// and they are the part of the surface most likely to rot silently
	// — nothing in help or completion shows them.
	Hidden bool
	// OwnerPath is the command that DECLARES the flag: Path itself for
	// owned and framework pairs, the registering ancestor for
	// inherited ones. Proofs are keyed by the owner, so a proof
	// exercised through any descendant credits the declaration.
	OwnerPath string
}

// Key names the pair the way the gate reports it.
func (p Pair) Key() string { return pairKey(p.Path, p.Flag) }

// OwnerKey names the declaration this pair's proof obligation sits on.
func (p Pair) OwnerKey() string { return pairKey(p.OwnerPath, p.Flag) }

func pairKey(path, flag string) string { return path + " --" + flag }

var (
	enumOnce  sync.Once
	enumPairs []Pair
)

// Enumerate derives the complete pair set from the live tree, cached
// per process (the tree is assembled once and never mutated after the
// walk).
func Enumerate() []Pair {
	enumOnce.Do(func() { enumPairs = enumerate(liveTree()) })
	return enumPairs
}

// enumerate walks one assembled tree. Split from Enumerate so the
// self-tests can run it against fixtures and against a second walk of
// the same tree.
func enumerate(root *cobra.Command) []Pair {
	var pairs []Pair
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		// Materialize cobra's lazily added --help: Execute registers it
		// per command at dispatch time, so a walk that skipped this
		// would enumerate less than the true parse surface.
		cmd.InitDefaultHelpFlag()
		path := cmd.CommandPath()

		inherited := map[string]bool{}
		cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
			inherited[f.Name] = true
			pairs = append(pairs, Pair{
				Path:      path,
				Flag:      f.Name,
				Class:     ClassInherited,
				Hidden:    f.Hidden,
				OwnerPath: ownerOf(cmd, f.Name),
			})
		})

		// Owned = the union of the command's local and its own
		// persistent registrations, minus what an ancestor declared.
		// The union matters: before parse time cobra has not merged
		// persistent flags into Flags(), so reading either set alone
		// under-counts. clibcobra.FlagMeta would miss the raw hidden
		// alias registrations entirely — pflag's VisitAll is the true
		// surface.
		seen := map[string]bool{}
		visitOwned := func(f *pflag.Flag) {
			if inherited[f.Name] || seen[f.Name] {
				return
			}
			seen[f.Name] = true
			class := ClassOwned
			if f.Name == "help" {
				class = ClassFramework
			}
			pairs = append(pairs, Pair{
				Path:      path,
				Flag:      f.Name,
				Class:     class,
				Hidden:    f.Hidden,
				OwnerPath: path,
			})
		}
		cmd.Flags().VisitAll(visitOwned)
		cmd.PersistentFlags().VisitAll(visitOwned)

		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
	slices.SortFunc(pairs, func(a, b Pair) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Flag, b.Flag))
	})
	return pairs
}

// ownerOf resolves which ancestor declares an inherited flag: the
// NEAREST one, matching pflag's shadowing order at parse time.
func ownerOf(cmd *cobra.Command, name string) string {
	for parent := cmd.Parent(); parent != nil; parent = parent.Parent() {
		if parent.PersistentFlags().Lookup(name) != nil {
			return parent.CommandPath()
		}
	}
	// An inherited flag with no declaring ancestor cannot happen in a
	// cobra tree; a nonsense owner key would silently satisfy nothing,
	// so fail loudly at the walk instead.
	panic(fmt.Sprintf("flagmatrix: inherited flag --%s on %q has no declaring ancestor", name, cmd.CommandPath()))
}

// requiredPairs is the gate's demand: every owned pair, plus the
// framework --help once at the root (see ClassFramework).
func requiredPairs(pairs []Pair) []Pair {
	var required []Pair
	for _, p := range pairs {
		if p.Class == ClassOwned || (p.Class == ClassFramework && p.Path == liveTree().CommandPath()) {
			required = append(required, p)
		}
	}
	return required
}
