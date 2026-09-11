package core

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/tui/components/dialog"
	"github.com/matcra587/jira-cli/internal/tui/icons"
	"github.com/matcra587/jira-cli/internal/tui/theme"
)

var _ tea.Model = App{}

// App is the root Bubble Tea model. It is data-agnostic: it owns the shared
// context, the section registry, the task manager and the live section
// instances, and routes messages without knowing what any section displays.
// Value semantics match the rest of the TUI; the pointer/map fields are shared
// references so mutations persist across the value copies Bubble Tea makes.
type App struct {
	ctx      *ProgramContext
	registry *Registry
	tasks    *TaskManager
	order    []SectionID
	cancel   context.CancelFunc // cancels ctx.Base on shutdown

	// Reconfigure is the wiring layer's hook for a config hot-reload: given the
	// fresh config it re-registers config-derived sections and returns the new
	// tab order plus the section IDs whose cached instances must be rebuilt
	// (their config may have changed). Nil disables structural reloads — the
	// App still swaps ctx.Config so value-only settings (refresh interval)
	// apply.
	Reconfigure func(ctx *ProgramContext, registry *Registry, cfg *config.Config) (order, invalidate []SectionID)

	current int
	// dialogs is the modal overlay stack drawn over the body. While any dialog
	// is open the App routes keys and clicks to it before capture routing or
	// global shortcuts, so the overlay is fully modal.
	dialogs *dialog.Stack
	// blurred tracks terminal focus (ReportFocus): refresh ticks are skipped
	// while true and a refresh fires immediately on regaining focus.
	blurred bool
	// paused stops the auto-refresh heartbeat from refetching while the user
	// inspects volatile state; unlike blurred it only clears on the R key.
	paused bool
	// appliedTheme is the theme name the styles were last derived from. The
	// reload comparison must use this, never a config-vs-config diff: a
	// ThemePreviewMsg re-derives styles with no config reload at all, so
	// only this field knows what is actually on screen — and it is what
	// makes commit-after-preview a flicker-free no-op.
	appliedTheme string
	// sections caches built instances so a section is constructed once and
	// keeps its state across tab switches; started records which have run
	// Init so the first-activation fetch fires exactly once per section.
	sections map[SectionID]Section
	started  map[SectionID]bool
}

// NewApp builds the root model. The first ID in order is the initially active
// section. The context's StartTask is wired to the task manager so any section
// can launch generation-tracked async work.
func NewApp(ctx *ProgramContext, registry *Registry, order []SectionID) App {
	tasks := NewTaskManager()
	ctx.StartTask = tasks.Start

	// Make Base cancellable so in-flight Jira fetches stop when the app exits.
	base := ctx.Base
	if base == nil {
		base = context.Background()
	}
	cancelCtx, cancel := context.WithCancel(base)
	ctx.Base = cancelCtx

	appliedTheme := ""
	if ctx.Config != nil {
		appliedTheme = ctx.Config.Theme.Name
	}
	a := App{
		ctx:          ctx,
		registry:     registry,
		tasks:        tasks,
		dialogs:      dialog.New(newDialogShell(ctx.Styles)),
		order:        order,
		cancel:       cancel,
		sections:     make(map[SectionID]Section),
		started:      make(map[SectionID]bool),
		appliedTheme: appliedTheme,
	}
	if len(order) > 0 {
		ctx.View = order[0]
	}
	return a
}

// build returns the cached section for an ID, constructing and caching it on
// first use. Construction is cheap and side-effect free; the fetch happens in
// Init, which build deliberately does not call (so reading a title for the tab
// bar never triggers a section's data load).
func (a App) build(id SectionID) Section {
	if s, ok := a.sections[id]; ok {
		return s
	}
	s, ok := a.registry.Build(id, a.ctx)
	if !ok {
		return nil
	}
	a.sections[id] = s
	return s
}

// activeID returns the currently selected section's ID, or "" if none.
func (a App) activeID() SectionID {
	if len(a.order) == 0 {
		return ""
	}
	return a.order[a.current]
}

// Init starts the active section, then eagerly starts every other registered
// section so background tabs fetch immediately and the tab bar shows real
// counts without a visit. It also arms the auto-refresh
// timer. The returned App is discarded by the caller (Bubble Tea keeps the
// model it already holds), but the started-map mutations persist because the
// map is shared.
func (a App) Init() tea.Cmd {
	_, cmd := a.activate(a.current)
	cmds := []tea.Cmd{cmd}
	for _, id := range a.order {
		if a.started[id] {
			continue
		}
		if s := a.build(id); s != nil {
			a.started[id] = true
			cmds = append(cmds, s.Init(a.ctx))
		}
	}
	cmds = append(cmds, a.refreshTick())
	return tea.Batch(cmds...)
}

