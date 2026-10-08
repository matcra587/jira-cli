package command

import (
	"fmt"
	"slices"
	"strings"

	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/jira"
)

// Issue field-flag names, spelled once so registration, alias resolution,
// composition and every refusal below name the same strings.
//
// This is the design's §2.1 field set and nothing else. What is ABSENT is
// as deliberate as what is here: there is no `--field key=value` and no
// `--custom`, because a generic setter is a worse-ergonomics duplicate of
// `--json-input`, which already reaches every field losslessly (§7). Two
// ways to set a custom field, one of them untyped and unvalidated, is a
// worse surface than one.
const (
	FlagProject           = "project"
	FlagIssueType         = "type"
	FlagSummary           = "summary"
	FlagAssignee          = "assignee"
	FlagUnassign          = "unassign"
	FlagReporter          = "reporter"
	FlagPriority          = "priority"
	FlagParent            = "parent"
	FlagDue               = "due"
	FlagLabel             = "label"
	FlagComponent         = "component"
	FlagFixVersion        = "fix-version"
	FlagAffectsVersion    = "affects-version"
	FlagOriginalEstimate  = "original-estimate"
	FlagRemainingEstimate = "remaining-estimate"

	// The removal half of every repeatable field, edit-only. All four
	// exist, and the symmetry is the point: a surface where a value can be
	// ADDED by flag but only REMOVED by hand-writing JSON is a hole a user
	// falls into rather than a boundary they can see. The design doc
	// authorized only --remove-label; the other three are a deliberate
	// widening past it.
	FlagRemoveLabel          = "remove-label"
	FlagRemoveComponent      = "remove-component"
	FlagRemoveFixVersion     = "remove-fix-version"
	FlagRemoveAffectsVersion = "remove-affects-version"
)

// Jira's own field ids, which are what a payload's `fields` and `update`
// objects are keyed by.
//
// They are NOT the flag names, and several differ in ways nothing but this
// table records: `--type` writes `issuetype`, `--due` writes `duedate`,
// and `--affects-version` writes `versions` while `--fix-version` writes
// `fixVersions` — the one pair whose spellings look interchangeable and
// are not. Keeping them here means the conflict check and the composer
// read the same id, so a payload key can never be checked under one
// spelling and written under another.
//
// Two are exported, each because a COMMAND rather than this registrar
// composes that field. FieldDescription: the body arrives through the
// Markdown route rather than a field flag, so the command writes it and the
// conflict check has to be told about it. FieldSummary: `issue clone
// --replace` rewrites the summary the copy ended up with, which is a
// transformation of a composed request rather than a flag setting a value.
// The rest stay unexported — a command reaching for one of those is
// bypassing the field registrar that owns it.
const (
	FieldDescription = "description"
	FieldSummary     = "summary"

	fieldIssueType       = "issuetype"
	fieldProject         = "project"
	fieldPriority        = "priority"
	fieldAssignee        = "assignee"
	fieldReporter        = "reporter"
	fieldLabels          = "labels"
	fieldComponents      = "components"
	fieldFixVersions     = "fixVersions"
	fieldAffectsVersions = "versions"
	fieldDueDate         = "duedate"
	fieldParent          = "parent"
	fieldTimeTracking    = "timetracking"
)

// IssueWriteVerb says which command is registering the field surface.
//
// An explicit, required parameter rather than a functional option with a
// default, which is where this departs from KeyedOption and MutationOption.
// Those registrars have a correct bare call — a keyed command normally DOES
// want --parallelism, a mutation normally wants only the preview — so an
// option that defaults to the common case is honest there. Here there is no
// common case: the two sets differ in five flags, and a bare call would have
// to silently pick one verb's surface for the other. Making the caller say
// which verb it is turns that into a compile error instead.
type IssueWriteVerb int

