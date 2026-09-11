// The results list's fetch lifecycle: first-page and
// follow-up loads, task-result application, background refresh, and the
// changed-row diffing that marks what a refresh touched.

package issues

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/components/picker"
	"github.com/matcra587/jira-cli/internal/tui/core"
)

// handleSpinner advances the spinner while busy; an idle section drops the
// tick, ending the stream until the next fetch starts one.
func (r *results) handleSpinner(msg spinner.TickMsg) tea.Cmd {
	if !r.busy() {
		return nil
	}
	var cmd tea.Cmd
	r.spin, cmd = r.spin.Update(msg)
	// The detail body is a cached viewport snapshot; while its comments are
	// still loading, re-render so the embedded spinner actually animates.
	if r.detailing && r.detailLoading {
		r.setDetailContent(renderDetail(r.detailIssue, true, r.detailWidth(), r.detailTab, r.md, r.spin.View(), r.ctx.BaseURL))
	}
	return cmd
}

// runFetch loads the first page of a JQL query as a generation-tracked task.
// The owner supplies the query; the result is applied here via
// handleTask(fetchScope), which also records the pagination token.
func (r *results) runFetch(jql string) tea.Cmd {
	if jql != r.lastJQL {
		// A different query is a new world, not a refresh — diffing its
		// results against the old query's snapshot would mark everything.
		r.seen, r.changed = nil, nil
	}
	r.loading = true
	r.loadingMore = false
	r.lastJQL = jql
	base := r.ctx.Base
	svc := r.ctx.Services
	return tea.Batch(r.spin.Tick, r.ctx.StartTask(core.TaskSpec{
		Scope: r.fetchScope(),
		Run: func() (any, error) {
			if svc == nil {
				return fetchResult{}, nil
			}
			issues, next, err := jira.ListIssuesPage(base, svc.Issues(), &jira.IssueListOptions{
				JQL:        jql,
				Fields:     fetchFields,
				MaxResults: 50,
			}, jira.PageCursor{})
			if err != nil {
				return nil, err
			}
			return fetchResult{issues: issues, cursor: next}, nil
		},
	}))
}

// maybeFetchMore starts the next page when the cursor sits on the last loaded
// row and Jira reported more results — scroll-to-fetch. It is a
// no-op while any fetch is in flight, mid-pagination, or on the final page.
func (r *results) maybeFetchMore() tea.Cmd {
	if !r.cursor.More() || r.loading || r.loadingMore {
		return nil
	}
	if r.list.Cursor() != len(r.shown)-1 || len(r.shown) == 0 {
		return nil
	}
	r.loadingMore = true
	jql, cur := r.lastJQL, r.cursor
	base := r.ctx.Base
	svc := r.ctx.Services
	return tea.Batch(r.spin.Tick, r.ctx.StartTask(core.TaskSpec{
		Scope: r.fetchScope(), // same scope: a new first-page fetch supersedes this
		Run: func() (any, error) {
			if svc == nil {
				return fetchMoreResult{}, nil
			}
			issues, next, err := jira.ListIssuesPage(base, svc.Issues(), &jira.IssueListOptions{
				JQL:        jql,
				Fields:     fetchFields,
				MaxResults: 50,
			}, cur)
			if err != nil {
				return nil, err
			}
			return fetchMoreResult{issues: issues, cursor: next}, nil
		},
	}))
}

