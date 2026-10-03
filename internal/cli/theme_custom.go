package cli

import (
	"encoding/hex"
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/gechr/clib/theme"
	clogtheme "github.com/gechr/clog/theme"

	"github.com/matcra587/jira-cli/internal/config"
)

// customTheme composes an independent preset before the help renderer snapshots
// it. Color construction belongs to this theme owner, never command handlers.
func customTheme(def *config.ThemeFile) (*theme.Theme, *clogtheme.Theme, error) {
	var base theme.Theme
	if err := base.UnmarshalText([]byte(def.Base)); err != nil {
		return nil, nil, err
	}
	bindings := customThemeBindings(&base)
	options := make([]theme.Option, 0, len(def.Colors)+1)
	for role, text := range def.Colors {
		binding, ok := bindings[role]
		if !ok {
			return nil, nil, fmt.Errorf("unknown custom style role %q", role)
		}
		value, err := customRGB(text)
		if err != nil {
			return nil, nil, err
		}
		styled := lipgloss.NewStyle()
		if binding.style != nil {
			styled = *binding.style
		}
		options = append(options, binding.apply(styled.Foreground(value)))
	}
	if def.EntityColors != nil {
		if len(def.EntityColors) == 0 {
			return nil, nil, fmt.Errorf("custom entity palette must not be empty")
		}
		values := make([]color.Color, 0, len(def.EntityColors))
		for _, text := range def.EntityColors {
			value, err := customRGB(text)
			if err != nil {
				return nil, nil, err
			}
			values = append(values, value)
		}
		options = append(options, theme.WithEntityColors(values...))
	}
	syntax := clogtheme.Dark()
	if base.Background == theme.BackgroundLight {
		syntax = clogtheme.Light()
	}
	var named clogtheme.Theme
	if err := named.UnmarshalText([]byte(def.Base)); err == nil {
		syntax = &named
	}
	syntaxBindings := customSyntaxBindings(syntax)
	for role, text := range def.Syntax {
		target, ok := syntaxBindings[role]
		if !ok {
			return nil, nil, fmt.Errorf("unknown custom syntax role %q", role)
		}
		value, err := customRGB(text)
		if err != nil {
			return nil, nil, err
		}
		*target = value
	}
	return base.With(options...), syntax, nil
}

type customStyleBinding struct {
	style *lipgloss.Style
	apply func(lipgloss.Style) theme.Option
}

func customThemeBindings(t *theme.Theme) map[string]customStyleBinding {
	return map[string]customStyleBinding{
		"blue":                     {style: t.Blue, apply: theme.WithBlue},
		"bold":                     {style: t.Bold, apply: theme.WithBold},
		"bold_green":               {style: t.BoldGreen, apply: theme.WithBoldGreen},
		"dim":                      {style: t.Dim, apply: theme.WithDim},
		"green":                    {style: t.Green, apply: theme.WithGreen},
		"magenta":                  {style: t.Magenta, apply: theme.WithMagenta},
		"orange":                   {style: t.Orange, apply: theme.WithOrange},
		"red":                      {style: t.Red, apply: theme.WithRed},
		"yellow":                   {style: t.Yellow, apply: theme.WithYellow},
		"help_alias":               {style: t.HelpAlias, apply: theme.WithHelpAlias},
		"help_arg":                 {style: t.HelpArg, apply: theme.WithHelpArg},
		"help_arg_optional":        {style: t.HelpArgOptional, apply: theme.WithHelpArgOptional},
		"help_bold_dim":            {style: t.HelpBoldDim, apply: theme.WithHelpBoldDim},
		"help_command":             {style: t.HelpCommand, apply: theme.WithHelpCommand},
		"help_desc_backtick":       {style: t.HelpDescBacktick, apply: theme.WithHelpDescBacktick},
		"help_desc_list":           {style: t.HelpDescList, apply: theme.WithHelpDescList},
		"help_desc_numbered_list":  {style: t.HelpDescNumberedList, apply: theme.WithHelpDescNumberedList},
		"help_desc_unordered_list": {style: t.HelpDescUnorderedList, apply: theme.WithHelpDescUnorderedList},
		"help_dim":                 {style: t.HelpDim, apply: theme.WithHelpDim},
		"help_enum_default":        {style: t.HelpEnumDefault, apply: theme.WithHelpEnumDefault},
		"help_flag":                {style: t.HelpFlag, apply: theme.WithHelpFlag},
		"help_flag_backtick":       {style: t.HelpFlagBacktick, apply: theme.WithHelpFlagBacktick},
		"help_flag_default":        {style: t.HelpFlagDefault, apply: theme.WithHelpFlagDefault},
		"help_flag_example":        {style: t.HelpFlagExample, apply: theme.WithHelpFlagExample},
		"help_flag_note":           {style: t.HelpFlagNote, apply: theme.WithHelpFlagNote},
		"help_key_value_separator": {style: t.HelpKeyValueSeparatorStyle, apply: theme.WithHelpKeyValueSeparatorStyle},
		"help_repeat_ellipsis":     {style: t.HelpRepeatEllipsis, apply: theme.WithHelpRepeatEllipsis},
		"help_section":             {style: t.HelpSection, apply: theme.WithHelpSection},
		"help_subcommand":          {style: t.HelpSubcommand, apply: theme.WithHelpSubcommand},
		"help_value_placeholder":   {style: t.HelpValuePlaceholder, apply: theme.WithHelpPlaceholder},
		"markdown_code":            {style: t.MarkdownCode, apply: theme.WithMarkdownCode},
		"markdown_text":            {style: t.MarkdownText, apply: theme.WithMarkdownText},
	}
}

func customSyntaxBindings(t *clogtheme.Theme) map[string]*color.Color {
	return map[string]*color.Color{
		"accent": &t.Accent, "bool_false": &t.BoolFalse, "bool_true": &t.BoolTrue,
		"comment": &t.Comment, "foreground": &t.Foreground, "key": &t.Key,
		"number": &t.Number, "secondary": &t.Secondary, "string": &t.String,
	}
}

func customRGB(text string) (color.Color, error) {
	if len(text) != 7 || text[0] != '#' {
		return nil, fmt.Errorf("custom color must be #RRGGBB")
	}
	bytes, err := hex.DecodeString(text[1:])
	if err != nil {
		return nil, fmt.Errorf("custom color must be #RRGGBB: %w", err)
	}
	return color.RGBA{R: bytes[0], G: bytes[1], B: bytes[2], A: 255}, nil
}
