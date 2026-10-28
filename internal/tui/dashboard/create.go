package dashboard

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/input"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type creationDraft struct {
	project                                core.IssueCreateProjectRead
	typeID, typeName, summary, markdown    string
	labels                                 []string
	labelsSet                              bool
	pendingLabel                           string
	assignee                               jira.Nullable[string]
	assigneeName                           string
	locked                                 bool
	outcome                                envelope.IssueCreateOutput
	notice                                 string
	reviewRequest                          uint64
	attempted                              core.IssueCreationInput
	attemptedProjectKey, attemptedTypeName string
}
type (
	creationEditor struct {
		draft  *creationDraft
		editor input.Editor
	}
	creationChoices struct {
		draft   *creationDraft
		request uint64
		kind    string
		choices []choice
		project core.IssueCreateProjectRead
	}
)

type (
	creationPrepared struct {
		draft   *creationDraft
		epoch   uint64
		input   core.IssueCreationInput
		request jira.IssueWriteRequest
	}
	creationFinished struct {
		draft     *creationDraft
		operation uint64
		outcome   envelope.IssueCreateOutput
	}
	creationReview struct {
		*dialog.Confirm
		prepared creationPrepared
	}
)

type creationDiscard struct {
	*dialog.Confirm
	draft *creationDraft
}

func (*creationDiscard) Title() string     { return "Discard new issue draft?" }
func (*creationDiscard) Hints() []key.Hint { return nil }
func (*creationDiscard) Footer() string    { return "y discard · n/esc back" }
func (d *creationDiscard) Content(width int) string {
	return strings.Join(layout.WrapLines(d.Confirm.Content(width), width), "\n")
}

func (d *creationDiscard) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.Confirm.Update(msg)
	return d, cmd, result
}

func (*creationReview) Title() string     { return "Review new issue" }
func (*creationReview) Hints() []key.Hint { return nil }
func (*creationReview) Footer() string    { return "y create · n/esc back" }
func (d *creationReview) Content(width int) string {
	return strings.Join(layout.WrapLines(d.Confirm.Content(width), width), "\n")
}

func (d *creationReview) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.Confirm.Update(msg)
	return d, cmd, result
}

type creationText struct {
	textDialog
	draft *creationDraft
}

func (d *creationText) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.textDialog.Update(msg)
	return d, cmd, result
}

type creationLabels struct {
	labelsDialog
	draft *creationDraft
}

type creationOutcomeDialog struct{ draft *creationDraft }

func (*creationOutcomeDialog) Title() string     { return "Previous creation draft" }
func (*creationOutcomeDialog) Hints() []key.Hint { return nil }
func (*creationOutcomeDialog) Footer() string    { return "pgup/down · esc" }
func (d *creationOutcomeDialog) Content(width int) string {
	authored := d.draft.attempted
	text := "This draft belongs to an attempted create. Inspect Jira before starting another.\n\n" + d.draft.attemptedProjectKey + " · project ID " + authored.ProjectID + "\n" + d.draft.attemptedTypeName + " · type ID " + authored.IssueTypeID + "\n\nSummary:\n" + authored.Summary + "\n\nDescription:\n" + authored.Markdown + "\n\nLabels:\n" + creationLabelsText(authored.Labels) + "\n\nAssignee: " + creationAssigneeText(authored.Assignee)
	return strings.Join(layout.WrapLines(cli.TextBlock(text), width), "\n")
}

func creationLabelsText(value jira.Nullable[[]string]) string {
	if !value.Set {
		return "Jira default (omitted)"
	}
	if value.Null {
		return "Explicit null"
	}
	if len(value.Value) == 0 {
		return "No labels (explicit empty array)"
	}
	return strings.Join(value.Value, "\n")
}

func creationAssigneeText(value jira.Nullable[string]) string {
	if !value.Set {
		return "Jira default (omitted)"
	}
	if value.Null {
		return "Unassigned (explicit null)"
	}
	return value.Value
}

func (d *creationOutcomeDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	if press, ok := msg.(tea.KeyPressMsg); ok && (press.String() == "esc" || press.String() == "enter" || press.String() == "q") {
		return d, nil, dialog.ResultClose
	}
	return d, nil, dialog.ResultNone
}

func (d *creationLabels) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.labelsDialog.Update(msg)
	return d, cmd, result
}

