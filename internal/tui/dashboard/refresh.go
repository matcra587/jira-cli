package dashboard

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type (
	refreshTick  struct{ generation uint64 }
	refreshCause uint8
)

const (
	refreshNone refreshCause = iota
	refreshAutomatic
	refreshManual
)

type rowChange uint8

const (
	rowNew rowChange = iota + 1
	rowUpdated
)

type refreshState struct {
	running, paused, blurred bool
	generation               uint64
	cancel                   context.CancelFunc
	interval                 time.Duration
	pending                  refreshCause
	inflight, background     bool
	held                     *task.FinishedMsg
	observed                 bool
	markers                  map[string]rowChange
}

type refreshClock func(time.Duration, uint64) (tea.Cmd, context.CancelFunc)

// The selected Primer has generation admission but no cancellable timer. Keep
// this wait within Tea's command lifecycle and the screen's read context.
func newRefreshClock(parent context.Context) refreshClock {
	return func(delay time.Duration, generation uint64) (tea.Cmd, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		return func() tea.Msg {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil
			case <-timer.C:
				return refreshTick{generation: generation}
			}
		}, cancel
	}
}

func (m *Model) refreshInterval() time.Duration {
	value, ok := m.options.SettingValues["tui_refresh_interval"]
	if !ok {
		return config.DefaultTUIRefreshInterval * time.Second
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 0 || seconds > config.MaxTUIRefreshInterval {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func (m *Model) refreshBusy() bool {
	return m.editing || m.filtering || m.settings != nil || m.dialogs.Active() ||
		m.creationEditor != nil || m.actionBusy || m.localSaveBusy
}

func (m *Model) automaticRefreshAllowed() bool {
	return m.refresh.running && !m.refresh.paused && !m.refresh.blurred &&
		m.query != "" && m.width >= 40 && m.height >= 12
}

func (m *Model) stopRefreshClock() {
	if m.refresh.cancel != nil {
		m.refresh.cancel()
		m.refresh.cancel = nil
	}
	m.refresh.generation++
	m.refresh.interval = 0
}

func (m *Model) armRefreshClock() tea.Cmd {
	interval := m.refreshInterval()
	if !m.automaticRefreshAllowed() || interval == 0 {
		m.stopRefreshClock()
		return nil
	}
	if m.refresh.cancel != nil && interval == m.refresh.interval {
		return nil
	}
	m.stopRefreshClock()
	m.refresh.interval = interval
	command, cancel := m.clock(interval, m.refresh.generation)
	m.refresh.cancel = cancel
	return command
}

func (m *Model) requestRefresh(cause refreshCause) {
	if m.query == "" {
		return
	}
	if cause == refreshAutomatic && !m.automaticRefreshAllowed() {
		return
	}
	if cause == refreshManual && m.refresh.inflight && m.refresh.background {
		if m.refresh.held != nil {
			// A paused response may be old by the time the user explicitly asks
			// for current data. Supersede it and admit one fresh manual read.
			m.tasks.Start(task.Spec{Scope: listScope})
			m.refresh.held, m.refresh.inflight = nil, false
			m.loading, m.loadingMore = false, false
		} else {
			// The read already in progress can satisfy this explicit request.
			m.refresh.background = false
		}
	}
	// An admitted full read already covers this request. A next-page read does
	// not replace the observed window, so defer one full read until it finishes.
	if m.loading && !m.loadingMore {
		return
	}
	if cause > m.refresh.pending {
		m.refresh.pending = cause
	}
}

func (m *Model) consumeRefreshTick(msg refreshTick) {
	if msg.generation != m.refresh.generation {
		return
	}
	m.stopRefreshClock()
	if m.refreshInterval() > 0 {
		m.requestRefresh(refreshAutomatic)
	}
}

func (m *Model) toggleRefreshPause() {
	m.refresh.paused = !m.refresh.paused
	m.stopRefreshClock()
	if m.refresh.paused {
		if m.refresh.pending == refreshAutomatic {
			m.refresh.pending = refreshNone
		}
		m.notice = fmt.Sprintf("Automatic refresh paused · %s resumes; %s refreshes now.", m.effectiveKey("R"), m.effectiveKey("r"))
		return
	}
	m.notice = "Automatic refresh resumed."
	m.requestRefresh(refreshAutomatic)
}

func (m *Model) beginRefresh(cause refreshCause) tea.Cmd {
	m.refresh.pending = refreshNone
	m.refresh.inflight, m.refresh.background = true, cause == refreshAutomatic
	// Refresh the already loaded window through the ordinary bounded walker.
	// It retains its duplicate/cursor/empty-page bounds rather than draining JQL.
	cmd := m.readList(false, max(pageSize, len(m.issues)))
	if cmd == nil {
		m.refresh.inflight = false
	}
	return cmd
}

func (m *Model) flushRefresh() tea.Cmd {
	if m.refreshBusy() || m.refresh.held != nil && m.refresh.background && !m.automaticRefreshAllowed() {
		return m.armRefreshClock()
	}
	var read, detail tea.Cmd
	if held := m.refresh.held; held != nil {
		m.refresh.held = nil
		if m.tasks.Accept(held.Scope, held.Gen) {
			detail = m.acceptList(*held)
		}
	}
	pending := m.refresh.pending
	allowed := pending == refreshManual || m.automaticRefreshAllowed()
	if pending != refreshNone && allowed && !m.loading {
		read = m.beginRefresh(pending)
	}
	return tea.Batch(detail, read, m.armRefreshClock())
}

func (m *Model) resetRefreshObservation() {
	m.stopRefreshClock()
	m.refresh.pending = refreshNone
	m.refresh.inflight = false
	m.refresh.held = nil
	m.refresh.observed = false
	m.refresh.markers = make(map[string]rowChange)
}

func observedIssueIdentity(issue envelope.IssueView) string {
	if issue.ID != "" {
		return "id:" + issue.ID
	}
	return "key:" + issue.Key
}

func (m *Model) observeChangedRows(issues []envelope.IssueView) {
	if !m.enabled("tui_change_markers") || !m.refresh.observed {
		m.refresh.markers = make(map[string]rowChange)
		m.refresh.observed = true
		return
	}
	previous := make(map[string]envelope.IssueView, len(m.issues))
	for _, issue := range m.issues {
		previous[observedIssueIdentity(issue)] = issue
	}
	markers := make(map[string]rowChange)
	for _, issue := range issues {
		old, found := previous[observedIssueIdentity(issue)]
		switch {
		case !found:
			markers[issue.Key] = rowNew
		// Native fields include non-comparable maps and raw unknown fields. Compare
		// the complete observed value, retaining omitted/null/zero distinctions.
		case old.Key != issue.Key || !reflect.DeepEqual(old.Fields, issue.Fields):
			markers[issue.Key] = rowUpdated
		default:
			if mark, ok := m.refresh.markers[issue.Key]; ok {
				markers[issue.Key] = mark
			}
		}
	}
	m.refresh.markers = markers
}

func (m *Model) acknowledgeRow() { delete(m.refresh.markers, m.selectedKey()) }

func (m *Model) copyChangeMarkers() map[string]rowChange { return maps.Clone(m.refresh.markers) }

// A write must not later be followed by a deferred pre-write observation.
// Supersede that read logically; its invocation context still owns cancellation.
func (m *Model) fenceRefreshForWrite() {
	if !m.refresh.inflight {
		return
	}
	m.tasks.Start(task.Spec{Scope: listScope})
	m.loading, m.loadingMore = false, false
	m.refresh.inflight = false
	m.refresh.held = nil
	m.refresh.pending = refreshAutomatic
}
