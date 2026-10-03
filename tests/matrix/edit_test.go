//go:build flagmatrix

package matrix

// `jira issue edit`'s 42 owned pairs: create's input surface plus the
// edit-only half — the four --remove-* partners, --unassign,
// --remaining-estimate, the notify pair, the bulk selectors with
// --force/--yes, and --parallelism. The payload proofs run through
// --dry-run exactly as create's do, with one semantic the evidence
// spells out: a repeatable field ADDS on edit (update ops), where create
// SETS (plain fields).

import (
	"strings"
	"testing"
)

// fwEditArgs is one key plus whatever the case sets. --summary is NOT
// baked in: every case must name its own change, so no case passes for
// riding another flag's composition.
func fwEditArgs(extra ...string) []string {
	return append([]string{"issue", "edit", "KEY-123"}, extra...)
}

// TestEditFieldFlagsComposePayload proves the field flags on edit: the
// scalars ride `fields` exactly as on create, and every repeatable pair
// rides `update` — adds as {add}, removes as {remove} — never a plain
// list that would silently replace what the issue already has.
func TestEditFieldFlagsComposePayload(t *testing.T) {
	t.Parallel()
	cases := []struct {
		flag     string
		extra    []string
		path     []any
		want     any
		evidence string
	}{
		{
			"summary",
			[]string{"--summary", "Edited summary"},
			[]any{"fields", "summary"},
			"Edited summary",
			"dry-run payload.fields.summary == the --summary value",
		},
		{
			"type",
			[]string{"--type", "Bug"},
			[]any{"fields", "issuetype", "name"},
			"Bug",
			"dry-run payload carries --type under Jira's id: fields.issuetype.name == \"Bug\"",
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
			"remaining-estimate",
			[]string{"--remaining-estimate", "1d"},
			[]any{"fields", "timetracking", "remainingEstimate"},
			"1d",
			"dry-run payload.fields.timetracking.remainingEstimate == \"1d\" (the edit-only flag)",
		},
		{
			"label",
			[]string{"--label", "triage"},
			[]any{"update", "labels", 0, "add"},
			"triage",
			"on EDIT --label composes an ADD op: payload.update.labels[0].add == \"triage\", never a fields.labels replacement",
		},
		{
			"remove-label",
			[]string{"--remove-label", "stale"},
			[]any{"update", "labels", 0, "remove"},
			"stale",
			"--remove-label composes a REMOVE op: payload.update.labels[0].remove == \"stale\"",
		},
		{
			"component",
			[]string{"--component", "api"},
			[]any{"update", "components", 0, "add", "name"},
			"api",
			"--component composes an ADD op with a name object: update.components[0].add.name == \"api\"",
		},
		{
			"remove-component",
			[]string{"--remove-component", "api"},
			[]any{"update", "components", 0, "remove", "name"},
			"api",
			"--remove-component composes a REMOVE op: update.components[0].remove.name == \"api\"",
		},
		{
			"fix-version",
			[]string{"--fix-version", "2026.9"},
			[]any{"update", "fixVersions", 0, "add", "name"},
			"2026.9",
			"--fix-version composes an ADD op: update.fixVersions[0].add.name == \"2026.9\"",
		},
		{
			"remove-fix-version",
			[]string{"--remove-fix-version", "2026.9"},
			[]any{"update", "fixVersions", 0, "remove", "name"},
			"2026.9",
			"--remove-fix-version composes a REMOVE op: update.fixVersions[0].remove.name == \"2026.9\"",
		},
		{
			"affects-version",
			[]string{"--affects-version", "2026.8"},
			[]any{"update", "versions", 0, "add", "name"},
			"2026.8",
			"--affects-version writes update.versions (NOT fixVersions): versions[0].add.name == \"2026.8\"",
		},
		{
			"remove-affects-version",
			[]string{"--remove-affects-version", "2026.8"},
			[]any{"update", "versions", 0, "remove", "name"},
			"2026.8",
			"--remove-affects-version composes a REMOVE op: update.versions[0].remove.name == \"2026.8\"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.flag, func(t *testing.T) {
			t.Parallel()
			stub := fwNewStub(t)
			res := fwExec(t, stub.url(), nil, "", append(fwEditArgs(tc.extra...), "--dry-run")...)
			if res.Exit != 0 {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", res.Exit, res.Stdout, res.Stderr)
			}
			env := fwParse(t, res)
			if got := fwAt(t, fwPayload(t, env), tc.path...); got != tc.want {
				t.Fatalf("payload at %v = %#v, want %#v", tc.path, got, tc.want)
			}
			Prove(t, fwEditPath, tc.flag, tc.evidence)
		})
	}
}

