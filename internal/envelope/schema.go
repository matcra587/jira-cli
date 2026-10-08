package envelope

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
)

// SchemaOf derives the published JSON-Schema map for an Output struct
// from its type: object properties from exported fields' json tags,
// required from fields without omitempty, recursion through structs and
// slices, maps and `any` as opaque values. overrides deep-merges on top
// — the home for descriptions, formats, and enum refinements the type
// system cannot express — so the shape always comes from the struct and
// only prose comes from prose.
//
// Honest limit (design §10): SchemaOf shares the Go declaration with
// the data it describes, so it can never catch the declaration being
// wrong — it cannot prove conditional presence ("preview appears iff
// dry-run"), required-non-zero, or variant shapes. It publishes what
// the struct says; deep validation against independently written
// expectations arrives with the audit harnesses.
//
// Derivation rules, chosen to match how encoding/json actually emits:
//   - json:"-" fields are skipped; unexported fields are skipped.
//   - omitempty means optional (absent from required).
//   - a pointer, slice, or map without omitempty marshals as null when
//     nil, so its type gains the "null" alternative.
//   - embedded structs inline their fields, as encoding/json does.
func SchemaOf(v any, overrides map[string]any) map[string]any {
	t := reflect.TypeOf(v)
	if t == nil {
		// A nil registration reaches here only through programmer error;
		// an unconstrained schema beats a reflect panic at the first
		// schema call, and the registration guard catches it.
		return mergeSchema(map[string]any{}, overrides)
	}
	return mergeSchema(schemaOfType(t, map[reflect.Type]bool{}), overrides)
}

// schemaOfType derives one type's schema. visited holds the struct types
// on the CURRENT derivation path: a self-referential type would recurse
// forever, so a revisit collapses to an opaque object — the same posture
// as a map field. Path-scoped (unmarked on return), so a type used twice
// as siblings still expands both times.
func schemaOfType(t reflect.Type, visited map[reflect.Type]bool) map[string]any {
	// RawMessage emits the JSON it contains, including scalar and null values.
	// Its Go byte-slice representation is not the published wire shape.
	if t == reflect.TypeFor[json.RawMessage]() {
		return map[string]any{}
	}
	// A wrapper whose MarshalJSON emits something other than its own field
	// layout must be schematized by what it EMITS. jira.Nullable[T] is the
	// live case — every generated wire field is one, and publishing its
	// {Set,Null,Value} internals would describe a shape no agent ever
	// sees. Detected structurally — a Marshaler generic named Nullable[…]
	// — rather than by naming jira.Nullable: schema derivation then works
	// for ANY wrapper of that shape, and this file needs no dependency on
	// the wire package to describe one. (A payload type elsewhere in this
	// package does import internal/jira for its passthrough fields; that
	// is a different edge, and internal/jira is itself a leaf, so neither
	// costs the package its cycle-free position — see doc.go.)
	if inner, ok := nullableElem(t); ok {
		return nullable(schemaOfType(inner, visited))
	}
	switch t.Kind() {
	case reflect.Pointer:
		return schemaOfType(t.Elem(), visited)
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Interface {
			return map[string]any{"type": "array"}
		}
		return map[string]any{"type": "array", "items": schemaOfType(t.Elem(), visited)}
	case reflect.Map:
		// Opaque by declaration: a dynamic map is caller data, not
		// contract surface the schema could promise keys for.
		return map[string]any{"type": "object"}
	case reflect.Interface:
		// any: no type constraint the derivation could state.
		return map[string]any{}
	case reflect.Struct:
		if visited[t] {
			return map[string]any{"type": "object"}
		}
		visited[t] = true
		defer delete(visited, t)
		var candidates []fieldCandidate
		collectStructFields(t, 0, &candidates, visited)
		properties := map[string]any{}
		required := []string{}
		for _, c := range resolveFields(candidates) {
			properties[c.name] = c.schema
			if !c.omitempty {
				required = append(required, c.name)
			}
		}
		slices.Sort(required)
		schema := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	default:
		return map[string]any{}
	}
}

// fieldCandidate is one visible field before name-conflict resolution —
// the derivation mirrors encoding/json, so candidates carry the embedding
// depth and whether their name came from a tag.
type fieldCandidate struct {
	name      string
	depth     int
	tagged    bool
	omitempty bool
	schema    map[string]any
}

// collectStructFields gathers every promotable field with its embedding
// depth. Unexported fields are skipped — EXCEPT an untagged embedded
// struct with an unexported type name, whose exported fields
// encoding/json still promotes.
func collectStructFields(t reflect.Type, depth int, out *[]fieldCandidate, visited map[reflect.Type]bool) {
	for f := range t.Fields() {
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct && (f.IsExported() || f.Type.Kind() != reflect.Pointer) {
				collectStructFields(et, depth+1, out, visited)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		// Both options permit absence. In particular, Nullable.IsZero makes
		// an unset omitzero field disappear while explicit null stays present.
		omitempty := hasOpt(opts, "omitempty") || hasOpt(opts, "omitzero")
		fieldSchema := schemaOfType(f.Type, visited)
		switch f.Type.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map:
			// Nil pointers, slices, and maps all marshal as null when the
			// field lacks an omission option, so the type must admit it.
			if !omitempty {
				fieldSchema = nullable(fieldSchema)
			}
		default:
		}
		*out = append(*out, fieldCandidate{
			name:      name,
			depth:     depth,
			tagged:    tag != "" && !strings.HasPrefix(tag, ","),
			omitempty: omitempty,
			schema:    fieldSchema,
		})
	}
}

