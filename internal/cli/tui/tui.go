package tui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	xstrings "github.com/gechr/x/strings"
	"github.com/spf13/cobra"

	"github.com/gechr/x/ptr"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/boardscope"
	"github.com/matcra587/jira-cli/internal/cli/cmdutil"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/core"
	"github.com/matcra587/jira-cli/internal/tui/icons"
	"github.com/matcra587/jira-cli/internal/tui/sections/issues"
	"github.com/matcra587/jira-cli/internal/tui/sections/settings"
	"github.com/matcra587/jira-cli/internal/tui/theme"
	"github.com/matcra587/jira-cli/internal/version"
)

// NewCommand constructs the tui subcommand.
func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Launch the persistent dashboard",
		Long: "Launch the terminal dashboard for the active profile. Use it when you want " +
			"an interactive issue triage view instead of one-shot command output.\n\n" +
			"The dashboard requires stdout to be an interactive terminal. In scripts or " +
			"agent workflows, use the resource commands such as `jira issue list` and " +
			"`jira search jql` instead.",
		Example: `$ jira tui

# Open the dashboard for a non-active profile
$ jira --profile prod tui`,
		GroupID: "dashboard",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := cli.RequireTTY(os.Stdout); err != nil {
				return err
			}
			return Run(cmd)
		},
	}
}

// Run builds and runs the dashboard for the command's profile. A missing or
// invalid credential is returned as an error (the program never opens); an
// unconfigured profile opens an empty dashboard the user can still navigate.
func Run(cmd *cobra.Command) error {
	app, err := buildApp(cmd)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(app, tea.WithContext(cmd.Context())).Run()
	return err
}

// buildApp resolves the Jira client and config for the active profile and wires
// them into the dashboard. It is the cobra/client glue; the view registration
// lives in newApp so it can be tested without a live client.
func buildApp(cmd *cobra.Command) (core.App, error) {
	client, profile, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return core.App{}, err
	}
	var svc core.Services
	if ok {
		svc = servicesAdapter{factory: cmdutil.ServicesForClient(client)}
	}
	// The profile's default board becomes its own tab when it resolves from
	// the boards cache (cache-only, no network). An unset default or a cold
	// cache just leaves the "board" tab dark — same as any unregistered name.
	var boardTitle, boardJQL string
	if scope, _, err := boardscope.FromFlags(cmd); err == nil {
		if clause, has := scope.JQLClause(); has {
			boardTitle = strings.TrimSpace(ptr.Deref(scope.Board.Name))
			if boardTitle == "" {
				boardTitle = "Board"
			}
			boardJQL = clause + " AND statusCategory != Done ORDER BY updated DESC"
		}
	}
	var cfg *config.Config
	if loaded, err := config.Load(config.WithPath(cmdutil.ConfigPath(cmd))); err == nil {
		cfg = loaded
	}
	// Resolve the same path config.Load used: an absent --config means the
	// default location, and the settings watcher must point at the real file.
	cfgPath := cmdutil.ConfigPath(cmd)
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	return newApp(svc, cfg, profile, cmd.Context(), cfgPath, boardTitle, boardJQL), nil
}

