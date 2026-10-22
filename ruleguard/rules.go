//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// unwrappedBlockingCall catches current Jira API methods called directly with
// a Cobra command's context. Use command.Spin and pass its callback context.
// The method names follow the network operations in core.JiraClient; local
// metadata such as BaseURL is excluded. This syntax check cannot prove that an
// arbitrary context variable came from a spinner, or follow calls through helpers.
// Test files are excluded by .golangci.yml.
func unwrappedBlockingCall(m dsl.Matcher) {
	m.Match(
		`$svc.$method($cmd.Context(), $*_)`,
		`$svc.$method($cmd.Context())`,
	).
		Where(m["method"].Text.Matches(`^(AddAttachment|AddComment|AddRemoteLink|AddWatcher|AddWorklog|CountIssues|CreateIssue|CurrentUser|DeleteAttachment|DeleteComment|DeleteIssue|DeleteIssueLink|DeleteRemoteLink|DoAPI|DoTransition|EditIssue|GetAttachment|GetAttachmentContent|GetBoardConfiguration|GetBoardProjects|GetBoards|GetBulkOperationProgress|GetComments|GetFilter|GetIssue|GetIssueLinkTypes|GetJQLReference|GetRemoteLinks|GetTransitions|GetUser|GetWatchers|GetWorklogs|LinkIssues|ParseJQL|RankIssues|RemoveWatcher|SearchIssues|SearchUsers|SubmitIssueMove|UpdateComment)$`)).
		Report(`blocking Jira call uses a command context directly — wrap it in command.Spin and pass the callback context`)
}

// rawPflagDeclaration catches pflag Var/VarP/P setters without clib metadata.
// Declare flags through command.Add* helpers or semantic bundlers. The command
// package owns those raw setters and is excluded along with tests.
//
// Matching uses method names. Bare String/Int/Bool forms are outside this rule
// because they also name clog builder methods. A passing lint run does not prove
// that every flag has metadata; the assembled-tree guardrails check that contract.
func rawPflagDeclaration(m dsl.Matcher) {
	m.Match(`$fs.$method($*_)`).
		Where(m["method"].Text.Matches(`^(String|Bool|Int|Int8|Int16|Int32|Int64|Uint|Uint8|Uint16|Uint32|Uint64|Float32|Float64|Duration|Count|IP|IPNet|IPMask|BytesHex|BytesBase64|StringSlice|StringArray|IntSlice|Int32Slice|Int64Slice|UintSlice|BoolSlice|Float32Slice|Float64Slice|DurationSlice|IPSlice|StringToString|StringToInt|StringToInt64)(Var)?P$|^(String|Bool|Int|Int8|Int16|Int32|Int64|Uint|Uint8|Uint16|Uint32|Uint64|Float32|Float64|Duration|Count|IP|IPNet|IPMask|BytesHex|BytesBase64|StringSlice|StringArray|IntSlice|Int32Slice|Int64Slice|UintSlice|BoolSlice|Float32Slice|Float64Slice|DurationSlice|IPSlice|StringToString|StringToInt|StringToInt64)Var$|^VarP?$`)).
		Report(`flag declared without clib metadata — use a command register-and-extend helper (command.AddStringVar/AddBoolVar/AddIntVar/AddStringSliceVar/AddStringArrayVar/AddString/AddStringP/AddBool/AddBoolP/AddInt/AddDuration) or a bundler so help and the docs reference get groups, placeholders and allowed values`)
}

// untypedEnvelopeData catches map literals passed to the shared envelope writers.
// Command handlers return registered output types through command.Spec; they do
// not call these writers directly. This narrow check does not follow variables or
// helper return values. Typed construction and schema conformance tests enforce
// the wider contract. The command package and tests are excluded.
func untypedEnvelopeData(m dsl.Matcher) {
	m.Match(
		`command.WriteEnvelope($_, $_, map[string]any{$*_})`,
		`command.WriteEnvelopeWithWarnings($_, $_, map[string]any{$*_}, $*_)`,
	).
		Report(`envelope data built as a map literal — return a registered internal/envelope output type through command.Spec`)
}

// bareRunField catches Cobra's error-discarding Run handler. command.Spec owns
// the RunE adapter that propagates returned errors to the shared failure boundary.
func bareRunField(m dsl.Matcher) {
	m.Match(`cobra.Command{$*_, Run: $_, $*_}`).
		Report(`construct commands through command.Spec — its RunE adapter preserves errors for the shared failure boundary`)
}
