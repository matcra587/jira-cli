package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/format"
	"maps"
	"os"
	"slices"
	"strings"
)

const (
	schemaPath = "schema/full.json"
	outPath    = "nodes_gen.go"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		return err
	}
	cat, err := buildCatalog(raw)
	if err != nil {
		return err
	}
	src, err := format.Source(render(cat))
	if err != nil {
		return fmt.Errorf("format generated source: %w", err)
	}
	return os.WriteFile(outPath, src, 0o600)
}

// --- schema parsing ---------------------------------------------------------

// rawDef is the subset of JSON Schema this generator understands. Anything
// it cannot classify is a hard error: a schema bump that introduces a new
// shape must fail regeneration loudly, never produce a silently thinner
// catalog.
type rawDef struct {
	Type                 string                     `json:"type"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	AdditionalProperties *bool                      `json:"additionalProperties"`
	AnyOf                []refOrSchema              `json:"anyOf"`
	AllOf                []json.RawMessage          `json:"allOf"`
	Items                json.RawMessage            `json:"items"`
	MinItems             *int                       `json:"minItems"`
	MaxItems             *int                       `json:"maxItems"`
}

type refOrSchema struct {
	Ref string `json:"$ref"`
}

type typeProp struct {
	Enum []string `json:"enum"`
}

type attrProp struct {
	// GoType is the rendered field type; Optional switches scalars to
	// pointers so an authored zero survives (width: 0 must not vanish).
	Key      string
	GoType   string
	Enum     []string
	Optional bool
}

type attrsIR struct {
	required []string
	allowed  []string
	open     bool
	props    map[string]*attrProp
}

type marksIR struct {
	max     int
	allowed []string // mark runtime types; nil = unconstrained
}

type contentIR struct {
	minItems int
	maxItems int
	refs     []string
}

type specIR struct {
	name     string
	typ      string
	variant  string
	required []string
	attrs    *attrsIR
	marks    *marksIR
	content  *contentIR
	open     bool
	isMark   bool
}

type viewIR struct {
	typ   string
	props []*attrProp
}

type catalog struct {
	sha         string
	definitions []string
	nodes       []*specIR
	marks       []*specIR
	groups      map[string][]string
	groupNames  []string
	views       []*viewIR
	markViews   []*viewIR
}

func buildCatalog(raw []byte) (*catalog, error) {
	var schema struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("parse %s: %w", schemaPath, err)
	}

	defs := map[string]*rawDef{}
	for name, body := range schema.Definitions {
		var d rawDef
		if err := json.Unmarshal(body, &d); err != nil {
			return nil, fmt.Errorf("parse definition %s: %w", name, err)
		}
		defs[name] = &d
	}

	cat := &catalog{
		sha:    fmt.Sprintf("%x", sha256.Sum256(raw)),
		groups: map[string][]string{},
	}
	cat.definitions = sortedKeys(defs)

	bases := map[string]*specIR{}
	var variantNames []string
	for _, name := range cat.definitions {
		d := defs[name]
		switch {
		case d.AllOf != nil:
			variantNames = append(variantNames, name)
		case d.AnyOf != nil:
			members, err := refNames(d.AnyOf)
			if err != nil {
				return nil, fmt.Errorf("group %s: %w", name, err)
			}
			cat.groups[name] = members
			cat.groupNames = append(cat.groupNames, name)
		case d.Type == "array":
			members, _, _, err := arrayMembers(name, d)
			if err != nil {
				return nil, err
			}
			cat.groups[name] = members
			cat.groupNames = append(cat.groupNames, name)
		case d.Type == "object":
			spec, err := baseSpec(name, d, defs)
			if err != nil {
				return nil, err
			}
			bases[name] = spec
		default:
			return nil, fmt.Errorf("definition %s: unrecognized shape", name)
		}
	}

	for _, name := range variantNames {
		spec, err := variantSpec(name, defs[name], bases, defs)
		if err != nil {
			return nil, err
		}
		if spec.isMark {
			cat.marks = append(cat.marks, spec)
		} else {
			cat.nodes = append(cat.nodes, spec)
		}
	}
	for _, name := range sortedKeys(bases) {
		if bases[name].isMark {
			cat.marks = append(cat.marks, bases[name])
		} else {
			cat.nodes = append(cat.nodes, bases[name])
		}
	}
	slices.SortFunc(cat.nodes, func(a, b *specIR) int { return strings.Compare(a.name, b.name) })
	slices.SortFunc(cat.marks, func(a, b *specIR) int { return strings.Compare(a.name, b.name) })

	if err := nameVariants(cat.nodes); err != nil {
		return nil, err
	}
	if err := nameVariants(cat.marks); err != nil {
		return nil, err
	}
	cat.views = buildViews(cat.nodes)
	cat.markViews = buildViews(cat.marks)
	return cat, nil
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

func refNames(refs []refOrSchema) ([]string, error) {
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		name, err := refName(r.Ref)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

func refName(ref string) (string, error) {
	name, ok := strings.CutPrefix(ref, "#/definitions/")
	if !ok || name == "" {
		return "", fmt.Errorf("unsupported $ref %q", ref)
	}
	return name, nil
}

// arrayMembers reads an array schema's items into member definition names.
func arrayMembers(name string, d *rawDef) (members []string, minItems, maxItems int, err error) {
	minItems, maxItems = 0, -1
	if d.MinItems != nil {
		minItems = *d.MinItems
	}
	if d.MaxItems != nil {
		maxItems = *d.MaxItems
	}
	if d.Items == nil {
		return nil, minItems, maxItems, nil
	}
	// Draft-04 tuple form: items may be a list of positional schemas
	// (blockTaskItem). The positions' member sets are unioned — the
	// capability matrix records what may appear, not where.
	var tuple []json.RawMessage
	if err := json.Unmarshal(d.Items, &tuple); err != nil {
		tuple = []json.RawMessage{d.Items}
	}
	for _, item := range tuple {
		got, err := itemMembers(name, item)
		if err != nil {
			return nil, 0, 0, err
		}
		for _, m := range got {
			if !slices.Contains(members, m) {
				members = append(members, m)
			}
		}
	}
	return members, minItems, maxItems, nil
}

func itemMembers(name string, item json.RawMessage) ([]string, error) {
	var items struct {
		Ref   string        `json:"$ref"`
		AnyOf []refOrSchema `json:"anyOf"`
	}
	if err := json.Unmarshal(item, &items); err != nil {
		return nil, fmt.Errorf("definition %s: items: %w", name, err)
	}
	switch {
	case items.Ref != "":
		ref, err := refName(items.Ref)
		if err != nil {
			return nil, fmt.Errorf("definition %s: %w", name, err)
		}
		return []string{ref}, nil
	case items.AnyOf != nil:
		members, err := refNames(items.AnyOf)
		if err != nil {
			return nil, fmt.Errorf("definition %s: %w", name, err)
		}
		return members, nil
	}
	return nil, fmt.Errorf("definition %s: items has neither $ref nor anyOf", name)
}

func baseSpec(name string, d *rawDef, defs map[string]*rawDef) (*specIR, error) {
	var tp typeProp
	rawType, ok := d.Properties["type"]
	if !ok {
		return nil, fmt.Errorf("definition %s: object without a type property", name)
	}
	if err := json.Unmarshal(rawType, &tp); err != nil || len(tp.Enum) != 1 {
		return nil, fmt.Errorf("definition %s: type property is not a single-value enum", name)
	}
	spec := &specIR{
		name:     name,
		typ:      tp.Enum[0],
		required: d.Required,
		open:     d.AdditionalProperties != nil && *d.AdditionalProperties,
		isMark:   strings.HasSuffix(name, "_mark"),
	}
	for _, key := range sortedKeys(d.Properties) {
		var err error
		switch key {
		case "type", "text", "version":
			// type is the discriminator; text and version are plain
			// scalars with nothing capability-shaped to record.
		case "attrs":
			spec.attrs, err = parseAttrs(name, d.Properties[key])
		case "marks":
			spec.marks, err = parseMarks(name, d.Properties[key], defs)
		case "content":
			spec.content, err = parseContent(name, d.Properties[key], defs)
		default:
			err = fmt.Errorf("definition %s: unhandled property %q", name, key)
		}
		if err != nil {
			return nil, err
		}
	}
	return spec, nil
}

func variantSpec(name string, d *rawDef, bases map[string]*specIR, defs map[string]*rawDef) (*specIR, error) {
	if len(d.AllOf) < 2 {
		return nil, fmt.Errorf("definition %s: allOf with fewer than two members", name)
	}
	var base refOrSchema
	if err := json.Unmarshal(d.AllOf[0], &base); err != nil || base.Ref == "" {
		return nil, fmt.Errorf("definition %s: allOf[0] is not a $ref", name)
	}
	baseName, err := refName(base.Ref)
	if err != nil {
		return nil, fmt.Errorf("definition %s: %w", name, err)
	}
	baseIR, ok := bases[baseName]
	if !ok {
		return nil, fmt.Errorf("definition %s: base %s is not a base definition", name, baseName)
	}

	spec := &specIR{
		name:     name,
		typ:      baseIR.typ,
		required: slices.Clone(baseIR.required),
		attrs:    baseIR.attrs,
		marks:    baseIR.marks,
		content:  baseIR.content,
		open:     baseIR.open,
		isMark:   baseIR.isMark,
	}
	spec.variant = variantLabel(name, baseName)

	for _, rawOverlay := range d.AllOf[1:] {
		var o rawDef
		if err := json.Unmarshal(rawOverlay, &o); err != nil {
			return nil, fmt.Errorf("definition %s: overlay: %w", name, err)
		}
		for _, req := range o.Required {
			if !slices.Contains(spec.required, req) {
				spec.required = append(spec.required, req)
			}
		}
		for _, key := range sortedKeys(o.Properties) {
			var err error
			switch key {
			case "attrs":
				spec.attrs, err = parseAttrs(name, o.Properties[key])
			case "marks":
				spec.marks, err = parseMarks(name, o.Properties[key], defs)
			case "content":
				spec.content, err = parseContent(name, o.Properties[key], defs)
			default:
				err = fmt.Errorf("definition %s: overlay of unhandled property %q", name, key)
			}
			if err != nil {
				return nil, err
			}
		}
	}
	return spec, nil
}

// variantLabel derives the variant name from the definition names:
// paragraph_with_no_marks_node over paragraph_node → "with_no_marks".
func variantLabel(name, baseName string) string {
	trimmed := trimDefSuffix(name)
	basePrefix := trimDefSuffix(baseName)
	if rest, ok := strings.CutPrefix(trimmed, basePrefix+"_"); ok {
		return rest
	}
	return trimmed
}

func trimDefSuffix(name string) string {
	name = strings.TrimSuffix(name, "_node")
	return strings.TrimSuffix(name, "_mark")
}

// nameVariants labels the base definitions that share a runtime type
// (codeBlock_node vs codeBlock_root_only_node): the shortest name is the
// primary, the rest get variant labels relative to it.
func nameVariants(specs []*specIR) error {
	byType := map[string][]*specIR{}
	for _, s := range specs {
		if s.variant == "" {
			byType[s.typ] = append(byType[s.typ], s)
		}
	}
	for _, group := range byType {
		if len(group) == 1 {
			continue
		}
		primary := group[0]
		for _, s := range group[1:] {
			if len(s.name) < len(primary.name) {
				primary = s
			}
		}
		for _, s := range group {
			if s != primary {
				s.variant = variantLabel(s.name, primary.name)
				if s.variant == trimDefSuffix(s.name) {
					return fmt.Errorf("definition %s: cannot derive variant label from %s", s.name, primary.name)
				}
			}
		}
	}
	return nil
}

func parseAttrs(defName string, raw json.RawMessage) (*attrsIR, error) {
	var probe struct {
		AnyOf []json.RawMessage `json:"anyOf"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("definition %s: attrs: %w", defName, err)
	}
	// len, not a nil check: an explicit empty `"anyOf": []` would leave the
	// merge loop unentered and merged nil — a panic, in a generator whose
	// contract is fail-loudly.
	if len(probe.AnyOf) == 0 {
		return parseAttrsObject(defName, raw)
	}
	// An attrs union (mediaSingle's percentage vs pixel width): allowed
	// keys are the union of branches, required keys the intersection —
	// only what holds regardless of branch is safe to assert shallowly.
	var merged *attrsIR
	for _, branch := range probe.AnyOf {
		ir, err := parseAttrsObject(defName, branch)
		if err != nil {
			return nil, err
		}
		if merged == nil {
			merged = ir
			continue
		}
		merged.required = intersect(merged.required, ir.required)
		for _, key := range ir.allowed {
			if !slices.Contains(merged.allowed, key) {
				merged.allowed = append(merged.allowed, key)
			}
		}
		merged.open = merged.open || ir.open
		for key, prop := range ir.props {
			if _, ok := merged.props[key]; !ok {
				merged.props[key] = prop
			}
		}
	}
	slices.Sort(merged.allowed)
	slices.Sort(merged.required)
	return merged, nil
}