// refreshEnabled reports whether auto-refresh is configured on (a missing
// config or non-positive tui.refresh_interval disables it). refreshTick and
// the focus-regain refresh share this one guard.
func (a App) refreshEnabled() bool {
	return a.ctx.Config != nil && a.ctx.Config.TUI.RefreshInterval > 0
}

// refreshTick arms the auto-refresh timer from config (tui.refresh_interval
// seconds). A missing config or a non-positive interval disables it.
func (a App) refreshTick() tea.Cmd {
	if !a.refreshEnabled() {
		return nil
	}
	d := time.Duration(a.ctx.Config.TUI.RefreshInterval) * time.Second
	return tea.Tick(d, func(time.Time) tea.Msg { return RefreshTickMsg{} })
}

// activate selects the section at idx (wrapping), building it if needed and
// running Init only the first time it becomes active. It returns the updated
// App (current is a value field, so it must be threaded back) and the
// section's startup command, if any.
func (a App) activate(idx int) (App, tea.Cmd) {
	n := len(a.order)
	if n == 0 {
		return a, nil
	}
	a.current = ((idx % n) + n) % n
	id := a.order[a.current]
	a.ctx.View = id

	s := a.build(id)
	if s == nil {
		return a, nil
	}
	if !a.started[id] {
		a.started[id] = true
		return a, s.Init(a.ctx)
	}
	return a, nil
}

