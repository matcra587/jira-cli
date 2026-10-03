package adf

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// The substrate's preservation contract: for input containing no
// insignificant whitespace (which is how Jira serves ADF), an unmodified
// parse→marshal round-trip reproduces the input bytes exactly. Three design
// choices carry that guarantee, and each kills a real corruption class:
//
//   - Every field's raw bytes are stored verbatim and re-emitted verbatim,
//     so attr values never pass through float64 (large integer IDs would
//     lose precision past 2^53) and strings are never re-escaped (Go's
//     encoder would rewrite "<" as "<").
//   - Fields are stored in parse order and re-emitted in that order, so an
//     input with unconventional key order still round-trips.
//   - Unknown keys and unknown node types get no special path — they are
//     ordinary raw fields, preserved like everything else.
//
// Only "marks" and "content" re-marshal from their decoded slices (they
// must, so an edit to a child is reflected); an untouched child re-emits
// its own raw fields, so the guarantee holds recursively. The scalar fields
// (type, text, attrs) are unexported with no direct setters precisely so a
// caller cannot mutate one and silently ship the stale raw bytes instead.
//
// Known limitation: the guarantee is per-key, not per-duplicate-key. JSON
// with the same key twice in one object (malformed, and never produced by
// Jira) decodes to the last occurrence, and both emitted occurrences of a
// re-marshaled key ("marks"/"content") render from that one value.

// Document is an ADF document: the root "doc" node plus its version. It is
// a Node underneath, so the whole preservation contract applies to the root
// object too (version and any future root-level keys ride the raw fields).
type Document struct {
	Node
}

// ErrNotDocument reports a root object whose type is not "doc" — parseable
// JSON, but not an ADF document. Callers distinguish it from JSON garbage
// with errors.Is.
var ErrNotDocument = errors.New("adf: root is not a document")

// ErrNoVersion reports a document root missing the required version key.
var ErrNoVersion = errors.New("adf: document has no version")

// ParseDocument parses an ADF document root, requiring only the invariants
// nothing can work without: a "doc" node carrying a version. It does NOT
// schema-validate — server-originated documents may carry nodes newer than
// the vendored schema and must still parse; use Validate for authored
// content.
func ParseDocument(data []byte) (*Document, error) {
	var d Document
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("adf: parse document: %w", err)
	}
	if d.Type() != "doc" {
		return nil, fmt.Errorf("%w: root type is %q, want \"doc\"", ErrNotDocument, d.Type())
	}
	if _, err := d.Version(); err != nil {
		return nil, err
	}
	return &d, nil
}

// NewDocument returns an empty version-1 document for programmatic
// authoring — the other entry point beside ParseDocument, and the target
// of the Markdown converter. It lives here rather than at each author site
// so the version key, which ParseDocument requires and Jira rejects a
// document without, can never be forgotten.
func NewDocument() *Document {
	d := &Document{Node: *NewNode(string(NodeTypeDoc))}
	d.setField("version", json.RawMessage("1"))
	return d
}

// Marshal serializes the document with the preservation contract intact —
// the entry point for bytes sent back to Jira. json.Marshal(doc) also
// works, but stdlib HTML-escapes a Marshaler's output (< to <):
// structurally identical, byte-different.
func (d *Document) Marshal() ([]byte, error) {
	return d.MarshalJSON()
}

// Version returns the document's ADF version (1, for every document Jira
// serves today).
func (d *Document) Version() (int, error) {
	raw, ok := d.Field("version")
	if !ok {
		return 0, ErrNoVersion
	}
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, fmt.Errorf("adf: document version: %w", err)
	}
	return v, nil
}

// Node is one ADF object — block, inline, or the doc root. A parsed Node
// remembers every field it was given, in order, as raw bytes; the typed
// views (Type, Text, Attrs, Marks, Content) are decoded from those same
// bytes at parse time.
type Node struct {
	// fields is the parse-ordered raw capture — the marshaling source of
	// truth. nil means the node was built programmatically, in which case
	// Marshal emits the canonical field order instead.
	fields  []rawField
	typ     string
	text    string
	attrs   json.RawMessage
	marks   []Mark
	content []Node
}

