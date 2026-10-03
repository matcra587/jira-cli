package adf

//go:generate go run ./gen

// NodeType is a runtime ADF node type ("paragraph", "panel", …). The known
// values are generated into nodes_gen.go; the substrate itself accepts any
// type string.
type NodeType string

// MarkType is a runtime ADF mark type ("strong", "link", …).
type MarkType string

// The spec types are the hand-written half of the capability matrix; the
// instances live in nodes_gen.go, generated from the vendored schema. One
// NodeSpec is one row of the matrix — a unique runtime type × capability
// combination (schema definition), NOT merely a node type: "type" alone
// cannot discriminate, because the same runtime type appears in different
// contexts with different allowed marks, attrs, and content (paragraph has
// no-marks/alignment/indentation variants; codeBlock has a root-only
// variant). Classification against these specs is the runtime context
// validation the generated typed views build on.

// NodeSpec describes one concrete node definition from the schema.
type NodeSpec struct {
	// Name is the schema definition name, e.g. "paragraph_with_no_marks_node".
	Name string `json:"name"`
	// Type is the runtime node type, e.g. "paragraph".
	Type NodeType `json:"type"`
	// Variant distinguishes definitions sharing a runtime type
	// ("with_no_marks", "root_only"); empty for a type's primary definition.
	Variant string `json:"variant"`
	// Required lists the object keys the definition requires ("type",
	// "content", "version", …).
	Required []string `json:"required"`
	// Attrs is nil when the definition declares no attrs object.
	Attrs *AttrsSpec `json:"attrs,omitempty"`
	// Marks is nil when the definition does not allow marks at all (the
	// schema closes most nodes with additionalProperties: false, so an
	// undeclared marks key is forbidden, not merely unconstrained).
	Marks *MarksSpec `json:"marks,omitempty"`
	// Content is nil for leaf definitions.
	Content *ContentSpec `json:"content,omitempty"`
	// Open reports whether the definition tolerates undeclared keys
	// (additionalProperties: true).
	Open bool `json:"open"`
}

// AttrsSpec bounds a definition's attrs object.
type AttrsSpec struct {
	// Required lists attr keys every valid instance must carry. For an
	// attrs union (anyOf) this is the intersection of the branches'
	// requirements — the keys required no matter which branch applies.
	Required []string `json:"required"`
	// Allowed lists every attr key any branch accepts.
	Allowed []string `json:"allowed"`
	// Open reports whether undeclared attr keys are tolerated.
	Open bool `json:"open"`
}

// MarksSpec bounds a definition's marks array.
type MarksSpec struct {
	// Max is the maximum mark count; -1 means unbounded.
	Max int `json:"max"`
	// Allowed lists the permitted mark types; nil means unconstrained.
	Allowed []MarkType `json:"allowed"`
}

// ContentSpec bounds a definition's content array.
type ContentSpec struct {
	// MinItems/MaxItems bound the child count; -1 means unbounded.
	MinItems int `json:"min_items"`
	MaxItems int `json:"max_items"`
	// Refs names the schema definitions valid as children — concrete
	// definitions or content groups (resolved through Groups).
	Refs []string `json:"refs"`
}

// MarkSpec describes one mark definition from the schema.
type MarkSpec struct {
	// Name is the schema definition name, e.g. "link_mark".
	Name string `json:"name"`
	// Type is the runtime mark type, e.g. "link".
	Type MarkType `json:"type"`
	// Attrs is nil when the mark declares no attrs object.
	Attrs *AttrsSpec `json:"attrs,omitempty"`
}