const (
	// CreateIssueVerb registers `issue create`'s field surface: the §2.1
	// set less --remaining-estimate (at creation, remaining IS original)
	// and less the two edit-only removal flags.
	CreateIssueVerb IssueWriteVerb = iota + 1
	// EditIssueVerb registers `issue edit`'s: the §2.1 set less --project,
	// which on an existing issue is the PROJECT MOVE rather than a field
	// write, and which the design holds back pending §8 item 1.
	EditIssueVerb
)

// IssueFieldFlags is the shared field surface `issue create` and `issue
// edit` bind, and the third of the design's three input routes (§2.1) —
// the one that exists so `--priority High` needs neither an ADF document
// nor a Markdown file.
//
// One struct for both verbs, with the verb recorded rather than two
// near-identical structs, because the overlap is eleven of sixteen flags
// and the composition rules are the interesting difference. A second
// struct would duplicate the field list and let the two drift in exactly
// the way §9's "the 14 field flags are their own unit" warns about.
type IssueFieldFlags struct {
	Project           string
	IssueType         string
	Summary           string
	Assignee          string
	Reporter          string
	Priority          string
	Parent            string
	Due               string
	OriginalEstimate  string
	RemainingEstimate string

	Labels          []string
	Components      []string
	FixVersions     []string
	AffectsVersions []string

	// The removal halves, populated only on edit — create registers no
	// --remove-* flag, so these stay empty there.
	RemoveLabels          []string
	RemoveComponents      []string
	RemoveFixVersions     []string
	RemoveAffectsVersions []string

	// Unassign clears the assignee. A separate flag rather than a sentinel
	// value for --assignee (jcli spells it `assign KEY x`), because a
	// sentinel is not discoverable: nothing in help, schema or completion
	// can show that one particular string means the opposite of the others.
	Unassign bool

	// verb is which command registered this bundle, recorded by
	// AddIssueFieldFlags so Apply knows whether a repeatable field SETS a
	// list or ADDS to one. Unexported because it is a fact about the
	// registration, not a thing a caller sets afterwards.
	verb IssueWriteVerb
}