func (m *Model) canCreate() bool {
	if m.actionBusy {
		m.notice = "A Jira action is still running."
		return false
	}
	if m.options.Actions.ReadOnly || m.options.Actions.Create == nil || m.options.Actions.PrepareCreate == nil {
		m.notice = "Issue creation is unavailable in read-only mode."
		return false
	}
	return true
}

func (m *Model) pickCreation(title, kind string, choices []choice) {
	m.pick(title, kind, "", choices)
	m.dialogs.Top().(*pickerDialog).creation = m.creation
}

func (m *Model) openCreation() tea.Cmd {
	if !m.canCreate() {
		return nil
	}
	if m.creation == nil {
		m.creation = &creationDraft{}
	}
	if m.creation.locked {
		m.pickCreation("Previous create attempt", "create-previous", []choice{{label: "Inspect operation history", value: "history"}, {label: "View previous draft", value: "draft"}, {label: "Start a different new draft", description: "Replaces this session draft; inspect Jira before continuing", value: "new"}})
		return nil
	}
	if m.creation.project.ID == "" {
		return m.creationProjects()
	}
	if m.creation.typeID == "" {
		return m.creationTypes()
	}
	m.showCreationEditor()
	return nil
}

func (m *Model) creationProjects() tea.Cmd {
	draft := m.creation
	choices := []choice{{label: "Enter project key or ID", value: ""}}
	if m.options.Project != "" {
		choices = append(choices, choice{label: "Configured project · " + cli.Text(m.options.Project), value: m.options.Project})
	}
	m.pickCreation("New issue · project", "create-project", choices)
	if m.options.Actions.Projects == nil {
		return nil
	}
	epoch := m.interaction
	read := m.options.Actions.Projects
	return m.tasks.Start(task.Spec{Scope: "create-choices", Run: func() (any, error) {
		projects, complete, err := read()
		for _, project := range projects {
			choices = append(choices, choice{label: cli.Text(project.Key) + " · " + cli.Text(project.ID), value: project.ID})
		}
		if !complete {
			choices[0].description = "Project list is bounded; enter another exact key or ID"
		}
		return creationChoices{draft: draft, request: epoch, kind: "create-project", choices: choices}, err
	}})
}

func (m *Model) resolveCreationProject(selector string) tea.Cmd {
	if m.options.Actions.Project == nil {
		return nil
	}
	draft, epoch, read := m.creation, m.interaction, m.options.Actions.Project
	return m.tasks.Start(task.Spec{Scope: "create-choices", Run: func() (any, error) {
		project, err := read(selector)
		return creationChoices{draft: draft, request: epoch, kind: "create-project-resolved", project: project}, err
	}})
}

func (m *Model) creationTypes() tea.Cmd {
	if m.options.Actions.CreateTypes == nil {
		return nil
	}
	draft, epoch, read, project := m.creation, m.interaction, m.options.Actions.CreateTypes, m.creation.project.ID
	return m.tasks.Start(task.Spec{Scope: "create-choices", Run: func() (any, error) {
		types, err := read(project)
		choices := make([]choice, 0, len(types))
		for id, native := range types {
			var value struct {
				Name string `json:"name"`
			}
			if parseErr := json.Unmarshal(native, &value); parseErr != nil {
				return nil, parseErr
			}
			choices = append(choices, choice{label: cli.Text(value.Name) + " · " + cli.Text(id), description: value.Name, value: id})
		}
		slices.SortFunc(choices, func(a, b choice) int { return strings.Compare(a.label, b.label) })
		return creationChoices{draft: draft, request: epoch, kind: "create-type", choices: choices}, err
	}})
}

func (m *Model) acceptCreationChoices(msg task.FinishedMsg) tea.Cmd {
	result, ok := msg.Result.(creationChoices)
	if !ok || result.draft != m.creation || result.request != m.interaction {
		return nil
	}
	if msg.Err != nil {
		if result.kind == "create-type" {
			m.creation.project = core.IssueCreateProjectRead{}
			m.creation.typeID = ""
		}
		m.notice = m.errorText(msg.Err)
		if m.creationEditor != nil {
			m.creation.notice = m.notice
		}
		return nil
	}
	if result.kind == "create-project-resolved" {
		m.creation.project = result.project
		m.creation.typeID = ""
		m.creation.typeName = ""
		return m.creationTypes()
	}
	if len(result.choices) == 0 {
		if result.kind == "create-type" {
			m.creation.project = core.IssueCreateProjectRead{}
			m.creation.typeID = ""
		}
		m.notice = "No creation choices were returned."
		return nil
	}
	if result.kind == "create-project" {
		if top, ok := m.dialogs.Top().(*pickerDialog); !ok || top.creation != m.creation || top.kind != "create-project" {
			return nil
		}
		m.dialogs.Pop()
	}
	m.pickCreation("New issue · "+strings.TrimPrefix(result.kind, "create-"), result.kind, result.choices)
	return nil
}