type rawField struct {
	key string
	raw json.RawMessage
}

// NewNode returns a programmatically-built node of the given type. Built
// nodes marshal in canonical field order (type, text, attrs, marks,
// content); parsed nodes keep their input order.
func NewNode(typ string) *Node {
	n := &Node{typ: typ}
	n.fields = append(n.fields, rawField{key: "type", raw: encodeString(typ)})
	return n
}

// Type returns the node's ADF type ("paragraph", "panel", …). Empty only
// for a malformed node that carried no type; the schema validator rejects
// those, the substrate preserves them.
func (n *Node) Type() string { return n.typ }

// Text returns the node's text, or "" when absent (only "text" nodes carry
// it).
func (n *Node) Text() string { return n.text }

// SetText replaces the node's text. The stored raw bytes are updated in
// step, so the change survives marshaling.
func (n *Node) SetText(s string) {
	n.text = s
	n.setField("text", encodeString(s))
}

// Attrs returns the node's attrs object as raw bytes, or nil when absent.
// Callers decode into whatever shape they need; the typed layer's generated
// attr structs are the usual target. A copy: the substrate's stored bytes
// ARE the output, so an aliased return would let one careless write emit
// corrupted JSON with no error anywhere.
func (n *Node) Attrs() json.RawMessage { return bytes.Clone(n.attrs) }

// SetAttrs replaces the node's attrs with the JSON encoding of v.
func (n *Node) SetAttrs(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("adf: encode attrs: %w", err)
	}
	n.attrs = raw
	n.setField("attrs", raw)
	return nil
}

// Marks returns the node's marks. The slice is the node's own: mutating an
// element is a real edit and will be reflected on marshal. Clipped, so an
// append allocates instead of racing spare capacity in the node's backing
// array (a grown slice the node cannot see).
func (n *Node) Marks() []Mark { return slices.Clip(n.marks) }

// SetMarks replaces the node's marks. A non-nil empty slice marshals as
// "marks": [] — distinct from removing the key, which no API offers because
// ADF never requires it.
func (n *Node) SetMarks(marks []Mark) {
	n.marks = marks
	n.setField("marks", nil)
}

// Content returns the node's children. The slice is the node's own:
// replacing, reordering, or mutating elements is a real edit and will be
// reflected on marshal, while untouched siblings keep their bytes. Clipped
// for the same reason as Marks.
func (n *Node) Content() []Node { return slices.Clip(n.content) }

// SetContent replaces the node's children.
func (n *Node) SetContent(content []Node) {
	n.content = content
	n.setField("content", nil)
}

// Field returns the raw bytes of any field by key — including keys the
// substrate has no typed view for (a future ADF addition, a document's
// "version"). For a duplicated key it returns the last occurrence, matching
// encoding/json semantics. After SetMarks/SetContent the stored raw is a
// placeholder, so those keys re-marshal from their live slices — the
// returned bytes are always valid JSON, never a stale or empty raw. Always
// a copy, for the same reason as Attrs.
func (n *Node) Field(key string) (json.RawMessage, bool) {
	for _, f := range slices.Backward(n.fields) {
		if f.key != key {
			continue
		}
		if f.raw == nil {
			var buf bytes.Buffer
			var err error
			switch key {
			case "marks":
				err = writeMarks(&buf, n.marks)
			case "content":
				err = writeNodes(&buf, n.content)
			}
			if err != nil {
				return nil, false
			}
			return buf.Bytes(), true
		}
		return bytes.Clone(f.raw), true
	}
	return nil, false
}

// hasField reports key presence without the copy Field performs — the
// classifier asks "is this key present" once per candidate spec, and
// cloning a content subtree per probe would make classification quadratic
// in document size.
func (n *Node) hasField(key string) bool {
	for i := range n.fields {
		if n.fields[i].key == key {
			return true
		}
	}
	return false
}

// rawAttrs is the classifier's uncloned attrs view — read-only by
// convention within the package. All exported access goes through Attrs,
// which copies.
func (n *Node) rawAttrs() json.RawMessage { return n.attrs }

