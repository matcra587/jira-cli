package dashboard

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/input"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
)

type descriptionIdentity struct{ site, account, route, id string }

func descriptionIdentityOf(session *core.IssueDescriptionSession) descriptionIdentity {
	s := session.Snapshot()
	return descriptionIdentity{site: s.Site, account: s.Account, route: s.APIBaseURL, id: s.IssueID}
}

type (
	descriptionDraft struct {
		session          *core.IssueDescriptionSession
		markdown, notice string
		reviewRequest    uint64
	}
	descriptionDialog struct {
		textDialog
		draft  *descriptionDraft
		reload bool
	}
)

func (d *descriptionDialog) Footer() string { return "^S review · f4 · esc" }
func (d *descriptionDialog) Content(width int) string {
	d.description = "Edit text between markers; keep protected regions intact. Ctrl+S reviews; Esc keeps this session draft. F4 reloads the source after confirmation."
	if d.draft.notice != "" {
		d.description = d.draft.notice + "\n\n" + d.description
	}
	return d.textDialog.Content(width)
}

func (d *descriptionDialog) ScrollTo() (int, int, bool) {
	if d.draft.notice != "" {
		return 0, 1, true
	}
	return d.textDialog.ScrollTo()
}

func (d *descriptionDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	if press, ok := msg.(tea.KeyPressMsg); ok {
		switch press.String() {
		case "ctrl+s":
			return d, nil, dialog.ResultSubmit
		case "f4":
			d.reload = true
			return d, nil, dialog.ResultSubmit
		}
		d.draft.notice = ""
	}
	_, cmd, result := d.textDialog.Update(msg)
	return d, cmd, result
}

type (
	descriptionSource struct {
		epoch              uint64
		session            *core.IssueDescriptionSession
		expectedID, target string
		old                *descriptionDialog
	}
	descriptionPrepared struct {
		epoch    uint64
		editor   *descriptionDialog
		markdown string
		review   core.IssueDescriptionMerge
	}
	descriptionFinished struct {
		operation uint64
		draft     *descriptionDraft
		review    core.IssueDescriptionMerge
		receipt   core.IssueActionReceipt
		key       string
	}
	descriptionReview struct {
		*dialog.Confirm
		prepared descriptionPrepared
	}
)

func (*descriptionReview) Title() string     { return "Review description" }
func (*descriptionReview) Hints() []key.Hint { return nil }
func (*descriptionReview) Footer() string    { return "y replace · n/esc back" }
func (d *descriptionReview) Content(width int) string {
	return strings.Join(layout.WrapLines(d.Confirm.Content(width), width), "\n")
}

func (d *descriptionReview) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.Confirm.Update(msg)
	return d, cmd, result
}

type descriptionReload struct {
	*dialog.Confirm
	editor *descriptionDialog
}

func (*descriptionReload) Title() string     { return "Reload description source?" }
func (*descriptionReload) Hints() []key.Hint { return nil }
func (*descriptionReload) Footer() string    { return "y reload · n/esc back" }
func (d *descriptionReload) Content(width int) string {
	return strings.Join(layout.WrapLines(d.Confirm.Content(width), width), "\n")
}

func (d *descriptionReload) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.Confirm.Update(msg)
	return d, cmd, result
}

func (m *Model) loadDescription(target, expectedID string, old *descriptionDialog) tea.Cmd {
	if old == nil && !m.canWrite() {
		return nil
	}
	if m.options.Actions.Description == nil || m.options.Actions.ReviewDescription == nil || m.options.Actions.ReplaceDescription == nil {
		m.notice = "Native description editing is unavailable."
		return nil
	}
	epoch, read := m.interaction, m.options.Actions.Description
	m.descriptionReadRequest = epoch
	m.notice = "Loading native description…"
	if old != nil {
		old.draft.notice = "Reloading native source…"
	}
	return m.tasks.Start(task.Spec{Scope: "description-source", Run: func() (any, error) {
		session, err := read(target)
		return descriptionSource{epoch: epoch, session: session, expectedID: expectedID, target: target, old: old}, err
	}})
}

func (m *Model) acceptDescriptionSource(msg task.FinishedMsg) {
	result, ok := msg.Result.(descriptionSource)
	if !ok {
		return
	}
	if result.epoch != m.interaction {
		if result.epoch == m.descriptionReadRequest && m.notice == "Loading native description…" {
			m.notice = ""
		}
		if result.old != nil && result.old.draft.notice == "Reloading native source…" {
			result.old.draft.notice = "Source reload was superseded; F4 can retry."
		}
		return
	}
	fail := func(text string) {
		m.notice = text
		if result.old != nil {
			result.old.draft.notice = text
		}
	}
	if msg.Err != nil {
		fail(m.errorText(msg.Err))
		return
	}
	if result.session == nil {
		fail("No native description session was returned.")
		return
	}
	snapshot := result.session.Snapshot()
	if result.expectedID != "" && snapshot.IssueID != result.expectedID {
		fail("Description response changed the immutable issue identity.")
		return
	}
	identity := descriptionIdentityOf(result.session)
	draft := m.descriptionDrafts[identity]
	if result.old != nil {
		if m.dialogs.Top() != result.old || identity != descriptionIdentityOf(result.old.draft.session) {
			fail("Description source changed scope; original draft retained.")
			return
		}
		draft = nil
	}
	if draft == nil {
		draft = &descriptionDraft{session: result.session, markdown: result.session.Markdown()}
	}
	area := input.NewTextArea()
	area.CharLimit = 0
	area.SetValue(draft.markdown)
	area.Focus()
	if area.Value() != draft.markdown {
		fail("This input cannot preserve the native Markdown exactly. Keep the source and use a native description draft editor.")
		return
	}
	d := &descriptionDialog{draft: draft, textDialog: textDialog{title: "Description · " + cli.Text(snapshot.IssueKey), kind: "description", target: snapshot.IssueKey, area: &area}}
	if result.old != nil {
		m.dialogs.Pop()
	}
	m.descriptionDrafts[identity] = draft
	m.notice = ""
	draft.notice = ""
	m.dialogs.Push(d)
}

