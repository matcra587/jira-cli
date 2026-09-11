// Package goldens pins the dashboard's rendered frames at the two reference
// terminal sizes (80x24 and 120x40) per section. The App is driven
// synchronously through its Update loop with fake services and the final
// frame is asserted against golden files (teatest's golden helper; run with
// -update to regenerate).
//
// A full teatest program run is deliberately not used for the assertion:
// the loading spinner renders a timing-dependent number of frames into the
// cumulative output stream, so whole-stream goldens flake. Driving Update
// directly and asserting the settled frame keeps the golden byte-stable.
package goldens

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/core"
	"github.com/matcra587/jira-cli/internal/tui/sections/issues"
	"github.com/matcra587/jira-cli/internal/tui/sections/settings"
)

// fakeIssueSvc serves a fixed issue list; embedding satisfies the rest.
type fakeIssueSvc struct {
	jira.IssueService
	issues []*jira.Issue
}

func (f fakeIssueSvc) List(context.Context, *jira.IssueListOptions) ([]*jira.Issue, *jira.Response, error) {
	return f.issues, nil, nil
}

// Transitions serves a fixed workflow so the transition-pick golden has choices
// to render; the pick is by name, so ids are arbitrary here.
func (fakeIssueSvc) Transitions(context.Context, string) ([]*jira.Transition, *jira.Response, error) {
	mk := func(id, name string) *jira.Transition { return &jira.Transition{ID: &id, Name: &name} }
	return []*jira.Transition{mk("11", "To Do"), mk("21", "In Progress"), mk("31", "Done")}, nil, nil
}

type fakeJQLSvc struct{ jira.JQLService }

func (fakeJQLSvc) Parse(context.Context, []string, string) ([]jira.ParsedQuery, *jira.Response, error) {
	return []jira.ParsedQuery{{}}, nil, nil
}

// fakeProjectSvc serves a fixed issue-type list and project list so the create
// overlay's type and project cycle fields have options to render.
type fakeProjectSvc struct{ jira.ProjectService }

func (fakeProjectSvc) ListIssueTypes(context.Context, string) ([]jira.ProjectIssueType, *jira.Response, error) {
	return []jira.ProjectIssueType{{ID: "1", Name: "Task"}, {ID: "2", Name: "Epic"}, {ID: "3", Name: "Subtask"}}, nil, nil
}

func (fakeProjectSvc) List(context.Context, *jira.ListOptions) ([]jira.ProjectSummary, *jira.Response, error) {
	return []jira.ProjectSummary{{Key: "JCT"}, {Key: "PROJ"}}, nil, nil
}

type fakeServices struct {
	core.Services
	issue jira.IssueService
}

func (f fakeServices) Issues() jira.IssueService     { return f.issue }
func (f fakeServices) JQL() jira.JQLService          { return fakeJQLSvc{} }
func (f fakeServices) Projects() jira.ProjectService { return fakeProjectSvc{} }

func mkIssue(key, status, summary, issueType, priority, assignee string) *jira.Issue {
	k, st, su := key, status, summary
	// Categories mirror what the live API attaches, so the pills pin the
	// category-keyed palette rather than the per-name hash fallback.
	categories := map[string][2]string{
		"To Do":       {"new", "blue-gray"},
		"In Progress": {"indeterminate", "yellow"},
		"Done":        {"done", "green"},
	}
	stat := &jira.Status{Name: &st}
	if c, ok := categories[status]; ok {
		catKey, catColor := c[0], c[1]
		stat.StatusCategory = &jira.StatusCategory{Key: &catKey, ColorName: &catColor}
	}
	f := &jira.IssueFields{Summary: &su, Status: stat}
	if issueType != "" {
		f.IssueType = &jira.IssueType{Name: &issueType}
	}
	if priority != "" {
		f.Priority = &jira.Priority{Name: &priority}
	}
	if assignee != "" {
		f.Assignee = &jira.User{DisplayName: &assignee}
	}
	return &jira.Issue{Key: &k, Fields: f}
}

func fixtureIssues() []*jira.Issue {
	return []*jira.Issue{
		mkIssue("JCT-1", "To Do", "Wire the flux capacitor", "Task", "High", "Ann Example"),
		mkIssue("JCT-2", "In Progress", "Reticulate splines before launch window", "Bug", "Highest", "Bob Sample"),
		mkIssue("JCT-3", "Done", "Document the splines", "Subtask", "Low", ""),
	}
}

// buildApp wires a dashboard exactly like the real entrypoint, minus the
// live client: issues + search + settings (always last, as the real order
// resolver appends it), fake services, no config file.
func buildApp() core.App {
	return buildAppWith(fakeIssueSvc{issues: fixtureIssues()})
}

