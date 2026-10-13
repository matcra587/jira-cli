//go:build outputaudit

package audit

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/root"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// Member is one row of the exact full-leaf inventory: a command path
// joined to one operation declaration. A path declaring variants yields
// one member per variant, so every reachable op is a first-class row.
type Member struct {
	Path    string
	Meta    command.Meta
	Aliases []string
	Hidden  bool
}

// Key identifies the member as "path|variant".
func (m Member) Key() string {
	return m.Path + "|" + m.Meta.Variant
}

// frameworkSurfaces is the bounded table of cobra/conductor-owned
// leaves that legitimately carry no operation metadata: they emit no
// envelope and no op. Every entry must match a live surface (stale
// entries fail) and must NOT carry metadata (a surface we author leaves
// this table). Cobra's default completion command never materializes —
// clib's completion wiring disables it and our authored `completion`
// command (a real inventory member) replaces it.
var frameworkSurfaces = map[string]string{
	"jira help": "cobra's built-in help command, materialized once the root grew subcommands",
}

// sources carries the join inputs, separated from live collection so
// every reconciliation failure branch is directly testable with a
// synthetic fixture (the R9 self-test). schemas maps op → registered
// Output sample; the sample stays reflectable so the identity-slot rule
// is checked against the TYPE, independent of the derived schema. The
// verb and docent-guide registries do not exist yet; they join here as
// further fields with their own reconciliation branches when they land.
type sources struct {
	tree      *cobra.Command
	schemas   map[string]any
	recipes   []Scenario
	framework map[string]string
}

var (
	liveOnce sync.Once
	liveRoot *cobra.Command
)

// liveTree assembles the production program once per test process and
// returns its root. The deps are inert on purpose: the inventory must
// never construct a client, and auto-detection must not probe the test
// environment.
func liveTree() *cobra.Command {
	liveOnce.Do(func() {
		deps := &root.Deps{
			JiraClient: func(ctx context.Context) (core.JiraClient, error) {
				return nil, errors.New("outputaudit: the inventory never constructs a jira client")
			},
			Detect: func() cli.Detection {
				return cli.Detection{Format: output.FormatJSON}
			},
		}
		program := root.NewProgram(deps, io.Discard, io.Discard)
		// Materialize cobra's lazily added built-ins so hidden and
		// framework surfaces are walked, not silently skipped — the 0.15
		// walker's exclusion is the weakness this rebuild removes.
		program.Root.InitDefaultHelpCmd()
		program.Root.InitDefaultCompletionCmd()
		liveRoot = program.Root
	})
	return liveRoot
}

// liveSources joins the real tree with the real registries and the
// committed scenario recipes.
func liveSources() sources {
	schemas := map[string]any{}
	for _, reg := range envelope.Snapshot() {
		schemas[reg.Op] = reg.Sample
	}
	return sources{tree: liveTree(), schemas: schemas, recipes: scenarios, framework: frameworkSurfaces}
}

// liveMembers reconciles the live sources.
func liveMembers() ([]Member, []string) {
	return reconcile(liveSources())
}