// newApp registers the dashboard's sections and returns the root model. Issues
// is first, so it is the landing view. New sections (boards, epics, worklogs)
// join by registering a factory here and appending to order — the App needs no
// change. The profile supplies the footer context (profile name, default
// project and board).
func newApp(svc core.Services, cfg *config.Config, profile config.Profile, base context.Context, cfgPath, boardTitle, boardJQL string) core.App {
	// Resolve and apply the configured clib theme ("auto" detects the
	// terminal background) before any section derives styles from it — the
	// glamour markdown style is built from this palette at section
	// construction. The legacy TUI did this in its own constructor; the
	// cutover entrypoint must do it itself.
	// Detect the "auto" theme's terminal background now, before Bubble Tea
	// owns stdin: run later (a preview or reload naming "auto"), the OSC
	// reply would arrive as keystrokes in whatever holds focus.
	theme.DetectAutoOnce()
	name := ""
	if cfg != nil {
		name = cfg.Theme.Name
	}
	theme.Apply(theme.Resolve(name))
	iconMode := ""
	if cfg != nil {
		iconMode = cfg.TUI.Icons
	}
	icons.Use(icons.For(icons.Resolve(iconMode)))

	ctx := core.NewProgramContext(svc, cfg)
	// tui.keys overrides; a bad override shouldn't kill the dashboard, so it
	// lands in the footer instead.
	if err := ctx.RebindKeys(cfg); err != nil {
		ctx.Err = err
	}
	ctx.ProfileName = profile.Name
	ctx.Project = profile.DefaultProject
	ctx.Board = profile.DefaultBoard
	ctx.BaseURL = profile.BaseURL
	ctx.DefaultIssueType = profile.DefaultIssueType
	ctx.WorkdaySeconds = profile.WorkdaySeconds
	ctx.Version = version.Version()
	ctx.ConfigPath = cfgPath
	if cfg != nil {
		ctx.SetPreviewFromConfig(cfg.TUI.Preview)
		ctx.SetPreviewRatioPercent(cfg.TUI.PreviewSize)
	}
	ctx.SetLenses(cfg)
	if base != nil {
		ctx.Base = base
	}
	registry := core.NewRegistry()
	registry.Register(issues.ID, issues.New)
	registry.Register(issues.SearchID, issues.NewSearch)
	registry.Register(issues.EpicsID, issues.NewEpics)
	if boardJQL != "" {
		registry.Register(issues.BoardID, issues.NewQuery(issues.BoardID, boardTitle, boardJQL))
	}
	registry.Register(settings.ID, settings.New)
	queries := registerQuerySections(registry, cfg)
	app := core.NewApp(ctx, registry, resolveOrder(cfg, registry, queries))
	// Config hot-reload: re-register the config-derived query sections and
	// rebuild the tab order. Every previous query instance is invalidated —
	// its JQL may have changed — and refetches on rebuild. prev is captured so
	// a shrinking section list still drops the orphaned tabs.
	prev := queries
	app.Reconfigure = func(_ *core.ProgramContext, reg *core.Registry, fresh *config.Config) ([]core.SectionID, []core.SectionID) {
		invalidate := make([]core.SectionID, 0, len(prev))
		for _, q := range prev {
			invalidate = append(invalidate, q.id)
		}
		next := registerQuerySections(reg, fresh)
		prev = next
		return resolveOrder(fresh, reg, next), invalidate
	}
	return app
}

// querySection pairs a configured section's ID with its title for ordering and
// default_tab matching.
type querySection struct {
	id    core.SectionID
	title string
}

// registerQuerySections registers one dashboard tab per tui.sections entry:
// each saved JQL query becomes its own section. Entries with no
// JQL are skipped; a missing title falls back to a numbered label.
func registerQuerySections(registry *core.Registry, cfg *config.Config) []querySection {
	if cfg == nil {
		return nil
	}
	var out []querySection
	for i, sec := range cfg.TUI.Sections {
		if xstrings.IsBlank(sec.JQL) {
			continue
		}
		title := strings.TrimSpace(sec.Title)
		if title == "" {
			title = fmt.Sprintf("Query %d", i+1)
		}
		// IDs use the sequential valid-section index, not the raw config slice
		// position, so a removed or blank entry doesn't shift its neighbors' IDs.
		id := issues.QueryID(len(out))
		registry.Register(id, issues.NewQuery(id, title, sec.JQL))
		out = append(out, querySection{id: id, title: title})
	}
	return out
}