// handleTask applies the shared task results (list fetch, transitions, mutation
// outcome). It returns true when it owned the scope.
func (r *results) handleTask(msg core.TaskFinishedMsg) (tea.Cmd, bool) {
	switch msg.Scope {
	case r.fetchScope():
		// Any landing on this scope — first page, follow-up page, or an error
		// from either — ends both in-flight states. Resetting loadingMore here
		// (not just in the fetchMoreResult arm) is what unblocks pagination
		// after a failed page fetch or a superseding first-page fetch.
		r.loading = false
		r.loadingMore = false
		if msg.Err != nil {
			r.err = msg.Err
			return nil, true
		}
		r.err = nil
		switch res := msg.Result.(type) {
		case fetchResult:
			sel := ""
			if cur := r.selected(); cur != nil {
				sel = issueKey(cur)
			}
			r.diffChanges(res.issues)
			r.all = res.issues
			r.fetched = true
			r.cursor = res.cursor
			r.pruneMarks()
			r.applyFilter()
			// Re-find the previously selected issue by key: a refresh (especially a
			// background one) may reorder the results, and keeping the cursor by
			// index would silently swap the issue the user is about to act on.
			r.selectKey(sel)
		case fetchMoreResult:
			// Pagination loads older issues, not changes — register them in the
			// snapshot so the next refresh doesn't call them new, but don't mark.
			if r.seen == nil {
				r.seen = make(map[string]string, len(res.issues))
			}
			for _, iss := range res.issues {
				r.seen[issueKey(iss)] = issueUpdated(iss)
			}
			r.all = append(r.all, res.issues...)
			r.cursor = res.cursor
			// Appending can't move existing rows, so the cursor stays put; the new
			// rows simply extend the list below it.
			r.applyFilter()
		}
		return nil, true
	case r.createMetaScope():
		if msg.Err != nil {
			// Degrade rather than block: open the form on the default type alone
			// (OpenCreate falls back when the option list is empty) and note the
			// load failure, so a transient createmeta error still lets a create
			// through.
			r.openCreateForm(r.createProject, nil, nil)
			return r.flashNotice("✗ couldn't load issue types: "+msg.Err.Error(), true), true
		}
		if res, ok := msg.Result.(createMetaResult); ok {
			if res.update {
				// A project-pill change: swap the open form's type list in place
				// rather than reopening it, keeping the draft and focus intact.
				r.dialogs.Update(formSetTypesMsg{types: issueTypeNames(res.types)})
			} else {
				r.openCreateForm(res.project, res.types, res.projects)
			}
		}
		return nil, true
	case r.transitionsScope():
		if msg.Err != nil {
			r.err = msg.Err
			return nil, true
		}
		if res, ok := msg.Result.(transitionsResult); ok {
			if len(res.transitions) == 0 {
				return r.flashNotice("no transitions available", false), true
			}
			if res.bulk {
				// A bulk pick parks a confirmation instead of mutating directly.
				r.pushPickWithGrace("Transition selection to:", transitionItems(res.transitions), func(sel picker.Item) tea.Cmd {
					return r.parkBulkTransition(sel.Label)
				})
			} else {
				key := res.issueKey
				r.pushPickWithGrace("Transition to:", transitionItems(res.transitions), func(sel picker.Item) tea.Cmd {
					return r.applySingleTransition(key, sel.Value, sel.Label)
				})
			}
		}
		return nil, true
	case r.mutateScope():
		r.bulkPending = false // any mutate completion ends the in-flight batch
		r.writing = false
		r.finishOp(msg.Err) // resolve the footer/log entry for this write
		formWrite := r.formWriting
		r.formWriting = false
		// Resolve an open form dialog: a nil error closes it, a non-nil error
		// reopens it with the message inline and the draft intact. Routed
		// unconditionally — a stack without the form (or with a different
		// dialog) ignores a message it doesn't own.
		r.dialogs.Update(formFinishMsg{err: msg.Err})
		if msg.Err != nil {
			if r.rollback != nil {
				r.rollback()
				r.rollback = nil
			}
			if formWrite {
				// The form now shows the error inline and holds the draft for a
				// retry; a detached toast would double-report it.
				return nil, true
			}
			// The optimistic change is already rolled back; the toast tells
			// the user why, then clears — a write error is transient state,
			// not a sticky section error.
			return r.flashNotice("✗ "+msg.Err.Error(), true), true
		}
		r.rollback = nil
		r.err = nil
		// A bulk transition reports per-issue outcomes in its result (the task
		// itself never errors on a partial failure); show the tally and drop the
		// selection before reconciling with the server.
		var note tea.Cmd
		if res, ok := msg.Result.(bulkResult); ok {
			note = r.flashNotice(res.summary(), false)
			r.marks = nil
		}
		if r.refetch != nil {
			return tea.Batch(note, r.refetch()), true
		}
		return note, true
	case r.detailScope():
		r.detailLoading = false
		if msg.Err != nil {
			r.err = msg.Err
			return nil, true
		}
		if res, ok := msg.Result.(detailResult); ok && res.issue != nil {
			r.detailIssue = res.issue
			// Re-touch with the fetched summary: a foreign-key jump opened a
			// bare stub, and a rename since the last visit is stale otherwise.
			r.ctx.Recent.Touch(issueKey(res.issue), issueSummary(res.issue))
			r.setDetailContent(renderDetail(res.issue, false, r.detailWidth(), r.detailTab, r.md, r.spin.View(), r.ctx.BaseURL))
		}
		return nil, true
	}
	return nil, false
}