func (m *Model) creationLine(title, kind, description string) {
	m.dialogs.Push(&creationText{draft: m.creation, textDialog: textDialog{title: title, kind: kind, description: description, line: input.NewLine("› ", "")}})
}

func (m *Model) acceptCreationPicker(d *pickerDialog, value string) tea.Cmd {
	if d.creation != m.creation || m.creation == nil {
		return nil
	}
	switch d.kind {
	case "create-previous":
		switch value {
		case "history":
			m.openOperations()
		case "draft":
			m.dialogs.Push(&creationOutcomeDialog{draft: m.creation})
		case "new":
			m.creation = &creationDraft{}
			return m.creationProjects()
		}
	case "create-project":
		if value == "" {
			m.creationLine("Project key or ID", "create-project-key", "Choose the project for this new issue.")
			return nil
		}
		return m.resolveCreationProject(value)
	case "create-type":
		m.creation.typeID = value
		for _, c := range d.choices {
			if c.value == value {
				m.creation.typeName = c.description
				break
			}
		}
		m.showCreationEditor()
	case "create-assign-mode":
		switch value {
		case "omit":
			m.creation.assignee = jira.Nullable[string]{}
			m.creation.assigneeName = "Jira default"
		case "clear":
			m.creation.assignee = jira.NullableNull[string]()
			m.creation.assigneeName = "Unassigned"
		case "id":
			m.creationLine("Assignee account ID", "create-assign-id", "Use an exact account ID. Jira decides assignability.")
		case "search":
			m.creationLine("Find assignee", "create-find-user", "Search the first 50 visible active users.")
		case "me":
			if m.options.Actions.CurrentUser == nil {
				return nil
			}
			draft, epoch, read := m.creation, m.interaction, m.options.Actions.CurrentUser
			return m.tasks.Start(task.Spec{Scope: "create-choices", Run: func() (any, error) {
				id, err := read()
				return creationChoices{draft: draft, request: epoch, kind: "create-assignee", choices: []choice{{label: "Authenticated account · " + cli.Text(id), value: id}}}, err
			}})
		}
	case "create-assignee":
		m.creation.assignee = jira.NullableOf(value)
		if entry, ok := d.menu.Selected(); ok {
			m.creation.assigneeName = entry.Name
		}
	}
	return nil
}

func (m *Model) acceptCreationText(d *creationText) tea.Cmd {
	if d.draft != m.creation {
		return nil
	}
	value := strings.TrimSpace(d.value())
	switch d.kind {
	case "create-project-key":
		return m.resolveCreationProject(value)
	case "create-assign-id":
		m.creation.assignee = jira.NullableOf(value)
		m.creation.assigneeName = value
	case "create-find-user":
		if m.options.Actions.Users == nil {
			return nil
		}
		draft, epoch, read := m.creation, m.interaction, m.options.Actions.Users
		return m.tasks.Start(task.Spec{Scope: "create-choices", Run: func() (any, error) {
			users, err := read(value)
			choices := make([]choice, 0, len(users))
			for _, user := range users {
				choices = append(choices, choice{label: cli.Text(user.DisplayName.Value + " · " + user.AccountID.Value), value: user.AccountID.Value})
			}
			return creationChoices{draft: draft, request: epoch, kind: "create-assignee", choices: choices}, err
		}})
	}
	return nil
}

func (m *Model) showCreationEditor() {
	draft := m.creation
	editor := input.NewEditor([]input.EditorEntry{{Label: "New issue", Title: draft.summary}}, input.WithEditorBodyMinHeight(1), input.WithBodyFetch(func(int) (string, error) { return draft.markdown, nil }))
	// Seed the already-local draft through the pinned editor's public lifecycle.
	// This callback performs no I/O and cannot outlive or target another editor.
	if seed := editor.Init(); seed != nil {
		next, _ := editor.Update(seed())
		editor = next.(input.Editor)
	}
	m.creationEditor = &creationEditor{draft: draft, editor: editor}
	m.resizeCreationEditor()
}

