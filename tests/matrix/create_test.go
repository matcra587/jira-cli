//go:build flagmatrix

package matrix

// `jira issue create`'s 27 owned pairs. The command's whole surface is
// request COMPOSITION, and --dry-run publishes the composed request at
// data.payload without contacting Jira — so most proofs here are "the
// payload carries this key with this value", which is an assertion that
// fails when the flag is ignored (the key is simply absent). The live
// pairs (--verify, the live half of --dry-run's contrast) assert what
// the stub recorded instead.

import (
	"strings"
	"testing"
)

// fwCreateArgs is the smallest valid create argv, which every case
// extends. The required trio doubles as three provable pairs of its own:
// each value surfaces in the payload under Jira's spelling.
func fwCreateArgs(extra ...string) []string {
	args := []string{"issue", "create", "--project", "KEY", "--type", "Task", "--summary", "Base summary"}
	return append(args, extra...)
}

// TestCreateFieldFlagsComposePayload proves the scalar and repeatable
// field flags on create: each one composes its Jira field into the
// dry-run payload, under Jira's own id — which for four of them is NOT
// the flag's name (--type → issuetype, --due → duedate,
// --affects-version → versions, --fix-version → fixVersions).
func TestCreateFieldFlagsComposePayload(t *testing.T) {
	t.Parallel()
	cases := []struct {
		flag     string
		extra    []string
		path     []any
		want     any
		evidence string
	}{
		{
			"project", nil,
			[]any{"fields", "project", "key"},
			"KEY",
			"dry-run payload.fields.project.key == \"KEY\", exactly the --project value",
		},
		{
			"type", nil,
			[]any{"fields", "issuetype", "name"},
			"Task",
			"dry-run payload carries --type under Jira's id: fields.issuetype.name == \"Task\"",
		},
		{
			"summary", nil,
			[]any{"fields", "summary"},
			"Base summary",
			"dry-run payload.fields.summary == the --summary value",
		},
		{
			"priority",
			[]string{"--priority", "High"},
			[]any{"fields", "priority", "name"},
			"High",
			"dry-run payload.fields.priority.name == \"High\"",
		},
		{
			"reporter",
			[]string{"--reporter", fwAccountID},
			[]any{"fields", "reporter", "accountId"},
			fwAccountID,
			"dry-run payload.fields.reporter.accountId == the given account id",
		},
		{
			"parent",
			[]string{"--parent", "KEY-1"},
			[]any{"fields", "parent", "key"},
			"KEY-1",
			"dry-run payload.fields.parent.key == \"KEY-1\"",
		},
		{
			"due",
			[]string{"--due", "2026-09-01"},
			[]any{"fields", "duedate"},
			"2026-09-01",
			"dry-run payload carries --due under Jira's id: fields.duedate == \"2026-09-01\"",
		},
		{
			"original-estimate",
			[]string{"--original-estimate", "2d 4h"},
			[]any{"fields", "timetracking", "originalEstimate"},
			"2d 4h",
			"dry-run payload.fields.timetracking.originalEstimate == \"2d 4h\"",
		},
		{
			"label",
			[]string{"--label", "security", "--label", "ops"},
			[]any{"fields", "labels", 1},
			"ops",
			"on CREATE --label SETS: payload.fields.labels == [security ops] as plain values, no update ops",
		},
		{
			"component",
			[]string{"--component", "api"},
			[]any{"fields", "components", 0, "name"},
			"api",
			"dry-run payload.fields.components[0].name == \"api\"",
		},
		{
			"fix-version",
			[]string{"--fix-version", "2026.9"},
			[]any{"fields", "fixVersions", 0, "name"},
			"2026.9",
			"dry-run payload.fields.fixVersions[0].name == \"2026.9\"",
		},
		{
			"affects-version",
			[]string{"--affects-version", "2026.8"},
			[]any{"fields", "versions", 0, "name"},
			"2026.8",
			"dry-run payload writes --affects-version to fields.versions (NOT fixVersions): versions[0].name == \"2026.8\"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.flag, func(t *testing.T) {
			t.Parallel()
			stub := fwNewStub(t)
			res := fwExec(t, stub.url(), nil, "", append(fwCreateArgs(tc.extra...), "--dry-run")...)
			if res.Exit != 0 {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", res.Exit, res.Stdout, res.Stderr)
			}
			env := fwParse(t, res)
			if got := fwAt(t, fwPayload(t, env), tc.path...); got != tc.want {
				t.Fatalf("payload at %v = %#v, want %#v", tc.path, got, tc.want)
			}
			Prove(t, fwCreatePath, tc.flag, tc.evidence)
		})
	}
}

