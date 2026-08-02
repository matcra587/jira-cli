package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/gechr/clog"
)

// generatedSchemas is the closed set of platform schemas emitted as wire
// types, in output order. It is the transitive shape the issue read path
// needs and nothing more — the whole spec would generate thousands of types
// the CLI never reads. A $ref that points outside this set is deliberately
// left as json.RawMessage (the raw-escape type), so growing the set later is
// additive, never a rewrite.
var generatedSchemas = []string{
	"IssueBean",
	"StatusDetails",
	"StatusCategory",
	"Scope",
	"ProjectDetails",
	"UpdatedProjectCategory",
	"AvatarUrlsBean",
	"UserDetails",
	"IssueTypeDetails",
	"Priority",
	"Resolution",
	// The issue-link set. An issue's links arrive inside its own
	// `fields.issuelinks` (Jira has no list-the-links endpoint), so
	// IssueLink is reachable from IssueFields, and LinkedIssue/
	// IssueLinkType/Fields are its transitive shape. "Fields" is the
	// spec's own name for the handful of key fields a LINKED issue
	// carries (summary, status, priority, issuetype) — a closed set,
	// unrelated to the tenant-dynamic IssueFields this package curates by
	// hand, and named here as the spec names it because the generator
	// takes schema names verbatim.
	"IssueLink",
	"LinkedIssue",
	"IssueLinkType",
	"Fields",
	// IssueLinkTypes is the response body of GET
	// /rest/api/3/issueLinkType (core.JiraClient.GetIssueLinkTypes) — a
	// one-member envelope around the IssueLinkType above it. Unlike the
	// four entries before it, nothing in the issue read reaches it: it is
	// here because `issue link create` has to turn a phrase a caller
	// typed ("blocks") into the type and DIRECTION Jira's own link
	// endpoint takes, and this list is the only place either comes from.
	"IssueLinkTypes",
	// Attachments, for the same structural reason the link set is here:
	// the spec exposes no list-an-issue's-attachments operation (only
	// /attachment/{id} and its content/thumbnail siblings), so an issue's
	// attachments arrive inside its own `fields.attachment` and Attachment
	// is reachable from IssueFields. Its one $ref is UserDetails, already
	// generated above, so this adds a single type.
	"Attachment",
	// Watchers is the first entry here NOT reachable from IssueBean, and
	// the reason is worth stating because every entry above it was. The
	// spec DOES publish a list endpoint for watchers — GET
	// /rest/api/3/issue/{issueIdOrKey}/watchers, `getIssueWatchers` —
	// unlike links and attachments, which have none and so arrive folded
	// into the issue read. So this type is the response body of a request
	// of its own (core.JiraClient.GetWatchers), not a member of
	// IssueFields, and nothing in the generated graph points at it.
	//
	// It is generated rather than hand-curated (the choice comment.go
	// made for Comment) because none of that type's reasons apply: a
	// Watchers payload carries no tenant-editable ADF body and no
	// tenant-dynamic field bag — it is four fixed members, and its one
	// $ref is UserDetails, already generated above. Hand-writing a
	// closed, fully-specified shape the generator can read straight off
	// the spec would only be a second place for it to drift.
	"Watchers",
	// The transition set, here for Watchers' reason rather than the issue
	// read's: GET /rest/api/3/issue/{issueIdOrKey}/transitions is an
	// endpoint of its own (core.JiraClient.GetTransitions), so Transitions
	// is a response body and IssueTransition its member. IssueBean does
	// declare a `transitions` property, but the spec types it loosely and
	// this package left it as []json.RawMessage; that stays as it is —
	// nothing reads an issue's transitions off an issue read.
	//
	// IssueTransition's one $ref inside the generated set is
	// StatusDetails, already above. Its `fields` property points at
	// FieldMetadata, which is NOT generated and so stays raw — deliberate:
	// the CLI reads `hasScreen` to decide whether a transition can carry a
	// comment at all (design §3.1) and never introspects the screen's
	// individual fields, so generating that subtree would be dozens of
	// types nothing reads.
	"Transitions",
	"IssueTransition",
	// The remote-issue-link set, here for the transition set's reason:
	// GET /rest/api/3/issue/{issueIdOrKey}/remotelink is an endpoint of
	// its own (core.JiraClient.GetRemoteLinks), so RemoteIssueLink is a
	// response body rather than a member of IssueFields. A remote link is
	// NOT an issue link — different endpoint, different resource, and the
	// CLI keeps them apart as `weblink` and `link`.
	//
	// It drags in four more types, which is more than any other entry
	// here adds, and they are worth naming: Application (which remote
	// system the item lives in), RemoteObject (the item itself — url,
	// title, summary), and RemoteObject's own Icon and Status. All four
	// arrive through single-ref allOf wrappers, which this generator
	// follows (see schemaNode's own ref resolution), so leaving them out
	// would emit `object` — the field carrying the url and title the list
	// command exists to publish — as raw JSON.
	//
	// Generated rather than hand-curated, for Watchers' reason: none of
	// Comment's apply. There is no tenant-editable ADF body and no
	// tenant-dynamic field bag anywhere in this subtree — every member is
	// fixed by the platform schema — so hand-writing it would only be a
	// second place for it to drift.
	"RemoteIssueLink",
	"Application",
	"RemoteObject",
	"Icon",
	"Status",
	// Filter is the response body of GET /rest/api/3/filter/{id}
	// (core.JiraClient.GetFilter), here for Watchers' reason: it is a
	// request of its own and nothing in the generated graph points at it.
	//
	// Exactly ONE of its fifteen properties is read — `jql`, the query a
	// `--filter` selector resolves to — and the rest come along because
	// the generator takes whole schemas. None of them drags in a new type:
	// `owner` and `sharedUsers` point at User and a paged user list, both
	// OUTSIDE this set, so they stay json.RawMessage exactly as the set's
	// own rule says. So this entry adds one type and no transitive shape,
	// which is why generating it beats hand-curating a one-field struct
	// that would then be a second place for the name `jql` to drift.
	"Filter",
}

