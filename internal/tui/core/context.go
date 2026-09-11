package core

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/tui/components/activity"
	"github.com/matcra587/jira-cli/internal/tui/keys"
	"github.com/matcra587/jira-cli/internal/tui/theme"
)

// PreviewPosition controls where the issue sidebar sits relative to the list.
type PreviewPosition string

const (
	// PreviewRight docks the sidebar to the right of the list (wide terminals).
	PreviewRight PreviewPosition = "right"
	// PreviewLeft docks the sidebar to the left of the list.
	PreviewLeft PreviewPosition = "left"
	// PreviewBottom docks the sidebar below the list (narrow terminals).
	PreviewBottom PreviewPosition = "bottom"
	// PreviewHidden closes the sidebar (config value only; SidebarOpen is the
	// runtime flag).
	PreviewHidden PreviewPosition = "hidden"
	// PreviewAuto picks right or bottom based on terminal width.
	PreviewAuto PreviewPosition = "auto"
)

// minWidthForRightPreview is the column threshold at or above which an "auto"
// preview docks to the right; below it the sidebar moves to the bottom so the
// list keeps a usable width.
const minWidthForRightPreview = 120

// TopChromeRows is the rows above the body: the tab row and its bottom rule.
// Exported because a section hit-testing absolute mouse coordinates (wheel
// routing) needs the body's screen offset.
const TopChromeRows = 2

// chromeRows is the number of rows the App chrome occupies: the top chrome
// plus the two-line footer (a labeled border and the hint line). The body
// region is sized to whatever remains.
const chromeRows = TopChromeRows + 2

// ProgramContext is the single shared state broadcast to every Section. The App
// owns one *ProgramContext and mutates it (notably on resize); Sections hold the
// pointer and read it during View, so layout, theme and config changes need no
// prop-drilling.
type ProgramContext struct {
	// Terminal geometry.
	ScreenWidth  int
	ScreenHeight int

	// Computed body regions. MainWidth/Height is the list area; Preview* is the
	// sidebar. Preview dimensions are zero when the sidebar is closed.
	// BodyHeight is the full region between the chrome (the whole list+sidebar
	// area), used by full-body views like the issue detail.
	MainWidth     int
	MainHeight    int
	PreviewWidth  int
	PreviewHeight int
	BodyHeight    int

	// SidebarOpen toggles the issue preview pane.
	SidebarOpen bool

	// previewPos is the configured preference; resolved via PreviewPosition.
	previewPos PreviewPosition
	// previewRatio is the preview pane's share of the split (width for side
	// docks, height for the bottom dock). Zero means the 50% default.
	previewRatio float64
	// zoomed gives the main pane the full body, preserving the split state
	// for the next toggle (tmux zoom).
	zoomed bool

	Styles Styles
	Keys   keys.Map
	// Lenses are the config-supplied quick-filters for the issues section
	// ([[tui.lenses]]); empty means the section's built-ins apply.
	// DefaultLens names the landing lens by title (case-insensitive).
	Lenses      []Lens
	DefaultLens string
	// Recent is the app-wide recently-viewed issue jumplist (ctrl+o).
	Recent *RecentList
	// Activity is the app-wide record of user-facing mutations, surfaced in the
	// footer status slot and the operation-log overlay. Sections write to it
	// (Start/Finish/Fail); only the footer and log read it back.
	Activity *activity.Registry
	Config   *config.Config
	View     SectionID
	Err      error
	Services Services

	// Profile context for the footer chrome: which profile/project/board the
	// dashboard is pointed at. All optional; empty fields are omitted.
	ProfileName string
	Project     string
	Board       string
	// Version is the build version shown right of the tab bar.
	// Empty hides the brand label.
	Version string
	// ConfigPath is the loaded config file's location, used by the settings
	// section to display it and to watch it for hot-reload. Empty disables
	// reloading.
	ConfigPath string
	// BaseURL is the Jira site root (e.g. https://acme.atlassian.net), used to
	// build issue links for "open in browser" and "copy url".
	BaseURL string
	// DefaultIssueType is the profile's issue type for creates ("" means the
	// caller picks a fallback).
	DefaultIssueType string
	// WorkdaySeconds is the active profile's working-day length, used to parse
	// relative worklog durations like "1d". Zero falls back to 8 hours.
	WorkdaySeconds int

	// Base is the program's root context, used to cancel in-flight Jira calls
	// launched from sections when the app shuts down.
	Base context.Context //nolint:containedctx // sections launch async Jira calls from Update, which has no context parameter; the program's root context is shared state by design

	// StartTask is wired by the App so any Section can launch async work that
	// flows back as a TaskFinishedMsg with generation tracking.
	StartTask func(TaskSpec) tea.Cmd
}