// setField updates the stored raw bytes for key, appending the field if the
// node did not have it. A nil raw is a placeholder for the fields that
// re-marshal from their typed slices ("marks", "content").
func (n *Node) setField(key string, raw json.RawMessage) {
	// A zero-value Node has no fields, so the first setter would switch
	// marshaling to the field-iteration path with no "type" key ever
	// written — a typeless node on the wire. Seed identity first; NewNode
	// callers already have it, so this only rescues the zero value.
	if n.fields == nil {
		n.fields = append(n.fields, rawField{key: "type", raw: encodeString(n.typ)})
	}
	for i := range n.fields {
		if n.fields[i].key == key {
			n.fields[i].raw = raw
			return
		}
	}
	n.fields = append(n.fields, rawField{key: key, raw: raw})
}

func (n *Node) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("adf: node: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("adf: node must be a JSON object, got %v", tok)
	}
	// Non-nil even when the object is empty: nil fields is the "built
	// programmatically" sentinel, and a parsed bare {} must round-trip as
	// {} — not grow an invented canonical "type" key.
	*n = Node{fields: []rawField{}}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("adf: node: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("adf: node: object key is %v, not a string", keyTok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("adf: node.%s: %w", key, err)
		}
		n.fields = append(n.fields, rawField{key: key, raw: raw})
		switch key {
		case "type":
			err = json.Unmarshal(raw, &n.typ)
		case "text":
			err = json.Unmarshal(raw, &n.text)
		case "attrs":
			n.attrs = raw
		case "marks":
			err = json.Unmarshal(raw, &n.marks)
		case "content":
			err = json.Unmarshal(raw, &n.content)
		}
		if err != nil {
			return fmt.Errorf("adf: node.%s: %w", key, err)
		}
	}
	return nil
}

// Value receiver on purpose, unlike every other Node method: Content()
// hands out []Node, and encoding/json only finds a value-receiver
// MarshalJSON on slice elements. Do not "fix" the inconsistency — pointer
// receivers here would silently drop the custom marshaling for children.
func (n Node) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	if n.fields == nil {
		// Built without NewNode (zero value) — emit the canonical order
		// from whatever typed state exists.
		if err := n.writeCanonical(&buf); err != nil {
			return nil, err
		}
		buf.WriteByte('}')
		return buf.Bytes(), nil
	}
	for i, f := range n.fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		writeJSONString(&buf, f.key)
		buf.WriteByte(':')
		var err error
		switch f.key {
		// marks/content re-marshal from their decoded slices so child edits
		// are reflected — except a parsed literal null (decoded to a nil
		// slice, original raw retained), which must re-emit as null, not be
		// silently promoted to [].
		case "marks":
			if f.raw != nil && n.marks == nil {
				_, err = buf.Write(f.raw)
			} else {
				err = writeMarks(&buf, n.marks)
			}
		case "content":
			if f.raw != nil && n.content == nil {
				_, err = buf.Write(f.raw)
			} else {
				err = writeNodes(&buf, n.content)
			}
		default:
			_, err = buf.Write(f.raw)
		}
		if err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (n Node) writeCanonical(buf *bytes.Buffer) error {
	buf.WriteString(`"type":`)
	writeJSONString(buf, n.typ)
	if n.text != "" {
		buf.WriteString(`,"text":`)
		writeJSONString(buf, n.text)
	}
	if n.attrs != nil {
		buf.WriteString(`,"attrs":`)
		buf.Write(n.attrs)
	}
	if n.marks != nil {
		buf.WriteString(`,"marks":`)
		if err := writeMarks(buf, n.marks); err != nil {
			return err
		}
	}
	if n.content != nil {
		buf.WriteString(`,"content":`)
		if err := writeNodes(buf, n.content); err != nil {
			return err
		}
	}
	return nil
}

// Mark is an inline annotation on a node. Same preservation rules as Node:
// raw fields in parse order, attrs never re-encoded.
type Mark struct {
	fields []rawField
	typ    string
	attrs  json.RawMessage
}

