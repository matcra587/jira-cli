package root

import (
	"io"
	"os"

	"github.com/charmbracelet/colorprofile"
	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/clib/help"
	"github.com/gechr/clog"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
)

// renderBareHelp retains clib's native renderer and section builder. Its
// Cobra callback chooses a color profile only from the environment; this
// adapter also honors the resolved --color flag and returns rendering errors.
func renderBareHelp(cmd *cobra.Command, renderer *help.Renderer, destination io.Writer) error {
	writer := colorprofile.NewWriter(destination, os.Environ())
	switch cli.ResolvedColorMode() {
	case clog.ColorAlways:
		writer.Profile = colorprofile.TrueColor
	case clog.ColorNever:
		writer.Profile = colorprofile.NoTTY
	case clog.ColorAuto:
		// Keep upstream terminal and environment detection.
	}
	sections := clibcobra.SectionsWithOptions(clibcobra.WithShowInheritedFlagsOnSubcommands())(cmd)
	return renderer.Render(writer, sections)
}