func (m *Model) acceptDescriptionEditor(d *descriptionDialog) tea.Cmd {
	d.draft.markdown = d.value()
	m.dialogs.Push(d)
	if d.reload {
		d.reload = false
		m.dialogs.Push(&descriptionReload{editor: d, Confirm: cancelDefaultConfirm("Discard this local description edit only after a fresh native source is loaded? Failed reads keep the current draft.", "Reload")})
		return nil
	}
	epoch, review, markdown := m.interaction, m.options.Actions.ReviewDescription, d.value()
	d.draft.reviewRequest = epoch
	d.draft.notice = "Preparing native review…"
	return m.tasks.Start(task.Spec{Scope: "description-review", Run: func() (any, error) {
		merged, err := review(d.draft.session, markdown)
		return descriptionPrepared{epoch: epoch, editor: d, markdown: markdown, review: merged}, err
	}})
}

func (m *Model) keepDescriptionDraft(d *descriptionDialog) {
	d.draft.markdown = d.value()
	identity := descriptionIdentityOf(d.draft.session)
	if d.value() == d.draft.session.Markdown() {
		delete(m.descriptionDrafts, identity)
	} else {
		m.descriptionDrafts[identity] = d.draft
	}
}

func (m *Model) acceptDescriptionPrepared(msg task.FinishedMsg) {
	prepared, ok := msg.Result.(descriptionPrepared)
	if !ok {
		return
	}
	d := prepared.editor
	if prepared.epoch != m.interaction || m.dialogs.Top() != d {
		if d.draft.reviewRequest == prepared.epoch {
			d.draft.notice = "Review was superseded; Ctrl+S reviews current text."
		}
		return
	}
	if msg.Err != nil {
		d.draft.notice = m.errorText(msg.Err)
		return
	}
	if !prepared.review.Changed {
		if prepared.markdown == d.draft.session.Markdown() {
			d.draft.notice = "No local changes to submit. F4 reloads current native text."
		} else {
			d.draft.notice = "Current native content already includes this edit; no write planned."
		}
		return
	}
	d.draft.notice = ""
	text := fmt.Sprintf("Replace description on %s (issue ID %s)?\n\nAuthored anchored Markdown:\n%s\n\nProtected native regions and unknown ADF are preserved. The reviewed native content is checked again before writing.\n\nThis write is unconditional and sends no server-enforced comparison condition. A concurrent change after the final read can still be overwritten.", cli.Text(prepared.review.Current.IssueKey), cli.Text(prepared.review.Current.IssueID), cli.TextBlock(prepared.markdown))
	m.dialogs.Push(&descriptionReview{prepared: prepared, Confirm: cancelDefaultConfirm(text, "Replace")})
}

func (m *Model) submitDescription(d *descriptionReview) tea.Cmd {
	if m.actionBusy || m.options.Actions.ReadOnly || m.options.Actions.ReplaceDescription == nil {
		d.prepared.editor.draft.notice = "Description replacement is currently unavailable; the draft is retained."
		return nil
	}
	if m.dialogs.Top() != d.prepared.editor {
		return nil
	}
	m.dialogs.Pop()
	m.actionBusy = true
	prepared, write := d.prepared, m.options.Actions.ReplaceDescription
	operation := m.startOperation("description", prepared.review.Current.IssueKey+" · issue ID "+prepared.review.Current.IssueID)
	m.notice = "Submitting reviewed native description…"
	return m.tasks.Start(task.Spec{Scope: "description-write", Run: func() (any, error) {
		receipt, key, err := write(prepared.editor.draft.session, prepared.markdown, prepared.review)
		return descriptionFinished{operation: operation, draft: prepared.editor.draft, review: prepared.review, receipt: receipt, key: key}, err
	}})
}

func (m *Model) acceptDescriptionFinished(msg task.FinishedMsg) tea.Cmd {
	m.actionBusy = false
	result, ok := msg.Result.(descriptionFinished)
	if !ok {
		m.notice = "Description outcome unavailable; inspect Jira before repeating."
		return nil
	}
	m.finishOperation(result.operation, result.receipt, msg.Err)
	if result.receipt.Applied {
		delete(m.descriptionDrafts, descriptionIdentityOf(result.draft.session))
		m.notice = "Jira acknowledged description replacement."
		if msg.Err != nil {
			m.notice += " " + m.errorText(msg.Err)
		}
		if result.key != "" {
			return m.openIssue(result.key)
		}
		return nil
	}
	if result.receipt.Attempted {
		m.notice = "Description request attempted; inspect Jira before repeating."
	} else if msg.Err == nil {
		m.notice = "Native description already matches; no write submitted."
	} else {
		m.notice = "Description not submitted. Draft retained."
	}
	if msg.Err != nil {
		m.notice += " " + m.errorText(msg.Err)
	}
	return nil
}