// reconcile is the pure join over the collected sources — the anti-gap
// core. Every problem NAMES the member (or table entry) so a red run
// points at the gap, never at the harness. The walk includes hidden
// commands: a compat alias or hidden surface is contract surface too.
func reconcile(src sources) (members []Member, problems []string) {
	claimed := map[string]bool{}
	frameworkSeen := map[string]bool{}
	// One command per (op, variant): a second claimant — hidden or not —
	// would ride the first's recipes and captures without ever being
	// executed itself. Genuine delegation gets its own op and a recipe
	// proving the delegation, never a borrowed identity.
	opOwner := map[string]string{}

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		path := cmd.CommandPath()
		if strings.TrimSpace(cmd.Short) == "" {
			// Help standard §1.12: an empty Short silently drops the
			// command from grouped help, so Short presence is a gate.
			problems = append(problems, fmt.Sprintf("command %q has an empty Short — it vanishes from grouped help (help standard §1.12)", path))
		}
		kids := cmd.Commands()
		// A parent with its own RunE is a runnable surface as well as a
		// group; both roles are inventoried.
		// Run (legacy) counts the same as RunE: a parent command executing
		// via the deprecated field must not slip past the inventory.
		if len(kids) == 0 || cmd.RunE != nil || cmd.Run != nil {
			switch _, framework := src.framework[path]; {
			case framework:
				frameworkSeen[path] = true
				if _, hasMeta := command.MetaFor(cmd); hasMeta {
					problems = append(problems, fmt.Sprintf("framework surface %q carries operation metadata — an authored command does not belong in the framework table", path))
				}
			default:
				metas, ok := command.MetaFor(cmd)
				if !ok {
					problems = append(problems, fmt.Sprintf("leaf %q has no operation metadata — declare it with command.SetMeta or add it to the framework table", path))
					break
				}
				for _, m := range metas {
					opKey := m.Op + "|" + m.Variant
					if owner, dup := opOwner[opKey]; dup && owner != path {
						problems = append(problems, fmt.Sprintf("command %q claims op %q variant %q already owned by %q — a same-op command would be 'covered' by recipes that never execute it", path, m.Op, m.Variant, owner))
					} else {
						opOwner[opKey] = path
					}
					member := Member{Path: path, Meta: m, Aliases: cmd.Aliases, Hidden: cmd.Hidden}
					members = append(members, member)
					claimed[m.Op] = true
					problems = append(problems, memberProblems(member, src)...)
				}
			}
		}
		for _, kid := range kids {
			walk(kid)
		}
	}
	walk(src.tree)

	// Reverse joins: a registered op no command emits and a framework
	// entry no surface matches are as red as a missing registration.
	for op := range src.schemas {
		if !claimed[op] {
			problems = append(problems, fmt.Sprintf("envelope-registered op %q is emitted by no command — dead registration", op))
		}
	}
	for path := range src.framework {
		if !frameworkSeen[path] {
			problems = append(problems, fmt.Sprintf("framework table names %q but no such surface exists — stale entry", path))
		}
	}
	problems = append(problems, recipeProblems(src.tree, src.recipes, members)...)

	slices.SortFunc(members, func(a, b Member) int { return cmp.Compare(a.Key(), b.Key()) })
	slices.Sort(problems)
	return members, problems
}

// memberProblems checks one member against the registries and the
// applicability map.
func memberProblems(member Member, src sources) []string {
	var problems []string
	m := member.Meta
	if len(applicabilityFor(m).Applies) == 0 {
		problems = append(problems, fmt.Sprintf("member %s (op %q) has no applicable axes — the applicability map does not cover it", member.Key(), m.Op))
	}
	sample, registered := src.schemas[m.Op]
	switch {
	case m.NoSuccessEnvelope == "" && !registered:
		problems = append(problems, fmt.Sprintf("member %s emits op %q with no envelope schema registration (envelope.Register beside the Output struct)", member.Key(), m.Op))
	case m.NoSuccessEnvelope != "" && registered:
		problems = append(problems, fmt.Sprintf("member %s declares no success envelope for op %q yet a schema is registered — the declaration and the registry disagree", member.Key(), m.Op))
	case registered:
		// The Ref marker's consumer: the Identity declaration is
		// cross-checked against the registered TYPE in both directions,
		// so a bare-scalar identity cannot hide behind an axis marked
		// not-applicable, and an undeclared ref slot cannot dodge M1.
		hasRef := hasRefSlot(reflect.TypeOf(sample))
		if m.Identity && !hasRef {
			problems = append(problems, fmt.Sprintf("member %s declares an identity slot but op %q's Output struct carries no envelope.Ref field — a bare-scalar identity violates M1", member.Key(), m.Op))
		}
		if !m.Identity && hasRef {
			problems = append(problems, fmt.Sprintf("member %s's Output struct for op %q carries an envelope.Ref field but the metadata does not declare Identity — M1 would silently go unchecked", member.Key(), m.Op))
		}
	}
	if !slices.ContainsFunc(src.recipes, func(s Scenario) bool {
		return s.Op == m.Op && s.Variant == m.Variant
	}) {
		problems = append(problems, fmt.Sprintf("member %s (op %q) has no corpus scenario recipe — its contract is unexecuted", member.Key(), m.Op))
	}
	return problems
}

