package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestIssueMetadataPreservesNativeDefinitions(t *testing.T) {
	t.Parallel()
	const field = `{"fieldId":"customfield_10123","key":"customfield_10123","name":"Future <field>","required":false,"schema":{"type":"future","customId":9007199254740993},"allowedValues":[],"defaultValue":null,"operations":[],"unknown":{"states":[null,[],{},0,false],"spelling":"\u003c&"}}`
	const native = `{"startAt":7,"maxResults":3,"total":9,"fields":[` + field + `],"future":{"amount":1.2300e+9}}`
	input := []byte(native)
	page, err := DecodeCreateIssueFields(input, 7, 3)
	if err != nil || page == nil {
		t.Fatalf("decode: %v", err)
	}
	if page.StartAt != 7 || page.MaxResults != 3 || page.Total != 9 || len(page.Entries) != 1 || string(page.Entries[0]) != field || string(page.Native) != native {
		t.Fatalf("metadata, native spelling or short-page facts changed: %+v", page)
	}
	// The caller owns its input buffer; later reuse cannot corrupt discovery.
	clear(input)
	if string(page.Native) != native || string(page.Entries[0]) != field {
		t.Fatal("metadata retains the caller's mutable buffer")
	}
	const typeEntry = `{"id":"10001","name":"Bug","subtask":false,"scope":null,"future":{"big":9007199254740993}}`
	types, err := DecodeCreateIssueTypes([]byte(`{"startAt":0,"maxResults":50,"total":1,"issueTypes":[`+typeEntry+`]}`), 0, 50)
	if err != nil || types == nil || len(types.Entries) != 1 || string(types.Entries[0]) != typeEntry {
		t.Fatalf("type identity or extensions lost: %+v %v", types, err)
	}
}

func TestIssueMetadataRejectsIncompletePages(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []struct {
		name     string
		member   string
		identity string
		decode   func([]byte, int, int) (*IssueMetadataPage, error)
	}{
		{name: "types", member: "issueTypes", identity: "id", decode: DecodeCreateIssueTypes},
		{name: "fields", member: "fields", identity: "fieldId", decode: DecodeCreateIssueFields},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			t.Parallel()
			row := `{"` + endpoint.identity + `":"10001","future":{"n":9007199254740993}}`
			if endpoint.name == "fields" {
				row = metadataCompleteField("10001")
			}
			base := `{"startAt":0,"maxResults":2,"total":1,"` + endpoint.member + `":[` + row + `]}`
			cases := map[string]string{
				"null-root": `null`, "array-root": `[]`, "missing-facts": `{}`, "invalid-json": `{`,
				"trailing-document": base + ` {}`, "trailing-garbage": base + ` trailing`,
				"duplicate-fact":          strings.Replace(base, `"total":1`, `"total":0,"total":1`, 1),
				"duplicate-nested-member": strings.Replace(base, `"n":9007199254740993`, `"n":0,"n":9007199254740993`, 1),
				"wrong-offset":            strings.Replace(base, `"startAt":0`, `"startAt":1`, 1),
				"zero-size":               strings.Replace(base, `"maxResults":2`, `"maxResults":0`, 1),
				"negative-size":           strings.Replace(base, `"maxResults":2`, `"maxResults":-1`, 1),
				"negative-total":          strings.Replace(base, `"total":1`, `"total":-1`, 1),
				"total-before-rows":       strings.Replace(base, `"total":1`, `"total":0`, 1),
				"nonfinal-empty":          strings.Replace(base, row, ``, 1),
				"repeated-identity":       strings.Replace(strings.Replace(base, `"total":1`, `"total":2`, 1), row, row+`,`+row, 1),
			}
			for _, fact := range []string{"startAt", "maxResults", "total", endpoint.member} {
				var members map[string]json.RawMessage
				if err := json.Unmarshal([]byte(base), &members); err != nil {
					t.Fatal(err)
				}
				members[fact] = json.RawMessage(`null`)
				encoded, err := json.Marshal(members)
				if err != nil {
					t.Fatal(err)
				}
				cases[fact+"-null"] = string(encoded)
				delete(members, fact)
				encoded, err = json.Marshal(members)
				if err != nil {
					t.Fatal(err)
				}
				cases[fact+"-absent"] = string(encoded)
			}
			for i, invalid := range []string{`null`, `[]`, `{}`, `{"` + endpoint.identity + `":null}`, `{"` + endpoint.identity + `":123}`, `{"` + endpoint.identity + `":""}`, `{"` + endpoint.identity + `":" "}`, `{"` + endpoint.identity + `":" 10001"}`} {
				cases[fmt.Sprintf("invalid-row-%d", i)] = strings.Replace(base, row, invalid, 1)
			}
			for name, body := range cases {
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					page, err := endpoint.decode([]byte(body), 0, 2)
					if page != nil || !errors.Is(err, ErrIssueMetadataInvalid) {
						t.Fatalf("incomplete metadata became a page: %+v %v", page, err)
					}
				})
			}
		})
	}
}

func TestIssueMetadataPagingBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		body        string
		start, size int
		valid       bool
	}{
		{name: "explicit-empty", body: `{"startAt":0,"maxResults":50,"total":0,"fields":[]}`, start: 0, size: 50, valid: true},
		{name: "terminal-empty", body: `{"startAt":4,"maxResults":50,"total":4,"fields":[]}`, start: 4, size: 50, valid: true},
		{name: "past-total", body: `{"startAt":5,"maxResults":50,"total":4,"fields":[]}`, start: 5, size: 50, valid: false},
		{name: "negative-request-offset", body: `{}`, start: -1, size: 50, valid: false},
		{name: "zero-request-size", body: `{}`, start: 0, size: 0, valid: false},
		{name: "negative-request-size", body: `{}`, start: 0, size: -1, valid: false},
		{name: "oversized-request", body: `{}`, start: 0, size: 201, valid: false},
		{name: "server-page-too-many", body: `{"startAt":0,"maxResults":1,"total":2,"fields":[{"fieldId":"a"},{"fieldId":"b"}]}`, start: 0, size: 2, valid: false},
		{name: "request-page-too-many", body: `{"startAt":0,"maxResults":2,"total":2,"fields":[{"fieldId":"a"},{"fieldId":"b"}]}`, start: 0, size: 1, valid: false},
		{name: "fractional-offset", body: `{"startAt":0.5,"maxResults":50,"total":0,"fields":[]}`, start: 0, size: 50, valid: false},
		{name: "string-total", body: `{"startAt":0,"maxResults":50,"total":"0","fields":[]}`, start: 0, size: 50, valid: false},
		{name: "overflow-total", body: `{"startAt":0,"maxResults":50,"total":999999999999999999999999,"fields":[]}`, start: 0, size: 50, valid: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := strings.ReplaceAll(tc.body, `{"fieldId":"a"},{"fieldId":"b"}`, metadataCompleteField("a")+`,`+metadataCompleteField("b"))
			page, err := DecodeCreateIssueFields([]byte(body), tc.start, tc.size)
			if tc.valid {
				if err != nil || page == nil || page.Entries == nil || len(page.Entries) != 0 {
					t.Fatalf("explicit empty page lost: %+v %v", page, err)
				}
			} else if page != nil || !errors.Is(err, ErrIssueMetadataInvalid) {
				t.Fatalf("invalid paging accepted: %+v %v", page, err)
			}
		})
	}
}

