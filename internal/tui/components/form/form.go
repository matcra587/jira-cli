package form

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	pkey "github.com/gechr/primer/key"
	"github.com/gechr/primer/prompt"
	xstrings "github.com/gechr/x/strings"
	"github.com/matcra587/jira-cli/internal/tui/components/input"
	"github.com/matcra587/jira-cli/internal/tui/components/pill"
	"github.com/matcra587/jira-cli/internal/tui/components/titlebox"
)

// FieldSpec declares one field of a form.
type FieldSpec struct {
	// Label renders above the field; empty omits the row.
	Label string
	// Placeholder shows in the empty field.
	Placeholder string
	// Initial pre-fills the field; the dirty guard compares against it, so a
	// prefilled-then-untouched form still cancels without a confirmation.
	Initial string
	// Multiline makes the field a textarea (enter inserts a newline and
	// ctrl+s submits) instead of a one-line input.
	Multiline bool
	// Rows is the textarea height; zero means 5.
	Rows int
	// Options, when non-empty, makes the field a cycle selector rather than a
	// text input: it holds one of the listed values and ←/→ (or h/l) step
	// through them. Its value is always one of Options, so a required cycle
	// field can never be blank. Initial selects the starting option (the first
	// when Initial matches none). Multiline/Autocomplete are ignored on a
	// cycle field.
	Options []string
	// Notify marks a cycle field whose value changes the owner wants to react
	// to — a project selector that drives a dependent type list, say. When such
	// a field steps to a new value the form returns EventChanged, so the owner
	// can refetch and push new Options back through SetOptions.
	Notify bool
	// Optional lets the field submit blank. A required field left blank
	// blocks the submit and takes focus instead. An Optional field renders an
	// "(optional)" marker on its label so the blank-is-fine contract is visible.
	Optional bool
	// Validate, when set, runs on trySubmit against the field's value. A
	// non-nil error blocks the submit, focuses the field, and renders the
	// message inline beneath it — the same gate the required check uses, but
	// for content the field itself can't express (a duration that won't parse).
	Validate func(string) error
	// Autocomplete, when set, watches this field for its trigger token and
	// offers fetched suggestions. Nil disables the seam.
	Autocomplete *Autocomplete
}

// Styles are the form's render styles, injected by the owner so the form
// stays theme-agnostic.
type Styles struct {
	Title              lipgloss.Style
	Label              lipgloss.Style
	LabelFocused       lipgloss.Style
	Border             lipgloss.Style // an unfocused field's box border and cycle chevrons
	BorderFocused      lipgloss.Style // the focused field's box border and cycle chevrons
	HintKey            lipgloss.Style
	HintText           lipgloss.Style
	Question           lipgloss.Style // the dirty-discard confirmation
	Suggestion         lipgloss.Style
	SuggestionSelected lipgloss.Style
	Error              lipgloss.Style // field and form-level validation/submit errors
}

// Config declares a whole form.
type Config struct {
	// Title renders above the fields (e.g. "comment on PROJ-1").
	Title  string
	Fields []FieldSpec
	// EditorHatch offers ctrl+e on a focused multiline field: the form emits
	// EventEditor and the owner takes the draft to an external editor. The
	// in-TUI field is always the default; the editor is the escape hatch.
	EditorHatch bool
	// Width bounds every field's rendered width.
	Width  int
	Styles Styles
}

// EventKind is what a completed Update asks the owner to do.
type EventKind int

const (
	// EventNone means the form consumed (or ignored) the message and stays open.
	EventNone EventKind = iota
	// EventSubmit means every required field is filled; read Values.
	EventSubmit
	// EventCancel means the user backed out (esc on a pristine form, or a
	// confirmed discard). The values are abandoned.
	EventCancel
	// EventEditor asks the owner to continue the focused multiline field in
	// the external editor, seeded with Values. The form stays open until the
	// owner closes it, so a failed editor launch loses nothing.
	EventEditor
	// EventChanged means a Notify cycle field just stepped to a new value; the
	// owner reads it (via Value) and may push dependent Options back through
	// SetOptions. The form stays open.
	EventChanged
)