// buildAppWith is buildApp over a caller-supplied issue service, so a golden
// can drive a gated write (see gatedIssueSvc) instead of the default fixture.
func buildAppWith(issue jira.IssueService) core.App {
	ctx := core.NewProgramContext(fakeServices{issue: issue}, nil)
	reg := core.NewRegistry()
	reg.Register(issues.ID, issues.New)
	reg.Register(issues.SearchID, issues.NewSearch)
	reg.Register(settings.ID, settings.New)
	ctx.Version = "v0.0.0-test"
	ctx.ProfileName = "default"
	ctx.Project = "JCT"
	return core.NewApp(ctx, reg, []core.SectionID{issues.ID, issues.SearchID, settings.ID})
}

// drive runs the model's Update loop over the cmd graph until it settles:
// every cmd executes on its own goroutine (a 60s refresh tick simply never
// delivers), batches fan out, and the loop stops once no message has
// arrived for a quiet period.
func drive(t *testing.T, m tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	msgs := make(chan tea.Msg, 256)
	var inflight atomic.Int64
	exec := func(c tea.Cmd) {
		if c == nil {
			return
		}
		inflight.Add(1)
		go func() {
			defer inflight.Add(-1)
			if msg := c(); msg != nil {
				// Non-blocking: a tick that fires after drive returned (the
				// refresh heartbeat, a flash clear) must not strand its
				// goroutine on a full channel across -count runs.
				select {
				case msgs <- msg:
				default:
				}
			}
		}()
	}
	exec(cmd)
	const quietWindow = 250 * time.Millisecond
	quiet := time.NewTimer(quietWindow)
	defer quiet.Stop()
	for {
		select {
		case msg := <-msgs:
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, c := range batch {
					exec(c)
				}
				continue
			}
			var c tea.Cmd
			m, c = m.Update(msg)
			exec(c)
			quiet.Reset(quietWindow)
		case <-quiet.C:
			// Don't settle while cmds are still running (a slow scheduler
			// could otherwise hand back a half-processed frame) — long-lived
			// timers (the 60s refresh tick) are the documented exception: two
			// consecutive quiet windows with the same in-flight count means
			// the rest are timers, not work.
			if inflight.Load() == 0 {
				return m
			}
			select {
			case msg := <-msgs:
				if batch, ok := msg.(tea.BatchMsg); ok {
					for _, c := range batch {
						exec(c)
					}
				} else {
					var c tea.Cmd
					m, c = m.Update(msg)
					exec(c)
				}
				quiet.Reset(quietWindow)
			case <-time.After(quietWindow):
				return m // only timers left in flight
			}
		}
	}
}

// frame renders the settled dashboard at the given size, optionally after
// extra key presses (e.g. tab to reach the search section).
func frame(t *testing.T, w, h int, keys ...tea.KeyPressMsg) string {
	t.Helper()
	app := buildApp()
	var m tea.Model = app
	init := m.(core.App).Init()
	m = drive(t, m, init)
	m = drive(t, m, func() tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} })
	for _, k := range keys {
		key := k
		m = drive(t, m, func() tea.Msg { return key })
	}
	return m.(core.App).View().Content
}

func assertGolden(t *testing.T, content string, wantHeight int) {
	t.Helper()
	// Every frame must be exactly the advertised height — a drifting footer
	// or an overflowing body is a layout regression even when the golden is
	// regenerated with -update.
	if got := len(strings.Split(strings.TrimRight(content, "\n"), "\n")); got != wantHeight {
		t.Errorf("frame height = %d lines, want %d", got, wantHeight)
	}
	teatest.RequireEqualOutput(t, []byte(content))
}

func tab() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyTab} }

func TestGoldenIssues80x24(t *testing.T) {
	assertGolden(t, frame(t, 80, 24), 24)
}

// TestGoldenIssues60x18 pins the narrow-terminal degradation: columns drop
// per the row layout plan and every chrome row clamps instead of wrapping —
// the frame-height assertion is the real guard here.
func TestGoldenIssues60x18(t *testing.T) {
	assertGolden(t, frame(t, 60, 18), 18)
}

func TestGoldenIssues120x40(t *testing.T) {
	assertGolden(t, frame(t, 120, 40), 40)
}

func TestGoldenSearch80x24(t *testing.T) {
	assertGolden(t, frame(t, 80, 24, tab()), 24)
}

func TestGoldenSearch120x40(t *testing.T) {
	assertGolden(t, frame(t, 120, 40, tab()), 40)
}

