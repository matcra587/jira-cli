package cmdutil

import (
	"os"
	"slices"
	"sort"

	clib "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/clib/complete"
	"github.com/gechr/clib/help"
	"github.com/gechr/clib/theme"
	xslices "github.com/gechr/x/slices"
	"github.com/gechr/x/terminal"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// helpMaxWidth caps rendered help on wide terminals. 100 matches the upper
// bound of clib's default 70-100 description measure, so flag descriptions
// and long-form prose share one readable right edge instead of flag lines
// running the full width of a wide terminal.
const helpMaxWidth = 100

// NewHelpRenderer builds the themed clib help renderer used by every command
// in jira-cli. The JIRA env-prefix is set so that JIRA_THEME, JIRA_LOG_LEVEL,
// and related clog/clib variables are honored (NO_COLOR itself is unprefixed
// per its spec). config.DefaultTheme resolves the JIRA_THEME override
// (or the dark built-in), which clib v0.5 no longer does for us after dropping
// the old theme.Default().
func NewHelpRenderer() *help.Renderer {
	theme.SetEnvPrefix("JIRA")
	th := config.DefaultTheme().With(
		theme.WithEnumStyle(theme.EnumStyleHighlightBoth),
		theme.WithHelpRepeatEllipsisEnabled(true),
	)
	opts := []help.RendererOption{
		// Smart backtick styling is clib's default; pinned explicitly because
		// the contextual token coloring is load-bearing for this help text
		// (evaluated and adopted, not merely inherited).
		help.WithBacktickStyle(help.BacktickStyleSmart),
	}
	// WithMaxWidth replaces the renderer's writer-width detection rather than
	// capping it, so it is only set when stdout is a terminal wider than the
	// cap. Narrower terminals and non-TTY writers (docs generation, piped
	// help) keep the auto-detected width they have today.
	if terminal.Width(os.Stdout) > helpMaxWidth {
		opts = append(opts, help.WithMaxWidth(helpMaxWidth))
	}
	return help.NewRenderer(th, opts...)
}

// StandardHelpSections returns the standard clib help sections for cmd,
// with subcommand listing made optional and the Output group rendered last.
func StandardHelpSections(cmd *cobra.Command) []help.Section {
	sections := clib.SectionsWithOptions(clib.WithSubcommandOptional())(cmd)
	if cmd == nil || !cmd.Runnable() || !cmd.HasSubCommands() || helpSectionsContainFlags(sections) {
		return orderFlagGroups(sections)
	}

	flagSections := runnableParentFlagSections(cmd)
	if len(flagSections) == 0 {
		return orderFlagGroups(sections)
	}
	markUsageWithOptions(sections)
	return orderFlagGroups(append(sections, flagSections...))
}

// flagGroupRank orders flag groups by the user's task flow: choose the target
// and payload, shape and guard the operation, choose output, then leave
// operational tuning and inherited Cobra options at the bottom. clib otherwise
// renders groups in first-seen registration order, which leaks the order flags
// happened to be declared in.
var flagGroupRank = map[string]int{
	"Filters":       1,
	"Fields":        2,
	"Input":         3,
	"User":          4,
	"Dashboard":     5,
	"Link":          6,
	"Transition":    7,
	"Rank":          8,
	"Worklog":       9,
	"Visibility":    10,
	"Sort":          11,
	"Pagination":    12,
	"Safety":        13,
	"Validation":    14,
	"ADF":           15,
	"Output":        16,
	"Cache":         17,
	"Configuration": 18,
	"Theme":         19,
	"Runtime":       20,
	"Execution":     21,
	// Inherited/global flag blocks always render last.
	"Options":        98,
	"Global Options": 99,
}

// unrankedGroupRank places any group not named in flagGroupRank just before the
// global Options block, so a newly added group surfaces near the bottom (and
// is obviously unranked) rather than landing mid-flow.
const unrankedGroupRank = 90

func groupRank(title string) int {
	if r, ok := flagGroupRank[title]; ok {
		return r
	}
	return unrankedGroupRank
}

// GroupRank returns the canonical task-flow sort rank for a flag-group title.
// It is exported so the docs generator can order flag groups identically to the
// terminal help, keeping the reference site and `--help` in step.
func GroupRank(title string) int {
	return groupRank(title)
}

// orderFlagGroups sorts the flag-group sections into the canonical task-flow
// order (flagGroupRank) while leaving structural sections — Usage, Examples,
// subcommand lists — exactly where they are. Only sections that carry a flag
// group are reordered; each sorted section is slotted back into a position a
// flag group already occupied, so non-flag sections never move.
func orderFlagGroups(sections []help.Section) []help.Section {
	var slots []int
	for i := range sections {
		if sectionHasFlagGroup(sections[i]) {
			slots = append(slots, i)
		}
	}
	if len(slots) < 2 {
		return sections
	}
	picked := xslices.Map(slots, func(i int) help.Section { return sections[i] })
	sort.SliceStable(picked, func(a, b int) bool {
		return groupRank(picked[a].Title) < groupRank(picked[b].Title)
	})
	out := slices.Clone(sections)
	for j, i := range slots {
		out[i] = picked[j]
	}
	return out
}

func sectionHasFlagGroup(section help.Section) bool {
	for _, content := range section.Content {
		if _, ok := content.(help.FlagGroup); ok {
			return true
		}
	}
	return false
}

func helpSectionsContainFlags(sections []help.Section) bool {
	return slices.ContainsFunc(sections, sectionHasFlagGroup)
}

func markUsageWithOptions(sections []help.Section) {
	for i := range sections {
		for j := range sections[i].Content {
			usage, ok := sections[i].Content[j].(help.Usage)
			if !ok {
				continue
			}
			usage.ShowOptions = true
			sections[i].Content[j] = usage
		}
	}
}

func runnableParentFlagSections(cmd *cobra.Command) []help.Section {
	metaByName := make(map[string]complete.FlagMeta)
	for _, meta := range clib.FlagMeta(cmd) {
		metaByName[meta.Name] = meta
	}

	var classified []help.ClassifiedFlag
	seen := make(map[string]struct{})
	addFlags := func(flags *pflag.FlagSet) {
		flags.VisitAll(func(flag *pflag.Flag) {
			if flag.Hidden {
				return
			}
			if _, ok := seen[flag.Name]; ok {
				return
			}
			seen[flag.Name] = struct{}{}
			meta := metaByName[flag.Name]
			classified = append(classified, help.ClassifiedFlag{
				Flag:  helpFlagFromPFlag(flag, meta),
				Group: meta.Group,
			})
		})
	}
	addFlags(cmd.LocalNonPersistentFlags())
	addFlags(cmd.PersistentFlags())

	if len(classified) == 0 {
		return nil
	}
	return help.BuildFlagSections(classified, help.WithKeepGroupOrder())
}

func helpFlagFromPFlag(flag *pflag.Flag, meta complete.FlagMeta) help.Flag {
	out := help.Flag{
		Short:         flag.Shorthand,
		Long:          flag.Name,
		Desc:          flag.Usage,
		Enum:          meta.Enum,
		EnumDefault:   meta.EnumDefault,
		EnumHighlight: meta.EnumHighlight,
		NoIndent:      meta.NoIndent,
	}
	if meta.HideLong {
		out.Long = ""
	}
	if meta.HideShort {
		out.Short = ""
	}
	if meta.HasArg {
		out.Placeholder = meta.Placeholder
		if out.Placeholder == "" {
			out.Placeholder = flag.Name
		}
		out.Repeatable = meta.IsSlice
	}
	return out
}
