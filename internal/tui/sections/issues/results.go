package issues

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/gechr/primer/flash"
	termansi "github.com/gechr/x/ansi"
	xmaps "github.com/gechr/x/maps"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/components/dialog"
	"github.com/matcra587/jira-cli/internal/tui/components/input"
	"github.com/matcra587/jira-cli/internal/tui/components/listviewport"
	"github.com/matcra587/jira-cli/internal/tui/components/markdown"
	"github.com/matcra587/jira-cli/internal/tui/components/suggest"
	"github.com/matcra587/jira-cli/internal/tui/core"
	"github.com/matcra587/jira-cli/internal/tui/theme"
)

// fetchResult, transitionsResult, and bulkResult are delivered as
// TaskFinishedMsg.Result.
type (
	// fetchResult delivers one page of issues plus the opaque continuation
	// cursor, so the list can fetch more when the user scrolls to the bottom.
	fetchResult struct {
		issues []*jira.Issue
		cursor jira.PageCursor
	}
	// fetchMoreResult appends a follow-up page to the existing result set.
	fetchMoreResult struct {
		issues []*jira.Issue
		cursor jira.PageCursor
	}
	transitionsResult struct {
		issueKey    string
		transitions []*jira.Transition
		// bulk routes the result to the bulk picker: the fetch ran against one
		// representative marked issue and the pick applies to the selection.
		bulk bool
	}
	// bulkResult reports the outcome of a bulk write. A per-issue failure
	// (e.g. the target status isn't reachable for that issue) is recorded here
	// rather than aborting the batch, so one bad issue can't strand the rest.
	bulkResult struct {
		verb      string // preformatted outcome suffix, e.g. `→ Done`, `commented`
		succeeded []string
		failed    map[string]error
	}
	// detailResult delivers the fully-fetched issue (description + comments) for
	// the detail view.
	detailResult struct{ issue *jira.Issue }
	// createMetaResult carries a project's issue types (and, on the initial open,
	// the selectable project list) back to the create form. On the initial open
	// it builds the form; when update is set — a project-pill change — it instead
	// swaps the open form's type list in place.
	createMetaResult struct {
		project  string
		types    []jira.ProjectIssueType
		projects []string
		update   bool
	}
)

// summary is the one-line status shown after a bulk write lands. On a
// partial failure it names the issues that didn't take, so the user can retry
// or inspect them rather than being told only a count.
func (b bulkResult) summary() string {
	if len(b.failed) == 0 {
		return fmt.Sprintf("%d %s", len(b.succeeded), b.verb)
	}
	keys := xmaps.KeysNatural(b.failed)
	return fmt.Sprintf("%d %s · failed: %s", len(b.succeeded), b.verb, strings.Join(keys, ", "))
}

// results is the list + sidebar + local filter + action controller shared by the
// Issues and Search sections. It owns everything except the query source: the
// owning section decides what JQL to run and renders its own header, then hands
// the fetched issues here. refetch lets a confirmed mutation reconcile with the
// server using the owner's current query.
// changeKind classifies what a refresh did to a row.
type changeKind int

const (
	changeNone changeKind = iota
	changeNew
	changeUpdated
)