func (m *Model) resizeCreationEditor() {
	if m.creationEditor != nil {
		next, _ := m.creationEditor.editor.Update(tea.WindowSizeMsg{Width: max(1, m.pageWidth()-1), Height: max(1, m.height-2)})
		m.creationEditor.editor = next.(input.Editor)
	}
}

func (m *Model) saveCreationEditor() {
	if m.creationEditor != nil {
		result := m.creationEditor.editor.Results()[0]
		m.creation.summary = result.Title
		m.creation.markdown = result.Body
	}
}

func (m *Model) creationPage() tea.View {
	view := m.creationEditor.editor.View()
	lines := strings.Split(view.Content, "\n")
	lines[len(lines)-1] = "tab · f2 labels · f3 assign · f5 new"
	lines = append(lines, "f4 context · ctrl+s review · esc")
	header := cli.Text(m.creation.project.Key+" · "+m.creation.typeName) + fmt.Sprintf(" · %d labels", len(m.creation.labels))
	if m.creation.assigneeName != "" {
		header += " · " + cli.Text(m.creation.assigneeName)
	}
	if m.creation.notice != "" {
		header = cli.Text(m.creation.notice)
	}
	view.Content = frame(header+"\n"+strings.Join(lines, "\n"), m.pageWidth(), m.height)
	if view.Cursor != nil {
		view.Cursor.Y++
	}
	return view
}

func (m *Model) updateCreationEditor(msg tea.Msg) tea.Cmd {
	if press, ok := msg.(tea.KeyPressMsg); ok {
		switch press.String() {
		case "esc":
			m.saveCreationEditor()
			m.creationEditor = nil
			m.notice = "New issue draft kept in this session; n resumes it."
			return nil
		case "ctrl+s":
			m.saveCreationEditor()
			return m.prepareCreation()
		case "f2":
			d := &creationLabels{draft: m.creation, labelsDialog: labelsDialog{target: "New issue", labels: slices.Clone(m.creation.labels), original: slices.Clone(m.creation.labels), rows: m.newLabelRows(), line: input.NewLine("+ ", "Literal label"), adding: true}}
			d.hint = "Ctrl+S uses this list; Esc keeps the draft."
			d.line.SetValue(m.creation.pendingLabel)
			d.rebuild()
			m.dialogs.Push(d)
			return nil
		case "f3":
			m.pickCreation("New issue · assignee", "create-assign-mode", []choice{{label: "Jira default", value: "omit"}, {label: "Unassign", value: "clear"}, {label: "Assign to me", value: "me"}, {label: "Find a person", value: "search"}, {label: "Use account ID", value: "id"}})
			return nil
		case "f4":
			m.saveCreationEditor()
			m.creationEditor = nil
			return m.creationProjects()
		case "f5":
			m.saveCreationEditor()
			m.dialogs.Push(&creationDiscard{draft: m.creation, Confirm: cancelDefaultConfirm("Discard this session's unsubmitted summary, description, labels and assignee and start a new draft? No Jira issue will be created.", "Discard")})
			return nil
		}
	}
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.creation.notice = ""
	}
	if _, ok := msg.(tea.PasteMsg); ok {
		m.creation.notice = ""
	}
	next, cmd := m.creationEditor.editor.Update(msg)
	m.creationEditor.editor = next.(input.Editor)
	return cmd
}

func (m *Model) prepareCreation() tea.Cmd {
	draft := m.creation
	if strings.TrimSpace(draft.summary) == "" {
		draft.notice = "Summary is required before review."
		return nil
	}
	if draft.pendingLabel != "" {
		if !slices.Contains(draft.labels, draft.pendingLabel) {
			draft.labels = append(draft.labels, draft.pendingLabel)
		}
		draft.labelsSet = true
		draft.pendingLabel = ""
	}
	authored := core.IssueCreationInput{ProjectID: draft.project.ID, IssueTypeID: draft.typeID, Summary: draft.summary, Markdown: draft.markdown, Assignee: draft.assignee}
	if draft.labelsSet {
		labels := slices.Clone(draft.labels)
		if labels == nil {
			labels = []string{}
		}
		authored.Labels = jira.NullableOf(labels)
	}
	epoch, prepare := m.interaction, m.options.Actions.PrepareCreate
	draft.reviewRequest = epoch
	draft.notice = "Preparing review…"
	return m.tasks.Start(task.Spec{Scope: "create-review", Run: func() (any, error) {
		request, err := prepare(authored)
		return creationPrepared{draft: draft, epoch: epoch, input: authored, request: request}, err
	}})
}

