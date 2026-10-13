package command

import (
	"encoding/csv"
	"reflect"
	"strings"
	"testing"
)

func TestCSVCompletionDistinguishesQuotedDelimitersAndLiteralFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		input, prefix, head string
		selected            []string
	}{
		{input: "", prefix: ""},
		{input: "nested/fi", prefix: "nested/fi"},
		{input: "earlier,nested/fi", prefix: "nested/fi", head: "earlier,", selected: []string{"earlier"}},
		{input: `"earlier,one","nested/fi,`, prefix: "nested/fi,", head: `"earlier,one",`, selected: []string{"earlier,one"}},
		{input: `"earlier""quote",next`, prefix: "next", head: `"earlier""quote",`, selected: []string{`earlier"quote`}},
		{input: `"earlier,one",`, head: `"earlier,one",`, selected: []string{"earlier,one"}},
		{input: `first,, `, prefix: " ", head: "first,,", selected: []string{"first", ""}},
		{input: `"complete"`, prefix: "complete"},
		{input: `"part""quote`, prefix: `part"quote`},
		{input: `"part""`, prefix: `part"`},
		{input: `"`, prefix: ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			completion, err := ParseCSVCompletion(tc.input)
			if err != nil || completion.Prefix() != tc.prefix || completion.head != tc.head || !reflect.DeepEqual(completion.Selected(), tc.selected) {
				t.Fatal("partial CSV lost a literal field or delimiter", completion, err)
			}
			selected := completion.Selected()
			if len(selected) > 0 {
				selected[0] = "changed copy"
				if !reflect.DeepEqual(completion.Selected(), tc.selected) {
					t.Fatal("selected fields alias the retained prefix")
				}
			}
		})
	}
}

func TestCSVCompletionCandidateRoundTripsAndPreservesCompletedSpelling(t *testing.T) {
	t.Parallel()
	const earlier = `"already,selected", literal leading space,"quote""name",`
	completion, err := ParseCSVCompletion(earlier + `"fi`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"file.txt", "file with spaces", "file,comma", `file"quote`, " literal leading space", "nested/file'name", "file$HOME;$(command)`text", ""} {
		t.Run(name, func(t *testing.T) {
			for _, encode := range []func(string) (string, error){completion.Candidate, completion.QuotedCandidate} {
				candidate, err := encode(name)
				if err != nil || !strings.HasPrefix(candidate, earlier) {
					t.Fatal("candidate changed completed CSV spelling", candidate, err)
				}
				fields, err := csv.NewReader(strings.NewReader(candidate)).Read()
				want := append(completion.Selected(), name)
				if err != nil || !reflect.DeepEqual(fields, want) {
					t.Fatal("completion changed native CSV values", candidate, fields, err)
				}
			}
		})
	}
	empty, err := (CSVCompletion{}).Candidate("")
	if err != nil {
		t.Fatal(err)
	}
	fields, err := csv.NewReader(strings.NewReader(empty)).Read()
	if err != nil || !reflect.DeepEqual(fields, []string{""}) {
		t.Fatal("empty candidate became an absent record", empty, fields, err)
	}
}

func TestCSVCompletionRefusesMalformedRecordsWithoutPartialState(t *testing.T) {
	t.Parallel()
	for _, value := range []string{`bare"quote`, `"closed"x`, `"closed" `, "first\nsecond", "\rfirst", `ok,"closed"x,next`} {
		completion, err := ParseCSVCompletion(value)
		if err == nil || completion.Prefix() != "" || completion.head != "" || len(completion.Selected()) != 0 {
			t.Fatal("malformed CSV supplied a partial completion", value, completion, err)
		}
	}
	completion, err := ParseCSVCompletion("earlier,")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"line\nbreak", "line\rbreak"} {
		for _, encode := range []func(string) (string, error){completion.Candidate, completion.QuotedCandidate} {
			if candidate, err := encode(value); candidate != "" || err == nil {
				t.Fatal("line-bearing candidate escaped the record protocol", candidate, err)
			}
		}
	}
}

func TestCSVDirectoryCompletionCanExtendThenCloseCurrentField(t *testing.T) {
	t.Parallel()
	const head = `"earlier,selected",`
	partial, err := ParseCSVCompletion(head + "candidate")
	if err != nil {
		t.Fatal(err)
	}
	directory, err := partial.DirectoryCandidate(`candidate,dir/`)
	if err != nil {
		t.Fatal(err)
	}
	continued, err := ParseCSVCompletion(directory + "lea")
	if err != nil || continued.Prefix() != "candidate,dir/lea" {
		t.Fatal("directory cannot extend current field", directory, continued, err)
	}
	final, err := continued.Candidate("candidate,dir/leaf.txt")
	if err != nil {
		t.Fatal(err)
	}
	fields, err := csv.NewReader(strings.NewReader(final)).Read()
	if err != nil || !reflect.DeepEqual(fields, []string{"earlier,selected", "candidate,dir/leaf.txt"}) {
		t.Fatal("directory continuation changed fields", fields, err)
	}
}