// typeOverrides redirects a property to a hand-written type. The one case
// today: the spec declares IssueBean.fields as a free-form object (issue
// fields are tenant-dynamic), so the typed model for it cannot come from the
// spec — internal/jira/fields.go curates it by hand instead.
var typeOverrides = map[string]string{
	"IssueBean.fields": "IssueFields",
}

// initialisms are name segments spelled in caps in Go identifiers. Only the
// segments that actually occur in the generated schemas need to be here;
// anything else is plain-capitalized.
var initialisms = map[string]string{
	"api":  "API",
	"id":   "ID",
	"uri":  "URI",
	"url":  "URL",
	"urls": "URLs",
	"uuid": "UUID",
}

// schemaNode is the subset of OpenAPI schema syntax the vendored platform
// document actually uses for the generated set. AdditionalProperties stays
// raw because the spec writes it as an object, an empty object, or a bool.
type schemaNode struct {
	Ref                  string                 `json:"$ref"`
	Type                 string                 `json:"type"`
	Properties           map[string]*schemaNode `json:"properties"`
	Items                *schemaNode            `json:"items"`
	AllOf                []*schemaNode          `json:"allOf"`
	AdditionalProperties json.RawMessage        `json:"additionalProperties"`
}

type openAPIDoc struct {
	Components struct {
		Schemas map[string]*schemaNode `json:"schemas"`
	} `json:"components"`
}

func main() {
	specPath := flag.String("spec", "spec/platform.json", "vendored platform OpenAPI document")
	outPath := flag.String("out", "wire_gen.go", "generated Go output file")
	flag.Parse()

	if err := run(*specPath, *outPath); err != nil {
		clog.Error().Parts(clog.PartMessage).Msg("gen-wire: " + err.Error())
		os.Exit(1)
	}
}