// NewProgramContext builds a context with default styles, key map and the given
// services and config. Either may be nil for tests or for a chrome-only run.
func NewProgramContext(svc Services, cfg *config.Config) *ProgramContext {
	return &ProgramContext{
		Styles:      DefaultStyles(),
		Keys:        keys.Default(),
		Recent:      NewRecentList(),
		Activity:    activity.New(),
		Config:      cfg,
		Services:    svc,
		Base:        context.Background(),
		SidebarOpen: true,
		previewPos:  PreviewAuto,
		// Safe no-op until NewApp wires the real task manager, so a section
		// constructed by a test or alternative renderer can call StartTask
		// without a nil panic.
		StartTask: func(TaskSpec) tea.Cmd { return nil },
	}
}

// SetPreviewPosition sets the configured sidebar preference and recomputes the
// layout for the current terminal size.
func (c *ProgramContext) SetPreviewPosition(p PreviewPosition) {
	c.zoomed = false // showing or moving the preview implies wanting it visible
	c.previewPos = p
	c.SetSize(c.ScreenWidth, c.ScreenHeight)
}

// PreviewPosition resolves "auto" to the concrete position for the current
// width. It is exported so a Section can render its sidebar on the correct edge.
func (c *ProgramContext) PreviewPosition() PreviewPosition {
	if c.previewPos == PreviewRight || c.previewPos == PreviewLeft || c.previewPos == PreviewBottom {
		return c.previewPos
	}
	if c.ScreenWidth >= minWidthForRightPreview {
		return PreviewRight
	}
	return PreviewBottom
}

// SetSize records the terminal size and splits it into the list and sidebar
// regions. With the sidebar closed the list takes the full body; with it open
// the split follows the resolved preview position (right ≈ half width, bottom ≈
// half height). All width/height math lives here so layout can never drift
// between components.
func (c *ProgramContext) SetSize(w, h int) {
	c.ScreenWidth = w
	c.ScreenHeight = h

	bodyH := max(h-chromeRows, 0)
	c.BodyHeight = bodyH

	if !c.SidebarOpen {
		c.MainWidth, c.MainHeight = w, bodyH
		c.PreviewWidth, c.PreviewHeight = 0, 0
		return
	}

	if c.zoomed {
		// Zoom: the list gets everything; the split state survives the toggle.
		c.MainWidth, c.MainHeight = w, bodyH
		c.PreviewWidth, c.PreviewHeight = 0, 0
		return
	}

	switch c.PreviewPosition() {
	case PreviewBottom:
		previewH := int(float64(bodyH) * c.ratio())
		c.MainWidth, c.MainHeight = w, bodyH-previewH
		c.PreviewWidth, c.PreviewHeight = w, previewH
	default: // PreviewRight / PreviewLeft — same split, sections render the order
		if w < minSplitWidth {
			// Too narrow for a readable side-by-side split: the list wins and
			// the preview returns as soon as the terminal widens.
			c.MainWidth, c.MainHeight = w, bodyH
			c.PreviewWidth, c.PreviewHeight = 0, 0
			return
		}
		previewW := int(float64(w) * c.ratio())
		c.MainWidth, c.MainHeight = w-previewW, bodyH
		c.PreviewWidth, c.PreviewHeight = previewW, bodyH
	}
}

// Split-ratio bounds: the preview can take 20–80% of the body; below
// minSplitWidth a side dock auto-hides entirely.
const (
	minPreviewRatio = 0.2
	maxPreviewRatio = 0.8
	minSplitWidth   = 60
)

// ratio is the effective preview share (default half).
func (c *ProgramContext) ratio() float64 {
	if c.previewRatio == 0 {
		return 0.5
	}
	return c.previewRatio
}

// AdjustPreviewRatio grows or shrinks the preview's share of the split by
// delta, clamped, and re-runs the layout.
func (c *ProgramContext) AdjustPreviewRatio(delta float64) {
	r := c.ratio() + delta
	if r < minPreviewRatio {
		r = minPreviewRatio
	}
	if r > maxPreviewRatio {
		r = maxPreviewRatio
	}
	c.previewRatio = r
	c.SetSize(c.ScreenWidth, c.ScreenHeight)
}

// SetPreviewRatioPercent applies the configured tui.preview_size (percent of
// the body given to the preview) and re-runs the layout, mirroring
// AdjustPreviewRatio. Zero/absent keeps the current ratio; out-of-range
// values clamp.
func (c *ProgramContext) SetPreviewRatioPercent(p int) {
	if p == 0 {
		return
	}
	r := float64(p) / 100
	if r < minPreviewRatio {
		r = minPreviewRatio
	}
	if r > maxPreviewRatio {
		r = maxPreviewRatio
	}
	c.previewRatio = r
	c.SetSize(c.ScreenWidth, c.ScreenHeight)
}

// ToggleZoom flips the tmux-style zoom: the main pane takes the full body,
// and toggling again restores the previous split. With the sidebar closed
// there is nothing to zoom away, so the key is a no-op — latent zoom state
// would otherwise make a reopened preview invisibly collapse.
func (c *ProgramContext) ToggleZoom() {
	if !c.SidebarOpen {
		return
	}
	c.zoomed = !c.zoomed
	c.SetSize(c.ScreenWidth, c.ScreenHeight)
}