// TestEditUnassign proves --unassign: the payload carries an EXPLICIT
// null assignee — the key present with a null value, which is Jira's
// documented "clear it" and the one place absent and null mean opposite
// things. An ignored flag composes nothing, so the key would be absent.
func TestEditUnassign(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	res := fwExec(t, stub.url(), nil, "", append(fwEditArgs("--unassign"), "--dry-run")...)
	env := fwParse(t, res)
	fwAssertNullAssignee(t, env)
	Prove(t, fwEditPath, "unassign",
		"dry-run payload.fields.assignee is PRESENT and null — the explicit clear, not an absent key")
}

// fwAssertNullAssignee asserts fields.assignee exists and is null —
// shared with the --remove-assignee alias proof, which must land on the
// identical composition.
func fwAssertNullAssignee(t *testing.T, env fwEnvelope) {
	t.Helper()
	fields, ok := fwAt(t, fwPayload(t, env), "fields").(map[string]any)
	if !ok {
		t.Fatalf("payload.fields is not an object")
	}
	got, present := fields["assignee"]
	if !present {
		t.Fatalf("payload.fields has NO assignee key — the clear composed nothing")
	}
	if got != nil {
		t.Fatalf("payload.fields.assignee = %#v, want null", got)
	}
}

// TestEditAssigneeMeStaysLiteralInPreview proves --assignee on edit,
// through the same sentinel edge create pins.
func TestEditAssigneeMeStaysLiteralInPreview(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	res := fwExec(t, stub.url(), nil, "", append(fwEditArgs("--assignee", "@me"), "--dry-run")...)
	env := fwParse(t, res)
	if got := fwAt(t, fwPayload(t, env), "fields", "assignee", "accountId"); got != "@me" {
		t.Fatalf("fields.assignee.accountId = %#v, want the literal \"@me\"", got)
	}
	if !fwHasWarning(env, "account_id_unresolved") {
		t.Fatalf("bare preview with @me raised no account_id_unresolved warning\nstdout: %s", res.Stdout)
	}
	Prove(t, fwEditPath, "assignee",
		"payload.fields.assignee.accountId stays the literal \"@me\" under bare --dry-run, with the account_id_unresolved warning")
}

// TestEditValidateRemoteResolvesMe proves --validate-remote on edit:
// the preview reads /myself and bakes the resolved id into the payload.
func TestEditValidateRemoteResolvesMe(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	res := fwExec(t, stub.url(), nil, "", append(fwEditArgs("--assignee", "@me"), "--dry-run", "--validate-remote")...)
	env := fwParse(t, res)
	if got := fwAt(t, fwPayload(t, env), "fields", "assignee", "accountId"); got != fwAccountID {
		t.Fatalf("fields.assignee.accountId = %#v, want the resolved %q", got, fwAccountID)
	}
	if len(stub.find("GET", "/rest/api/3/myself")) == 0 {
		t.Fatalf("no GET /myself recorded; requests: %v", stub.requests())
	}
	Prove(t, fwEditPath, "validate-remote",
		"--dry-run --validate-remote sent GET /myself and the payload carries the RESOLVED account id in place of @me")
}