// resolveOrder builds the tab order from config, keeping only sections that are
// actually registered — so a configured-but-unimplemented tab (e.g. "epics")
// is skipped rather than opening a blank view — and floats the configured
// default_tab to the front as the landing view. The configured tabs list is
// authoritative for which views are visible: default_tab is honored only when
// it also appears in tabs, so it can never silently add an unlisted view. It
// falls back to the built-in order when config is absent or names no known
// section. New sections light up automatically as they are registered.
func resolveOrder(cfg *config.Config, registry *core.Registry, queries []querySection) []core.SectionID {
	fallback := []core.SectionID{issues.ID, issues.SearchID}
	if registry.Has(settings.ID) {
		fallback = append(fallback, settings.ID)
	}
	if cfg == nil {
		return fallback
	}
	// A tabs/default_tab name resolves to a builtin section ID first, then to a
	// configured section's title (case-insensitive). Builtin precedence means a
	// query titled "Search" can never hijack default_tab = "search".
	idFor := func(name string) (core.SectionID, bool) {
		if id := core.SectionID(name); registry.Has(id) {
			return id, true
		}
		for _, q := range queries {
			if strings.EqualFold(q.title, name) {
				return q.id, true
			}
		}
		return "", false
	}
	var order []core.SectionID
	for _, name := range cfg.TUI.Tabs {
		if id, ok := idFor(name); ok && !contains(order, id) {
			order = append(order, id)
		}
	}
	if len(order) == 0 {
		order = fallback
	}
	// Configured query sections always show — defining one in tui.sections is
	// the opt-in, and removing the entry is how you hide it. Tabs may name a
	// query by title to position it explicitly; the rest slot in after the
	// triage home (or lead when Issues is not visible).
	order = insertQueries(order, queries)
	// Settings always rides last unless tabs placed it explicitly, so older
	// tabs lists (written before the section existed) still get it.
	if registry.Has(settings.ID) && !contains(order, settings.ID) {
		order = append(order, settings.ID)
	}
	if def, ok := idFor(cfg.TUI.DefaultTab); ok && contains(order, def) {
		order = floatFront(order, def)
	}
	return order
}

// insertQueries splices the configured sections into the tab order directly
// after the Issues tab, or in front when Issues is hidden.
func insertQueries(order []core.SectionID, queries []querySection) []core.SectionID {
	if len(queries) == 0 {
		return order
	}
	ids := make([]core.SectionID, 0, len(queries))
	for _, q := range queries {
		if !contains(order, q.id) { // already placed explicitly via tabs
			ids = append(ids, q.id)
		}
	}
	if len(ids) == 0 {
		return order
	}
	for i, id := range order {
		if id == issues.ID {
			out := append([]core.SectionID{}, order[:i+1]...)
			out = append(out, ids...)
			return append(out, order[i+1:]...)
		}
	}
	return append(ids, order...)
}

// floatFront returns order with id (expected to be present) moved to the front,
// preserving the relative order of the remaining sections.
func floatFront(order []core.SectionID, id core.SectionID) []core.SectionID {
	out := make([]core.SectionID, 0, len(order))
	out = append(out, id)
	for _, x := range order {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}

func contains(ids []core.SectionID, id core.SectionID) bool {
	return slices.Contains(ids, id)
}

// servicesAdapter satisfies core.Services by delegating to the CLI's service
// factory. It lives here, in the wiring layer, so core never imports the cli
// packages and no import cycle can form. The plural method names match the TUI
// seam; the factory uses singular names.
type servicesAdapter struct{ factory *cmdutil.ServiceFactory }

var _ core.Services = servicesAdapter{}

func (a servicesAdapter) Issues() jira.IssueService     { return a.factory.Issue() }
func (a servicesAdapter) Search() jira.SearchService    { return a.factory.Search() }
func (a servicesAdapter) JQL() jira.JQLService          { return a.factory.JQL() }
func (a servicesAdapter) Users() jira.UserService       { return a.factory.User() }
func (a servicesAdapter) Worklogs() jira.WorklogService { return a.factory.Worklog() }
func (a servicesAdapter) Projects() jira.ProjectService { return a.factory.Project(0) }
func (a servicesAdapter) Labels() jira.LabelService     { return a.factory.Label() }