// AddIssueFieldFlags registers the §2.1 field surface for one verb.
//
// It panics on an unknown verb for the reason AddKeyedFlags panics on a
// malformed opt-out: this is startup wiring, and a command whose field
// surface is half-declared must never reach a tree.
//
// A caller MUST follow this with ResolveIssueFieldAliases from its
// Spec.Validate. This function runs at construction, before cobra has
// parsed anything, so it can neither fold an alias into its canonical flag
// nor see which flags were given — the same timing constraint AddBodyFlags
// and AddListFlags both document.
func AddIssueFieldFlags(cmd *cobra.Command, f *IssueFieldFlags, verb IssueWriteVerb) {
	switch verb {
	case CreateIssueVerb, EditIssueVerb:
		f.verb = verb
	default:
		panic(fmt.Sprintf("command: %q registers issue field flags for unknown verb %d", cmd.Name(), verb))
	}

	// Shared by both verbs. --type is here rather than in the create-only
	// block because acli carries it on edit too: an issue's type is an
	// ordinary field, and changing Bug to Task is a field write, unlike
	// changing its PROJECT.
	AddStringVar(cmd, &f.Summary, FlagSummary, "",
		"One-line summary",
		FlagOpts{Terse: "Summary", Placeholder: "text"})
	AddStringVar(cmd, &f.IssueType, FlagIssueType, "",
		"Issue type, by name",
		FlagOpts{Terse: "Issue type", Placeholder: "name", Complete: "predictor=cacheissuetype"})
	AddStringVar(cmd, &f.Priority, FlagPriority, "",
		"Priority, by name",
		FlagOpts{Terse: "Priority", Placeholder: "name", Complete: "predictor=cachepriority"})
	AddStringVar(cmd, &f.Assignee, FlagAssignee, "",
		"Assignee, by Atlassian account id"+MeHelp,
		FlagOpts{Terse: "Assignee", Placeholder: "account-id"})
	AddStringVar(cmd, &f.Reporter, FlagReporter, "",
		"Reporter, by Atlassian account id"+MeHelp,
		FlagOpts{Terse: "Reporter", Placeholder: "account-id"})
	AddStringVar(cmd, &f.Parent, FlagParent, "",
		"Parent issue, by key",
		FlagOpts{Terse: "Parent", Placeholder: "KEY-1", Complete: "predictor=issuekey"})
	AddStringVar(cmd, &f.Due, FlagDue, "",
		"Due date, as YYYY-MM-DD",
		FlagOpts{Terse: "Due date", Placeholder: "date"})
	AddStringVar(cmd, &f.OriginalEstimate, FlagOriginalEstimate, "",
		"Original estimate, in Jira's own duration format (`2d 4h`)",
		FlagOpts{Terse: "Original estimate", Placeholder: "duration"})
	// Every repeatable field's ADD half, from the one table that also
	// drives the removal halves below, the conflict checks and the
	// composition — so a fifth repeatable field is one row rather than
	// four edits in four places.
	for _, r := range f.repeatables() {
		opts := FlagOpts{Terse: r.singular, Placeholder: "name"}
		if r.field == fieldLabels {
			opts.Complete = "predictor=cachelabel,comma"
		}
		AddStringSliceVar(cmd, r.add, r.addFlag, nil,
			issueListFlagUsage(verb, r.singular, r.plural), opts)
	}

	switch verb {
	case CreateIssueVerb:
		AddStringVar(cmd, &f.Project, FlagProject, "",
			"Project to create the issue in, by key",
			FlagOpts{Terse: "Project", Placeholder: "KEY", Complete: "predictor=cacheproject"})
	case EditIssueVerb:
		// --remaining-estimate is edit-only because at creation the
		// remaining estimate IS the original one, so offering both on
		// create would be two flags writing one value.
		AddStringVar(cmd, &f.RemainingEstimate, FlagRemainingEstimate, "",
			"Remaining estimate, in Jira's own duration format (`2d 4h`)",
			FlagOpts{Terse: "Remaining estimate", Placeholder: "duration"})
		// The removal halves, edit-only: on create there is nothing to
		// remove from. All four are registered, so every repeatable field
		// can be undone with a flag rather than only by hand-writing a
		// payload — see the FlagRemove* block for why the asymmetry the
		// design left behind was worth closing.
		for _, r := range f.repeatables() {
			opts := FlagOpts{Terse: "Remove " + r.plural, Placeholder: "name"}
			if r.field == fieldLabels {
				opts.Complete = "predictor=cachelabel,comma"
			}
			AddStringSliceVar(cmd, r.remove, r.removeFlag, nil,
				fmt.Sprintf("%s to REMOVE, repeatable", r.singular), opts)
		}
		AddBoolVar(cmd, &f.Unassign, FlagUnassign, false,
			"Clear the assignee, leaving the issue unassigned",
			FlagOpts{Terse: "Unassign"})
	}

	AddHiddenAliases(cmd, issueFieldAliasesFor(verb))
}

// issueListFlagUsage words a repeatable field's help for the verb it is
// registered on, because the SAME flag genuinely does two different things.
//
// On create the list is the issue's whole starting value; on edit it is
// added to whatever is already there. Help that said only "labels" would
// leave a caller on edit expecting a replacement and getting an append,
// which is the surprise this wording exists to prevent — the add/remove/set
// choice is argued at Apply, and this is where a caller meets it.
func issueListFlagUsage(verb IssueWriteVerb, singular, plural string) string {
	if verb == CreateIssueVerb {
		return singular + " to set, repeatable"
	}
	return fmt.Sprintf("%s to ADD, repeatable — it does not replace the existing %s", singular, plural)
}