func (m *Model) acceptCreationPrepared(msg task.FinishedMsg) {
	prepared, ok := msg.Result.(creationPrepared)
	if !ok || prepared.draft != m.creation {
		return
	}
	if prepared.epoch != m.interaction || m.creationEditor == nil || m.dialogs.Active() {
		if m.creation.reviewRequest == prepared.epoch {
			m.creation.notice = "Review was superseded; Ctrl+S prepares current values."
		}
		return
	}
	if msg.Err != nil {
		m.creation.notice = m.errorText(msg.Err)
		return
	}
	m.creation.notice = ""
	description := "Create " + cli.Text(m.creation.typeName) + " in " + cli.Text(m.creation.project.Key) + "?\n\nSummary:\n" + cli.Text(prepared.input.Summary) + "\n\nDescription (Markdown):\n" + cli.TextBlock(prepared.input.Markdown) + "\n\nLabels:\n" + cli.TextBlock(creationLabelsText(prepared.input.Labels)) + "\n\nAssignee: " + cli.Text(creationAssigneeText(prepared.input.Assignee)) + "\n\nProject ID: " + cli.Text(prepared.input.ProjectID) + " · type ID: " + cli.Text(prepared.input.IssueTypeID) + "\nCreation is submitted once. Inspect an uncertain attempt before creating again."
	m.dialogs.Push(&creationReview{Confirm: cancelDefaultConfirm(description, "Create"), prepared: prepared})
}

func (m *Model) submitCreation(d *creationReview) tea.Cmd {
	if !m.canCreate() || d.prepared.draft != m.creation || m.creation.locked {
		return nil
	}
	draft, request, write := m.creation, d.prepared.request, m.options.Actions.Create
	draft.attempted = d.prepared.input
	draft.attempted.Labels.Value = slices.Clone(d.prepared.input.Labels.Value)
	draft.attemptedProjectKey, draft.attemptedTypeName = draft.project.Key, draft.typeName
	draft.locked = true
	m.actionBusy = true
	m.creationEditor = nil
	operation := m.startOperation("create", draft.project.Key+" · type ID "+draft.typeID)
	m.notice = "Submitting new issue…"
	return m.tasks.Start(task.Spec{Scope: "create-write", Run: func() (any, error) {
		outcome, err := write(request)
		return creationFinished{draft: draft, operation: operation, outcome: outcome}, err
	}})
}

func (m *Model) acceptCreationFinished(msg task.FinishedMsg) tea.Cmd {
	m.actionBusy = false
	result, ok := msg.Result.(creationFinished)
	if !ok {
		m.notice = "Creation outcome unavailable; inspect Jira before creating again."
		return nil
	}
	result.draft.outcome = result.outcome
	result.draft.locked = result.outcome.Attempted || result.outcome.Applied
	m.finishOperation(result.operation, core.IssueActionReceipt{Attempted: result.outcome.Attempted, Applied: result.outcome.Applied}, msg.Err)
	if result.outcome.Applied && result.outcome.Issue != nil {
		issue := result.outcome.Issue
		for i := range m.operations {
			if m.operations[i].id == result.operation {
				m.operations[i].detail = "Created " + cli.Text(issue.Key) + " · issue ID " + cli.Text(issue.ID) + " · " + m.operations[i].detail
			}
		}
		m.notice = "Jira acknowledged creation of " + cli.Text(issue.Key) + "."
		if msg.Err != nil {
			m.notice += " " + m.errorText(msg.Err)
		}
		return m.openIssue(issue.Key)
	}
	switch {
	case result.outcome.Applied:
		m.notice = "Jira acknowledged creation without a usable identity. Inspect the project before creating again."
	case result.outcome.Attempted:
		m.notice = "Creation attempted; inspect the project before creating again."
	default:
		m.notice = "No creation submitted; n resumes the draft."
	}
	if msg.Err != nil {
		m.notice += " " + m.errorText(msg.Err)
	}
	return nil
}