func parseAttrsObject(defName string, raw json.RawMessage) (*attrsIR, error) {
	var obj rawDef
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("definition %s: attrs: %w", defName, err)
	}
	ir := &attrsIR{
		required: slices.Clone(obj.Required),
		allowed:  sortedKeys(obj.Properties),
		open:     obj.AdditionalProperties != nil && *obj.AdditionalProperties,
		props:    map[string]*attrProp{},
	}
	slices.Sort(ir.required)
	for _, key := range ir.allowed {
		prop, err := parseAttrProp(key, obj.Properties[key])
		if err != nil {
			return nil, err
		}
		prop.Optional = !slices.Contains(ir.required, key)
		ir.props[key] = prop
	}
	return ir, nil
}

func parseAttrProp(key string, raw json.RawMessage) (*attrProp, error) {
	var p struct {
		Type string            `json:"type"`
		Enum []json.RawMessage `json:"enum"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		// A type union or nested schema — carry it raw.
		return &attrProp{Key: key, GoType: "json.RawMessage"}, nil
	}
	prop := &attrProp{Key: key}
	for _, e := range p.Enum {
		var s string
		if err := json.Unmarshal(e, &s); err != nil {
			// Non-string enum member; fall back to the declared type.
			prop.Enum = nil
			break
		}
		prop.Enum = append(prop.Enum, s)
	}
	switch {
	case p.Type == "string" || (p.Type == "" && prop.Enum != nil):
		prop.GoType = "string"
	case p.Type == "number":
		prop.GoType = "float64"
	case p.Type == "integer":
		prop.GoType = "int"
	case p.Type == "boolean":
		prop.GoType = "bool"
	default:
		prop.GoType = "json.RawMessage"
	}

	return prop, nil
}

func parseMarks(defName string, raw json.RawMessage, defs map[string]*rawDef) (*marksIR, error) {
	var m rawDef
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("definition %s: marks: %w", defName, err)
	}
	ir := &marksIR{max: -1}
	if m.MaxItems != nil {
		ir.max = *m.MaxItems
	}
	if m.Items == nil {
		return ir, nil
	}
	members, _, _, err := arrayMembers(defName, &m)
	if err != nil {
		return nil, err
	}
	for _, member := range members {
		markDef, ok := defs[member]
		if !ok {
			return nil, fmt.Errorf("definition %s: marks item %s not found", defName, member)
		}
		var tp typeProp
		if err := json.Unmarshal(markDef.Properties["type"], &tp); err != nil || len(tp.Enum) != 1 {
			return nil, fmt.Errorf("definition %s: marks item %s has no single type", defName, member)
		}
		ir.allowed = append(ir.allowed, tp.Enum[0])
	}
	return ir, nil
}

func parseContent(defName string, raw json.RawMessage, defs map[string]*rawDef) (*contentIR, error) {
	var c struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("definition %s: content: %w", defName, err)
	}
	if c.Ref != "" {
		// Content delegated to a named array definition (table cells).
		name, err := refName(c.Ref)
		if err != nil {
			return nil, fmt.Errorf("definition %s: %w", defName, err)
		}
		group, ok := defs[name]
		if !ok || group.Type != "array" {
			return nil, fmt.Errorf("definition %s: content $ref %s is not an array definition", defName, name)
		}
		minItems, maxItems := 0, -1
		if group.MinItems != nil {
			minItems = *group.MinItems
		}
		if group.MaxItems != nil {
			maxItems = *group.MaxItems
		}
		return &contentIR{minItems: minItems, maxItems: maxItems, refs: []string{name}}, nil
	}
	var arr rawDef
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("definition %s: content: %w", defName, err)
	}
	members, minItems, maxItems, err := arrayMembers(defName, &arr)
	if err != nil {
		return nil, err
	}
	return &contentIR{minItems: minItems, maxItems: maxItems, refs: members}, nil
}

func intersect(a, b []string) []string {
	var out []string
	for _, v := range a {
		if slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	return out
}

// buildViews merges each runtime type's definitions into one typed view.
// Attr properties merge across a type's definitions; the primary
// definition wins a conflict.
func buildViews(specs []*specIR) []*viewIR {
	byType := map[string][]*specIR{}
	var order []string
	for _, s := range specs {
		if _, ok := byType[s.typ]; !ok {
			order = append(order, s.typ)
		}
		byType[s.typ] = append(byType[s.typ], s)
	}
	slices.Sort(order)
	views := make([]*viewIR, 0, len(order))
	for _, typ := range order {
		view := &viewIR{typ: typ}
		props := map[string]*attrProp{}
		group := byType[typ]
		slices.SortFunc(group, func(a, b *specIR) int {
			if (a.variant == "") != (b.variant == "") {
				if a.variant == "" {
					return -1
				}
				return 1
			}
			return strings.Compare(a.name, b.name)
		})
		for _, s := range group {
			if s.attrs == nil {
				continue
			}
			for key, prop := range s.attrs.props {
				if _, ok := props[key]; !ok {
					props[key] = prop
				}
			}
		}
		for _, key := range sortedKeys(props) {
			view.props = append(view.props, props[key])
		}
		views = append(views, view)
	}
	return views
}

// --- naming -----------------------------------------------------------------

var initialisms = map[string]string{
	"id": "ID", "url": "URL", "uuid": "UUID", "guid": "GUID", "adf": "ADF",
}

// exportedName turns a camelCase schema token into an exported Go name,
// fixing initialisms on word boundaries (localId → LocalID).
func exportedName(token string) string {
	token = strings.TrimLeft(token, "_")
	var b strings.Builder
	for _, word := range splitCamel(token) {
		// splitCamel always appends the trailing segment, so an empty
		// token (a schema key of underscores) yields one empty word —
		// skip it instead of panicking on word[:1].
		if word == "" {
			continue
		}
		if fixed, ok := initialisms[strings.ToLower(word)]; ok {
			b.WriteString(fixed)
			continue
		}
		b.WriteString(strings.ToUpper(word[:1]))
		b.WriteString(word[1:])
	}
	return b.String()
}

func splitCamel(s string) []string {
	var words []string
	start := 0
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			words = append(words, s[start:i])
			start = i
		}
	}
	return append(words, s[start:])
}

// --- rendering --------------------------------------------------------------

func render(cat *catalog) []byte {
	var b bytes.Buffer
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	p("// Code generated by internal/adf/gen from schema/full.json; DO NOT EDIT.")
	p("")
	p("package adf")
	p("")
	p("import (")
	p("\t\"encoding/json\"")
	p("\t\"fmt\"")
	p(")")
	p("")
	p("// SchemaSHA256 is the digest of the vendored schema this file was")
	p("// generated from; the drift test recomputes it.")
	p("const SchemaSHA256 = %q", cat.sha)
	p("")
	p("// SchemaDefinitions lists every definition in the vendored schema.")
	p("var SchemaDefinitions = []string{")
	for _, name := range cat.definitions {
		p("\t%q,", name)
	}
	p("}")

	renderTypeConsts(p, "NodeType", cat.views)
	renderTypeConsts(p, "MarkType", cat.markViews)

	p("")
	p("// Groups maps each schema content group to its member definitions.")
	p("var Groups = map[string][]string{")
	for _, name := range cat.groupNames {
		// Bare element literals — gofumpt strips the redundant []string.
		p("\t%q: %s,", name, strings.TrimPrefix(stringSliceLit(cat.groups[name]), "[]string"))
	}
	p("}")

	renderSpecs(p, cat)
	renderViews(p, cat.views, false)
	renderViews(p, cat.markViews, true)
	renderDispatch(p, cat.views, false)
	renderDispatch(p, cat.markViews, true)

	return b.Bytes()
}

func renderTypeConsts(p func(string, ...any), kind string, views []*viewIR) {
	p("")
	p("// Known %s values, one per runtime type in the schema.", kind)
	p("const (")
	for _, v := range views {
		p("\t%s %s = %q", constName(kind, v.typ), kind, v.typ)
	}
	p(")")
}

func constName(kind, typ string) string {
	return kind + exportedName(typ)
}

func renderSpecs(p func(string, ...any), cat *catalog) {
	p("")
	p("// NodeSpecs holds one capability row per node definition.")
	p("var NodeSpecs = map[string]*NodeSpec{")
	for _, s := range cat.nodes {
		p("\t%q: {", s.name)
		p("\t\tName: %q,", s.name)
		p("\t\tType: %s,", constName("NodeType", s.typ))
		if s.variant != "" {
			p("\t\tVariant: %q,", s.variant)
		}
		if len(s.required) > 0 {
			p("\t\tRequired: %s,", stringSliceLit(s.required))
		}
		renderAttrsSpec(p, s.attrs)
		if s.marks != nil {
			if s.marks.allowed == nil {
				p("\t\tMarks: &MarksSpec{Max: %d},", s.marks.max)
			} else {
				p("\t\tMarks: &MarksSpec{Max: %d, Allowed: %s},", s.marks.max, markTypeSliceLit(s.marks.allowed))
			}
		}
		if s.content != nil {
			p("\t\tContent: &ContentSpec{MinItems: %d, MaxItems: %d, Refs: %s},",
				s.content.minItems, s.content.maxItems, stringSliceLit(s.content.refs))
		}
		if s.open {
			p("\t\tOpen: true,")
		}
		p("\t},")
	}
	p("}")

	p("")
	p("// MarkSpecs holds one capability row per mark definition.")
	p("var MarkSpecs = map[string]*MarkSpec{")
	for _, s := range cat.marks {
		p("\t%q: {", s.name)
		p("\t\tName: %q,", s.name)
		p("\t\tType: %s,", constName("MarkType", s.typ))
		renderAttrsSpec(p, s.attrs)
		p("\t},")
	}
	p("}")
}

func renderAttrsSpec(p func(string, ...any), a *attrsIR) {
	if a == nil {
		return
	}
	p("\t\tAttrs: &AttrsSpec{")
	if len(a.required) > 0 {
		p("\t\t\tRequired: %s,", stringSliceLit(a.required))
	}
	if len(a.allowed) > 0 {
		p("\t\t\tAllowed: %s,", stringSliceLit(a.allowed))
	}
	if a.open {
		p("\t\t\tOpen: true,")
	}
	p("\t\t},")
}

func renderViews(p func(string, ...any), views []*viewIR, mark bool) {
	kindWord, underlying, accessor := "node", "Node", "Node"
	if mark {
		kindWord, underlying, accessor = "mark", "Mark", "Mark"
	}
	for _, v := range views {
		view := viewName(v.typ, mark)
		p("")
		p("// %s is the typed view of a %q %s.", view, v.typ, kindWord)
		p("type %s struct {", view)
		p("\t%s *%s", kindWord, underlying)
		if len(v.props) > 0 {
			p("\tAttrs %sAttrs", view)
		}
		p("}")
		if len(v.props) > 0 {
			p("")
			p("// %sAttrs is the typed attrs of a %q %s.", view, v.typ, kindWord)
			p("type %sAttrs struct {", view)
			for _, prop := range v.props {
				renderAttrField(p, prop)
			}
			p("}")
		}
		p("")
		p("// As%s types n as a %q %s, decoding its attrs.", view, v.typ, kindWord)
		p("func As%s(%s *%s) (*%s, error) {", view, kindWord[:1], underlying, view)
		p("\tif %s.Type() != %q {", kindWord[:1], v.typ)
		p("\t\treturn nil, fmt.Errorf(\"adf: %s is %%q, not %%q\", %s.Type(), %q)", kindWord, kindWord[:1], v.typ)
		p("\t}")
		p("\tv := &%s{%s: %s}", view, kindWord, kindWord[:1])
		if len(v.props) > 0 {
			p("\tif raw := %s.Attrs(); raw != nil {", kindWord[:1])
			p("\t\tif err := json.Unmarshal(raw, &v.Attrs); err != nil {")
			p("\t\t\treturn nil, fmt.Errorf(\"adf: %s attrs: %%w\", err)", v.typ)
			p("\t\t}")
			p("\t}")
		}
		p("\treturn v, nil")
		p("}")
		p("")
		p("// %s returns the underlying substrate %s.", accessor, kindWord)
		p("func (v *%s) %s() *%s { return v.%s }", view, accessor, underlying, kindWord)
		p("")
		if mark {
			p("// MarkType returns %s.", constName("MarkType", v.typ))
			p("func (v *%s) MarkType() MarkType { return %s }", view, constName("MarkType", v.typ))
			p("")
			p("func (v *%s) typedMark() {}", view)
		} else {
			p("// NodeType returns %s.", constName("NodeType", v.typ))
			p("func (v *%s) NodeType() NodeType { return %s }", view, constName("NodeType", v.typ))
			p("")
			p("func (v *%s) typedNode() {}", view)
		}
	}
}

func renderAttrField(p func(string, ...any), prop *attrProp) {
	goType := prop.GoType
	tag := prop.Key
	if prop.Optional {
		if goType == "float64" || goType == "int" || goType == "bool" {
			goType = "*" + goType
		}
		tag += ",omitempty"
	}
	line := fmt.Sprintf("\t%s %s `json:%q`", exportedName(prop.Key), goType, tag)
	if len(prop.Enum) > 0 {
		// Quote the values: enums include control characters ("\n" on
		// hardBreak.text), which would break the comment line verbatim.
		quoted := make([]string, len(prop.Enum))
		for i, v := range prop.Enum {
			quoted[i] = fmt.Sprintf("%q", v)
		}
		line += " // one of: " + strings.Join(quoted, ", ")
	}
	p("%s", line)
}

func renderDispatch(p func(string, ...any), views []*viewIR, mark bool) {
	p("")
	if mark {
		p("// MarkTyped returns the typed view for m, or ErrUnknownMarkType for")
		p("// a mark type outside the generated catalog.")
		p("func MarkTyped(m *Mark) (TypedMark, error) {")
		p("\tswitch m.Type() {")
		for _, v := range views {
			p("\tcase %q:", v.typ)
			p("\t\treturn As%s(m)", viewName(v.typ, true))
		}
		p("\t}")
		p("\treturn nil, fmt.Errorf(\"%%w: %%q\", ErrUnknownMarkType, m.Type())")
		p("}")
		return
	}
	p("// Typed returns the typed view for n, or ErrUnknownNodeType for a")
	p("// node type outside the generated catalog.")
	p("func Typed(n *Node) (TypedNode, error) {")
	p("\tswitch n.Type() {")
	for _, v := range views {
		p("\tcase %q:", v.typ)
		p("\t\treturn As%s(n)", viewName(v.typ, false))
	}
	p("\t}")
	p("\treturn nil, fmt.Errorf(\"%%w: %%q\", ErrUnknownNodeType, n.Type())")
	p("}")
}

func viewName(typ string, mark bool) string {
	name := exportedName(typ)
	if mark {
		name += "Mark"
	}
	return name
}

func stringSliceLit(values []string) string {
	if values == nil {
		return "nil"
	}
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

func markTypeSliceLit(types []string) string {
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = constName("MarkType", t)
	}
	return "[]MarkType{" + strings.Join(names, ", ") + "}"
}
