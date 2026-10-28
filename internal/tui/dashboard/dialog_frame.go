package dashboard

import (
	"charm.land/lipgloss/v2"
	"github.com/gechr/primer/dialog"

	"github.com/matcra587/jira-cli/internal/cli"
)

func newCommandStack() *dialog.Stack {
	return dialog.New(commandFrame())
}

func commandFrame() dialog.Frame {
	return commandFrameHeight(24)
}

func commandFrameHeight(height int) dialog.Frame {
	return dialogFrameSize(76, height)
}

func dialogFrameSize(width, height int) dialog.Frame {
	return dialogFramePadding(width, height, 2)
}

func dialogFramePadding(width, height, padding int) dialog.Frame {
	styles := dialog.Styles{Box: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, padding)}
	if cli.StyleEnabled() {
		theme := cli.ResolvedTheme()
		styles.Box = styles.Box.BorderForeground(theme.Blue.GetForeground())
		styles.Title, styles.HintKey, styles.HintText = *theme.Bold, *theme.Blue, *theme.Dim
	}
	return dialog.NewFrame(dialog.FrameConfig{
		Styles: styles, MaxWidth: width, MaxHeight: height,
		WidthFraction: .95, HeightFraction: .95, Margin: 1,
	})
}