func TestGoldenSettings80x24(t *testing.T) {
	assertGolden(t, frame(t, 80, 24, tab(), tab()), 24)
}

func TestGoldenSettings120x40(t *testing.T) {
	assertGolden(t, frame(t, 120, 40, tab(), tab()), 40)
}

func help() tea.KeyPressMsg { return tea.KeyPressMsg{Code: '?', Text: "?"} }

// TestGoldenHelp* pins the full-keymap overlay over the issues section. It is
// the regression guard for the help sheet's framing: the sheet draws its own
// box and helpsheet sizes its columns from that box, so a bare render collapses
// the width and wraps the longer descriptions ("select all/none"). The dialog
// Shell must place the sheet without re-wrapping it.
func TestGoldenHelp80x24(t *testing.T) {
	assertGolden(t, frame(t, 80, 24, help()), 24)
}

func TestGoldenHelp120x40(t *testing.T) {
	assertGolden(t, frame(t, 120, 40, help()), 40)
}

func facetKey() tea.KeyPressMsg      { return tea.KeyPressMsg{Code: 'f', Text: "f"} }
func transitionKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 't', Text: "t"} }

// TestGoldenFacet* pins the facet picker over the issues section: pressing 'f'
// pushes a filterable pick dialog onto the section's dialog stack, and the
// Shell frames it centered over the list. It is the regression guard for the
// section overlays' migration onto the dialog stack.
func TestGoldenFacet80x24(t *testing.T) {
	assertGolden(t, frame(t, 80, 24, facetKey()), 24)
}

func TestGoldenFacet120x40(t *testing.T) {
	assertGolden(t, frame(t, 120, 40, facetKey()), 40)
}

// TestGoldenTransition* pins the transition picker: 't' fetches the selected
// issue's transitions (via the fake service) and opens the same framed pick.
func TestGoldenTransition80x24(t *testing.T) {
	assertGolden(t, frame(t, 80, 24, transitionKey()), 24)
}

func TestGoldenTransition120x40(t *testing.T) {
	assertGolden(t, frame(t, 120, 40, transitionKey()), 40)
}

func createKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'n', Text: "n"} }

// TestGoldenCreate* pins the new-issue overlay: 'n' fetches the project's issue
// types (via the fake project service), then opens the five-field form — the
// type cycle field plus summary, assignee, labels, and description. The 80x24
// frame is the regression guard for the form fitting the height cap without
// clipping its hint row below the fold.
func TestGoldenCreate80x24(t *testing.T) {
	assertGolden(t, frame(t, 80, 24, createKey()), 24)
}

func TestGoldenCreate120x40(t *testing.T) {
	assertGolden(t, frame(t, 120, 40, createKey()), 40)
}

// gatedIssueSvc blocks AddComment on a channel the test controls, so a comment
// submit stays in flight long enough to snapshot the form's submitting frame.
// Closing release lets the write land (or fail, per err); the fixture List and
// Transitions come from the embedded fake so the reconcile after a success has
// rows to render.
type gatedIssueSvc struct {
	fakeIssueSvc
	release <-chan struct{}
	err     error
}

func (g gatedIssueSvc) AddComment(context.Context, string, *jira.CommentAddRequest) (*jira.Comment, *jira.Response, error) {
	<-g.release
	return &jira.Comment{}, nil, g.err
}

func commentKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'c', Text: "c"} }
func ctrlS() tea.KeyPressMsg      { return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl} }

// driveGated drives m to quiescence, but the first time the loop goes quiet with
// a write still blocked (the gated AddComment) it snapshots that mid-flight
// frame, releases the gate, and resumes to the settled frame — the two frames
// the plain drive() harness cannot separate, since it settles to a single
// quiescent frame.
func driveGated(t *testing.T, m tea.Model, cmd tea.Cmd, release chan struct{}) (mid string, settled tea.Model) {
	t.Helper()
	msgs := make(chan tea.Msg, 256)
	var inflight atomic.Int64
	exec := func(c tea.Cmd) {
		if c == nil {
			return
		}
		inflight.Add(1)
		go func() {
			defer inflight.Add(-1)
			if msg := c(); msg != nil {
				select {
				case msgs <- msg:
				default:
				}
			}
		}()
	}
	exec(cmd)
	const quietWindow = 250 * time.Millisecond
	released := false
	quiet := time.NewTimer(quietWindow)
	defer quiet.Stop()
	step := func(msg tea.Msg) {
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				exec(c)
			}
			return
		}
		var c tea.Cmd
		m, c = m.Update(msg)
		exec(c)
	}
	for {
		select {
		case msg := <-msgs:
			step(msg)
			quiet.Reset(quietWindow)
		case <-quiet.C:
			if inflight.Load() == 0 {
				return mid, m
			}
			select {
			case msg := <-msgs:
				step(msg)
				quiet.Reset(quietWindow)
			case <-time.After(quietWindow):
				// Work is still in flight with nothing to deliver: the blocked
				// gated write. Snapshot it, release the gate, and resume; a
				// second arrival here (after release) means only timers remain.
				if released {
					return mid, m
				}
				mid = m.(core.App).View().Content
				close(release)
				released = true
				quiet.Reset(quietWindow)
			}
		}
	}
}