// Model is one open form. The zero value is inert; construct with New.
type Model struct {
	title          string
	fields         []field
	fieldErrs      []string       // per-field validation error, parallel to fields; "" clear
	acceptedDetail map[int]string // field index → last-accepted suggestion Detail (e.g. an accountId)
	formErr        string         // form-level error (a submit the owner reports as failed)
	focus          int
	confirming     bool
	editorHatch    bool
	// submitting freezes the form while an async submit is in flight: the foot
	// row shows submitFrame plus "submitting…" in place of the key hints. The
	// owner sets it on submit and clears it when the write resolves.
	submitting  bool
	submitFrame string
	width       int
	styles      Styles
	ac          acState
}

// field pairs a spec with whichever input kind it declared.
type field struct {
	spec       FieldSpec
	line       input.Line
	area       input.Area
	sel        int // selected option index for a cycle field (Options non-empty)
	initialSel int // the sel a cycle field opened on, for the dirty check
}

// isCycle reports that the field is a cycle selector (fixed Options stepped
// with ←/→) rather than a text input.
func (f *field) isCycle() bool { return len(f.spec.Options) > 0 }

// New builds a focused form: the first field owns the keyboard.
func New(cfg Config) Model {
	m := Model{
		title:       cfg.Title,
		editorHatch: cfg.EditorHatch,
		width:       cfg.Width,
		styles:      cfg.Styles,
	}
	for i, spec := range cfg.Fields {
		f := field{spec: spec}
		if len(spec.Options) > 0 {
			// A cycle field carries no text widget: its value is the selected
			// option, seeded from Initial (or the first option when Initial
			// names none). initialSel pins the opening selection so dirty()
			// compares against the resolved option, not the raw Initial string
			// (which may name no option, e.g. an unset default).
			f.sel = indexOfOption(spec.Options, spec.Initial)
			f.initialSel = f.sel
			m.fields = append(m.fields, f)
			continue
		}
		// A text field renders inside a titled box, so its widget is sized to the
		// content area left after the border and padding — not the full form
		// width — or the body would overrun the frame.
		fw := max(1, cfg.Width-titlebox.Chrome)
		if spec.Multiline {
			rows := spec.Rows
			if rows == 0 {
				rows = 5
			}
			f.area = input.NewArea(spec.Placeholder, fw, rows)
			f.area.SetValue(spec.Initial)
			if i != 0 {
				f.area.Blur()
			}
		} else {
			f.line = input.NewLine("", spec.Placeholder)
			f.line.SetWidth(fw)
			f.line.SetValue(spec.Initial)
			if i != 0 {
				f.line.Blur()
			}
		}
		m.fields = append(m.fields, f)
	}
	m.fieldErrs = make([]string, len(m.fields))
	m.acceptedDetail = make(map[int]string, len(m.fields))
	return m
}

// AcceptedDetail returns the Detail of the suggestion last accepted into field
// i (empty when none was accepted, or a later edit invalidated it). The owner
// uses it to recover the opaque value behind a display label — an assignee's
// accountId, say — without re-resolving the field text.
func (m *Model) AcceptedDetail(i int) string { return m.acceptedDetail[i] }

// SetSubmitting marks the form as awaiting an async submit, rendering frame
// (a spinner glyph) plus "submitting…" in place of the hint row. It clears any
// prior form-level error so the two never show at once.
func (m *Model) SetSubmitting(frame string) {
	m.submitting = true
	m.submitFrame = frame
	m.formErr = ""
}

// SetError clears the submitting state and shows msg as a form-level error
// line above the hint row — the seam an owner uses to surface a failed submit
// while keeping the draft intact.
func (m *Model) SetError(msg string) {
	m.submitting = false
	m.formErr = msg
}

// Active reports whether the form is open — a zero Model is inert.
func (m *Model) Active() bool { return len(m.fields) > 0 }

// SetValue replaces field i's content without touching its Initial, so the
// dirty guard keeps comparing against the true baseline — the seam a caller
// rebuilding a form mid-edit (a resize) needs to carry a draft over.
func (m *Model) SetValue(i int, s string) {
	if i < 0 || i >= len(m.fields) {
		return
	}
	if m.fields[i].isCycle() {
		m.fields[i].sel = indexOfOption(m.fields[i].spec.Options, s)
		return
	}
	if m.fields[i].spec.Multiline {
		m.fields[i].area.SetValue(s)
		return
	}
	m.fields[i].line.SetValue(s)
}