// TestEditMarkdownRoutes proves the Markdown route's two flags on edit.
func TestEditMarkdownRoutes(t *testing.T) {
	t.Parallel()
	t.Run("markdown", func(t *testing.T) {
		t.Parallel()
		stub := fwNewStub(t)
		res := fwExec(t, stub.url(), nil, "", append(fwEditArgs("--markdown", "Rewritten description."), "--dry-run")...)
		fwAssertDescription(t, fwParse(t, res), "Rewritten description.")
		Prove(t, fwEditPath, "markdown",
			"dry-run payload.fields.description is an ADF doc carrying the --markdown text, description_route == \"markdown\"")
	})
	t.Run("markdown-file", func(t *testing.T) {
		t.Parallel()
		stub := fwNewStub(t)
		res := fwExec(t, stub.url(), map[string]string{"notes.md": "From the edit file route."}, "",
			append(fwEditArgs("--markdown-file", "notes.md"), "--dry-run")...)
		fwAssertDescription(t, fwParse(t, res), "From the edit file route.")
		Prove(t, fwEditPath, "markdown-file",
			"dry-run payload.fields.description is an ADF doc carrying the seeded file's text, description_route == \"markdown\"")
	})
}

// TestEditJSONInputReadsStdin proves --json-input on edit, in its `-`
// stdin form, carrying the custom field the flag route cannot reach.
func TestEditJSONInputReadsStdin(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	res := fwExec(t, stub.url(), nil, `{"fields":{"customfield_10016":5}}`,
		"issue", "edit", "KEY-123", "--json-input", "-", "--dry-run")
	env := fwParse(t, res)
	if got := fwAt(t, fwPayload(t, env), "fields", "customfield_10016"); got != float64(5) {
		t.Fatalf("fields.customfield_10016 = %#v, want 5", got)
	}
	if got := fwAt(t, env.Data, "description_route"); got != "adf" {
		t.Fatalf("description_route = %#v, want \"adf\"", got)
	}
	Prove(t, fwEditPath, "json-input",
		"--json-input - read the payload from STDIN: data.payload carries its customfield_10016=5 verbatim, description_route == \"adf\"")
}

// TestEditHiddenAliases proves edit's ten hidden aliases, each by an
// alias-ONLY invocation landing on the canonical composition. --labels
// additionally proves the refusal rule: a disagreement with --label
// names only the canonical spelling.
func TestEditHiddenAliases(t *testing.T) {
	t.Parallel()

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
			res := fwExec(t, stub.url(), tc.files, "", append(fwEditArgs(tc.extra...), "--dry-run")...)
			fwAssertDescription(t, fwParse(t, res), tc.text)
			Prove(t, fwEditPath, tc.flag,
				"hidden --"+tc.flag+" alone composed fields.description exactly as the canonical route would")
		})
	}

	payloadFile := map[string]string{"payload.json": `{"fields":{"customfield_10050":"kept"}}`}
	for _, flag := range []string{"from-json", "body-adf"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			stub := fwNewStub(t)
			res := fwExec(t, stub.url(), payloadFile, "", "issue", "edit", "KEY-123", "--"+flag, "payload.json", "--dry-run")
			env := fwParse(t, res)
			if got := fwAt(t, fwPayload(t, env), "fields", "customfield_10050"); got != "kept" {
				t.Fatalf("fields.customfield_10050 = %#v, want the alias-fed payload's value", got)
			}
			Prove(t, fwEditPath, flag,
				"hidden --"+flag+" alone fed the whole payload: data.payload carries its customfield_10050 verbatim, as --json-input would")
		})
	}

	t.Run("labels", func(t *testing.T) {
		t.Parallel()
		stub := fwNewStub(t)
		res := fwExec(t, stub.url(), nil, "", append(fwEditArgs("--labels", "triage"), "--dry-run")...)
		env := fwParse(t, res)
		if got := fwAt(t, fwPayload(t, env), "update", "labels", 0, "add"); got != "triage" {
			t.Fatalf("update.labels[0].add = %#v, want \"triage\"", got)
		}
		// The refusal half, once per family: a --label/--labels
		// disagreement names ONLY the canonical spelling.
		conflict := fwExec(t, stub.url(), nil, "", append(fwEditArgs("--label", "one", "--labels", "two"), "--dry-run")...)
		msg := fwRefusal(t, conflict, "flag_conflict", "label")
		if strings.Contains(msg, "--labels") {
			t.Fatalf("disagreement refusal named the hidden --labels alias: %s", msg)
		}
		Prove(t, fwEditPath, "labels",
			"hidden --labels alone composed update.labels[0].add; a --label/--labels disagreement refused flag_conflict naming ONLY --label")
	})

	t.Run("remove-labels", func(t *testing.T) {
		t.Parallel()
		stub := fwNewStub(t)
		res := fwExec(t, stub.url(), nil, "", append(fwEditArgs("--remove-labels", "stale"), "--dry-run")...)
		env := fwParse(t, res)
		if got := fwAt(t, fwPayload(t, env), "update", "labels", 0, "remove"); got != "stale" {
			t.Fatalf("update.labels[0].remove = %#v, want \"stale\"", got)
		}
		Prove(t, fwEditPath, "remove-labels",
			"hidden --remove-labels alone composed update.labels[0].remove == \"stale\", exactly as canonical --remove-label would")
	})

	t.Run("remove-assignee", func(t *testing.T) {
		t.Parallel()
		stub := fwNewStub(t)
		res := fwExec(t, stub.url(), nil, "", append(fwEditArgs("--remove-assignee"), "--dry-run")...)
		fwAssertNullAssignee(t, fwParse(t, res))
		Prove(t, fwEditPath, "remove-assignee",
			"hidden --remove-assignee alone composed the explicit null fields.assignee, exactly as canonical --unassign would")
	})
}