// autoRefresh refetches on the shared refresh tick. It deliberately skips when
// the user is mid-interaction (filter/modal/detail), a fetch is already in
// flight, or a mutation is reconciling — a background reload must never stomp
// optimistic state or yank the view out from under the user.
func (r *results) autoRefresh() tea.Cmd {
	// loadingMore counts as in-flight too: a tick mid-pagination would
	// supersede the page fetch and yank the user back to page one.
	if r.refetch == nil || r.capturing() || r.loading || r.loadingMore || !r.canMutate() {
		return nil
	}
	return r.refetch()
}

// diffChanges compares an incoming refresh against the last snapshot and
// marks rows that are new or carry a newer updated timestamp. The first
// fetch only seeds the snapshot — flagging the whole landing page as "new"
// would be noise.
func (r *results) diffChanges(incoming []*jira.Issue) {
	prev := r.seen
	page := make(map[string]string, len(incoming))
	for _, iss := range incoming {
		page[issueKey(iss)] = issueUpdated(iss)
	}
	if prev != nil {
		for _, iss := range incoming {
			key := issueKey(iss)
			was, known := prev[key]
			switch {
			case !known:
				r.markChanged(key, changeNew)
			case was != issueUpdated(iss):
				r.markChanged(key, changeUpdated)
			}
		}
		// Drop marks for rows that left the page: they aren't renderable, so
		// the map would only grow without bound.
		for key := range r.changed {
			if _, ok := page[key]; !ok {
				delete(r.changed, key)
			}
		}
	}
	// The next snapshot is the page plus everything previously seen that the
	// page doesn't override — a paginated-in issue that re-ranks onto page
	// one later must not read as "new". The snapshot resets with the query.
	for key, was := range prev {
		if _, ok := page[key]; !ok {
			page[key] = was
		}
	}
	r.seen = page
}

func (r *results) markChanged(key string, kind changeKind) {
	if r.changed == nil {
		r.changed = make(map[string]changeKind)
	}
	r.changed[key] = kind
}

// clearChanged drops an issue's change mark, reporting whether one cleared
// (the rows then need a rebuild for the dot to disappear).
func (r *results) clearChanged(key string) bool {
	if key == "" || r.changed[key] == changeNone {
		return false
	}
	delete(r.changed, key)
	return true
}

// viewSelected clears the selected row's change dot — deliberate navigation
// onto a row counts as viewing it — and repaints when one cleared. It is
// called from user-driven selection paths only, never from refresh-side
// preview syncs, where a stale cursor index could wipe an unviewed mark.
func (r *results) viewSelected() {
	if sel := r.selected(); sel != nil && r.clearChanged(issueKey(sel)) {
		r.rebuildRows()
	}
}