// TestCreateAssigneeMeStaysLiteralInPreview proves --assignee, on the
// §2.1 sentinel edge: a bare preview must leave @me UNRESOLVED in the
// payload and warn, because resolving it is a site read a dry run never
// makes.
func TestCreateAssigneeMeStaysLiteralInPreview(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	res := fwExec(t, stub.url(), nil, "", append(fwCreateArgs("--assignee", "@me"), "--dry-run")...)
	env := fwParse(t, res)
	if got := fwAt(t, fwPayload(t, env), "fields", "assignee", "accountId"); got != "@me" {
		t.Fatalf("fields.assignee.accountId = %#v, want the literal \"@me\"", got)
	}
	if !fwHasWarning(env, "account_id_unresolved") {
		t.Fatalf("bare preview with @me raised no account_id_unresolved warning\nstdout: %s", res.Stdout)
	}
	if len(stub.requests()) != 0 {
		t.Fatalf("bare preview contacted the stub: %v", stub.requests())
	}
	Prove(t, fwCreatePath, "assignee",
		"payload.fields.assignee.accountId stays the literal \"@me\" under bare --dry-run, with the account_id_unresolved warning and zero stub requests")
}

// TestCreateValidateRemoteResolvesMe proves --validate-remote: it is the
// one thing that makes a preview read the site, and the payload shows
// the difference — @me becomes the account id /myself answered.
func TestCreateValidateRemoteResolvesMe(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	res := fwExec(t, stub.url(), nil, "", append(fwCreateArgs("--assignee", "@me"), "--dry-run", "--validate-remote")...)
	env := fwParse(t, res)
	if got := fwAt(t, fwPayload(t, env), "fields", "assignee", "accountId"); got != fwAccountID {
		t.Fatalf("fields.assignee.accountId = %#v, want the resolved %q", got, fwAccountID)
	}
	if fwHasWarning(env, "account_id_unresolved") {
		t.Fatalf("resolved preview still warns account_id_unresolved")
	}
	if len(stub.find("GET", "/rest/api/3/myself")) == 0 {
		t.Fatalf("no GET /myself recorded; requests: %v", stub.requests())
	}
	Prove(t, fwCreatePath, "validate-remote",
		"--dry-run --validate-remote sent GET /myself and the payload carries the RESOLVED account id where bare --dry-run keeps the @me literal")
}

// TestCreateMarkdownRoutes proves the Markdown route's two flags: the
// text lands in the payload as an ADF description, and the envelope
// names the route that produced it.
func TestCreateMarkdownRoutes(t *testing.T) {
	t.Parallel()
	t.Run("markdown", func(t *testing.T) {
		t.Parallel()
		stub := fwNewStub(t)
		res := fwExec(t, stub.url(), nil, "", append(fwCreateArgs("--markdown", "Reproduced on staging."), "--dry-run")...)
		env := fwParse(t, res)
		fwAssertDescription(t, env, "Reproduced on staging.")
		Prove(t, fwCreatePath, "markdown",
			"dry-run payload.fields.description is an ADF doc carrying the --markdown text, description_route == \"markdown\"")
	})
	t.Run("markdown-file", func(t *testing.T) {
		t.Parallel()
		stub := fwNewStub(t)
		res := fwExec(t, stub.url(), map[string]string{"notes.md": "From the file route."}, "",
			append(fwCreateArgs("--markdown-file", "notes.md"), "--dry-run")...)
		env := fwParse(t, res)
		fwAssertDescription(t, env, "From the file route.")
		Prove(t, fwCreatePath, "markdown-file",
			"dry-run payload.fields.description is an ADF doc carrying the seeded file's text, description_route == \"markdown\"")
	})
}