// resolveFields applies encoding/json's visibility rules per name: the
// shallowest depth wins; at equal shallowest depth a single tagged field
// beats untagged ones; otherwise the name is dropped entirely — exactly
// the conflicts encoding/json silently omits from its output. Publishing
// anything else would promise a field the wire never carries.
func resolveFields(candidates []fieldCandidate) []fieldCandidate {
	byName := map[string][]fieldCandidate{}
	order := []string{}
	for _, c := range candidates {
		if _, seen := byName[c.name]; !seen {
			order = append(order, c.name)
		}
		byName[c.name] = append(byName[c.name], c)
	}

	var out []fieldCandidate
	for _, name := range order {
		group := byName[name]
		minDepth := group[0].depth
		for _, c := range group[1:] {
			minDepth = min(minDepth, c.depth)
		}
		var shallow, tagged []fieldCandidate
		for _, c := range group {
			if c.depth != minDepth {
				continue
			}
			shallow = append(shallow, c)
			if c.tagged {
				tagged = append(tagged, c)
			}
		}
		switch {
		case len(shallow) == 1:
			out = append(out, shallow[0])
		case len(tagged) == 1:
			out = append(out, tagged[0])
		default:
			// Ambiguous: encoding/json drops the field; so do we.
		}
	}
	return out
}

func hasOpt(opts, want string) bool {
	return slices.Contains(strings.Split(opts, ","), want)
}

func nullable(schema map[string]any) map[string]any {
	switch t := schema["type"].(type) {
	case string:
		schema["type"] = []string{t, "null"}
	case nil:
		// no type constraint: already accepts null
	}
	return schema
}

// mergeSchema deep-merges override onto base and returns base. Map values
// merge recursively; anything else in the override replaces the derived
// value — the escape hatch for formats, enums, and hand-tuned types.
//
// Structural keys are guarded: an override's "properties" or "items"
// merges where the derived schema already carries that key, or where the
// derived node is a bare any ({}, no type) — the deliberate escape hatch
// an `any` field produces, whose shape the registration override
// supplies. What stays forbidden is inventing structure on a node the
// derivation typed as an opaque object (a map field, a cycle cut): a
// partial invented properties map would turn "anything allowed" into
// "only these keys", contradicting what the code really emits. Opacity
// is judged on the node as derived — before this loop applies any
// override keys — so map iteration order cannot change the outcome.
func mergeSchema(base, override map[string]any) map[string]any {
	_, baseTyped := base["type"]
	for key, ov := range override {
		bm, baseIsMap := base[key].(map[string]any)
		om, overrideIsMap := ov.(map[string]any)
		if key == "properties" || key == "items" {
			if !overrideIsMap {
				continue
			}
			if !baseIsMap {
				if baseTyped {
					continue // opaque by derivation: prose only
				}
				base[key] = cloneSchema(om) // a copy: aliasing the override table would let schema consumers pollute it
				continue
			}
		}
		if baseIsMap && overrideIsMap {
			base[key] = mergeSchema(bm, om)
			continue
		}
		base[key] = ov
	}
	return base
}

// cloneSchema deep-copies a schema node so returned schemas never alias
// registration override tables.
func cloneSchema(node map[string]any) map[string]any {
	out := make(map[string]any, len(node))
	for k, v := range node {
		out[k] = cloneSchemaValue(v)
	}
	return out
}

func cloneSchemaValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneSchema(value)
	case []any:
		out := slices.Clone(value)
		for i, child := range out {
			out[i] = cloneSchemaValue(child)
		}
		return out
	case []string:
		return slices.Clone(value)
	default:
		return value
	}
}

// nullableElem reports whether t is a Nullable[T] wrapper and returns T.
// The match is structural: a struct named "Nullable[...]" carrying the
// Set/Null/Value trio and implementing json.Marshaler.
func nullableElem(t reflect.Type) (reflect.Type, bool) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || !strings.HasPrefix(t.Name(), "Nullable[") {
		return nil, false
	}
	value, hasValue := t.FieldByName("Value")
	_, hasSet := t.FieldByName("Set")
	_, hasNull := t.FieldByName("Null")
	if !hasValue || !hasSet || !hasNull {
		return nil, false
	}
	if !t.Implements(marshalerType) && !reflect.PointerTo(t).Implements(marshalerType) {
		return nil, false
	}
	return value.Type, true
}

var marshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