// TestEditDryRunSendsNothing proves --dry-run on edit by the live
// contrast: same argv, one PUT live, zero requests under the flag — and
// the preview still publishes the per-key result shape.
func TestEditDryRunSendsNothing(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)

	preview := fwExec(t, stub.url(), nil, "", append(fwEditArgs("--summary", "Edited"), "--dry-run")...)
	env := fwParse(t, preview)
	if got := fwAt(t, env.Data, "dry_run"); got != true {
		t.Fatalf("data.dry_run = %#v, want true", got)
	}
	if got := fwAt(t, env.Data, "results", 0, "issue", "key"); got != "KEY-123" {
		t.Fatalf("preview results[0].issue.key = %#v, want \"KEY-123\"", got)
	}
	if reqs := stub.requests(); len(reqs) != 0 {
		t.Fatalf("--dry-run contacted the stub: %v", reqs)
	}

	fwExec(t, stub.url(), nil, "", fwEditArgs("--summary", "Edited")...)
	if len(stub.find("PUT", "/rest/api/3/issue/KEY-123")) != 1 {
		t.Fatalf("live run did not PUT exactly once; requests: %v", stub.requests())
	}
	Prove(t, fwEditPath, "dry-run",
		"same argv: with --dry-run the stub recorded ZERO requests while results[] previews the key; without it, one PUT /issue/KEY-123 landed")
}

// TestEditVerifyReadsBack proves --verify on edit: the 204 endpoint has
// no confirmation to give, so the flag alone adds the GET whose stored
// summary lands at results[0].data.verified.
func TestEditVerifyReadsBack(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	fwExec(t, stub.url(), nil, "", fwEditArgs("--summary", "Edited")...)
	if len(stub.find("GET", "/rest/api/3/issue/")) != 0 {
		t.Fatalf("an edit WITHOUT --verify read something back: %v", stub.requests())
	}

	res := fwExec(t, stub.url(), nil, "", fwEditArgs("--summary", "Edited", "--verify")...)
	env := fwParse(t, res)
	if got := fwAt(t, env.Data, "results", 0, "data", "verified", "summary"); got != fwStoredSummary {
		t.Fatalf("results[0].data.verified.summary = %#v, want %q", got, fwStoredSummary)
	}
	if len(stub.find("GET", "/rest/api/3/issue/KEY-123")) != 1 {
		t.Fatalf("--verify did not GET the key back exactly once; requests: %v", stub.requests())
	}
	Prove(t, fwEditPath, "verify",
		"--verify added exactly one GET /issue/KEY-123 and results[0].data.verified.summary carries the readback fixture's value; without the flag, no GET")
}