func TestEditMetadataPreservesRawMapAndPresence(t *testing.T) {
	t.Parallel()
	const definition = `{"required":false,"key":"customfield_10123","name":"Future field","operations":[],"schema":{"type":"future"},"allowedValues":[],"defaultValue":null,"unknown":{"big":9007199254740993,"text":"\u003c"}}`
	const document = `{"fields":{"customfield_10123":` + definition + `},"future":[]}`
	input := []byte(document)
	read, err := DecodeEditIssueFields(input)
	if err != nil || len(read.Fields) != 1 || string(read.Fields["customfield_10123"]) != definition || string(read.Native) != document {
		t.Fatalf("edit metadata changed: %+v %v", read, err)
	}
	clear(input)
	if string(read.Fields["customfield_10123"]) != definition || string(read.Native) != document {
		t.Fatal("edit metadata retains caller's mutable buffer")
	}
	empty, err := DecodeEditIssueFields([]byte(`{"fields":{}}`))
	if err != nil || empty.Fields == nil || len(empty.Fields) != 0 {
		t.Fatalf("explicit empty map lost: %+v %v", empty, err)
	}
}

func TestEditMetadataRejectsIncompleteDefinitions(t *testing.T) {
	t.Parallel()
	definition := metadataCompleteField("summary")
	duplicateRequired := strings.Replace(definition, `"required":true`, `"required":false,"required":true`, 1)
	for name, input := range map[string]string{
		"null-root": `null`, "missing-fields": `{}`, "null-fields": `{"fields":null}`, "array-fields": `{"fields":[]}`,
		"trailing-document": `{"fields":{}} {}`, "invalid-json": `{`, "missing-field-id": `{"fields":{"":` + metadataCompleteField("") + `}}`,
		"blank-field-id": `{"fields":{" ":` + metadataCompleteField(" ") + `}}`, "padded-field-id": `{"fields":{" summary":` + metadataCompleteField(" summary") + `}}`,
		"null-definition": `{"fields":{"summary":null}}`, "array-definition": `{"fields":{"summary":[]}}`,
		"duplicate-field": `{"fields":{"summary":` + definition + `,"summary":` + definition + `}}`, "duplicate-nested-member": `{"fields":{"summary":` + duplicateRequired + `}}`,
		"contradictory-id": `{"fields":{"summary":{"key":"summary","name":"Summary","required":false,"operations":[],"schema":{"type":"future"},"fieldId":"priority"}}}`, "null-declared-id": `{"fields":{"summary":{"key":"summary","name":"Summary","required":false,"operations":[],"schema":{"type":"future"},"fieldId":null}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			read, err := DecodeEditIssueFields([]byte(input))
			if read.Fields != nil || len(read.Native) != 0 || !errors.Is(err, ErrIssueMetadataInvalid) {
				t.Fatalf("incomplete edit metadata accepted: %+v %v", read, err)
			}
		})
	}
}

func TestIssueMetadataPreservesUnknownOperations(t *testing.T) {
	t.Parallel()
	definition := strings.Replace(metadataCompleteField("summary"), `["set"]`, `["future-operation",""]`, 1)
	page, err := DecodeCreateIssueFields([]byte(`{"startAt":0,"maxResults":50,"total":1,"fields":[`+definition+`]}`), 0, 50)
	if err != nil || page == nil || len(page.Entries) != 1 || string(page.Entries[0]) != definition {
		t.Fatalf("unknown operation names changed in create metadata: %+v %v", page, err)
	}
	read, err := DecodeEditIssueFields([]byte(`{"fields":{"summary":` + definition + `}}`))
	if err != nil || string(read.Fields["summary"]) != definition {
		t.Fatalf("unknown operation names changed in edit metadata: %+v %v", read, err)
	}
}

func FuzzIssueMetadataDecoders(f *testing.F) {
	f.Add([]byte(`{"startAt":0,"maxResults":50,"total":0,"fields":[]}`))
	f.Add([]byte(`{"fields":{}}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, decode := range []func([]byte, int, int) (*IssueMetadataPage, error){DecodeCreateIssueTypes, DecodeCreateIssueFields} {
			page, err := decode(data, 0, 50)
			if err == nil && (page == nil || !bytes.Equal(page.Native, data)) {
				t.Fatal("successful page lacks the exact native document")
			}
		}
		read, err := DecodeEditIssueFields(data)
		if err == nil && (read.Fields == nil || !bytes.Equal(read.Native, data)) {
			t.Fatal("successful edit metadata lacks its native map or document")
		}
	})
}