// IssueFieldAliases is the ONE table registration and resolution both
// read, so neither half can drift from the other — BodyFlagAliases' own
// shape and reasoning.
//
// Short, because §5's field-flag rows are short, and §7 records why the
// obvious additions are absent: `--components` is aliased by nothing
// because no tool spells it plural, and only `--labels` has a genuine
// plural precedent (acli edit). A "plural tolerance" rule that invented the
// rest was drafted and cut.
var IssueFieldAliases = []HiddenAlias{
	{Flag: "labels", Canonical: FlagLabel},
	{Flag: "remove-labels", Canonical: FlagRemoveLabel},
	{Flag: "remove-assignee", Canonical: FlagUnassign},
}

// issueFieldAliasesFor narrows the table to the aliases whose canonical
// flag this verb actually registered. Registering `--remove-labels` on
// create would offer an alias for a flag that is not there, and folding it
// in would then fail looking the canonical one up.
func issueFieldAliasesFor(verb IssueWriteVerb) []HiddenAlias {
	if verb == EditIssueVerb {
		return IssueFieldAliases
	}
	return xslices.Filter(IssueFieldAliases, func(a HiddenAlias) bool {
		return a.Canonical == FlagLabel
	})
}

// ResolveIssueFieldAliases folds every hidden field alias into its
// canonical flag, through the shared resolver (ResolveHiddenAliases,
// aliases.go) that every alias table in this CLI now goes through.
//
// It takes the DEFAULT refusal wording, which is §2's "errors never name
// an alias" rule: --label and --unassign are what help, the schema and
// completions advertise, so a message naming --labels would send an
// introspecting agent hunting a flag it was never told about.
//
// Must run AFTER cobra has parsed, so a command's Spec.Validate is where
// this call belongs.
func (f *IssueFieldFlags) ResolveIssueFieldAliases(fs *pflag.FlagSet) error {
	return ResolveHiddenAliases(fs, issueFieldAliasesFor(f.verb))
}

// Given reports whether any field flag was set at all. `edit` asks this to
// refuse an invocation that would send Jira a request changing nothing.
func (f *IssueFieldFlags) Given() bool {
	return len(f.givenFlags()) > 0
}

// givenFlags returns the field flags actually set, in a FIXED order.
//
// Fixed rather than map order, because this drives error messages: a
// refusal that listed the colliding flags in a different order on each run
// of the same command reads as a different error each time.
func (f *IssueFieldFlags) givenFlags() []string {
	var given []string
	for _, b := range f.bindings() {
		if b.given {
			given = append(given, b.flag)
		}
	}
	return given
}

// binding ties one flag to the Jira field id it writes and whether it was
// given. The single table Conflict, Given and the refusals all read, so a
// flag added to this bundle cannot be composed but left unchecked.
type binding struct {
	flag  string
	field string
	given bool
}

func (f *IssueFieldFlags) bindings() []binding {
	all := []binding{
		{FlagSummary, FieldSummary, f.Summary != ""},
		{FlagProject, fieldProject, f.Project != ""},
		{FlagIssueType, fieldIssueType, f.IssueType != ""},
		{FlagPriority, fieldPriority, f.Priority != ""},
		{FlagAssignee, fieldAssignee, f.Assignee != ""},
		{FlagUnassign, fieldAssignee, f.Unassign},
		{FlagReporter, fieldReporter, f.Reporter != ""},
		{FlagParent, fieldParent, f.Parent != ""},
		{FlagDue, fieldDueDate, f.Due != ""},
		{FlagOriginalEstimate, fieldTimeTracking, f.OriginalEstimate != ""},
		{FlagRemainingEstimate, fieldTimeTracking, f.RemainingEstimate != ""},
	}
	// The repeatable pairs come from the ONE table that also drives their
	// composition and their refusals, so a field added there is checked
	// here without anybody remembering to add it twice.
	for _, r := range f.repeatables() {
		all = append(all,
			binding{r.addFlag, r.field, len(r.adds()) > 0},
			binding{r.removeFlag, r.field, len(r.removes()) > 0})
	}
	return all
}