// recipeProblems validates the recipe table against the reconciled
// member set: an orphaned recipe, a duplicate id, an unknown stub, or a
// category the member's metadata does not demand are all the
// wrong-label class of gap (a mislabeled scenario silently skips its
// checks — design §10).
func recipeProblems(tree *cobra.Command, recipes []Scenario, members []Member) []string {
	var problems []string
	seen := map[string]bool{}
	seenTriple := map[string]string{}
	for _, s := range recipes {
		if seen[s.ID] {
			problems = append(problems, fmt.Sprintf("scenario %q declared twice", s.ID))
		}
		seen[s.ID] = true
		// The capture join scans recipes first-match by (op, variant,
		// category), so a second recipe on the same triple would run but
		// never be consulted by any axis check — a silently-dead check.
		triple := s.Op + "\x00" + s.Variant + "\x00" + string(s.Category)
		if prev, dup := seenTriple[triple]; dup {
			problems = append(problems, fmt.Sprintf("scenario %q duplicates op %q variant %q category %q already covered by %q — the second capture would be silently unused", s.ID, s.Op, s.Variant, s.Category, prev))
		} else {
			seenTriple[triple] = s.ID
		}
		if s.Stub != "" {
			if _, ok := stubServers[s.Stub]; !ok {
				problems = append(problems, fmt.Sprintf("scenario %q references unknown stub %q", s.ID, s.Stub))
			}
		}
		member, ok := memberFor(members, s.Op, s.Variant)
		if !ok {
			problems = append(problems, fmt.Sprintf("scenario %q targets op %q variant %q, which no inventory member emits — orphaned recipe", s.ID, s.Op, s.Variant))
			continue
		}
		// A recipe must actually INVOKE the command it claims to cover:
		// resolve its args against the live tree and compare paths, so an
		// op string can never be satisfied by a capture of something else.
		if resolved, resolveErr := resolveArgsPath(tree, s.Args); resolveErr != nil {
			problems = append(problems, fmt.Sprintf("scenario %q args %v do not resolve against the command tree: %v", s.ID, s.Args, resolveErr))
		} else if resolved != member.Path {
			problems = append(problems, fmt.Sprintf("scenario %q claims op %q (command %q) but its args invoke %q — the capture never executes the claimed command", s.ID, s.Op, member.Path, resolved))
		} else if member.Meta.Variant != "" && !argsSelectVariant(s.Args, member.Meta.Variant) {
			// The path check cannot see flags (cobra Find strips them), so
			// a variant sharing its command's path needs its selecting
			// token verified in the argv — else the variant's cells record
			// against a capture of the default form.
			problems = append(problems, fmt.Sprintf("scenario %q claims variant %q of op %q but its args %v never pass that token — the capture executes the default form", s.ID, member.Meta.Variant, s.Op, s.Args))
		}
		if !slices.Contains(categoriesFor(member.Meta), s.Category) {
			problems = append(problems, fmt.Sprintf("scenario %q labels itself %q, a category op %q's metadata does not demand — a wrong label silently skips checks", s.ID, s.Category, s.Op))
		}
	}
	return problems
}

// memberFor resolves the member emitting op+variant. Paths can share an
// op (a shorthand command delegating to another's operation), so the
// match is by op+variant, not path.
func memberFor(members []Member, op, variant string) (Member, bool) {
	for _, member := range members {
		if member.Meta.Op == op && member.Meta.Variant == variant {
			return member, true
		}
	}
	return Member{}, false
}

// refType is the envelope.Ref marker interface, resolved once for the
// reflection walk.
var refType = reflect.TypeOf((*envelope.Ref)(nil)).Elem()

// hasRefSlot reports whether t (a registered Output struct) carries any
// field — at any depth, through embeds, pointers, and slices — whose
// type implements envelope.Ref.
func hasRefSlot(t reflect.Type) bool {
	return hasRefSlotVisited(t, map[reflect.Type]bool{})
}

func hasRefSlotVisited(t reflect.Type, visited map[reflect.Type]bool) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice ||
		t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	// The unwrapped type may itself be the ref ([]IssueRef, map[string]IssueRef)
	// — test it directly before demanding a struct to walk.
	if t.Implements(refType) || reflect.PointerTo(t).Implements(refType) {
		return true
	}
	if t.Kind() != reflect.Struct || visited[t] {
		return false
	}
	visited[t] = true
	for f := range t.Fields() {
		if f.Type.Implements(refType) {
			return true
		}
		if hasRefSlotVisited(f.Type, visited) {
			return true
		}
	}
	return false
}

// resolveArgsPath walks the tree the way cobra dispatch would for the
// given args and returns the command path that would execute — flags fall
// away, the first non-flag tokens select subcommands.
func resolveArgsPath(root *cobra.Command, args []string) (string, error) {
	cmd, _, err := root.Find(args)
	if err != nil {
		return "", err
	}
	return cmd.CommandPath(), nil
}

// argsSelectVariant reports whether args pass the variant-selecting token
// — bare ("--count") or with a value ("--count=5").
func argsSelectVariant(args []string, variant string) bool {
	for _, a := range args {
		if a == variant || strings.HasPrefix(a, variant+"=") {
			return true
		}
	}
	return false
}