func metadataCompleteField(id string) string {
	return fmt.Sprintf(`{"fieldId":%q,"key":%q,"name":"Field","required":true,"operations":["set"],"schema":{"type":"future"},"future":{"n":9007199254740993}}`, id, id)
}

func TestIssueMetadataRejectsMissingOrInvalidRequiredDefinitionFacts(t *testing.T) {
	t.Parallel()
	for _, domain := range []string{"create", "edit"} {
		t.Run(domain, func(t *testing.T) {
			t.Parallel()
			for _, fact := range []string{"key", "name", "required", "operations", "schema"} {
				for _, state := range []string{"absent", "null", "wrong-type"} {
					t.Run(fact+"-"+state, func(t *testing.T) {
						t.Parallel()
						var members map[string]json.RawMessage
						if err := json.Unmarshal([]byte(metadataCompleteField("summary")), &members); err != nil {
							t.Fatal(err)
						}
						switch state {
						case "absent":
							delete(members, fact)
						case "null":
							members[fact] = json.RawMessage(`null`)
						case "wrong-type":
							members[fact] = json.RawMessage(`123`)
						}
						encoded, err := json.Marshal(members)
						if err != nil {
							t.Fatal(err)
						}
						if domain == "create" {
							page, err := DecodeCreateIssueFields([]byte(`{"startAt":0,"maxResults":50,"total":1,"fields":[`+string(encoded)+`]}`), 0, 50)
							if page != nil || !errors.Is(err, ErrIssueMetadataInvalid) {
								t.Fatalf("incomplete definition became create metadata: %+v %v", page, err)
							}
						} else {
							read, err := DecodeEditIssueFields([]byte(`{"fields":{"summary":` + string(encoded) + `}}`))
							if read.Fields != nil || !errors.Is(err, ErrIssueMetadataInvalid) {
								t.Fatalf("incomplete definition became edit metadata: %+v %v", read, err)
							}
						}
					})
				}
			}
		})
	}
}

func TestIssueMetadataRejectsInvalidSchemaAndOperationMembers(t *testing.T) {
	t.Parallel()
	for name, replacements := range map[string]map[string]json.RawMessage{
		"schema-type-absent": {"schema": json.RawMessage(`{}`)},
		"schema-type-null":   {"schema": json.RawMessage(`{"type":null}`)},
		"schema-type-number": {"schema": json.RawMessage(`{"type":123}`)},
		"operation-null":     {"operations": json.RawMessage(`[null]`)},
		"operation-object":   {"operations": json.RawMessage(`[{}]`)},
		"operation-number":   {"operations": json.RawMessage(`[1]`)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var members map[string]json.RawMessage
			if err := json.Unmarshal([]byte(metadataCompleteField("summary")), &members); err != nil {
				t.Fatal(err)
			}
			for key, value := range replacements {
				members[key] = value
			}
			encoded, err := json.Marshal(members)
			if err != nil {
				t.Fatal(err)
			}
			page, err := DecodeCreateIssueFields([]byte(`{"startAt":0,"maxResults":50,"total":1,"fields":[`+string(encoded)+`]}`), 0, 50)
			if page != nil || !errors.Is(err, ErrIssueMetadataInvalid) {
				t.Fatalf("invalid known metadata member accepted: %+v %v", page, err)
			}
			read, err := DecodeEditIssueFields([]byte(`{"fields":{"summary":` + string(encoded) + `}}`))
			if read.Fields != nil || !errors.Is(err, ErrIssueMetadataInvalid) {
				t.Fatalf("invalid known edit metadata member accepted: %+v %v", read, err)
			}
		})
	}
}