// repeatable is one add/remove flag pair and the Jira field both write.
//
// The single table behind every repeatable field's registration, conflict
// check and composition. Four fields with identical mechanics were four
// copies of the same twenty lines before the removal flags landed; adding
// three more copies is how the fifth field would have got a subtly
// different rule.
type repeatable struct {
	field      string
	addFlag    string
	removeFlag string
	// add and remove POINT AT the bundle's slices rather than copying
	// them, so this one table can drive registration (which needs a
	// pointer to bind) as well as the checks and the composition (which
	// need the parsed values). A value-copying table would have forced a
	// second, pointer-bearing one beside it, and two tables listing the
	// same four fields is precisely the drift this type exists to stop.
	add    *[]string
	remove *[]string
	// bare marks an operand Jira takes as a plain string rather than a
	// {"name": …} object. Labels are the only one, and the asymmetry is
	// Jira's rather than a choice made here.
	bare bool
	// singular and plural word this pair's help text.
	singular string
	plural   string
}

func (f *IssueFieldFlags) repeatables() []repeatable {
	return []repeatable{
		{
			field: fieldLabels, addFlag: FlagLabel, removeFlag: FlagRemoveLabel,
			add: &f.Labels, remove: &f.RemoveLabels, bare: true,
			singular: "Label", plural: "labels",
		},
		{
			field: fieldComponents, addFlag: FlagComponent, removeFlag: FlagRemoveComponent,
			add: &f.Components, remove: &f.RemoveComponents,
			singular: "Component", plural: "components",
		},
		{
			field: fieldFixVersions, addFlag: FlagFixVersion, removeFlag: FlagRemoveFixVersion,
			add: &f.FixVersions, remove: &f.RemoveFixVersions,
			singular: "Fix version", plural: "fix versions",
		},
		{
			field: fieldAffectsVersions, addFlag: FlagAffectsVersion, removeFlag: FlagRemoveAffectsVersion,
			add: &f.AffectsVersions, remove: &f.RemoveAffectsVersions,
			singular: "Affects version", plural: "affects versions",
		},
	}
}

// adds and removes read the bound slices through the table's pointers.
func (r repeatable) adds() []string    { return *r.add }
func (r repeatable) removes() []string { return *r.remove }

// operand wraps one value in the shape its field takes on the wire.
func (r repeatable) operand(value string) any {
	if r.bare {
		return value
	}
	return jira.NameRef{Name: value}
}

// Conflict enforces the refusals that are internal to this bundle: two
// flags that would write contradictory things into one Jira field.
//
// Run from Spec.Validate, AFTER ResolveIssueFieldAliases, so a value that
// arrived through an alias is visible here — otherwise
// `--remove-assignee --assignee X` would slip past a check that only sees
// the canonical flags, which is the same ordering BodyFlags documents.
func (f *IssueFieldFlags) Conflict() error {
	if f.Assignee != "" && f.Unassign {
		e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
			"--%s names who to assign the issue to and --%s clears the assignee — pick one and drop the other",
			FlagAssignee, FlagUnassign,
		))
		e.Field = FlagAssignee
		return e
	}
	// A value named in BOTH halves of one pair is a request to add and
	// remove it in a single operation list, whose outcome depends on the
	// order Jira applies the verbs in — which the API does not promise.
	// Refusing is the only answer that cannot be silently wrong, and it
	// applies to every repeatable field rather than to labels alone.
	for _, r := range f.repeatables() {
		both := xslices.Filter(r.adds(), func(v string) bool {
			return slices.Contains(r.removes(), v)
		})
		if len(both) == 0 {
			continue
		}
		slices.Sort(both)
		e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
			"--%s and --%s both name %s, so the result would depend on which Jira applied last — drop it from one of them",
			r.addFlag, r.removeFlag, quoteAll(both),
		))
		e.Field = r.addFlag
		return e
	}
	return nil
}