// Update routes messages by kind:
//   - WindowSizeMsg recomputes the shared layout and is broadcast to every
//     built section so backgrounded views reflow before they are shown.
//   - Global keys (quit, section switch) are handled here.
//   - An accepted TaskFinishedMsg is broadcast to every section so the one that
//     owns the scope applies it even if the user has since switched views; a
//     superseded result is dropped.
//   - Everything else (input) goes to the active section only.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.ctx.SetSize(msg.Width, msg.Height)
		return a.broadcast(msg)

	case tea.KeyPressMsg:
		// Dialogs are modal: while any is open the App routes keys to the stack
		// before capture routing — a section can start capturing underneath an
		// open dialog (an async task opening its controller), and keys must
		// drive the visible overlay, not mutate hidden UI.
		if a.dialogs.Active() {
			cmd, popped, res := a.dialogs.Update(msg)
			// A palette accept replays the chosen command's key. The dialog is
			// already popped, so the replay drives the underlying view — not the
			// palette — and cannot be re-intercepted by the just-closed overlay
			// (pop-then-replay, strictly ordered within this Update).
			if res == dialog.ResultSubmit {
				if p, ok := popped.(*paletteDialog); ok {
					if entry, ok := p.Selected(); ok && entry.Key != "" {
						return a.Update(replayKey(entry.Key))
					}
				}
			}
			return a, cmd
		}
		// A section capturing text input gets keys before global shortcuts, so
		// a filter/editor can contain "q" or "tab" without quitting/switching
		// (and "?" types rather than opening help).
		if id := a.activeID(); id != "" {
			if cur := a.build(id); cur != nil && cur.CapturesInput() {
				next, cmd := cur.Update(msg)
				a.sections[id] = next
				return a, cmd
			}
		}
		switch {
		case key.Matches(msg, a.ctx.Keys.CommandPalette):
			a.dialogs.Push(a.newPaletteDialog())
			return a, nil
		case key.Matches(msg, a.ctx.Keys.Help):
			a.dialogs.Push(a.newHelpDialog())
			return a, nil
		case key.Matches(msg, a.ctx.Keys.OpLog):
			a.dialogs.Push(a.newLogDialog())
			return a, nil
		case key.Matches(msg, a.ctx.Keys.Quit):
			if a.cancel != nil {
				a.cancel()
			}
			return a, tea.Quit
		case key.Matches(msg, a.ctx.Keys.NextSection):
			return a.activate(a.current + 1)
		case key.Matches(msg, a.ctx.Keys.PrevSection):
			return a.activate(a.current - 1)
		case key.Matches(msg, a.ctx.Keys.TogglePreview):
			a.cyclePreview()
			// Re-broadcast the size so every built section re-lays-out for the new
			// split (or the sidebar being hidden).
			return a.broadcast(tea.WindowSizeMsg{Width: a.ctx.ScreenWidth, Height: a.ctx.ScreenHeight})
		case key.Matches(msg, a.ctx.Keys.GrowPreview):
			a.ctx.AdjustPreviewRatio(0.05)
			return a.broadcast(tea.WindowSizeMsg{Width: a.ctx.ScreenWidth, Height: a.ctx.ScreenHeight})
		case key.Matches(msg, a.ctx.Keys.ShrinkPreview):
			a.ctx.AdjustPreviewRatio(-0.05)
			return a.broadcast(tea.WindowSizeMsg{Width: a.ctx.ScreenWidth, Height: a.ctx.ScreenHeight})
		case key.Matches(msg, a.ctx.Keys.TogglePause):
			a.paused = !a.paused
			if a.paused || !a.refreshEnabled() {
				return a, nil
			}
			// Resuming refetches immediately rather than waiting out the
			// remainder of the interval, mirroring the focus-return path.
			model, cmd := a.broadcast(RefreshTickMsg{})
			return model, tea.Batch(cmd, a.refreshTick())
		case key.Matches(msg, a.ctx.Keys.Zoom):
			a.ctx.ToggleZoom()
			return a.broadcast(tea.WindowSizeMsg{Width: a.ctx.ScreenWidth, Height: a.ctx.ScreenHeight})
		}

	case tea.MouseClickMsg:
		// A click drives an open dialog (the help sheet dismisses on any click),
		// mirroring the key path above.
		if a.dialogs.Active() {
			cmd, _, _ := a.dialogs.Update(msg)
			return a, cmd
		}
		// The tab row is App chrome: a click on a tab activates it — except
		// while the active section captures input (a modal/filter/detail must
		// not lose its section underneath it), matching the keyboard guard.
		// Anything below falls through to the section's own hit-testing.
		if msg.Button == tea.MouseLeft && msg.Y == 0 {
			if id := a.activeID(); id != "" {
				if cur := a.build(id); cur != nil && cur.CapturesInput() {
					return a, nil
				}
			}
			if i, ok := a.tabAt(msg.X); ok {
				return a.activate(i)
			}
			return a, nil
		}

	case ErrorMsg:
		a.ctx.Err = msg.Err
		return a, nil

	case RefreshTickMsg:
		// While the terminal is unfocused only the timer is kept alive — no
		// refetching for a dashboard nobody is looking at. Focus triggers an
		// immediate round instead.
		if a.blurred || a.paused {
			// No re-arm: the resume paths (FocusMsg, the R toggle) start a
			// fresh timer, and re-arming here would stack one extra
			// self-sustaining heartbeat per pause/blur cycle.
			return a, nil
		}
		// Every idle section refetches; the timer is re-armed for the next round.
		model, cmd := a.broadcast(msg)
		return model, tea.Batch(cmd, a.refreshTick())

	case tea.FocusMsg:
		// Coming back to the terminal: refresh everything now rather than
		// waiting out the remainder of the interval — but only when refresh is
		// enabled at all (focus must not bypass a disabled interval), and
		// re-arm the timer so the cycle survives even if no tick was in flight.
		wasBlurred := a.blurred
		a.blurred = false
		if wasBlurred && a.refreshEnabled() && !a.paused {
			model, cmd := a.broadcast(RefreshTickMsg{})
			return model, tea.Batch(cmd, a.refreshTick())
		}
		return a, nil

	case tea.BlurMsg:
		a.blurred = true
		return a, nil

	case RestyleMsg:
		// A first-class broadcast: any producer (the icons preview, a future
		// density toggle) reaches every section, matching the message's
		// contract — routing it like an ordinary message would deliver it to
		// the active section only.
		return a.broadcast(msg)

	case ThemePreviewMsg:
		// Live preview: re-derive the shared styles and let every section
		// restyle what it already renders. No section instance is dropped —
		// the settings picker driving the preview must survive — and nothing
		// refetches; cached content re-renders through RestyleMsg.
		theme.Reload(theme.Resolve(msg.Name))
		a.ctx.Styles = DefaultStyles()
		a.dialogs.SetShell(newDialogShell(a.ctx.Styles))
		a.appliedTheme = msg.Name
		return a.broadcast(RestyleMsg{})

	case ConfigReloadedMsg:
		return a.applyConfig(msg)

	case spinner.TickMsg:
		// Spinner ticks go to every section: each spinner ignores ticks that
		// aren't its own, so the section that started the stream advances even
		// while backgrounded (its fetch keeps running either way).
		return a.broadcast(msg)

	case TaskFinishedMsg:
		if !a.tasks.Accept(msg.Scope, msg.Gen) {
			return a, nil // superseded by a newer task in this scope
		}
		// Task errors belong to the owning section (sticky fetch errors,
		// transient write toasts) — the app banner is reserved for app-level
		// failures delivered via ErrorMsg.
		return a.broadcast(msg)
	}

	// A section-addressed message reaches every section, not just the active
	// one — the addressee checks the address, so e.g. a flash clear can land
	// on a section the user has since tabbed away from.
	if _, ok := msg.(SectionMsg); ok {
		return a.broadcast(msg)
	}

	id := a.activeID()
	cur := a.build(id)
	if cur == nil {
		return a, nil
	}
	var cmd tea.Cmd
	cur, cmd = cur.Update(msg)
	a.sections[id] = cur
	return a, cmd
}