// NewMark returns a programmatically-built mark of the given type.
func NewMark(typ string) *Mark {
	m := &Mark{typ: typ}
	m.fields = append(m.fields, rawField{key: "type", raw: encodeString(typ)})
	return m
}

// Type returns the mark's ADF type ("strong", "link", …).
func (m *Mark) Type() string { return m.typ }

// Attrs returns the mark's attrs object as raw bytes, or nil when absent.
func (m *Mark) Attrs() json.RawMessage { return bytes.Clone(m.attrs) }

// SetAttrs replaces the mark's attrs with the JSON encoding of v.
func (m *Mark) SetAttrs(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("adf: encode mark attrs: %w", err)
	}
	m.attrs = raw
	for i := range m.fields {
		if m.fields[i].key == "attrs" {
			m.fields[i].raw = raw
			return nil
		}
	}
	m.fields = append(m.fields, rawField{key: "attrs", raw: raw})
	return nil
}

func (m *Mark) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("adf: mark: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("adf: mark must be a JSON object, got %v", tok)
	}
	// Non-nil for the same reason as Node: a parsed bare {} must not grow
	// an invented canonical "type" key on re-marshal.
	*m = Mark{fields: []rawField{}}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("adf: mark: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("adf: mark: object key is %v, not a string", keyTok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("adf: mark.%s: %w", key, err)
		}
		m.fields = append(m.fields, rawField{key: key, raw: raw})
		switch key {
		case "type":
			if err := json.Unmarshal(raw, &m.typ); err != nil {
				return fmt.Errorf("adf: mark.type: %w", err)
			}
		case "attrs":
			m.attrs = raw
		}
	}
	return nil
}

func (m Mark) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	if m.fields == nil {
		buf.WriteString(`"type":`)
		writeJSONString(&buf, m.typ)
		if m.attrs != nil {
			buf.WriteString(`,"attrs":`)
			buf.Write(m.attrs)
		}
		buf.WriteByte('}')
		return buf.Bytes(), nil
	}
	for i, f := range m.fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		writeJSONString(&buf, f.key)
		buf.WriteByte(':')
		buf.Write(f.raw)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// writeNodes and writeMarks build the "content"/"marks" arrays by calling
// each element's MarshalJSON directly. Routing them through json.Marshal
// instead would HTML-escape <, >, & across the already-assembled bytes —
// stdlib post-processes a Marshaler's output — and break byte preservation
// from the outside.
func writeNodes(buf *bytes.Buffer, nodes []Node) error {
	buf.WriteByte('[')
	for i := range nodes {
		if i > 0 {
			buf.WriteByte(',')
		}
		b, err := nodes[i].MarshalJSON()
		if err != nil {
			// The index is the context a caller can act on — MarshalJSON
			// failures otherwise surface with no path to the bad child.
			return fmt.Errorf("adf: content[%d]: %w", i, err)
		}
		buf.Write(b)
	}
	buf.WriteByte(']')
	return nil
}

func writeMarks(buf *bytes.Buffer, marks []Mark) error {
	buf.WriteByte('[')
	for i := range marks {
		if i > 0 {
			buf.WriteByte(',')
		}
		b, err := marks[i].MarshalJSON()
		if err != nil {
			return fmt.Errorf("adf: marks[%d]: %w", i, err)
		}
		buf.Write(b)
	}
	buf.WriteByte(']')
	return nil
}

// writeJSONString writes s as a JSON string without HTML escaping — Go's
// default encoder rewrites <, >, & as \u escapes, which would break byte
// preservation for canonical emission and gratuitously uglify authored
// content. ADF keys and type names are plain ASCII; text can be anything.
//
// Do NOT "simplify" this to json.Encoder + SetEscapeHTML(false): stdlib
// still unconditionally escapes U+2028/U+2029, so a document carrying them
// raw would re-emit differently — the byte-identical round-trip contract
// breaks invisibly (and Encode appends a newline). Pinned by test.
func writeJSONString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
				continue
			}
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
}

func encodeString(s string) json.RawMessage {
	var b bytes.Buffer
	writeJSONString(&b, s)
	return b.Bytes()
}