type results struct {
	ctx     *core.ProgramContext
	id      core.SectionID // namespaces task scopes so sections don't collide
	list    *listviewport.Model
	refetch func() tea.Cmd

	all   []*jira.Issue
	shown []*jira.Issue

	// marks is the multi-select set, keyed by issue key. Marks survive a refetch
	// only for issues that are still present; markedKeys filters against all.
	// markAnchor is the last space-toggled key, the start of a range select.
	marks      map[string]bool
	markAnchor string

	// changed marks rows a background refresh added or modified, keyed by
	// issue key, until the user views them; seen is the key→updated snapshot
	// the next refresh diffs against.
	changed map[string]changeKind
	seen    map[string]string

	filter      string
	filtering   bool
	filterInput input.Line

	// facet narrows the rows to one status/assignee/label value on top of
	// the text filter.
	facet facet

	// dialogs is the section's modal overlay stack (facet, jumplist,
	// transition, and bulk-confirm dialogs); each dialog rides it with its
	// outcome bound at push time (see sectionPick/sectionConfirm).
	dialogs *dialog.Stack

	md         *markdown.Renderer // cached, theme-styled body renderer
	headerRows int                // owner header height (set by applySize), for click hit-tests
	loading    bool
	fetched    bool // a fetch has landed at least once; gates the tab-bar count
	err        error
	flash      flash.State   // transient action feedback, auto-cleared
	spin       spinner.Model // animates while a fetch is in flight

	// Pagination for the current query: lastJQL is what runFetch last ran
	// (fetch-more must repeat it verbatim), cursor is the opaque continuation
	// handle from the last page, and loadingMore guards against stacking page
	// fetches while one is in flight.
	lastJQL     string
	cursor      jira.PageCursor
	loadingMore bool

	rollback    func()
	bulkPending bool // a bulk write is in flight; blocks re-entry
	writing     bool // a single-issue write (comment/assign/edit/worklog) is in flight
	// formWriting marks that the in-flight single-issue write came from the open
	// form dialog, so handleTask resolves the form (close on success, inline
	// error on failure) instead of showing a detached toast.
	formWriting bool

	// writeOp tracks the current mutation's activity-registry entry so its
	// footer/log line resolves (done or failed) when the write lands. writeOpID
	// is 0 when no op is recorded; writeOpDesc carries the resolved text and
	// hyperlinked key to apply on success.
	writeOpID   uint64
	writeOpDesc activityDesc

	// createProject is the project the open create form targets; the assignee
	// suggestion source scopes its search to it.
	createProject string
	// Session-scoped suggestion caches for the create form (TTL, in-memory),
	// built lazily on the first create so a repeated open or keystroke reuses
	// the last good result instead of re-hitting Jira. Nil until then.
	issueTypeCache *suggest.Cached[string, []jira.ProjectIssueType]
	assigneeCache  *suggest.Cached[assigneeKey, []*jira.User]
	labelCache     *suggest.Cached[string, []string]
	// projectCache holds the selectable target projects for the create pill,
	// keyed by a constant (the list is tenant-wide, not per project).
	projectCache *suggest.Cached[struct{}, []string]

	// preview is the always-visible issue detail beside/below the list; it
	// scrolls independently (PageUp/PageDown) for issues with long descriptions.
	// previewKey/previewW track the issue and width its content was last rendered
	// for, so syncPreview rebuilds (and scrolls to top) only when the selection or
	// layout actually changes — leaving the scroll offset alone between frames so
	// PageUp/PageDown stick.
	preview    viewport.Model
	previewKey string
	previewW   int

	// detail is the scrollable full-issue view, shown over the list while
	// detailing is true. detailTab selects the Overview/Comments sub-tab.
	detailing     bool
	detailLoading bool
	// detailLines/previewLines are the view.Sync-normalized content lines
	// backing each viewport; RenderContent consumes them for scrollbar alignment.
	detailLines  []string
	previewLines []string
	detailTab    int
	detailIssue  *jira.Issue
	detail       viewport.Model
}

// submittingGlyph is the spinner frame the form dialog's foot row shows beside
// "submitting…". It is MiniDot's first frame — a fixed glyph rather than the
// live list spinner's current tick, so the submitting foot row is stable (and
// its golden byte-stable) whatever the list spinner was doing when the form
// opened. The indicator does not animate; a write is brief.
func submittingGlyph() string {
	return theme.StatusInProgress.Render(spinner.MiniDot.Frames[0])
}

func newResults(ctx *core.ProgramContext, id core.SectionID) results {
	return results{
		ctx: ctx, id: id,
		list: listviewport.New(), preview: viewport.New(), detail: viewport.New(),
		dialogs: dialog.New(newSectionShell(ctx.Styles)),
		md:      markdown.NewRenderer(markdown.StyleFromTheme(theme.Theme)),
		spin:    spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(theme.StatusInProgress)),
	}
}

// Task scopes are namespaced by section ID so Issues and Search — both built and
// both receiving the App's broadcast TaskFinishedMsgs — never apply each other's
// results or share a generation counter.
func (r *results) fetchScope() core.TaskScope { return core.TaskScope(string(r.id) + ".fetch") }

func (r *results) transitionsScope() core.TaskScope {
	return core.TaskScope(string(r.id) + ".transitions")
}

func (r *results) mutateScope() core.TaskScope { return core.TaskScope(string(r.id) + ".mutate") }

func (r *results) detailScope() core.TaskScope { return core.TaskScope(string(r.id) + ".detail") }

func (r *results) createMetaScope() core.TaskScope {
	return core.TaskScope(string(r.id) + ".createmeta")
}

