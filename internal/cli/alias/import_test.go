package alias

import (
	"maps"
	"slices"
	"testing"

	"github.com/gechr/x/shell"
)

func TestAliasYAMLImportKeepsLiteralStringsAndAnchors(t *testing.T) {
	t.Parallel()
	const document = `base: &base " issue list --jql 'literal #tag' "
copy: *base
'my.inbox': "version"
`
	got, err := parseImport([]byte(document))
	want := map[string]string{"base": " issue list --jql 'literal #tag' ", "copy": " issue list --jql 'literal #tag' ", "my.inbox": "version"}
	if err != nil || !maps.Equal(got, want) {
		t.Fatalf("YAML import changed authored names, text or anchors: %v %v", got, err)
	}
}

func TestAliasYAMLImportRejectsWrongTypesDuplicatesAndExtraDocuments(t *testing.T) {
	t.Parallel()
	for _, document := range []string{"", "null", "[]", "scalar", "a: true", "a: 123", "a: null", "null: version", "a: &empty null\nb: *empty", "<<: {a: null}\nb: version", "123: version", "a: [version]", "a: {b: version}", "a: version\na: 'issue list'", "a: version\n---\nb: version", "a: [", "a: &a {self: *a}"} {
		t.Run(document, func(t *testing.T) {
			t.Parallel()
			if _, err := parseImport([]byte(document)); err == nil {
				t.Fatal("malformed alias YAML passed the string-map boundary")
			}
		})
	}
	if got, err := parseImport([]byte("{}")); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("an explicit empty map is a valid import: %v %v", got, err)
	}
}

func TestAliasStoredArgvRoundTripsPOSIXQuoting(t *testing.T) {
	t.Parallel()
	const authored = " issue list --jql 'literal #tag' "
	if got := storeExpansion([]string{authored}); got != authored {
		t.Fatal("single expansion string was rewritten")
	}
	want := []string{"issue", "list", "--jql", "project = DEMO AND path = 'C:\\file'", "--status", "To Do,In Progress", "", "#literal", "$(touch /tmp/should-not-run)"}
	got, err := shell.Split(storeExpansion(want))
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("stored argv lost shell-sensitive arguments: %q %v", got, err)
	}
}

func TestAliasYAMLImportPreservesMergeAndExplicitOverride(t *testing.T) {
	t.Parallel()
	got, err := parseImport([]byte("<<: [{mine: 'issue list', other: version}, {last: version}]\nmine: version\n"))
	want := map[string]string{"mine": "version", "other": "version", "last": "version"}
	if err != nil || !maps.Equal(got, want) {
		t.Fatalf("YAML merge or explicit override changed: %v %v", got, err)
	}
}