func run(specPath, outPath string) error {
	// A codegen tool reading the spec path it was invoked with is the whole
	// job; there is no untrusted input here.
	raw, err := os.ReadFile(specPath) //nolint:gosec // developer tool, path comes from its own -spec flag
	if err != nil {
		return err
	}

	var doc openAPIDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", specPath, err)
	}

	src, err := generate(doc.Components.Schemas)
	if err != nil {
		return err
	}

	formatted, err := format.Source(src)
	if err != nil {
		return fmt.Errorf("format generated source: %w", err)
	}

	// 0o600 satisfies the gosec write bar; the committed file keeps whatever
	// mode git already tracks because WriteFile does not chmod existing files.
	return os.WriteFile(outPath, formatted, 0o600)
}

func generate(schemas map[string]*schemaNode) ([]byte, error) {
	var b strings.Builder
	b.WriteString("// Code generated by gen-wire from spec/platform.json. DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// Regenerate with: go generate ./internal/jira\n")
	b.WriteString("package jira\n\n")
	b.WriteString("import (\n\t\"encoding/json\"\n\t\"maps\"\n)\n\n")

	for _, name := range generatedSchemas {
		node, ok := schemas[name]
		if !ok {
			return nil, fmt.Errorf("schema %q not found in spec", name)
		}
		if len(node.Properties) == 0 {
			return nil, fmt.Errorf("schema %q has no properties", name)
		}
		writeSchema(&b, name, node)
	}

	writeSchemaPropertyMap(&b)
	return []byte(b.String()), nil
}

// writeSchema emits one wire type plus its raw-capture plumbing: a
// methodless alias (so the custom (un)marshal can recurse into plain struct
// decoding), the declared-key list, and the Extra round-trip methods.
func writeSchema(b *strings.Builder, name string, node *schemaNode) {
	props := slices.Sorted(maps.Keys(node.Properties))
	alias := lowerFirst(name) + "Alias"
	keys := lowerFirst(name) + "Keys"

	fmt.Fprintf(b, "// %s mirrors the vendored Jira Platform schema %q.\n", name, name)
	fmt.Fprintf(b, "type %s struct {\n", name)
	for _, prop := range props {
		typ, note := propType(name, prop, node.Properties[prop])
		if note != "" {
			note = " // " + note
		}
		fmt.Fprintf(b, "\t%s %s `json:\"%s,omitzero\"`%s\n", fieldName(prop), typ, prop, note)
	}
	b.WriteString("\n\t// Extra carries every property of the payload this model does not\n")
	b.WriteString("\t// declare, verbatim, so unmodeled data survives a round-trip.\n")
	b.WriteString("\tExtra map[string]json.RawMessage `json:\"-\"`\n")
	b.WriteString("}\n\n")

	fmt.Fprintf(b, "type %s %s\n\n", alias, name)

	fmt.Fprintf(b, "var %s = []string{\n", keys)
	for _, prop := range props {
		fmt.Fprintf(b, "\t%q,\n", prop)
	}
	b.WriteString("}\n\n")

	fmt.Fprintf(b, "func (v %s) MarshalJSON() ([]byte, error) {\n", name)
	fmt.Fprintf(b, "\treturn marshalMergeExtra(%s(v), v.Extra, %s)\n", alias, keys)
	b.WriteString("}\n\n")

	fmt.Fprintf(b, "func (v *%s) UnmarshalJSON(data []byte) error {\n", name)
	fmt.Fprintf(b, "\treturn unmarshalKeepExtra(data, (*%s)(v), &v.Extra, %s)\n", alias, keys)
	b.WriteString("}\n\n")
}

func writeSchemaPropertyMap(b *strings.Builder) {
	b.WriteString("// wireSchemaProperties maps each generated spec schema to the property\n")
	b.WriteString("// names its wire type declares. The spec drift test diffs this against\n")
	b.WriteString("// the vendored document, so a spec bump that adds or removes a property\n")
	b.WriteString("// fails CI by name until the types are regenerated.\n")
	b.WriteString("var wireSchemaProperties = map[string][]string{\n")
	for _, name := range generatedSchemas {
		fmt.Fprintf(b, "\t%q: %sKeys,\n", name, lowerFirst(name))
	}
	b.WriteString("}\n\n")
	b.WriteString("// WireSchemaProperties reports the modeled property set per generated\n")
	b.WriteString("// schema. A clone, so no caller can disarm the drift check by mutating\n")
	b.WriteString("// the package map.\n")
	b.WriteString("func WireSchemaProperties() map[string][]string {\n")
	b.WriteString("\treturn maps.Clone(wireSchemaProperties)\n")
	b.WriteString("}\n")
}