// SetOptions replaces cycle field i's options — the seam an owner uses to swap
// a dependent list live (the type list that changes with the selected project).
// The selection stays on its current value when that value survives the swap,
// otherwise it snaps to the first option so the field never points past the
// end. A no-op on a non-cycle field or an empty list (which would degrade the
// field to a blank). initialSel tracks the new selection, so an owner-driven
// swap does not by itself read as a user edit — the field that drove the change
// is what marks the form dirty.
func (m *Model) SetOptions(i int, options []string) {
	if i < 0 || i >= len(m.fields) || len(options) == 0 || !m.fields[i].isCycle() {
		return
	}
	m.fields[i].sel = indexOfOption(options, m.fields[i].value())
	m.fields[i].spec.Options = options
	m.fields[i].initialSel = m.fields[i].sel
}

// Value returns field i's current content ("" out of range).
func (m *Model) Value(i int) string {
	if i < 0 || i >= len(m.fields) {
		return ""
	}
	return m.fields[i].value()
}

// Values returns every field's current content in declaration order.
func (m *Model) Values() []string {
	out := make([]string, len(m.fields))
	for i := range m.fields {
		out[i] = m.fields[i].value()
	}
	return out
}

// dirty reports whether any field differs from its initial content — the
// gate on the discard confirmation.
func (m *Model) dirty() bool {
	for i := range m.fields {
		if m.fields[i].isCycle() {
			// Compare selections, not strings: a cycle field's value resolves to
			// a real option even when Initial named none, so a string compare
			// would read pristine-but-unset as dirty.
			if m.fields[i].sel != m.fields[i].initialSel {
				return true
			}
			continue
		}
		if m.fields[i].value() != m.fields[i].spec.Initial {
			return true
		}
	}
	return false
}

// Update routes a message into the form. The event tells the owner what
// completed; consumed reports whether the form owned the message, so an owner
// layering global keys knows when to fall through.
func (m *Model) Update(msg tea.Msg) (tea.Cmd, EventKind, bool) {
	if !m.Active() {
		return nil, EventNone, false
	}
	if sug, ok := msg.(SuggestionsMsg); ok {
		if m.confirming {
			// Dropping the result must also drop the query, or a resumed
			// form would see "same token" forever and never refetch.
			m.ac.clear()
			return nil, EventNone, true
		}
		m.applySuggestions(sug)
		return nil, EventNone, true
	}
	key, isKey := msg.(tea.KeyPressMsg)
	if m.confirming {
		if !isKey {
			// A paste (or any stray message) must not mutate the draft the
			// confirmation is protecting; only y/n/esc move the state.
			return nil, EventNone, true
		}
		return nil, m.updateConfirm(key), true
	}
	if m.submitting {
		// Frozen while an async submit is in flight: the owner clears the state
		// through SetError or by closing the form. Swallow input so a stray key
		// can't edit the draft mid-write or fire a second submit.
		return nil, EventNone, true
	}
	if !isKey {
		// Paste and ticks flow into the focused field; paste can change the
		// autocomplete token exactly like typing.
		m.clearErrors()
		cmd := m.fields[m.focus].update(msg)
		return tea.Batch(cmd, m.syncAutocomplete()), EventNone, true
	}
	if m.ac.visible() {
		if cmd, ev, done := m.updateSuggesting(key); done {
			return cmd, ev, true
		}
	}
	switch key.String() {
	case "esc":
		if m.dirty() {
			m.confirming = true
			return nil, EventNone, true
		}
		return nil, EventCancel, true
	case "ctrl+s":
		return nil, m.trySubmit(), true
	case "tab":
		m.moveFocus(1)
		return nil, EventNone, true
	case "shift+tab":
		m.moveFocus(-1)
		return nil, EventNone, true
	case "ctrl+e":
		if m.editorHatch && m.fields[m.focus].spec.Multiline {
			return nil, EventEditor, true
		}
	case "enter":
		// Enter on a one-line field advances to the next field (a newline is
		// meaningless there) and submits from the last one. Multiline fields
		// keep enter as a newline via the textarea below.
		if !m.fields[m.focus].spec.Multiline {
			if m.focus < len(m.fields)-1 {
				m.moveFocus(1)
				return nil, EventNone, true
			}
			return nil, m.trySubmit(), true
		}
	}
	m.clearErrors()
	before := m.fields[m.focus].value()
	cmd := m.fields[m.focus].update(msg)
	// A Notify cycle field that stepped to a new value tells the owner to react
	// (refetch a dependent list); an ordinary edit is EventNone.
	if f := &m.fields[m.focus]; f.isCycle() && f.spec.Notify && f.value() != before {
		return cmd, EventChanged, true
	}
	return tea.Batch(cmd, m.syncAutocomplete()), EventNone, true
}