// TestEditNotify proves the notify pair on the wire, where it actually
// rides: --notify=false puts notifyUsers=false on the PUT's query, and
// the default sends NOTHING — no notifyUsers parameter at all.
func TestEditNotify(t *testing.T) {
	t.Parallel()
	t.Run("notify", func(t *testing.T) {
		t.Parallel()
		stub := fwNewStub(t)
		fwExec(t, stub.url(), nil, "", fwEditArgs("--summary", "Edited", "--notify=false")...)
		suppressed := stub.find("PUT", "/rest/api/3/issue/KEY-123")
		if len(suppressed) != 1 || !strings.Contains(suppressed[0].RawQuery, "notifyUsers=false") {
			t.Fatalf("--notify=false did not put notifyUsers=false on the PUT query; requests: %v", stub.requests())
		}

		deflt := fwNewStub(t)
		fwExec(t, deflt.url(), nil, "", fwEditArgs("--summary", "Edited")...)
		bare := deflt.find("PUT", "/rest/api/3/issue/KEY-123")
		if len(bare) != 1 || strings.Contains(bare[0].RawQuery, "notifyUsers") {
			t.Fatalf("default run should send NO notifyUsers parameter, query was %q", bare[0].RawQuery)
		}
		Prove(t, fwEditPath, "notify",
			"--notify=false put notifyUsers=false on the PUT's query string; the default sent no notifyUsers parameter at all")
	})
	t.Run("skip-notify", func(t *testing.T) {
		t.Parallel()
		stub := fwNewStub(t)
		fwExec(t, stub.url(), nil, "", fwEditArgs("--summary", "Edited", "--skip-notify")...)
		puts := stub.find("PUT", "/rest/api/3/issue/KEY-123")
		if len(puts) != 1 || !strings.Contains(puts[0].RawQuery, "notifyUsers=false") {
			t.Fatalf("hidden --skip-notify did not fold to notifyUsers=false; requests: %v", stub.requests())
		}
		Prove(t, fwEditPath, "skip-notify",
			"hidden --skip-notify alone put notifyUsers=false on the PUT's query, exactly as canonical --notify=false would")
	})
}

// TestEditSelectorConfirmation proves --jql, --force and its hidden
// --yes in one mechanism: a selector-driven live run RESOLVES first
// (the stub sees the search carrying the query), refuses without a
// confirmation in a terminal-less run, and proceeds to the per-key PUTs
// only when --force (or --yes) answers up front.
func TestEditSelectorConfirmation(t *testing.T) {
	t.Parallel()
	const query = "project = KEY AND labels = needs-triage"

	unforced := fwNewStub(t)
	res := fwExec(t, unforced.url(), nil, "", "issue", "edit", "--jql", query, "--summary", "Edited")
	fwRefusal(t, res, "flag_required", "force")
	searches := unforced.find("POST", "/rest/api/3/search/jql")
	if len(searches) != 1 || !strings.Contains(searches[0].Body, query) {
		t.Fatalf("selector did not resolve through /search/jql with the query verbatim; requests: %v", unforced.requests())
	}
	if len(unforced.find("PUT", "/rest/api/3/issue/")) != 0 {
		t.Fatalf("refused run still sent a PUT: %v", unforced.requests())
	}
	Prove(t, fwEditPath, "jql",
		"--jql resolved via POST /search/jql whose body carries the query verbatim, and the run acted on the two keys the search answered")

	forced := fwNewStub(t)
	fwExec(t, forced.url(), nil, "", "issue", "edit", "--jql", query, "--summary", "Edited", "--force")
	if got := len(forced.find("PUT", "/rest/api/3/issue/")); got != 2 {
		t.Fatalf("--force run sent %d PUTs, want one per resolved key (2); requests: %v", got, forced.requests())
	}
	Prove(t, fwEditPath, "force",
		"without --force the terminal-less selector run refused flag_required(force) after resolving, sending no PUT; with it both resolved keys were PUT")

	viaYes := fwNewStub(t)
	fwExec(t, viaYes.url(), nil, "", "issue", "edit", "--jql", query, "--summary", "Edited", "--yes")
	if got := len(viaYes.find("PUT", "/rest/api/3/issue/")); got != 2 {
		t.Fatalf("--yes run sent %d PUTs, want 2; requests: %v", got, viaYes.requests())
	}
	Prove(t, fwEditPath, "yes",
		"hidden --yes alone skipped the selector confirmation exactly as --force does: the same argv refused without it and PUT both keys with it")
}