// Zoomed reports whether the main pane is zoomed.
func (c *ProgramContext) Zoomed() bool { return c.zoomed }

// RebindKeys rebuilds the key map from defaults plus the config's tui.keys
// overrides, so a removed override returns to its default on hot-reload. On
// an invalid override (unknown action) the current map is kept and the error
// returned for the caller to surface.
func (c *ProgramContext) RebindKeys(cfg *config.Config) error {
	m := keys.Default()
	if cfg != nil && len(cfg.TUI.Keys) > 0 {
		if err := m.Rebind(cfg.TUI.Keys); err != nil {
			return err
		}
	}
	c.Keys = m
	return nil
}

// Lens is a named quick-filter JQL for the issues section.
type Lens struct {
	Name string
	JQL  string
}

// SetLenses applies the config's [[tui.lenses]] entries and tui.default_lens.
// Entries missing a title or JQL are skipped rather than rendering a blank
// chip; an empty surviving list clears back to the section's built-ins.
func (c *ProgramContext) SetLenses(cfg *config.Config) {
	c.Lenses = nil
	c.DefaultLens = ""
	if cfg == nil {
		return
	}
	for _, l := range cfg.TUI.Lenses {
		if xstrings.IsBlank(l.Title) || xstrings.IsBlank(l.JQL) {
			continue
		}
		c.Lenses = append(c.Lenses, Lens{Name: l.Title, JQL: l.JQL})
	}
	c.DefaultLens = strings.TrimSpace(cfg.TUI.DefaultLens)
}

// SetPreviewFromConfig applies a tui.preview value: right/left/bottom dock the
// open sidebar there, "hidden" closes it, "auto" opens it with width-resolved
// placement. An empty or unknown value leaves the current state alone, so a
// config without the key never fights the p-key cycle on reload.
func (c *ProgramContext) SetPreviewFromConfig(v string) {
	pos := PreviewPosition(strings.ToLower(strings.TrimSpace(v)))
	switch pos {
	case PreviewRight, PreviewLeft, PreviewBottom, PreviewAuto:
		c.SidebarOpen = true
		c.SetPreviewPosition(pos) // recomputes the split via SetSize
	case PreviewHidden:
		c.SidebarOpen = false
		c.SetSize(c.ScreenWidth, c.ScreenHeight)
	}
}

// Styles bundles the chrome styles the App and Sections share. Content styles
// (status colors, priorities, entity colors) stay in the theme package; this
// bundle is just the structural chrome that the new architecture owns.
type Styles struct {
	Header             lipgloss.Style
	HeaderRule         lipgloss.Style // wraps the tab row, drawing the bottom divider
	TabActive          lipgloss.Style // selected section pill
	TabInactive        lipgloss.Style // unselected section pill
	Brand              lipgloss.Style // product label right of the tab row
	Footer             lipgloss.Style
	FooterRule         lipgloss.Style // dim style for the labeled-border dashes
	HintKey            lipgloss.Style // key glyphs in the footer hint line
	HintDesc           lipgloss.Style // descriptions in the footer hint line
	Error              lipgloss.Style
	Sidebar            lipgloss.Style
	SidebarBorder      lipgloss.Style // left-edge divider (sidebar docked right)
	SidebarBorderRight lipgloss.Style // right-edge divider (sidebar docked left)
	SidebarBorderTop   lipgloss.Style // top-edge divider (sidebar docked bottom)
	Overlay            lipgloss.Style // bordered modal box for the action controller
	HelpBox            lipgloss.Style // bordered box for the full-keymap help sheet
}

// DefaultStyles derives the chrome bundle from the shared clib theme so a theme
// swap reskins the new TUI alongside the old one.
func DefaultStyles() Styles {
	return Styles{
		Header: theme.Title,
		HeaderRule: lipgloss.NewStyle().
			BorderStyle(lipgloss.ThickBorder()).
			BorderBottom(true).
			BorderForeground(theme.Theme.Dim.GetForeground()),
		TabActive:   lipgloss.NewStyle().Bold(true).Reverse(true).Padding(0, 1),
		TabInactive: lipgloss.NewStyle().Faint(true).Padding(0, 1),
		Brand:       lipgloss.NewStyle().Bold(true).Foreground(theme.Theme.Magenta.GetForeground()),
		Footer:      lipgloss.NewStyle().Foreground(theme.ColorStatusBarFg),
		FooterRule:  theme.HelpDesc,
		HintKey:     theme.HelpKey,
		HintDesc:    theme.HelpDesc,
		Error:       theme.StatusErr,
		Sidebar: lipgloss.NewStyle().
			Padding(0, 1),
		SidebarBorder: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(theme.Theme.Dim.GetForeground()),
		SidebarBorderRight: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(theme.Theme.Dim.GetForeground()),
		SidebarBorderTop: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(theme.Theme.Dim.GetForeground()),
		Overlay: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.Theme.Blue.GetForeground()).
			Padding(0, 1),
		HelpBox: theme.HelpOverlay,
	}
}