// ConflictWithPayload enforces §2.1's cross-route rule: a field flag
// beside `--json-input` is fine until the two collide.
//
// **It checks BOTH halves of the payload**, which is the part that is easy
// to get wrong and expensive to get wrong quietly. jira.IssueWriteRequest.
// Keys reads `fields` and `update` together for exactly this caller: a
// field set as `update.priority[{"set":"High"}]` is every bit as real as
// one in `fields`, so a `fields`-only check would let it sit beside
// `--priority` and produce a request whose meaning depends on which half
// Jira applies last.
//
// **The DESCRIPTION is deliberately not checked here**, and its absence is
// reasoned rather than forgotten. The description arrives through the
// Markdown route, and §2.1 refuses `--json-input` beside `--markdown`
// OUTRIGHT — a blanket refusal, not a key-level one, because the
// description can appear as `fields.description` or as
// `update.description` and deciding a winner between them is exactly what
// the blanket rule exists to avoid. So by the time a payload could be
// inspected for a description collision, the combination that would cause
// one has already been refused. A check here would be unreachable, and an
// unreachable guard is worse than none: it reads as protection nobody can
// test.
func (f *IssueFieldFlags) ConflictWithPayload(payload jira.IssueWriteRequest) error {
	inPayload := payload.Keys()
	if len(inPayload) == 0 {
		return nil
	}
	for _, b := range f.bindings() {
		if b.given && slices.Contains(inPayload, b.field) {
			return payloadCollision(b.flag, b.field)
		}
	}
	return nil
}

// payloadCollision is the one refusal both halves of the check above
// raise. CodeFlagConflict rather than CodeFlagValueInvalid: neither input
// is wrong on its own, and the fix is to drop one of them.
func payloadCollision(flag, field string) error {
	e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
		"--%s and --%s both set %q — the flag and the payload disagree about one field, so drop whichever you did not mean",
		flag, FlagJSONInput, field,
	))
	e.Field = flag
	return e
}

// quoteAll renders a value list for one sentence of prose.
func quoteAll(values []string) string {
	return strings.Join(xslices.Map(values, func(v string) string {
		return fmt.Sprintf("%q", v)
	}), ", ")
}

// Apply folds every given field flag into req.
//
// **The add-versus-set decision for repeatable fields lives here, and it
// differs by verb.** On CREATE there is nothing to add to, so a list is
// simply the field's starting value and rides `fields`. On EDIT a list
// ADDS, through Jira's `update` verbs, and that is the least surprising
// reading rather than an arbitrary pick:
//
//   - The design pairs --label with a separate --remove-label. If --label
//     replaced the whole list, --remove-label could not exist as a
//     meaningful flag — a caller replacing the list would simply omit what
//     they wanted gone. A removal flag is only coherent alongside an
//     additive one.
//   - The design's own conflict rule refuses "--label with --remove-label
//     naming the same value". Naming the same value is only a CONFLICT if
//     the two are parallel add and remove sets; under set-semantics it
//     would be merely redundant.
//   - Replacing is the destructive reading. A caller who types
//     `--label urgent` on an issue that already has three labels and finds
//     the other two deleted has lost data the CLI never showed them;
//     adding a label they did not want is trivially undone.
//
// The whole-list REPLACEMENT that `update`'s third verb offers is
// deliberately not on a flag, and the omission is honest rather than an
// oversight: the design authorizes one removal flag (--remove-label) and
// no set-flags at all, so replacing a component list goes through
// `--json-input`, which is the route §2.1 designates for everything the
// scalar flags do not reach. Inventing --set-component here would widen
// the surface past what the design settled.
func (f *IssueFieldFlags) Apply(req *jira.IssueWriteRequest) error {
	if err := f.applyScalars(req); err != nil {
		return err
	}
	if f.verb == CreateIssueVerb {
		return f.applyListsAsFields(req)
	}
	return f.applyListsAsOps(req)
}