// TestEditFilterSelector proves --filter: the id fetches the saved
// filter, the search runs the JQL stored THERE (a string the argv never
// contained), and the envelope publishes that resolved query.
func TestEditFilterSelector(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	res := fwExec(t, stub.url(), nil, "", "issue", "edit", "--filter", "42", "--summary", "Edited", "--force")
	env := fwParse(t, res)
	if len(stub.find("GET", "/rest/api/3/filter/42")) != 1 {
		t.Fatalf("--filter 42 did not GET /filter/42; requests: %v", stub.requests())
	}
	searches := stub.find("POST", "/rest/api/3/search/jql")
	if len(searches) != 1 || !strings.Contains(searches[0].Body, fwFilterJQL) {
		t.Fatalf("search did not run the filter's stored JQL; requests: %v", stub.requests())
	}
	if got := fwAt(t, env.Data, "selector", "jql"); got != fwFilterJQL {
		t.Fatalf("data.selector.jql = %#v, want the filter's stored %q", got, fwFilterJQL)
	}
	Prove(t, fwEditPath, "filter",
		"--filter 42 fetched GET /filter/42 and searched with the JQL stored there (absent from the argv), publishing it at data.selector.jql")
}

// TestEditFromFile proves --from-file: the keys come from the seeded
// file — comments and blanks skipped — with no site read, and the
// preview enumerates exactly them. An ignored flag would refuse for
// naming no issues at all.
func TestEditFromFile(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	keys := "KEY-201\n# a comment\n\nKEY-202\n"
	res := fwExec(t, stub.url(), map[string]string{"keys.txt": keys}, "",
		"issue", "edit", "--from-file", "keys.txt", "--summary", "Edited", "--dry-run")
	env := fwParse(t, res)
	if got := fwAt(t, env.Data, "results", 0, "issue", "key"); got != "KEY-201" {
		t.Fatalf("results[0].issue.key = %#v, want the file's first key", got)
	}
	if got := fwAt(t, env.Data, "results", 1, "issue", "key"); got != "KEY-202" {
		t.Fatalf("results[1].issue.key = %#v, want the file's second key", got)
	}
	if got := fwAt(t, env.Data, "selector", "kind"); got != "from-file" {
		t.Fatalf("data.selector.kind = %#v, want \"from-file\"", got)
	}
	if reqs := stub.requests(); len(reqs) != 0 {
		t.Fatalf("--from-file preview contacted the stub: %v", reqs)
	}
	Prove(t, fwEditPath, "from-file",
		"--from-file's keys (comment and blank lines skipped) became results[] in file order with selector.kind == from-file and zero stub requests")
}

// TestEditParallelismBounds proves --parallelism by its bounds refusal:
// a value below the documented minimum is refused by taxonomy code with
// the flag named — which cannot happen if the flag is never read.
func TestEditParallelismBounds(t *testing.T) {
	t.Parallel()
	stub := fwNewStub(t)
	res := fwExec(t, stub.url(), nil, "", fwEditArgs("--summary", "Edited", "--parallelism", "0")...)
	fwRefusal(t, res, "flag_value_invalid", "parallelism")
	if len(stub.requests()) != 0 {
		t.Fatalf("refused run contacted the stub: %v", stub.requests())
	}
	Prove(t, fwEditPath, "parallelism",
		"--parallelism 0 refused flag_value_invalid naming the flag, before any request — the bounds check reads the parsed value")
}