// updateConfirm drives the discard confirmation: y abandons the form, n (or
// esc) resumes editing, anything else is swallowed so a stray key can never
// drop the draft.
func (m *Model) updateConfirm(key tea.KeyPressMsg) EventKind {
	switch key.String() {
	case "y", "Y":
		return EventCancel
	case "n", "N", "esc":
		m.confirming = false
	}
	return EventNone
}

// trySubmit emits EventSubmit when every required field is filled and passes
// its Validate; otherwise it focuses the first offending field, records any
// validation message, and keeps the form open.
func (m *Model) trySubmit() EventKind {
	for i := range m.fields {
		if !m.fields[i].spec.Optional && xstrings.IsBlank(m.fields[i].value()) {
			m.setFocus(i)
			return EventNone
		}
	}
	for i := range m.fields {
		m.fieldErrs[i] = ""
	}
	for i := range m.fields {
		v := m.fields[i].spec.Validate
		if v == nil {
			continue
		}
		if err := v(m.fields[i].value()); err != nil {
			m.fieldErrs[i] = err.Error()
			m.setFocus(i)
			return EventNone
		}
	}
	m.formErr = ""
	return EventSubmit
}

// clearErrors drops the focused field's inline error and any form-level error
// once the user edits, so a correction wipes the message it is fixing.
func (m *Model) clearErrors() {
	if m.focus < len(m.fieldErrs) {
		m.fieldErrs[m.focus] = ""
	}
	// A fresh edit to the focused field invalidates any accountId (or other
	// Detail) recorded when a suggestion was accepted there — the text no longer
	// matches the value it stood for.
	delete(m.acceptedDetail, m.focus)
	m.formErr = ""
}

// moveFocus advances the ring by delta, wrapping at both ends.
func (m *Model) moveFocus(delta int) {
	if len(m.fields) < 2 {
		return
	}
	m.setFocus((m.focus + delta + len(m.fields)) % len(m.fields))
}

func (m *Model) setFocus(i int) {
	if i == m.focus {
		return
	}
	m.fields[m.focus].blur()
	m.focus = i
	m.fields[i].focus()
	m.ac.clear() // suggestions belong to the field that was being typed in
}

// View renders the whole form — the scrollable body then the pinned foot — as
// one string. It is the plain path (tests, any owner that frames the form
// itself); an owner that scrolls a tall form draws Body and Foot separately so
// the foot row never scrolls off (see [Model.Body], [Model.Foot]).
func (m *Model) View() string {
	if !m.Active() {
		return ""
	}
	return m.Body() + m.Foot()
}

// Body is the scrollable part of the form: the title and the labeled fields
// (with any suggestion list under the focused one). FocusRegion is measured
// against exactly this, so an owner can scroll Body to follow focus and pin
// Foot beneath the viewport.
func (m *Model) Body() string {
	if !m.Active() {
		return ""
	}
	var b strings.Builder
	if m.title != "" {
		b.WriteString(m.styles.Title.Render(m.title))
		b.WriteString("\n")
	}
	for i := range m.fields {
		b.WriteString(m.fieldBlock(i))
	}
	return b.String()
}

