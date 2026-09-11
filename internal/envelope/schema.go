package envelope

import (
	"reflect"
	"strings"
)

// OpenSchema marks a wire type whose JSON object legitimately carries keys
// beyond its named members (a reviewed dynamic boundary, e.g. Jira's
// tenant-defined fields block). SchemaOf publishes such a type with
// `additionalProperties: true`, and the conformance guardrail accepts
// undeclared keys inside it instead of reporting drift.
type OpenSchema interface {
	OpenSchemaProperties() string
}

// SchemaOf derives the published JSON-Schema map for an Output struct from
// its type: object properties from exported fields' json tags, `required`
// from fields without omitempty, recursion through structs and slices,
// `map[string]…` and `any` as opaque objects/values. `overrides` deep-merges
// on top — the home for descriptions, formats, and enum refinements the
// type system cannot express — so the shape always comes from the struct
// and only prose comes from prose.
//
// Derivation rules, chosen to match how encoding/json actually emits:
//   - json:"-" fields are skipped; unexported fields are skipped.
//   - omitempty ⇒ optional (absent from required).
//   - a pointer without omitempty marshals as null when nil ⇒ type gains
//     the "null" alternative.
//   - embedded structs inline their fields, as encoding/json does.
func SchemaOf(v any, overrides map[string]any) map[string]any {
	t := reflect.TypeOf(v)
	if t == nil {
		// A nil registration reaches here only through programmer error;
		// returning an unconstrained schema beats a reflect panic at the
		// first `agent schema` call.
		return mergeSchema(map[string]any{}, overrides)
	}
	schema := schemaOfType(t, map[reflect.Type]bool{})
	return mergeSchema(schema, overrides)
}

// schemaOfType derives one type's schema. visited holds the struct types on
// the CURRENT derivation path: a self-referential type (ADF's Node.Content
// []Node) would otherwise recurse forever, so a revisit collapses to an
// opaque object — the same posture as map fields. Path-scoped (unmarked on
// return), so a type used twice as siblings still expands both times.
func schemaOfType(t reflect.Type, visited map[reflect.Type]bool) map[string]any {
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
		// Opaque by declaration: dynamic maps are caller data, not contract
		// surface; the conformance walker treats property-less objects the
		// same way.
		return map[string]any{"type": "object"}
	case reflect.Interface:
		// any: no type constraint.
		return map[string]any{}
	case reflect.Struct:
		if visited[t] {
			return map[string]any{"type": "object"}
		}
		visited[t] = true
		defer delete(visited, t)
		properties := map[string]any{}
		required := []string{}
		collectStructFields(t, properties, &required, visited)
		schema := map[string]any{"type": "object", "properties": properties}
		// A type that declares itself an open schema (jira.IssueFields:
		// tenant-defined customfield_* keys plus raw unmodeled system
		// fields ride beside the named members) publishes
		// additionalProperties so the conformance walker and consumers
		// know undeclared keys are contract, not drift.
		if _, open := reflect.New(t).Interface().(OpenSchema); open {
			schema["additionalProperties"] = true
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	default:
		return map[string]any{}
	}
}

func collectStructFields(t reflect.Type, properties map[string]any, required *[]string, visited map[reflect.Type]bool) {
	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			// Embedded struct without its own json name: fields inline.
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				collectStructFields(et, properties, required, visited)
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		omitempty := hasOpt(opts, "omitempty")
		fieldSchema := schemaOfType(f.Type, visited)
		switch f.Type.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map:
			// Nil pointers, slices, and maps all marshal as null when the
			// field lacks omitempty.
			if !omitempty {
				fieldSchema = nullable(fieldSchema)
			}
		default:
		}
		properties[name] = fieldSchema
		if !omitempty {
			*required = append(*required, name)
		}
	}
}

func hasOpt(opts, want string) bool {
	for opts != "" {
		var o string
		o, opts, _ = strings.Cut(opts, ",")
		if o == want {
			return true
		}
	}
	return false
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
// Structural keys are guarded: an override's "properties" or "items" merges
// where the derived schema already carries that key, or where the derived
// node is a bare any ({}, no type) — the deliberate union escape hatch an
// `any` field produces, whose shape the registration override supplies.
// What stays forbidden is inventing structure on a node the derivation
// typed as an opaque object (a map field, a cycle cut): a partial invented
// properties map would turn "anything allowed" into "only these keys",
// failing conformance for fields the code really emits. Opacity is judged
// on the node as derived — before this loop applies any override keys — so
// map iteration order cannot change the outcome.
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
				base[key] = ov // bare-any node: the override supplies the shape
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