// capturing reports that keys must reach this component directly (filter editing,
// an open action, or the scrollable detail view), bypassing global shortcuts.
func (r *results) capturing() bool {
	return r.filtering || r.detailing || r.modalOpen()
}

// modalOpen reports that an overlay covers the body (any dialog on the stack:
// the text/create form, the facet/jumplist/transition picker, or the bulk
// confirmation), so list-targeted mouse events must not act on the rows
// underneath it.
func (r *results) modalOpen() bool {
	return r.dialogs.Active()
}

// flashDuration is how long a transient status toast stays up.
const flashDuration = 4 * time.Second

// flashClearMsg is a flash clear tick addressed to its originating section:
// every section sees it (core broadcasts SectionMsg), only the owner clears,
// so two sections' flash counters can never wipe each other's toasts.
type flashClearMsg struct {
	id    core.SectionID
	clear flash.ClearMsg
}

// Section implements core.SectionMsg.
func (m flashClearMsg) Section() core.SectionID { return m.id }

// flashNotice shows a transient status message and returns the tick that
// clears it. Setting a newer flash invalidates the older clear tick.
func (r *results) flashNotice(msg string, isErr bool) tea.Cmd {
	clear := flashClearMsg{id: r.id, clear: r.flash.Set(msg, isErr)}
	return tea.Tick(flashDuration, func(time.Time) tea.Msg { return clear })
}

// handleFlashClear clears the toast when the tick is ours.
func (r *results) handleFlashClear(msg flashClearMsg) {
	if msg.id == r.id {
		r.flash.Clear(msg.clear)
	}
}

// busy reports that a fetch is in flight somewhere in the section, which is
// what keeps the spinner ticking.
func (r *results) busy() bool { return r.loading || r.loadingMore || r.detailLoading }

// applySize sizes the list within the main content region, reserving rows for
// the owner's header lines plus the two shared rows view() always draws: the
// status line and the column header.
func (r *results) applySize(reservedHeaderRows int) {
	r.headerRows = reservedHeaderRows // click hit-tests share the layout's header budget
	h := max(r.ctx.MainHeight-reservedHeaderRows-2, 0)
	r.list.SetSize(r.ctx.MainWidth, h)

	// The detail view fills the body, reserving rows for its sub-tab pills and
	// hint line, plus one column for the scrollbar.
	dh := max(r.ctx.BodyHeight-reservedHeaderRows-2, 0)
	r.detail.SetWidth(r.detailWidth())
	r.detail.SetHeight(dh)
	if r.detailIssue != nil {
		r.setDetailContent(renderDetail(r.detailIssue, r.detailLoading, r.detailWidth(), r.detailTab, r.md, r.spin.View(), r.ctx.BaseURL))
	}
	// Rows embed the width (right-aligned age column), so a resize must rebuild
	// them. applyFilter's trailing refreshPreview() also re-sizes and re-renders
	// the preview pane, covering what the old explicit syncPreview() did here.
	r.applyFilter()
}

// Count reports the loaded issue total for the tab bar (core.Counter); ok is
// false until the first fetch lands, so an unvisited section shows no count.
func (r *results) Count() (int, bool) { return len(r.all), r.fetched }

// selectKey moves the cursor to the issue with the given key if it is still in
// the filtered view, and repoints the preview. A miss (or empty key) leaves the
// clamped cursor where applyFilter put it.
func (r *results) selectKey(key string) {
	if key == "" {
		return
	}
	for i, iss := range r.shown {
		if issueKey(iss) == key {
			r.list.SetCursor(i)
			r.syncPreview()
			return
		}
	}
}

func (r *results) selected() *jira.Issue {
	c := r.list.Cursor()
	if c < 0 || c >= len(r.shown) {
		return nil
	}
	return r.shown[c]
}

func (r *results) find(key string) *jira.Issue {
	for _, iss := range r.all {
		if issueKey(iss) == key {
			return iss
		}
	}
	return nil
}