// Foot is the form's pinned chrome: any form-level error above the hint (or
// submitting, or discard-confirm) row. It must stay visible however the body
// scrolls — it carries the submit/cancel/confirm affordances — so an owner
// renders it outside the scroll viewport.
func (m *Model) Foot() string {
	if !m.Active() {
		return ""
	}
	var b strings.Builder
	if m.formErr != "" {
		b.WriteString(m.styles.Error.Render(m.formErr))
		b.WriteString("\n")
	}
	b.WriteString(m.footRow())
	return b.String()
}

// cycleLabelWidth aligns the pill selectors' labels into a column.
const cycleLabelWidth = 11

// fieldBlock renders one field's whole block — a titled box for a text field or
// a compact pill row for a cycle selector, then any inline validation error,
// and, on the focused field with the suggestion list up, the autocomplete list.
// View concatenates the blocks and FocusRegion measures them, so both share one
// line accounting and can never drift; every block ends on a newline, so it
// contributes exactly its newline count in lines to the joined view.
func (m *Model) fieldBlock(i int) string {
	var b strings.Builder
	if m.fields[i].isCycle() {
		b.WriteString(m.cycleRow(i))
	} else {
		b.WriteString(m.boxField(i))
	}
	b.WriteString("\n")
	if m.fieldErrs[i] != "" {
		b.WriteString(m.styles.Error.Render("  " + m.fieldErrs[i]))
		b.WriteString("\n")
	}
	if i == m.focus && m.ac.visible() {
		b.WriteString(m.viewSuggestions())
	}
	return b.String()
}

// frameStyles picks the border/label styles for field i: the focused field
// gets the accent pair, every other the dim pair.
func (m *Model) frameStyles(i int) (frame, label lipgloss.Style) {
	if i == m.focus {
		return m.styles.BorderFocused, m.styles.LabelFocused
	}
	return m.styles.Border, m.styles.Label
}

// boxField draws a text field inside a titled box: the label rides the top
// border, the widget fills the padded interior, and the border color marks
// focus. An optional field carries an "(optional)" marker in its title so the
// blank-is-fine contract stays visible.
func (m *Model) boxField(i int) string {
	f := &m.fields[i]
	frame, label := m.frameStyles(i)
	title := f.spec.Label
	if f.spec.Optional {
		title += " (optional)"
	}
	return titlebox.Render(title, f.view(), m.width, titlebox.Styles{Border: frame, Title: label})
}

// cycleRow renders a cycle selector as a compact labeled pill —
// "type       ‹ Task ›" — rather than a box: a one-of list needs no text area,
// and boxing its short value would only waste vertical space. The chevrons and
// label take the accent styling when focused.
func (m *Model) cycleRow(i int) string {
	f := &m.fields[i]
	frame, label := m.frameStyles(i)
	return pill.Render(f.spec.Label, f.value(), cycleLabelWidth, pill.Styles{Label: label, Chevron: frame})
}

// FocusRegion reports the focused field's block position within View(): top is
// its first line (0-based), height its line span. A framing Shell scrolls to
// keep that range visible as focus moves, so a form taller than the height cap
// follows the cursor instead of stranding it off-screen. ok is false for an
// inert form. Lines are counted by newline exactly as View joins the blocks,
// so the region lands on the same rows the Shell renders.
func (m *Model) FocusRegion() (top, height int, ok bool) {
	if !m.Active() {
		return 0, 0, false
	}
	if m.title != "" {
		top += strings.Count(m.styles.Title.Render(m.title), "\n") + 1
	}
	for i := 0; i < m.focus; i++ {
		top += strings.Count(m.fieldBlock(i), "\n")
	}
	return top, strings.Count(m.fieldBlock(m.focus), "\n"), true
}

// footRow is the bottom line: the submitting indicator while an async submit is
// in flight, otherwise the form's key hints (which hintRow chooses among).
func (m *Model) footRow() string {
	if m.submitting {
		return m.submitFrame + " " + m.styles.HintText.Render("submitting…")
	}
	return m.hintRow()
}