// fwAssertDescription asserts the composed description is an ADF doc
// containing text, and that the envelope attributes it to the Markdown
// route.
func fwAssertDescription(t *testing.T, env fwEnvelope, text string) {
	t.Helper()
	desc := fwAt(t, fwPayload(t, env), "fields", "description")
	if got := fwAt(t, desc, "type"); got != "doc" {
		t.Fatalf("description.type = %#v, want \"doc\"", got)
	}
	if !strings.Contains(fwJSONOf(t, desc), text) {
		t.Fatalf("description ADF does not carry %q: %s", text, fwJSONOf(t, desc))
	}
	if got := fwAt(t, env.Data, "description_route"); got != "markdown" {
		t.Fatalf("description_route = %#v, want \"markdown\"", got)
	}
}

// TestCreateJSONInputReadsStdin proves --json-input, through its `-`
// form: the payload arrives on STANDARD INPUT and survives composition
// verbatim — including a custom field no flag can reach.
func TestCreateJSONInputReadsStdin(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	stdin := `{"fields":{"project":{"key":"KEY"},"issuetype":{"name":"Task"},"summary":"From stdin","customfield_10016":5}}`
	res := fwExec(t, stub.url(), nil, stdin, "issue", "create", "--json-input", "-", "--dry-run")
	env := fwParse(t, res)
	payload := fwPayload(t, env)
	if got := fwAt(t, payload, "fields", "customfield_10016"); got != float64(5) {
		t.Fatalf("fields.customfield_10016 = %#v, want 5", got)
	}
	if got := fwAt(t, payload, "fields", "summary"); got != "From stdin" {
		t.Fatalf("fields.summary = %#v, want the stdin document's", got)
	}
	if got := fwAt(t, env.Data, "description_route"); got != "adf" {
		t.Fatalf("description_route = %#v, want \"adf\"", got)
	}
	Prove(t, fwCreatePath, "json-input",
		"--json-input - read the payload from STDIN: data.payload carries its customfield_10016=5 and summary verbatim, description_route == \"adf\"")
}

// TestCreateHiddenAliases proves the eight hidden compatibility aliases
// on create, each by an alias-ONLY invocation whose effect lands under
// the canonical flag's composition. --body additionally proves §2's
// refusal rule: a disagreement with --markdown names ONLY the canonical.
func TestCreateHiddenAliases(t *testing.T) {
	t.Parallel()

	// The Markdown-route aliases: text in, ADF description out.
	markdownAliases := []struct {
		flag  string
		extra []string
		files map[string]string
		text  string
	}{
		{"body", []string{"--body", "Body alias text."}, nil, "Body alias text."},
		{"description", []string{"--description", "Description alias text."}, nil, "Description alias text."},
		{"body-file", []string{"--body-file", "in.md"}, map[string]string{"in.md": "Body-file alias text."}, "Body-file alias text."},
		{"template", []string{"--template", "in.md"}, map[string]string{"in.md": "Template alias text."}, "Template alias text."},
		{"description-file", []string{"--description-file", "in.md"}, map[string]string{"in.md": "Description-file alias text."}, "Description-file alias text."},
	}
	for _, tc := range markdownAliases {
		t.Run(tc.flag, func(t *testing.T) {
			t.Parallel()
			stub := fwNewStub(t)
			res := fwExec(t, stub.url(), tc.files, "", append(fwCreateArgs(tc.extra...), "--dry-run")...)
			env := fwParse(t, res)
			fwAssertDescription(t, env, tc.text)
			evidence := "hidden --" + tc.flag + " alone composed fields.description exactly as the canonical route would"
			if tc.flag == "body" {
				// The refusal half of the alias contract, once per family:
				// a disagreement names the CANONICAL flag, never the alias.
				conflict := fwExec(t, stub.url(), nil, "",
					append(fwCreateArgs("--markdown", "one", "--body", "two"), "--dry-run")...)
				msg := fwRefusal(t, conflict, "flag_conflict", "markdown")
				if strings.Contains(msg, "--body") || !strings.Contains(msg, "--markdown") {
					t.Fatalf("disagreement refusal must name only the canonical --markdown, got: %s", msg)
				}
				evidence += "; a --markdown/--body disagreement refused flag_conflict naming ONLY --markdown"
			}
			Prove(t, fwCreatePath, tc.flag, evidence)
		})
	}

	// The ADF-route aliases: a whole payload file, unmodeled field kept.
	payloadFile := map[string]string{"payload.json": `{"fields":{"project":{"key":"KEY"},"issuetype":{"name":"Task"},"summary":"Via alias","customfield_10050":"kept"}}`}
	for _, flag := range []string{"from-json", "body-adf"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			stub := fwNewStub(t)
			res := fwExec(t, stub.url(), payloadFile, "", "issue", "create", "--"+flag, "payload.json", "--dry-run")
			env := fwParse(t, res)
			if got := fwAt(t, fwPayload(t, env), "fields", "customfield_10050"); got != "kept" {
				t.Fatalf("fields.customfield_10050 = %#v, want the alias-fed payload's value", got)
			}
			Prove(t, fwCreatePath, flag,
				"hidden --"+flag+" alone fed the whole payload: data.payload carries its customfield_10050 verbatim, as --json-input would")
		})
	}

	t.Run("labels", func(t *testing.T) {
		t.Parallel()
		stub := fwNewStub(t)
		res := fwExec(t, stub.url(), nil, "", append(fwCreateArgs("--labels", "one", "--labels", "two"), "--dry-run")...)
		env := fwParse(t, res)
		if got := fwAt(t, fwPayload(t, env), "fields", "labels", 1); got != "two" {
			t.Fatalf("fields.labels[1] = %#v, want \"two\"", got)
		}
		Prove(t, fwCreatePath, "labels",
			"hidden --labels alone composed fields.labels == [one two], exactly as canonical --label would")
	})
}