// view composes the header (owner-supplied), status line, list, and sidebar,
// overlaying the action controller when open.
func (r *results) view(header string) string {
	if r.detailing {
		return lipgloss.JoinVertical(lipgloss.Left, header, r.detailPills(), r.detailHint(), r.detailBody())
	}
	main := lipgloss.JoinVertical(lipgloss.Left, header, r.statusLine(), columnHeader(r.ctx.MainWidth-3), r.list.View())

	body := main
	if r.ctx.SidebarOpen && r.ctx.PreviewWidth > 0 {
		side := r.ctx.Styles.Sidebar.Render(vpContent(r.previewLines, r.preview))
		switch r.ctx.PreviewPosition() {
		case core.PreviewRight:
			side = r.ctx.Styles.SidebarBorder.Render(side)
			body = lipgloss.JoinHorizontal(lipgloss.Top, main, side)
		case core.PreviewLeft:
			side = r.ctx.Styles.SidebarBorderRight.Render(side)
			body = lipgloss.JoinHorizontal(lipgloss.Top, side, main)
		default: // bottom
			side = r.ctx.Styles.SidebarBorderTop.Render(side)
			body = lipgloss.JoinVertical(lipgloss.Left, main, side)
		}
	}
	// BodyHeight, not MainHeight: body spans the whole region between the App
	// chrome (the list plus any docked preview), so the dialog centers over all
	// of it and the Shell's height cap has the full body to work with rather
	// than squishing the box into the list portion and scrolling it.
	body = r.dialogs.View(body, r.ctx.ScreenWidth, r.ctx.BodyHeight)
	return body
}

// overlayMargin keeps the section overlays' box off the screen edges when the
// terminal, not core.NewDialogShell's width cap, binds.
const overlayMargin = 6

// restyle re-derives everything this list caches from the theme after a live
// preview swap: the markdown renderer, the spinner (styled at construction),
// the styled rows, and any open detail content. Deliberately no fetch — a
// theme preview must stay free.
func (r *results) restyle() {
	// Rebuild the dialog stack's frame from the re-derived styles so an open
	// pick/confirm overlay never renders with stale chrome (mirrors the App's
	// SetShell in its own restyle path).
	r.dialogs.SetShell(newSectionShell(r.ctx.Styles))
	r.md = markdown.NewRenderer(markdown.StyleFromTheme(theme.Theme))
	// Restyle the spinner in place: replacing the model would mint a new
	// internal id and the in-flight tick chain (whose messages carry the old
	// id) would be rejected, freezing the spinner mid-fetch.
	r.spin.Style = theme.StatusInProgress
	r.applyFilter()
	if r.detailIssue != nil {
		r.setDetailContent(renderDetail(r.detailIssue, r.detailLoading, r.detailWidth(), r.detailTab, r.md, r.spin.View(), r.ctx.BaseURL))
	}
}

// detailHint is the detail view's hint row, mnemonic-styled through the same
// helper as the footer so single-letter verbs read as "(o)pen in browser".
func (r *results) detailHint() string {
	pairs := [][2]string{
		{"esc/enter", "back"},
		{"tab", "switch"},
		{"↑/↓", "scroll"},
		{"o", "open in browser"},
		{"q", "quit"},
	}
	segs := make([]string, 0, len(pairs))
	for _, p := range pairs {
		segs = append(segs, core.HintSegment(r.ctx.Styles, p[0], p[1]))
	}
	// Clamp to the screen: a wrapped hint row would break the detail
	// view's fixed row budget on narrow terminals.
	return termansi.Truncate(strings.Join(segs, "  "), r.ctx.ScreenWidth, "…")
}

func (r *results) statusLine() string {
	switch {
	case r.filtering:
		return r.filterInput.View()
	case r.flash.Active():
		// Above the sticky fetch error: a toast is visible for 4 seconds and
		// the error comes straight back; the other order hides the toast
		// entirely behind an error the user has already seen.
		if r.flash.Err {
			return theme.StatusErr.Render(r.flash.Msg)
		}
		return r.flash.Msg
	case r.err != nil:
		return r.ctx.Styles.Error.Render("error: " + r.err.Error())
	case r.loading:
		// Above the facet chip: a refresh in flight must stay visible even
		// while a facet narrows the rows.
		return r.spin.View() + " loading…"
	case r.facet.active():
		chip := r.facet.String()
		if r.filter != "" {
			chip += " · filter: " + r.filter
		}
		return fmt.Sprintf("%s · %d shown · f to change", chip, len(r.shown))
	case len(r.marks) > 0:
		return fmt.Sprintf("%d selected · t/a/c act on all", len(r.marks))
	case r.filter != "":
		return "filter: " + r.filter
	case r.loadingMore:
		return fmt.Sprintf("%d loaded · %s fetching more…", len(r.all), r.spin.View())
	case r.fetched && r.cursor.More():
		return fmt.Sprintf("%d loaded · ↓ to bottom for more", len(r.all))
	default:
		return ""
	}
}