// propType maps one property schema to the Go type of its struct field, plus
// an optional trailing comment naming what a raw escape stands in for. Every
// field is Nullable so present, explicitly-null, and absent stay distinct
// through a round-trip.
func propType(owner, prop string, node *schemaNode) (typ, note string) {
	if override, ok := typeOverrides[owner+"."+prop]; ok {
		return "Nullable[" + override + "]", "hand-curated; see fields.go"
	}

	inner, note := innerType(node)
	return "Nullable[" + inner + "]", note
}

// innerType maps a schema to a bare Go type, used both for whole properties
// and for array/map elements (which need no Nullable of their own — the
// container's presence already carries the semantics).
func innerType(node *schemaNode) (typ, note string) {
	if node == nil {
		return "json.RawMessage", "spec: untyped"
	}

	if ref := refName(node); ref != "" {
		if slices.Contains(generatedSchemas, ref) {
			return ref, ""
		}
		return "json.RawMessage", "spec: " + ref
	}

	switch node.Type {
	case "string":
		return "string", ""
	case "boolean":
		return "bool", ""
	case "integer":
		// int64 across the board: the spec mixes int32/int64 formats and a
		// single width keeps the generated surface predictable.
		return "int64", ""
	case "number":
		return "float64", ""
	case "array":
		inner, note := innerType(node.Items)
		return "[]" + inner, note
	}

	// Objects: typed additionalProperties become a map; a free-form or
	// unrepresentable shape stays raw.
	if ap := additionalProps(node); ap != nil {
		inner, note := innerType(ap)
		return "map[string]" + inner, note
	}
	return "json.RawMessage", "spec: untyped object"
}

// refName resolves the two ref spellings the platform spec uses: a direct
// $ref, and the allOf-with-single-ref wrapper it emits when a property adds
// a description to a referenced schema.
func refName(node *schemaNode) string {
	ref := node.Ref
	if ref == "" && len(node.AllOf) == 1 {
		ref = node.AllOf[0].Ref
	}
	return strings.TrimPrefix(ref, "#/components/schemas/")
}

// additionalProps decodes additionalProperties when it is a real schema.
// Booleans, empty objects, and schemas with nothing usable all mean
// "untyped" and return nil.
func additionalProps(node *schemaNode) *schemaNode {
	if len(node.AdditionalProperties) == 0 {
		return nil
	}
	var ap schemaNode
	if err := json.Unmarshal(node.AdditionalProperties, &ap); err != nil {
		return nil
	}
	if ap.Ref == "" && ap.Type == "" && len(ap.AllOf) == 0 {
		return nil
	}
	return &ap
}

// fieldName turns a JSON property name into an exported Go identifier:
// camel-case segments capitalized, initialisms in caps, and a "Field" prefix
// when the name cannot start an identifier (the avatar sizes: "16x16").
func fieldName(prop string) string {
	var out strings.Builder
	for _, seg := range camelSegments(prop) {
		if caps, ok := initialisms[strings.ToLower(seg)]; ok {
			out.WriteString(caps)
			continue
		}
		out.WriteString(strings.ToUpper(seg[:1]))
		out.WriteString(seg[1:])
	}
	name := out.String()
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		name = "Field" + name
	}
	return name
}

// lowerFirst lowers a schema name's first rune, forming the unexported
// alias/key identifiers that sit beside each generated type. Schema names
// are ASCII by construction (they come from Go-identifier-safe spec names).
func lowerFirst(s string) string {
	return strings.ToLower(s[:1]) + s[1:]
}

// camelSegments splits a camelCase JSON name at uppercase boundaries so each
// word can be capitalized or initialism-mapped independently.
func camelSegments(s string) []string {
	var segs []string
	start := 0
	for i, r := range s {
		if i > start && r >= 'A' && r <= 'Z' {
			segs = append(segs, s[start:i])
			start = i
		}
	}
	if start < len(s) {
		segs = append(segs, s[start:])
	}
	return segs
}