// applyScalars writes the single-valued fields, which ride `fields` on
// BOTH verbs: setting a summary is the same request whether the issue is
// being made or changed.
func (f *IssueFieldFlags) applyScalars(req *jira.IssueWriteRequest) error {
	set := func(field string, value any) error { return req.SetField(field, value) }

	if f.Summary != "" {
		if err := set(FieldSummary, f.Summary); err != nil {
			return err
		}
	}
	if f.Project != "" {
		if err := set(fieldProject, jira.KeyRef{Key: f.Project}); err != nil {
			return err
		}
	}
	if f.IssueType != "" {
		if err := set(fieldIssueType, jira.NameRef{Name: f.IssueType}); err != nil {
			return err
		}
	}
	if f.Priority != "" {
		if err := set(fieldPriority, jira.NameRef{Name: f.Priority}); err != nil {
			return err
		}
	}
	if f.Assignee != "" {
		if err := set(fieldAssignee, jira.AccountIDRef{AccountID: f.Assignee}); err != nil {
			return err
		}
	}
	if f.Unassign {
		// An explicit JSON null, which is Jira's documented way to clear an
		// assignee. An ABSENT assignee means "do not touch it", so this is
		// the one place the difference between absent and null is the whole
		// point of the request.
		if err := set(fieldAssignee, nil); err != nil {
			return err
		}
	}
	if f.Reporter != "" {
		if err := set(fieldReporter, jira.AccountIDRef{AccountID: f.Reporter}); err != nil {
			return err
		}
	}
	if f.Parent != "" {
		if err := set(fieldParent, jira.KeyRef{Key: f.Parent}); err != nil {
			return err
		}
	}
	if f.Due != "" {
		if err := set(fieldDueDate, f.Due); err != nil {
			return err
		}
	}
	// ONE key for two flags. There is no top-level fields.originalEstimate
	// to write to, so both nest inside `timetracking` — and composing them
	// in a single call is what stops the second flag's SetField from
	// overwriting the first flag's whole object.
	if f.OriginalEstimate != "" || f.RemainingEstimate != "" {
		if err := set(fieldTimeTracking, jira.TimeTracking{
			OriginalEstimate:  f.OriginalEstimate,
			RemainingEstimate: f.RemainingEstimate,
		}); err != nil {
			return err
		}
	}
	return nil
}

// applyListsAsFields writes the repeatable fields as plain values, which is
// create's shape: the list IS the field.
//
// The removal halves are unreachable here — AddIssueFieldFlags registers no
// --remove-* flag on create, because there is nothing yet to remove from —
// so this writes r.adds() and never consults r.removes().
func (f *IssueFieldFlags) applyListsAsFields(req *jira.IssueWriteRequest) error {
	for _, r := range f.repeatables() {
		if len(r.adds()) == 0 {
			continue
		}
		operands := xslices.Map(r.adds(), r.operand)
		if err := req.SetField(r.field, operands); err != nil {
			return err
		}
	}
	return nil
}

// applyListsAsOps writes the repeatable fields as `update` operations,
// which is edit's shape — see Apply for why adding rather than replacing is
// the least surprising reading.
//
// Adds come before removes in each field's operation list. The order is
// observable (Jira applies the verbs in sequence) but cannot change the
// outcome here, because Conflict has already refused any value appearing in
// both halves — which is precisely why that refusal exists rather than a
// documented precedence rule.
func (f *IssueFieldFlags) applyListsAsOps(req *jira.IssueWriteRequest) error {
	for _, r := range f.repeatables() {
		if len(r.adds()) == 0 && len(r.removes()) == 0 {
			continue
		}
		ops := make([]jira.FieldOp, 0, len(r.adds())+len(r.removes()))
		for _, v := range r.adds() {
			ops = append(ops, jira.FieldOp{Add: r.operand(v)})
		}
		for _, v := range r.removes() {
			ops = append(ops, jira.FieldOp{Remove: r.operand(v)})
		}
		if err := req.SetOps(r.field, ops); err != nil {
			return err
		}
	}
	return nil
}