// hintRow is the bottom line: the discard question while confirming, the
// suggestion bindings while the list is up, else the form's own bindings.
// Single-letter keys render inline in their description (primer's key.Inline
// "(n)ew" style); multi-letter keys fall back to "key desc".
func (m *Model) hintRow() string {
	if m.confirming {
		return m.styles.Question.Render("discard input?") + m.renderHints([]pkey.Hint{
			{Key: "y", Desc: "yes"},
			{Key: "n", Desc: "no"},
		})
	}
	if m.ac.visible() {
		return m.renderHints([]pkey.Hint{
			{Key: "↑↓", Desc: "choose"},
			{Key: "enter", Desc: "insert"},
			{Key: "esc", Desc: "dismiss"},
		})
	}
	hints := make([]pkey.Hint, 0, 5)
	if m.multiline() {
		hints = append(hints, pkey.Hint{Key: "ctrl+s", Desc: "submit"})
	} else {
		hints = append(hints, pkey.Hint{Key: "enter", Desc: "submit"})
	}
	if m.fields[m.focus].isCycle() {
		hints = append(hints, pkey.Hint{Key: "←→", Desc: "choose"})
	}
	if len(m.fields) > 1 {
		hints = append(hints, pkey.Hint{Key: "tab", Desc: "next field"})
	}
	if m.editorHatch {
		hints = append(hints, pkey.Hint{Key: "ctrl+e", Desc: "editor"})
	}
	hints = append(hints, pkey.Hint{Key: "esc", Desc: "cancel"})
	return m.renderHints(hints)
}

func (m *Model) renderHints(hints []pkey.Hint) string {
	prefix := " "
	return pkey.Renderer{
		Gap:    "   ",
		Styles: pkey.Styles{Key: m.styles.HintKey, Text: m.styles.HintText},
		Prefix: &prefix,
		Inline: true,
	}.Render(hints)
}

// multiline reports whether any field is a textarea, which moves submit from
// enter to ctrl+s.
func (m *Model) multiline() bool {
	for i := range m.fields {
		if m.fields[i].spec.Multiline {
			return true
		}
	}
	return false
}

// indexOfOption returns the position of want in options, or 0 when it names
// none — so a cycle field always starts on a valid option.
func indexOfOption(options []string, want string) int {
	return max(slices.Index(options, want), 0)
}

// cycle steps a cycle field's selection by delta, wrapping at both ends.
func (f *field) cycle(delta int) {
	f.sel = prompt.StepChoice(f.sel, len(f.spec.Options), delta, true)
}

func (f *field) value() string {
	switch {
	case f.isCycle():
		if f.sel < 0 || f.sel >= len(f.spec.Options) {
			return ""
		}
		return f.spec.Options[f.sel]
	case f.spec.Multiline:
		return f.area.Value()
	}
	return f.line.Value()
}

func (f *field) focus() {
	switch {
	case f.isCycle(): // no text widget to focus; m.focus tracks the highlight
	case f.spec.Multiline:
		f.area.Focus()
	default:
		f.line.Focus()
	}
}

func (f *field) blur() {
	switch {
	case f.isCycle():
	case f.spec.Multiline:
		f.area.Blur()
	default:
		f.line.Blur()
	}
}

func (f *field) update(msg tea.Msg) tea.Cmd {
	if f.isCycle() {
		// A cycle field owns only ←/→ (and h/l); every other key is inert. It
		// never emits a command — the selection is local state.
		if k, ok := msg.(tea.KeyPressMsg); ok {
			switch k.String() {
			case "right", "l":
				f.cycle(1)
			case "left", "h":
				f.cycle(-1)
			}
		}
		return nil
	}
	if f.spec.Multiline {
		return f.area.Update(msg)
	}
	return f.line.Update(msg)
}

func (f *field) view() string {
	switch {
	case f.isCycle():
		return "‹ " + f.value() + " ›"
	case f.spec.Multiline:
		return f.area.View()
	}
	return f.line.View()
}

func (f *field) beforeCursor() string {
	switch {
	case f.isCycle():
		return ""
	case f.spec.Multiline:
		return f.area.BeforeCursor()
	}
	return f.line.BeforeCursor()
}

func (f *field) replaceBeforeCursor(n int, s string) {
	switch {
	case f.isCycle():
	case f.spec.Multiline:
		f.area.ReplaceBeforeCursor(n, s)
	default:
		f.line.ReplaceBeforeCursor(n, s)
	}
}