// broadcast forwards a message to every built section, writing each returned
// value back to the cache so state persists. Used for messages that are not
// addressed to the focused view: layout changes and async results that a
// background section may own. Sections ignore scopes that are not theirs.
func (a App) broadcast(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for id, s := range a.sections {
		next, cmd := s.Update(msg)
		a.sections[id] = next
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return a, tea.Batch(cmds...)
}

// View composes the tab bar, the section body and the footer. The sidebar split
// is part of the section body via ProgramContext; the App only owns chrome.
func (a App) View() tea.View {
	if a.ctx.ScreenWidth == 0 {
		return tea.NewView("")
	}
	body := ""
	if s := a.build(a.activeID()); s != nil {
		body = s.View()
	}
	// Pin the body to the exact region between chrome so the footer stays anchored
	// to the bottom and a short section can't let it float up (or a tall one push
	// it off-screen).
	bodyH := max(a.ctx.ScreenHeight-chromeRows, 0)
	body = lipgloss.NewStyle().
		Width(a.ctx.ScreenWidth).
		Height(bodyH).
		MaxHeight(bodyH).
		Render(body)
	out := lipgloss.JoinVertical(
		lipgloss.Left,
		a.ctx.Styles.HeaderRule.Width(a.ctx.ScreenWidth).Render(a.tabBar()),
		body,
		a.footer(),
	)
	out = a.dialogs.View(out, a.ctx.ScreenWidth, a.ctx.ScreenHeight)
	v := tea.NewView(out)
	v.AltScreen = true
	// Enable mouse cell-motion reporting so wheel events reach the focused
	// viewport (detail/preview scroll). v2 reports the mouse only while a view
	// requests it; without this the terminal sends no MouseWheelMsg at all.
	v.MouseMode = tea.MouseModeCellMotion
	// Focus reporting drives the focus-aware refresh (pause while the terminal
	// is unfocused). Basic key disambiguation is always requested by bubbletea
	// v2 (keyboardEnhancementsFlags starts at 1); the KeyboardEnhancements
	// struct only adds extras we don't need yet, so it stays unset.
	v.ReportFocus = true
	return v
}

// CurrentSection exposes the active section for tests and wiring.
func (a App) CurrentSection() Section { return a.build(a.activeID()) }

// applyConfig hot-applies a reloaded config: the shared pointer is swapped (so
// value settings like the refresh interval take effect on the next tick), the
// Reconfigure hook rebuilds the tab order and names the section instances to
// drop, new sections are started, the active tab is kept by ID where possible,
// and everything is re-laid-out. The message is re-broadcast last so sections
// can refresh what they display.
func (a App) applyConfig(msg ConfigReloadedMsg) (tea.Model, tea.Cmd) {
	if msg.Config == nil {
		return a, nil
	}
	// If the old config had refresh disabled there is no tick loop running to
	// pick up a newly enabled interval, so arm one here. When a loop is already
	// running it re-arms itself with the new interval (and dies naturally if
	// the reload disabled it) — arming again would double the heartbeat.
	wasTicking := a.ctx.Config != nil && a.ctx.Config.TUI.RefreshInterval > 0
	a.ctx.Config = msg.Config
	// Re-dock the sidebar per the new config (no-op when the key is absent, so
	// a reload doesn't fight the p-key cycle).
	a.ctx.SetPreviewFromConfig(msg.Config.TUI.Preview)
	a.ctx.SetPreviewRatioPercent(msg.Config.TUI.PreviewSize)
	// Lenses re-read from the new config; the issues section reads the set
	// per call, so changed chips and JQL take effect on the next render.
	a.ctx.SetLenses(msg.Config)
	// The glyph set re-resolves every reload: render helpers read it per
	// call, so a changed tui.icons shows on the next frame with no rebuild.
	icons.Use(icons.For(icons.Resolve(msg.Config.TUI.Icons)))
	// A changed theme.name re-derives everything: the shared theme styles,
	// the chrome bundle, and — because sections cache derived renderers
	// (markdown, list styles) at construction — every section instance,
	// which the start loop below rebuilds. An unchanged name is a no-op so
	// an ordinary config edit never flickers the styles. The comparison is
	// against the name the styles were actually derived from (appliedTheme),
	// never the previous config: a ThemePreviewMsg re-derives styles without
	// any config reload, so the config's old name says nothing about what is
	// on screen — and a commit-after-preview must be the no-op branch.
	if msg.Config.Theme.Name != a.appliedTheme {
		a.appliedTheme = msg.Config.Theme.Name
		theme.Reload(theme.Resolve(msg.Config.Theme.Name))
		a.ctx.Styles = DefaultStyles()
		a.dialogs.SetShell(newDialogShell(a.ctx.Styles))
		clear(a.sections)
		clear(a.started)
	}
	var cmds []tea.Cmd
	// A reload may have rewritten the active lens's JQL out from under the
	// cached rows; a refresh tick re-runs every section's current query so
	// the list matches the header instead of waiting for the next interval.
	cmds = append(cmds, func() tea.Msg { return RefreshTickMsg{} })
	// Key overrides re-apply from defaults each reload (removals restore the
	// default binding); a bad override keeps the current map and shows why.
	// A clean reload supersedes any earlier failure still in the footer.
	if err := a.ctx.RebindKeys(msg.Config); err != nil {
		a.ctx.Err = err
	} else {
		a.ctx.Err = nil
	}
	if !wasTicking {
		cmds = append(cmds, a.refreshTick())
	}
	if a.Reconfigure != nil {
		keep := a.activeID()
		order, invalidate := a.Reconfigure(a.ctx, a.registry, msg.Config)
		if len(order) > 0 {
			a.order = order
		}
		for _, id := range invalidate {
			delete(a.sections, id)
			delete(a.started, id)
		}
		// Drop instances that no longer appear in the order; their state would
		// otherwise linger invisibly and resurrect stale on a later re-add.
		inOrder := make(map[SectionID]bool, len(a.order))
		for _, id := range a.order {
			inOrder[id] = true
		}
		for id := range a.sections {
			if !inOrder[id] {
				delete(a.sections, id)
				delete(a.started, id)
			}
		}
		a.current = 0
		for i, id := range a.order {
			if id == keep {
				a.current = i
				break
			}
		}
		if len(a.order) > 0 {
			a.ctx.View = a.order[a.current]
		}
	}
	// Start anything new (a freshly added query tab fetches immediately).
	for _, id := range a.order {
		if a.started[id] {
			continue
		}
		if s := a.build(id); s != nil {
			a.started[id] = true
			cmds = append(cmds, s.Init(a.ctx))
		}
	}
	// Relayout for safety, then let sections see the reload itself.
	model, cmd := a.broadcast(tea.WindowSizeMsg{Width: a.ctx.ScreenWidth, Height: a.ctx.ScreenHeight})
	model2, cmd2 := model.(App).broadcast(msg)
	return model2, tea.Batch(append(cmds, cmd, cmd2)...)
}

// cyclePreview rotates the issue preview through right → bottom → left →
// hidden, so the user can pick the split that suits their terminal. The
// context is a pointer, so the mutation persists; the caller re-broadcasts the
// size to relayout.
func (a App) cyclePreview() {
	switch {
	case !a.ctx.SidebarOpen:
		a.ctx.SidebarOpen = true
		// Reopen where the user configured the sidebar, falling back to right.
		pos := PreviewRight
		if a.ctx.Config != nil {
			switch p := PreviewPosition(strings.ToLower(strings.TrimSpace(a.ctx.Config.TUI.Preview))); p {
			case PreviewRight, PreviewLeft, PreviewBottom, PreviewAuto:
				pos = p
			}
		}
		a.ctx.SetPreviewPosition(pos)
	case a.ctx.PreviewPosition() == PreviewRight:
		a.ctx.SetPreviewPosition(PreviewBottom)
	case a.ctx.PreviewPosition() == PreviewBottom:
		a.ctx.SetPreviewPosition(PreviewLeft)
	default: // left → hidden
		a.ctx.SidebarOpen = false
		a.ctx.SetSize(a.ctx.ScreenWidth, a.ctx.ScreenHeight)
	}
}