// frameFormLifecycle opens a comment on the first issue, types a draft, and
// submits against a gated service whose AddComment returns writeErr once
// released. It returns the mid-flight (submitting) frame and the settled frame
// — a closed overlay on success, or the reopened form with an inline error.
func frameFormLifecycle(t *testing.T, w, h int, writeErr error) (mid, settled string) {
	t.Helper()
	release := make(chan struct{})
	svc := gatedIssueSvc{
		issues:  fixtureIssues(),
		release: release,
		err:     writeErr,
	}
	var m tea.Model = buildAppWith(svc)
	m = drive(t, m, m.(core.App).Init())
	m = drive(t, m, func() tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} })
	m = drive(t, m, func() tea.Msg { return commentKey() })
	m = drive(t, m, func() tea.Msg { return tea.KeyPressMsg{Text: "ship it"} })
	var settledModel tea.Model
	mid, settledModel = driveGated(t, m, func() tea.Msg { return ctrlS() }, release)
	return mid, settledModel.(core.App).View().Content
}

func opLogKey() tea.KeyPressMsg { return tea.KeyPressMsg{Text: "L", Code: 'L'} }

// frameActivityLog drives a completed comment write so the activity registry
// holds a resolved entry, then opens the operation-log overlay with L. It pins
// both the footer status slot (the resolved write) and the log dialog listing.
func frameActivityLog(t *testing.T, w, h int) string {
	t.Helper()
	release := make(chan struct{})
	svc := gatedIssueSvc{issues: fixtureIssues(), release: release}
	var m tea.Model = buildAppWith(svc)
	m = drive(t, m, m.(core.App).Init())
	m = drive(t, m, func() tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} })
	m = drive(t, m, func() tea.Msg { return commentKey() })
	m = drive(t, m, func() tea.Msg { return tea.KeyPressMsg{Text: "ship it"} })
	_, settled := driveGated(t, m, func() tea.Msg { return ctrlS() }, release)
	settled = drive(t, settled, func() tea.Msg { return opLogKey() })
	return settled.(core.App).View().Content
}

// TestGoldenActivityLog* pins the operation-log overlay over the issues list,
// after one comment write has resolved — the log line and the footer's resolved
// status slot both render.
func TestGoldenActivityLog80x24(t *testing.T) {
	assertGolden(t, frameActivityLog(t, 80, 24), 24)
}

func TestGoldenActivityLog120x40(t *testing.T) {
	assertGolden(t, frameActivityLog(t, 120, 40), 40)
}

// TestGoldenFormSubmitting* pins the comment form's in-flight frame: the hint
// row is replaced by the spinner and "submitting…" while the write is gated.
func TestGoldenFormSubmitting80x24(t *testing.T) {
	mid, _ := frameFormLifecycle(t, 80, 24, nil)
	assertGolden(t, mid, 24)
}

func TestGoldenFormSubmitting120x40(t *testing.T) {
	mid, _ := frameFormLifecycle(t, 120, 40, nil)
	assertGolden(t, mid, 40)
}

// TestGoldenFormSubmitted* pins the settled frame after the gated write lands:
// the overlay is gone and the list is back.
func TestGoldenFormSubmitted80x24(t *testing.T) {
	_, settled := frameFormLifecycle(t, 80, 24, nil)
	assertGolden(t, settled, 24)
}

func TestGoldenFormSubmitted120x40(t *testing.T) {
	_, settled := frameFormLifecycle(t, 120, 40, nil)
	assertGolden(t, settled, 40)
}

// TestGoldenFormError* pins the failure path: the write returns an error, so the
// form stays open with the reason inline and the draft intact.
func TestGoldenFormError80x24(t *testing.T) {
	_, settled := frameFormLifecycle(t, 80, 24, errors.New("service unavailable"))
	assertGolden(t, settled, 24)
}

func TestGoldenFormError120x40(t *testing.T) {
	_, settled := frameFormLifecycle(t, 120, 40, errors.New("service unavailable"))
	assertGolden(t, settled, 40)
}