// TestCreateDryRunSendsNothing proves --dry-run by the strongest
// available contrast: the SAME argv sends one POST live and ZERO
// requests under the flag — and the preview publishes no identity,
// because no issue exists.
func TestCreateDryRunSendsNothing(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)

	preview := fwExec(t, stub.url(), nil, "", append(fwCreateArgs(), "--dry-run")...)
	env := fwParse(t, preview)
	if got := fwAt(t, env.Data, "dry_run"); got != true {
		t.Fatalf("data.dry_run = %#v, want true", got)
	}
	if _, hasIssue := env.Data["issue"]; hasIssue {
		t.Fatalf("preview published data.issue, which only a live create can produce")
	}
	if reqs := stub.requests(); len(reqs) != 0 {
		t.Fatalf("--dry-run contacted the stub: %v", reqs)
	}

	live := fwExec(t, stub.url(), nil, "", fwCreateArgs()...)
	liveEnv := fwParse(t, live)
	if got := fwAt(t, liveEnv.Data, "issue", "key"); got != fwCreatedKey {
		t.Fatalf("live data.issue.key = %#v, want %q", got, fwCreatedKey)
	}
	if len(stub.find("POST", "/rest/api/3/issue")) != 1 {
		t.Fatalf("live run did not POST exactly once; requests: %v", stub.requests())
	}
	Prove(t, fwCreatePath, "dry-run",
		"same argv: with --dry-run the stub recorded ZERO requests and data.issue is absent; without it, one POST /rest/api/3/issue landed and data.issue.key arrived")
}

// TestCreateVerifyReadsBack proves --verify: it is the only thing that
// makes a create issue a SECOND request, and the readback's stored
// summary — which exists only in the stub's GET fixture — surfaces at
// data.verified.
func TestCreateVerifyReadsBack(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	bare := fwExec(t, stub.url(), nil, "", fwCreateArgs()...)
	bareEnv := fwParse(t, bare)
	if len(stub.find("GET", "/rest/api/3/issue/")) != 0 {
		t.Fatalf("a create WITHOUT --verify read something back: %v", stub.requests())
	}
	if _, has := bareEnv.Data["verified"]; has {
		t.Fatalf("data.verified present without --verify")
	}

	verified := fwExec(t, stub.url(), nil, "", append(fwCreateArgs(), "--verify")...)
	env := fwParse(t, verified)
	if got := fwAt(t, env.Data, "verified", "summary"); got != fwStoredSummary {
		t.Fatalf("data.verified.summary = %#v, want the readback fixture's %q", got, fwStoredSummary)
	}
	readbacks := stub.find("GET", "/rest/api/3/issue/"+fwCreatedKey)
	if len(readbacks) != 1 {
		t.Fatalf("--verify did not GET the created key back exactly once; requests: %v", stub.requests())
	}
	Prove(t, fwCreatePath, "verify",
		"--verify added exactly one GET /issue/"+fwCreatedKey+" and data.verified.summary carries the readback fixture's value; without the flag, no GET and no data.verified")
}
